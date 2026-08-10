package bitbucketdc

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scm-bench/scm-bench/internal/config"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// Fetcher builds a normalized snapshot from a Bitbucket DC instance.
//
// The guiding rule is that a failed fetch must never look like a passing or
// failing setting. Anything that could not be read is recorded in Available as
// false, and policies turn that into MANUAL.
type Fetcher struct {
	client      *Client
	cfg         config.Config
	toolVersion string
	concurrency int
	now         time.Time

	// groupCache memoises group expansion, which is otherwise the single most
	// repeated call in a large instance.
	groupMu    sync.Mutex
	groupCache map[string][]string
	groupFail  map[string]bool

	// credentialsVerified records that the preflight got a non-401 answer, and
	// therefore that the credential is genuinely accepted by this instance.
	// After that point a 401 can only be an authorization decision, which is
	// what unreadable uses it for. Set once in Fetch before any goroutine
	// starts, and only read afterwards.
	credentialsVerified bool

	// orgAdmins is the instance-level administrator set, with groups already
	// expanded. Instance administrators can administer every repository on the
	// instance, so they belong in each repository's administrator set — a
	// repository whose only administrators hold SYS_ADMIN is not a repository
	// with no administrators.
	//
	// Written once in Fetch after fetchOrganization returns and before any
	// repository goroutine starts, and only read afterwards, which is the same
	// arrangement credentialsVerified relies on.
	orgAdmins scm.EffectivePrincipals

	warnMu   sync.Mutex
	warnings []string
	warnSeen map[string]bool

	// progress reports how far along the scan is. It is called from the
	// repository goroutines, so it must be safe for concurrent use; nil means
	// nobody is watching.
	progress         func(string)
	onRepositoryDone func(string)
}

// FetchOptions narrows and tunes a scan.
type FetchOptions struct {
	// Projects limits the scan to these project keys. Empty means all.
	Projects []string
	// Repositories limits the scan to "PROJECT/slug" entries. Empty means all
	// repositories in the selected projects.
	Repositories []string
	// Concurrency bounds simultaneous repository fetches. Zero uses 8.
	Concurrency int
	// ToolVersion is stamped into the snapshot metadata.
	ToolVersion string
	// Now fixes the clock used for age calculations, for reproducible tests.
	Now time.Time
	// Progress receives a one-line summary each time a repository finishes.
	// Called concurrently. Nil discards it, which is what a non-interactive
	// run wants: a scan piped into a file should not narrate itself.
	Progress func(string)
	// OnRepositoryDone fires once a repository's requests have all completed,
	// which is what lets a caller group them: the requests themselves arrive
	// interleaved across however many repositories are in flight.
	OnRepositoryDone func(fullName string)
}

// NewFetcher returns a fetcher bound to a client and configuration.
func NewFetcher(client *Client, cfg config.Config) *Fetcher {
	return &Fetcher{
		client:      client,
		cfg:         cfg,
		concurrency: 8,
		now:         time.Now().UTC(),
		groupCache:  map[string][]string{},
		groupFail:   map[string]bool{},
		warnSeen:    map[string]bool{},
	}
}

func (f *Fetcher) logf(format string, args ...any) { f.client.logf(format, args...) }

// warn records an instance-level problem once.
func (f *Fetcher) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	f.warnMu.Lock()
	defer f.warnMu.Unlock()
	if f.warnSeen[msg] {
		return
	}
	f.warnSeen[msg] = true
	f.warnings = append(f.warnings, msg)
	f.client.warnf("%s", msg)
}

// Fetch captures the snapshot.
func (f *Fetcher) Fetch(ctx context.Context, opts FetchOptions) (*scm.Snapshot, error) {
	if opts.Concurrency > 0 {
		f.concurrency = opts.Concurrency
	}
	if !opts.Now.IsZero() {
		f.now = opts.Now.UTC()
	}
	f.toolVersion = opts.ToolVersion
	f.progress = opts.Progress
	f.onRepositoryDone = opts.OnRepositoryDone

	// Recorded before anything is fetched, so a snapshot archived for later
	// re-evaluation still carries what the capture was exposed to.
	for _, w := range f.client.TransportWarnings() {
		f.warn("%s", w)
	}

	if err := f.verifyCredentials(ctx); err != nil {
		return nil, err
	}

	snapshot := &scm.Snapshot{
		SchemaVersion: scm.SchemaVersion,
		Metadata: scm.Metadata{
			Tool:        "scm-bench",
			ToolVersion: opts.ToolVersion,
			Platform:    scm.PlatformBitbucketDC,
			BaseURL:     f.client.BaseURL(),
			GeneratedAt: f.now,
		},
	}

	org, err := f.fetchOrganization(ctx)
	if err != nil {
		return nil, err
	}
	snapshot.Organization = org
	// Captured before the repository goroutines start, so resolveAdmins can
	// read it without synchronisation.
	f.orgAdmins = org.EffectiveAdmins

	projects, err := f.fetchProjects(ctx, opts)
	if err != nil {
		return nil, err
	}
	snapshot.Projects = projects

	// A scan that covered no repository is not a clean instance, but it renders
	// as one: every repository-scope control simply has nothing to report, the
	// instance-scope ones carry the score on their own, and the summary looks
	// like an audit. One mistyped --project key is enough to produce it, and
	// nothing else in the output would say so.
	if repos := countRepositories(projects); repos == 0 {
		f.warn("the scan covered 0 repositories, so only instance-level controls were evaluated; " +
			"check --project/--repository, and whether the token can see the repositories you expected")
	}

	// Repository access can only be decided once every grant has been seen.
	f.markRepositoryAccess(ctx, snapshot)

	f.warnMu.Lock()
	snapshot.Metadata.Warnings = append([]string(nil), f.warnings...)
	f.warnMu.Unlock()
	return snapshot, nil
}

// verifyCredentials fails the scan before any work is done when the instance
// rejects the credential outright.
//
// Without this, a mistyped token produces a complete-looking report: every
// control reports MANUAL because nothing could be read, the score is 0, and the
// exit code is 0 because nothing technically failed. That is the worst possible
// output for an audit tool — it is indistinguishable from a real result unless
// the reader notices that *everything* is MANUAL.
//
// Only a 401 is fatal here. Anything else means the credential was accepted and
// this particular endpoint was not reachable, which is the ordinary case the
// rest of the scan is built to degrade through.
func (f *Fetcher) verifyCredentials(ctx context.Context) error {
	f.logf("verifying credentials")
	err := f.client.get(ctx, "/api/1.0/application-properties", nil, nil)
	if IsUnauthorized(err) {
		return fmt.Errorf("the instance rejected the credentials: %w\n"+
			"check --token (BITBUCKET_TOKEN), or --username/--password (BITBUCKET_USERNAME/BITBUCKET_PASSWORD)", err)
	}
	f.credentialsVerified = true
	return nil
}

// unreadable reports whether err means "this credential could not read that",
// as opposed to "the scan cannot continue".
//
// It exists because Bitbucket answers 401, not 403, on the admin endpoints for
// a user who is authenticated but is not an instance administrator:
//
//	GET /application-properties        200
//	GET /admin/permissions/users       401  You are not permitted to access this resource
//
// Treating that as fatal aborted the whole scan for a project administrator —
// two requests in, no report written at all — while the README promised those
// two instance-scope controls would report MANUAL and the scan would carry on.
// The preflight is what makes this safe to distinguish: once the same
// credential has been accepted, a later 401 cannot mean it was rejected.
func (f *Fetcher) unreadable(err error) bool {
	return IsUnavailable(err) || (f.credentialsVerified && IsUnauthorized(err))
}

// fetchOrganization reads instance-level administrators and the user directory.
// Both need admin rights; a read-only token without them yields an empty but
// clearly-marked-unavailable organization rather than an error.
func (f *Fetcher) fetchOrganization(ctx context.Context) (scm.Organization, error) {
	org := scm.Organization{Available: map[string]bool{}}

	f.logf("fetching global permissions")
	userPerms, err := getPaged[apiUserPermission](ctx, f.client, "/api/1.0/admin/permissions/users", nil)
	switch {
	case err == nil:
		org.Available["adminUsers"] = true
		for _, up := range userPerms {
			if !isGlobalAdminPermission(up.Permission) {
				continue
			}
			org.Admins = append(org.Admins, scm.PrincipalPermission{
				Name:        up.User.Name,
				DisplayName: up.User.DisplayName,
				Type:        "user",
				Permission:  up.Permission,
				Active:      up.User.Active,
			})
		}
	case f.unreadable(err):
		org.Available["adminUsers"] = false
		f.warn("global user permissions are not readable (%v); instance administrator rules will report MANUAL", err)
	default:
		return org, fmt.Errorf("fetch global user permissions: %w", err)
	}

	groupPerms, err := getPaged[apiGroupPermission](ctx, f.client, "/api/1.0/admin/permissions/groups", nil)
	switch {
	case err == nil:
		org.Available["adminGroups"] = true
		for _, gp := range groupPerms {
			if !isGlobalAdminPermission(gp.Permission) {
				continue
			}
			org.Admins = append(org.Admins, scm.PrincipalPermission{
				Name:       gp.Group.Name,
				Type:       "group",
				Permission: gp.Permission,
			})
		}
	case f.unreadable(err):
		org.Available["adminGroups"] = false
		f.warn("global group permissions are not readable (%v)", err)
	default:
		return org, fmt.Errorf("fetch global group permissions: %w", err)
	}

	// An administrator count only means something once admin groups have been
	// expanded to the people actually in them.
	org.EffectiveAdmins = f.expandPrincipals(ctx, org.Admins, org.Available["adminUsers"] && org.Available["adminGroups"])

	f.logf("fetching user directory")
	users, err := getPaged[apiUser](ctx, f.client, "/api/1.0/admin/users", nil)
	switch {
	case err == nil:
		org.Available["users"] = true
		activityKnown := false
		for _, u := range users {
			user := scm.User{
				Name:         u.Name,
				DisplayName:  u.DisplayName,
				EmailAddress: u.EmailAddress,
				Active:       u.Active,
				InactiveDays: -1,
			}
			if u.LastAuthenticationTimestamp != nil && *u.LastAuthenticationTimestamp > 0 {
				user.LastActivityEpoch = *u.LastAuthenticationTimestamp / 1000
				user.InactiveDays = f.daysSince(user.LastActivityEpoch)
				activityKnown = true
			}
			org.Users = append(org.Users, user)
		}
		// One user with a timestamp is enough to show the field is populated;
		// none at all means the instance never reports it.
		org.Available["userActivity"] = activityKnown
		if !activityKnown && len(users) > 0 {
			f.warn("no user reports a last-authentication timestamp; dormant-account rules will report MANUAL")
		}
	case f.unreadable(err):
		org.Available["users"] = false
		org.Available["userActivity"] = false
		f.warn("the user directory is not readable (%v); dormant-account rules will report MANUAL", err)
	default:
		return org, fmt.Errorf("fetch users: %w", err)
	}

	return org, nil
}

// expandPrincipals resolves a grant list to the set of users it reaches,
// expanding groups. complete stays true only if every group was expandable.
func (f *Fetcher) expandPrincipals(ctx context.Context, grants []scm.PrincipalPermission, sourcesAvailable bool) scm.EffectivePrincipals {
	out := scm.EffectivePrincipals{Complete: sourcesAvailable}
	seen := map[string]bool{}
	addUser := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out.Users = append(out.Users, name)
	}

	for _, g := range grants {
		switch g.Type {
		case "group":
			out.Groups = append(out.Groups, g.Name)
			members, ok := f.groupMembers(ctx, g.Name)
			if !ok {
				out.Complete = false
				continue
			}
			for _, m := range members {
				addUser(m)
			}
		default:
			addUser(g.Name)
		}
	}

	sort.Strings(out.Users)
	sort.Strings(out.Groups)
	out.Count = len(out.Users)
	return out
}

func isGlobalAdminPermission(p string) bool {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "ADMIN", "SYS_ADMIN":
		return true
	}
	return false
}

// scanPosition is where the scan has got to across projects. Repositories are
// counted within a project rather than instance-wide: the total is only known
// once every project's repository list has been fetched, and delaying the
// first progress line until then would leave the longest silence exactly where
// a large instance most needs a sign of life.
type scanPosition struct {
	project  int
	projects int
}

// reportProgress emits one line describing how far along the scan is. It is
// called from the repository goroutines; the callback is responsible for its
// own synchronisation.
func (f *Fetcher) reportProgress(pos scanPosition, projectKey string, done, total int) {
	if f.progress == nil {
		return
	}
	if pos.projects > 1 {
		f.progress(fmt.Sprintf("scanning · project %d/%d · %s %d/%d repositories",
			pos.project, pos.projects, projectKey, done, total))
		return
	}
	f.progress(fmt.Sprintf("scanning · %s %d/%d repositories", projectKey, done, total))
}

// fetchProjects resolves the target projects and their repositories.
func (f *Fetcher) fetchProjects(ctx context.Context, opts FetchOptions) ([]scm.Project, error) {
	want, err := parseTargets(opts)
	if err != nil {
		return nil, err
	}

	var apiProjects []apiProject
	if len(want.projects) > 0 {
		for _, key := range want.keys() {
			var p apiProject
			if err := f.client.get(ctx, "/api/1.0/projects/"+url.PathEscape(key), nil, &p); err != nil {
				return nil, fmt.Errorf("fetch project %s: %w", key, err)
			}
			apiProjects = append(apiProjects, p)
		}
	} else {
		f.logf("listing projects")
		all, err := getPaged[apiProject](ctx, f.client, "/api/1.0/projects", nil)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		apiProjects = all
	}

	projects := make([]scm.Project, 0, len(apiProjects))
	for i, ap := range apiProjects {
		project := scm.Project{
			Key:    ap.Key,
			Name:   ap.Name,
			Type:   ap.Type,
			Public: ap.Public,
		}
		perms, permsRead := f.fetchProjectPermissions(ctx, ap.Key)
		project.Permissions = perms
		parent := projectContext{perms: perms, permsRead: permsRead}

		repos, err := f.fetchRepositories(ctx, ap, parent, want, scanPosition{project: i + 1, projects: len(apiProjects)})
		if err != nil {
			return nil, err
		}
		project.Repositories = repos
		projects = append(projects, project)
	}
	return projects, nil
}

// projectContext is what a repository needs to know about the project above
// it: the grant table, and whether that table could be read in full. The two
// travel together because using one without the other is the bug this type
// exists to prevent.
type projectContext struct {
	perms scm.Permissions
	// permsRead is false when a project grant table came back unreadable, in
	// which case every repository below it has an administrator set that is a
	// lower bound rather than a count.
	permsRead bool
}

// fetchProjectPermissions reads the project grant table and the default
// permission handed to every licensed user.
//
// The second return value reports whether the grant tables were read in full.
// It is not decoration: a project administrator's grants apply to every
// repository in the project, so a table that could not be read leaves each of
// those repositories with an administrator set that is a lower bound. Without
// this the tables came back empty and indistinguishable from a project that
// genuinely grants nothing, and the repositories underneath reported a
// confident FAIL built on a count nobody had been able to take.
func (f *Fetcher) fetchProjectPermissions(ctx context.Context, key string) (scm.Permissions, bool) {
	perms := scm.Permissions{}
	available := true
	base := "/api/1.0/projects/" + url.PathEscape(key)

	if users, err := getPaged[apiUserPermission](ctx, f.client, base+"/permissions/users", nil); err == nil {
		for _, up := range users {
			perms.Users = append(perms.Users, scm.PrincipalPermission{
				Name:        up.User.Name,
				DisplayName: up.User.DisplayName,
				Type:        "user",
				Permission:  up.Permission,
				Active:      up.User.Active,
			})
		}
	} else if f.unreadable(err) {
		available = false
		f.warn("project %s user permissions are not readable (%v)", key, err)
	} else {
		available = false
		f.warn("project %s user permissions failed: %v", key, err)
	}

	if groups, err := getPaged[apiGroupPermission](ctx, f.client, base+"/permissions/groups", nil); err == nil {
		for _, gp := range groups {
			perms.Groups = append(perms.Groups, scm.PrincipalPermission{
				Name:       gp.Group.Name,
				Type:       "group",
				Permission: gp.Permission,
			})
		}
	} else if f.unreadable(err) {
		available = false
		f.warn("project %s group permissions are not readable (%v)", key, err)
	} else {
		// Without this branch anything that is not a plain "not readable" —
		// a 401, a transport failure, a malformed response — vanishes, and the
		// permission table silently looks like it has no groups in it.
		available = false
		f.warn("project %s group permissions failed: %v", key, err)
	}

	perms.DefaultPermission, perms.DefaultPermissionKnown = f.fetchDefaultPermission(ctx, key)
	return perms, available
}

// fetchDefaultPermission probes which blanket permission, if any, the project
// grants to every licensed user. The API answers one permission at a time, so
// this walks from the most permissive down and reports the first hit.
// It returns the permission and whether every probe actually answered: a
// partial probe cannot distinguish "nothing is granted" from "we could not
// look", and the policy needs to know which it is.
func (f *Fetcher) fetchDefaultPermission(ctx context.Context, key string) (string, bool) {
	base := "/api/1.0/projects/" + url.PathEscape(key) + "/permissions/"
	known := true
	for _, perm := range []string{"PROJECT_ADMIN", "PROJECT_WRITE", "PROJECT_READ"} {
		var resp struct {
			Permitted bool `json:"permitted"`
		}
		if err := f.client.get(ctx, base+perm+"/all", nil, &resp); err != nil {
			known = false
			if !f.unreadable(err) {
				f.warn("project %s default permission probe failed: %v", key, err)
			}
			continue
		}
		if resp.Permitted {
			// known, not true: the walk goes from most permissive down, so a
			// hit here is only the whole answer if every probe above it
			// answered. If the PROJECT_ADMIN probe failed and PROJECT_WRITE
			// says yes, the real default could still be PROJECT_ADMIN — and
			// reporting PROJECT_WRITE as certain understates the grant with
			// exactly the confidence it has not earned.
			return perm, known
		}
	}
	return "", known
}

// fetchRepositories lists and then fully populates the repositories of one
// project, bounded by the configured concurrency.
func (f *Fetcher) fetchRepositories(ctx context.Context, project apiProject, parent projectContext, want targets, pos scanPosition) ([]scm.Repository, error) {
	f.logf("listing repositories in %s", project.Key)
	apiRepos, err := getPaged[apiRepository](ctx, f.client, "/api/1.0/projects/"+url.PathEscape(project.Key)+"/repos", nil)
	if err != nil {
		return nil, fmt.Errorf("list repositories in %s: %w", project.Key, err)
	}

	selected := make([]apiRepository, 0, len(apiRepos))
	for _, r := range apiRepos {
		if !want.selects(project.Key, r.Slug) {
			continue
		}
		if f.cfg.SkipArchivedRepositories && r.Archived {
			f.logf("skipping archived repository %s/%s", project.Key, r.Slug)
			continue
		}
		selected = append(selected, r)
	}

	out := make([]scm.Repository, len(selected))
	sem := make(chan struct{}, f.concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var completed atomic.Int64

	for i, r := range selected {
		wg.Add(1)
		go func(i int, r apiRepository) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			repo, err := f.fetchRepository(ctx, project, r, parent)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			out[i] = repo
			f.reportProgress(pos, project.Key, int(completed.Add(1)), len(selected))
		}(i, r)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchRepository populates every setting a policy might read for one
// repository. Sub-fetch failures are recorded, not propagated: one repository
// with a missing add-on should not abort the scan.
func (f *Fetcher) fetchRepository(ctx context.Context, project apiProject, r apiRepository, parent projectContext) (scm.Repository, error) {
	full := project.Key + "/" + r.Slug
	f.logf("scanning %s", full)

	// Every request below is tagged with this repository, so a caller showing
	// them can group what arrives interleaved from concurrent fetches.
	ctx = withScope(ctx, full)
	if f.onRepositoryDone != nil {
		defer f.onRepositoryDone(full)
	}

	repo := scm.Repository{
		Slug:       r.Slug,
		Name:       r.Name,
		ProjectKey: project.Key,
		FullName:   full,
		// A repository is reachable anonymously if either it or its project is
		// public, so the effective flag is the union.
		Public:    r.Public || project.Public,
		Archived:  r.Archived,
		Forkable:  r.Forkable,
		Available: map[string]bool{},
	}
	base := "/api/1.0/projects/" + url.PathEscape(project.Key) + "/repos/" + url.PathEscape(r.Slug)

	// The default branch anchors nearly every other check, so resolve it first.
	repo.DefaultBranch, repo.DefaultBranchDisplay, repo.Empty = f.fetchDefaultBranch(ctx, base, &repo)

	model := f.fetchBranchModel(ctx, project.Key, r.Slug)

	f.fetchPullRequestSettings(ctx, base, &repo)
	f.fetchBranchRestrictions(ctx, project.Key, r.Slug, model, &repo)
	f.fetchRequiredBuilds(ctx, project.Key, r.Slug, model, &repo)
	f.fetchHooks(ctx, base, &repo)
	f.fetchBranches(ctx, base, &repo)
	f.fetchSecurityPolicy(ctx, base, &repo)
	f.fetchRepositoryPermissions(ctx, base, parent, &repo)

	repo.Permissions.PublicAccess = repo.Public
	repo.Permissions.DefaultPermission = parent.perms.DefaultPermission
	repo.Permissions.DefaultPermissionKnown = parent.perms.DefaultPermissionKnown

	if err := ctx.Err(); err != nil {
		return repo, err
	}
	return repo, nil
}

// fetchDefaultBranch tries the modern endpoint, then the legacy one.
func (f *Fetcher) fetchDefaultBranch(ctx context.Context, base string, repo *scm.Repository) (ref, display string, empty bool) {
	for _, path := range []string{base + "/default-branch", base + "/branches/default"} {
		var out apiRef
		err := f.client.get(ctx, path, nil, &out)
		if err == nil && out.ID != "" {
			repo.Available["defaultBranch"] = true
			return out.ID, fallbackDisplay(out), false
		}
		if err != nil && IsNotFound(err) {
			// A 404 here is ambiguous: the endpoint may be absent on this
			// version, or the repository may simply be empty. Keep trying.
			continue
		}
		if err != nil && !f.unreadable(err) {
			repo.Errors = append(repo.Errors, fmt.Sprintf("default branch: %v", err))
			repo.Available["defaultBranch"] = false
			return "", "", false
		}
	}

	// Fall back to the branch listing, which also settles whether the
	// repository is empty.
	branches, err := getPaged[apiBranch](ctx, f.client, base+"/branches", nil)
	if err != nil {
		repo.Available["defaultBranch"] = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("default branch: %v", err))
		return "", "", false
	}
	if len(branches) == 0 {
		repo.Available["defaultBranch"] = true
		return "", "", true
	}
	for _, b := range branches {
		if b.IsDefault {
			repo.Available["defaultBranch"] = true
			return b.ID, b.DisplayID, false
		}
	}
	repo.Available["defaultBranch"] = false
	repo.Errors = append(repo.Errors, "default branch could not be determined")
	return "", "", false
}

func fallbackDisplay(ref apiRef) string {
	if ref.DisplayID != "" {
		return ref.DisplayID
	}
	return normalizeRef(ref.ID)
}

// fetchBranchModel reads the branching model that gives MODEL_* matchers their
// meaning. It is an optional add-on API; absence downgrades matcher resolution
// to documented defaults rather than failing.
func (f *Fetcher) fetchBranchModel(ctx context.Context, projectKey, slug string) branchModel {
	var model branchModel
	path := "/branch-utils/latest/projects/" + url.PathEscape(projectKey) + "/repos/" + url.PathEscape(slug) + "/branchmodel"
	if err := f.client.get(ctx, path, nil, &model); err != nil {
		if !f.unreadable(err) {
			f.warn("branch model for %s/%s failed: %v", projectKey, slug, err)
		}
		return branchModel{}
	}
	model.resolved = true
	return model
}

func (f *Fetcher) fetchPullRequestSettings(ctx context.Context, base string, repo *scm.Repository) {
	var settings apiPullRequestSettings
	if err := f.client.get(ctx, base+"/settings/pull-requests", nil, &settings); err != nil {
		repo.Available["pullRequestSettings"] = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("pull request settings: %v", err))
		return
	}
	repo.Available["pullRequestSettings"] = true
	repo.PullRequestSettings = scm.PullRequestSettings{
		RequiredApprovers:        settings.RequiredApprovers.Int(),
		RequiredAllApprovers:     settings.RequiredAllApprovers.Bool(),
		RequiredAllTasksComplete: settings.RequiredAllTasksComplete.Bool(),
		RequiredSuccessfulBuilds: settings.RequiredSuccessfulBuilds.Int(),
		UnapproveOnUpdate:        settings.UnapproveOnUpdate.Bool(),
		DefaultStrategy:          settings.MergeConfig.DefaultStrategy.ID,
	}
	for _, s := range settings.MergeConfig.Strategies {
		// Bitbucket omits "enabled" on the strategies it has switched on in
		// some versions, so a missing flag means enabled.
		enabled := true
		if s.Enabled != nil {
			enabled = *s.Enabled
		}
		repo.PullRequestSettings.MergeStrategies = append(repo.PullRequestSettings.MergeStrategies, scm.MergeStrategy{
			ID:      s.ID,
			Name:    s.Name,
			Enabled: enabled,
		})
	}
	repo.Available["mergeStrategies"] = len(repo.PullRequestSettings.MergeStrategies) > 0
}

func (f *Fetcher) fetchBranchRestrictions(ctx context.Context, projectKey, slug string, model branchModel, repo *scm.Repository) {
	path := "/branch-permissions/2.0/projects/" + url.PathEscape(projectKey) + "/repos/" + url.PathEscape(slug) + "/restrictions"
	restrictions, err := getPaged[apiRestriction](ctx, f.client, path, nil)
	if err != nil {
		repo.Available["branchRestrictions"] = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("branch restrictions: %v", err))
		return
	}
	repo.Available["branchRestrictions"] = true
	for _, r := range restrictions {
		br := scm.BranchRestriction{
			ID:                   r.ID,
			Type:                 strings.ToLower(strings.TrimSpace(r.Type.ID)),
			MatcherID:            r.Matcher.ID,
			MatcherType:          strings.ToUpper(strings.TrimSpace(r.Matcher.Type.ID)),
			MatcherText:          r.Matcher.DisplayID,
			Scope:                r.Scope.Type,
			MatchesDefaultBranch: matchesDefaultBranch(r.Matcher, repo.DefaultBranch, repo.DefaultBranchDisplay, model),
			ExemptAccessKeys:     len(r.AccessKeys),
		}
		for _, u := range r.Users {
			br.ExemptUsers = append(br.ExemptUsers, u.Name)
		}
		br.ExemptGroups = append(br.ExemptGroups, r.Groups...)
		repo.BranchRestrictions = append(repo.BranchRestrictions, br)
	}
}

func (f *Fetcher) fetchRequiredBuilds(ctx context.Context, projectKey, slug string, model branchModel, repo *scm.Repository) {
	prefix := "/required-builds/latest/projects/" + url.PathEscape(projectKey) + "/repos/" + url.PathEscape(slug)
	// The collection endpoint was renamed between releases; try both spellings
	// before concluding the add-on is absent.
	var (
		conditions []apiRequiredBuild
		err        error
	)
	for _, path := range []string{prefix + "/conditions", prefix + "/condition"} {
		conditions, err = getPaged[apiRequiredBuild](ctx, f.client, path, nil)
		if err == nil {
			break
		}
		if !IsNotFound(err) {
			break
		}
	}
	if err != nil {
		repo.Available["requiredBuilds"] = false
		if f.unreadable(err) {
			repo.Errors = append(repo.Errors, "required builds API unavailable (Bitbucket 8.0+ with the required-builds feature); merge-check rule reports MANUAL")
		} else {
			repo.Errors = append(repo.Errors, fmt.Sprintf("required builds: %v", err))
		}
		return
	}
	repo.Available["requiredBuilds"] = true
	for _, c := range conditions {
		rb := scm.RequiredBuild{
			ID:                   c.ID,
			BuildParentKeys:      c.BuildParentKeys,
			MatcherID:            c.RefMatcher.ID,
			MatcherType:          strings.ToUpper(strings.TrimSpace(c.RefMatcher.Type.ID)),
			MatcherText:          c.RefMatcher.DisplayID,
			MatchesDefaultBranch: matchesDefaultBranch(c.RefMatcher, repo.DefaultBranch, repo.DefaultBranchDisplay, model),
		}
		if c.ExemptRefMatcher != nil {
			rb.ExemptMatcherID = c.ExemptRefMatcher.ID
			// An exemption covering the default branch cancels the condition.
			if matchesDefaultBranch(*c.ExemptRefMatcher, repo.DefaultBranch, repo.DefaultBranchDisplay, model) {
				rb.MatchesDefaultBranch = false
			}
		}
		repo.RequiredBuilds = append(repo.RequiredBuilds, rb)
	}
}

func (f *Fetcher) fetchHooks(ctx context.Context, base string, repo *scm.Repository) {
	hooks, err := getPaged[apiHook](ctx, f.client, base+"/settings/hooks", nil)
	if err != nil {
		repo.Available["hooks"] = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("hooks: %v", err))
		return
	}
	repo.Available["hooks"] = true
	for _, h := range hooks {
		repo.Hooks = append(repo.Hooks, scm.Hook{
			Key:        h.Details.Key,
			Name:       h.Details.Name,
			Type:       h.Details.Type,
			Enabled:    h.Enabled,
			Configured: h.Configured,
			Scope:      h.Scope.Type,
		})
	}
}

// maxBranchesForCommitLookup bounds the per-branch commit fallback. Above this
// the scan would issue thousands of requests, so the ages stay unknown and the
// stale-branch rule reports MANUAL instead.
const maxBranchesForCommitLookup = 200

func (f *Fetcher) fetchBranches(ctx context.Context, base string, repo *scm.Repository) {
	query := url.Values{"details": []string{"true"}}
	branches, err := getPaged[apiBranch](ctx, f.client, base+"/branches", query)
	if err != nil {
		repo.Available["branches"] = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("branches: %v", err))
		return
	}
	repo.Available["branches"] = true

	agesComplete := true
	for _, b := range branches {
		branch := scm.Branch{
			ID:                b.ID,
			DisplayID:         b.DisplayID,
			IsDefault:         b.IsDefault,
			LatestCommit:      b.LatestCommit,
			LatestCommitEpoch: b.latestCommitEpoch(),
			AgeDays:           -1,
		}
		if branch.LatestCommitEpoch == 0 && len(branches) <= maxBranchesForCommitLookup && b.LatestCommit != "" {
			// details=true did not carry commit metadata on this version;
			// ask for the commit directly.
			branch.LatestCommitEpoch = f.fetchCommitEpoch(ctx, base, b.LatestCommit)
		}
		if branch.LatestCommitEpoch > 0 {
			branch.AgeDays = f.daysSince(branch.LatestCommitEpoch)
		} else {
			agesComplete = false
		}
		repo.Branches = append(repo.Branches, branch)
	}
	if len(branches) == 0 {
		agesComplete = true
	}
	repo.Available["branchAges"] = agesComplete
	if !agesComplete {
		repo.Errors = append(repo.Errors, "commit timestamps unavailable for some branches; stale-branch rule reports MANUAL")
	}
}

func (f *Fetcher) fetchCommitEpoch(ctx context.Context, base, commitID string) int64 {
	var commit struct {
		CommitterTimestamp int64 `json:"committerTimestamp"`
		AuthorTimestamp    int64 `json:"authorTimestamp"`
	}
	if err := f.client.get(ctx, base+"/commits/"+url.PathEscape(commitID), nil, &commit); err != nil {
		return 0
	}
	if commit.CommitterTimestamp > 0 {
		return commit.CommitterTimestamp / 1000
	}
	if commit.AuthorTimestamp > 0 {
		return commit.AuthorTimestamp / 1000
	}
	return 0
}

// fetchSecurityPolicy probes the configured paths on the default branch.
func (f *Fetcher) fetchSecurityPolicy(ctx context.Context, base string, repo *scm.Repository) {
	if repo.Empty {
		// An empty repository has no default branch to hold a policy file.
		// That is a known state, and the rule reports it as not applicable.
		repo.Available["files"] = true
		return
	}
	if repo.DefaultBranch == "" {
		// The repository has commits but we could not resolve its default
		// branch, so nothing was browsed. Marking this available would let the
		// rule report "no security policy found" having looked nowhere.
		repo.Available["files"] = false
		repo.Errors = append(repo.Errors, "default branch unresolved, so no path could be browsed for a security policy")
		return
	}

	query := url.Values{
		"at":    []string{repo.DefaultBranch},
		"limit": []string{"1"},
	}
	available := true
	for _, path := range f.cfg.SecurityPolicyPaths {
		repo.Files.Probed = append(repo.Files.Probed, path)

		var resp apiBrowseResponse
		err := f.client.get(ctx, base+"/browse/"+escapePath(path), query, &resp)
		if err != nil {
			if IsNotFound(err) {
				continue // the file is simply not there
			}
			available = false
			repo.Errors = append(repo.Errors, fmt.Sprintf("browse %s: %v", path, err))
			break
		}
		if resp.isFile() {
			repo.Files.SecurityPolicyPaths = append(repo.Files.SecurityPolicyPaths, path)
		}
	}
	repo.Available["files"] = available
}

// escapePath escapes each path segment while keeping the separators.
func escapePath(p string) string {
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// fetchRepositoryPermissions reads the repository grant table and resolves the
// effective administrator set, unioning repository and project grants and
// expanding groups.
func (f *Fetcher) fetchRepositoryPermissions(ctx context.Context, base string, parent projectContext, repo *scm.Repository) {
	// The project half counts: a project administrator administers every
	// repository in the project, so an unread project table leaves this
	// repository's answer incomplete just as surely as an unread repository one.
	available := parent.permsRead

	if users, err := getPaged[apiUserPermission](ctx, f.client, base+"/permissions/users", nil); err == nil {
		for _, up := range users {
			repo.Permissions.Users = append(repo.Permissions.Users, scm.PrincipalPermission{
				Name:        up.User.Name,
				DisplayName: up.User.DisplayName,
				Type:        "user",
				Permission:  up.Permission,
				Active:      up.User.Active,
			})
		}
	} else {
		available = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("repository user permissions: %v", err))
	}

	if groups, err := getPaged[apiGroupPermission](ctx, f.client, base+"/permissions/groups", nil); err == nil {
		for _, gp := range groups {
			repo.Permissions.Groups = append(repo.Permissions.Groups, scm.PrincipalPermission{
				Name:       gp.Group.Name,
				Type:       "group",
				Permission: gp.Permission,
			})
		}
	} else {
		available = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("repository group permissions: %v", err))
	}

	repo.Available["permissions"] = available
	repo.Admins = f.resolveAdmins(ctx, repo.Permissions, parent.perms, available)
	repo.Available["admins"] = repo.Admins.Complete
}

// resolveAdmins unions repository REPO_ADMIN, project PROJECT_ADMIN and
// instance ADMIN/SYS_ADMIN grants, expanding every admin group to its members.
//
// The instance grants are the ones that used to be missing, and their absence
// produced a confident wrong answer rather than a missing one: a repository
// administered only by the instance's administrators — ordinary for a small
// project — counted zero administrators, and CIS-1.3.7 reported "Only 0
// administrator(s) can manage this repository". isAdminPermission has always
// accepted ADMIN and SYS_ADMIN, values that can only come from the global
// permission table, so the intent was there; the grants were not.
//
// Completeness now also depends on the instance grants being readable. That is
// not a regression in coverage: CIS-1.3.7 decides PASS from a lower bound
// before it consults completeness, so a repository that already has enough
// administrators still passes. What changes is the case that was wrong — too
// few administrators, instance grants unreadable — which becomes MANUAL
// instead of a FAIL nobody could act on.
func (f *Fetcher) resolveAdmins(ctx context.Context, repoPerms, projectPerms scm.Permissions, permsAvailable bool) scm.EffectivePrincipals {
	admins := scm.EffectivePrincipals{Complete: permsAvailable && f.orgAdmins.Complete}
	seen := map[string]bool{}
	groupsSeen := map[string]bool{}

	addUser := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		admins.Users = append(admins.Users, name)
	}
	addGroup := func(name string) {
		if name == "" || groupsSeen[name] {
			return
		}
		groupsSeen[name] = true
		admins.Groups = append(admins.Groups, name)
		members, ok := f.groupMembers(ctx, name)
		if !ok {
			// The group holds admin rights but we cannot see who is in it, so
			// the count below is a lower bound.
			admins.Complete = false
			return
		}
		for _, m := range members {
			addUser(m)
		}
	}

	for _, table := range []scm.Permissions{repoPerms, projectPerms} {
		for _, u := range table.Users {
			if isAdminPermission(u.Permission) {
				addUser(u.Name)
			}
		}
		for _, g := range table.Groups {
			if isAdminPermission(g.Permission) {
				addGroup(g.Name)
			}
		}
	}

	// Instance administrators, already expanded by fetchOrganization. The users
	// go in directly rather than through addGroup: expandPrincipals resolved
	// the groups once for the whole instance, and re-expanding them per
	// repository would repeat that work for every repository on the instance.
	for _, name := range f.orgAdmins.Users {
		addUser(name)
	}
	for _, name := range f.orgAdmins.Groups {
		if !groupsSeen[name] {
			groupsSeen[name] = true
			admins.Groups = append(admins.Groups, name)
		}
	}

	sort.Strings(admins.Users)
	sort.Strings(admins.Groups)
	admins.Count = len(admins.Users)
	return admins
}

func isAdminPermission(p string) bool {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "REPO_ADMIN", "PROJECT_ADMIN", "ADMIN", "SYS_ADMIN":
		return true
	}
	return false
}

// groupMembers expands a group, memoising both successes and failures.
func (f *Fetcher) groupMembers(ctx context.Context, group string) ([]string, bool) {
	f.groupMu.Lock()
	if members, ok := f.groupCache[group]; ok {
		f.groupMu.Unlock()
		return members, true
	}
	if f.groupFail[group] {
		f.groupMu.Unlock()
		return nil, false
	}
	f.groupMu.Unlock()

	query := url.Values{"context": []string{group}}
	users, err := getPaged[apiUser](ctx, f.client, "/api/1.0/admin/groups/more-members", query)

	f.groupMu.Lock()
	defer f.groupMu.Unlock()
	if err != nil {
		f.groupFail[group] = true
		f.warn("group %q could not be expanded (%v); administrator counts are lower bounds", group, err)
		return nil, false
	}
	members := make([]string, 0, len(users))
	for _, u := range users {
		if u.Active {
			members = append(members, u.Name)
		}
	}
	f.groupCache[group] = members
	return members, true
}

// markRepositoryAccess flags which directory users can actually reach code,
// which the dormant-account rule needs to avoid reporting service accounts
// that hold no grants at all.
func (f *Fetcher) markRepositoryAccess(ctx context.Context, snapshot *scm.Snapshot) {
	withAccess := map[string]bool{}
	everyoneHasAccess := false

	// EffectiveAdmins rather than Admins: the latter is the grant table as
	// written, where an entry may be a group, and filtering it to Type ==
	// "user" dropped everyone who holds instance administrator rights through
	// one — which is how most instances grant them.
	//
	// The people dropped were not a marginal set. An instance administrator can
	// read every repository on the instance, so they are the account with the
	// most access on it; leaving them out of withAccess meant CIS-1.3.1 skipped
	// them, and a dormant instance administrator is the single dormant account
	// most worth finding. fetchOrganization has already expanded the groups, so
	// the answer is sitting here ready to use.
	for _, name := range snapshot.Organization.EffectiveAdmins.Users {
		withAccess[name] = true
	}

	collect := func(perms scm.Permissions) {
		for _, u := range perms.Users {
			withAccess[u.Name] = true
		}
		for _, g := range perms.Groups {
			// Every granted group is expanded, not just the admin ones: a user
			// whose only access comes through a read-only group still has
			// access to code, and missing them would quietly excuse a dormant
			// account from review. Expansion is memoised, so repeated groups
			// cost nothing.
			members, ok := f.groupMembers(ctx, g.Name)
			if !ok {
				continue
			}
			for _, m := range members {
				withAccess[m] = true
			}
		}
		if perms.DefaultPermission != "" {
			everyoneHasAccess = true
		}
	}

	for _, p := range snapshot.Projects {
		collect(p.Permissions)
		for _, r := range p.Repositories {
			collect(r.Permissions)
		}
	}

	for i := range snapshot.Organization.Users {
		u := &snapshot.Organization.Users[i]
		u.HasRepositoryAccess = (everyoneHasAccess && u.Active) || withAccess[u.Name]
	}
}

func countRepositories(projects []scm.Project) int {
	n := 0
	for _, p := range projects {
		n += len(p.Repositories)
	}
	return n
}

func (f *Fetcher) daysSince(epoch int64) int {
	if epoch <= 0 {
		return -1
	}
	d := f.now.Sub(time.Unix(epoch, 0).UTC())
	if d < 0 {
		// A timestamp in the future (clock skew) is best reported as fresh.
		return 0
	}
	return int(d.Hours() / 24)
}

// targets is the resolved --project/--repository selection.
//
// Both flags are additive includes, and keeping them additive is the whole
// point of the type. --project and --repository used to share one filter, so
// `--project PLATFORM --repository OTHER/app` scanned no repository in
// PLATFORM at all: naming any repository turned the filter on for every
// project, and PLATFORM had no entry in it. The project was still fetched and
// still appeared in the snapshot, just empty — so its controls did not report
// MANUAL, they vanished, and the report looked like a clean scan of a project
// nobody had looked at.
type targets struct {
	// projects is every project key to visit, keyed by its lowercased form.
	// The value is the spelling the user gave, which is what goes in the
	// request path and in error messages.
	projects map[string]string
	// wholeProjects holds the lowercased keys named by --project, which select
	// every repository beneath them.
	wholeProjects map[string]bool
	// repositories holds lowercased "project/slug" entries from --repository.
	repositories map[string]bool
}

// selects reports whether a repository is in scope.
func (t targets) selects(projectKey, slug string) bool {
	// No --repository at all: --project already narrowed the projects, and
	// everything inside them is wanted.
	if len(t.repositories) == 0 {
		return true
	}
	if t.wholeProjects[strings.ToLower(projectKey)] {
		return true
	}
	return t.repositories[strings.ToLower(projectKey+"/"+slug)]
}

// keys returns the project keys to fetch, in a stable order.
func (t targets) keys() []string {
	out := make([]string, 0, len(t.projects))
	for lower := range t.projects {
		out = append(out, lower)
	}
	sort.Strings(out)
	for i, lower := range out {
		out[i] = t.projects[lower]
	}
	return out
}

// parseTargets normalizes the project and repository filters.
//
// Comparison is case-insensitive because Bitbucket's REST paths are: `-p PRJ`
// and `-r prj/app` name the same project, and treating them as two fetched it
// twice, put it in the snapshot twice, and doubled every finding under it.
func parseTargets(opts FetchOptions) (targets, error) {
	t := targets{
		projects:      map[string]string{},
		wholeProjects: map[string]bool{},
		repositories:  map[string]bool{},
	}
	addProject := func(key string) {
		lower := strings.ToLower(key)
		if _, seen := t.projects[lower]; !seen {
			t.projects[lower] = key
		}
	}

	for _, p := range opts.Projects {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		addProject(p)
		t.wholeProjects[strings.ToLower(p)] = true
	}

	for _, r := range opts.Repositories {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		// Rejected rather than skipped. A silently dropped `--repository
		// payments-api` left the repository filter empty, which does not mean
		// "that one repository" — it means no filter, and the scan quietly
		// covered the entire instance instead of the one repository asked for.
		key, slug, ok := strings.Cut(r, "/")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(slug) == "" {
			return targets{}, fmt.Errorf("--repository %q must be PROJECT/slug", r)
		}
		t.repositories[strings.ToLower(r)] = true
		// Naming a repository implies scanning its project.
		addProject(key)
	}
	return t, nil
}
