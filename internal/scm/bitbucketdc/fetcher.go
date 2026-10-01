package bitbucketdc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scm-bench/bitbucket-bench/internal/config"
	"github.com/scm-bench/bitbucket-bench/internal/scm"
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

	// orgAdmins is the set of people holding instance administrator rights,
	// and orgAdminsKnown whether that set was read. A repository's own
	// administrators are counted without them: an instance administrator can
	// administer every repository, so counting them made CIS-1.3.7 pass on
	// every repository of any instance that satisfies CIS-1.3.3.
	//
	// Written once in Fetch after fetchOrganization returns and before any
	// repository goroutine starts, and only read afterwards, which is the same
	// arrangement credentialsVerified relies on.
	orgAdmins      map[string]bool
	orgAdminsKnown bool

	// licensedActive is how many active accounts hold LICENSED_USER, and
	// licensedKnown whether that list was read. Every other account is
	// refused at sign-in ("You do not have permission to access Bitbucket",
	// measured on 10.4) whatever grants it holds, so this is the population a
	// blanket grant reaches. Written before the repository goroutines start.
	licensedActive int
	licensedKnown  bool

	warnMu   sync.Mutex
	warnings []string
	warnSeen map[string]bool
	// unlisted collects the projects whose repository list could not be read,
	// under warnMu.
	unlisted []string

	// repoSlots bounds repository fetches across every project at once.
	repoSlots chan struct{}

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
			Tool:        "bitbucket-bench",
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

	f.warnMu.Lock()
	snapshot.Metadata.Warnings = append([]string(nil), f.warnings...)
	snapshot.Metadata.Unlisted = append([]string(nil), f.unlisted...)
	f.warnMu.Unlock()
	sort.Strings(snapshot.Metadata.Unlisted)
	return snapshot, nil
}

// verifyCredentials fails the scan before any work is done unless the instance
// accepted the credential as some user.
//
// Without this, a mistyped token produces a complete-looking report: every
// control reports MANUAL because nothing could be read, the score is 0, and the
// exit code is 0 because nothing technically failed. That is the worst possible
// output for an audit tool — it is indistinguishable from a real result unless
// the reader notices that *everything* is MANUAL.
//
// The question is asked of /users because it answers only an authenticated
// caller. This used to ask /application-properties, which answers anonymous
// callers too — and Bitbucket does not reject a bearer token it does not
// recognise, it serves the request as anonymous. Against Bitbucket 10.4 a
// revoked or mistyped token passed the preflight with a 200, every admin
// endpoint's 401 was then filed as "this token lacks admin rights", and on an
// instance with public projects the scan went on to audit exactly what an
// anonymous visitor can see, presented as an audit of the instance.
//
// Anything but a 2xx is fatal here, for the same reason: nothing has been
// fetched yet, so failing costs nothing, while carrying on lets every later
// request fail in a way that reads like a finding.
func (f *Fetcher) verifyCredentials(ctx context.Context) error {
	f.logf("verifying credentials")
	err := f.client.get(ctx, "/api/1.0/users", url.Values{"limit": []string{"1"}}, nil)
	switch {
	case err == nil:
		f.credentialsVerified = true
		return nil
	case basicAuthDisabled(err):
		// The default on Bitbucket 10 for a fresh install, and something
		// administrators switch on elsewhere. The instance's own message says
		// what happened but not what to do instead.
		return fmt.Errorf("this instance does not accept passwords over its REST API (%w)\n"+
			"create an HTTP access token (Profile -> Manage account -> HTTP access tokens) and pass it with --token (BITBUCKET_TOKEN)", err)
	case IsUnauthorized(err), IsForbidden(err):
		return fmt.Errorf("the instance did not accept the credentials: %w\n"+
			"check --token (BITBUCKET_TOKEN), or --username/--password (BITBUCKET_USERNAME/BITBUCKET_PASSWORD); "+
			"a token that has expired, been revoked or belongs to another instance is answered this way", err)
	case IsNotFound(err):
		return fmt.Errorf("no Bitbucket REST API answered at %s/rest (%w)\n"+
			"check --url, including the context path if Bitbucket is served under one (https://host/bitbucket)", f.client.BaseURL(), err)
	default:
		return fmt.Errorf("could not verify the credentials: %w", err)
	}
}

// basicAuthDisabled recognises the instance refusing a password outright. It
// arrives as a 403 with nothing but this sentence to tell it apart from any
// other refusal.
func basicAuthDisabled(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, m := range apiErr.Messages {
		if strings.Contains(strings.ToLower(m), "basic authentication has been disabled") {
			return true
		}
	}
	return false
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

// fetchOrganization reads who administers the instance, who may use it, and
// when each account last authenticated.
//
// The questions are put to Bitbucket's own permission resolution —
// /users?permission=… — rather than answered from grant tables. The tables
// need a global permission to read, and an HTTP access token cannot carry one
// whoever holds it ("Tokens may not have the following permission: ADMIN", on
// Bitbucket 10.4), while password authentication is off by default there. So
// for the credential the README recommends, every instance-level control
// reported MANUAL. /users?permission=ADMIN answers any authenticated caller,
// already includes SYS_ADMIN, and expands groups itself.
func (f *Fetcher) fetchOrganization(ctx context.Context) (scm.Organization, error) {
	org := scm.Organization{Available: map[string]bool{}}

	f.logf("resolving instance administrators")
	admins, err := getPaged[apiUser](ctx, f.client, "/api/1.0/users", url.Values{"permission": []string{"ADMIN"}})
	switch {
	case err == nil:
		org.Available["admins"] = true
		org.EffectiveAdmins = scm.EffectivePrincipals{Complete: true}
		for _, u := range admins {
			// An inactive account cannot sign in, so it administers nothing.
			if u.Active {
				org.EffectiveAdmins.Users = append(org.EffectiveAdmins.Users, u.Name)
			}
		}
		sort.Strings(org.EffectiveAdmins.Users)
		org.EffectiveAdmins.Count = len(org.EffectiveAdmins.Users)
	case f.unreadable(err):
		org.Available["admins"] = false
		f.warn("instance administrators could not be resolved (%v); CIS-1.3.3 will report MANUAL, and repository administrator counts cannot exclude them", err)
	default:
		org.Available["admins"] = false
		f.warn("instance administrators could not be resolved: %v", err)
	}
	f.orgAdmins = map[string]bool{}
	for _, name := range org.EffectiveAdmins.Users {
		f.orgAdmins[name] = true
	}
	f.orgAdminsKnown = org.Available["admins"]

	// The grant table as written, groups as groups. Only a password session
	// of an instance administrator can read it, so it is evidence when
	// available and nothing depends on it. Not finding it is not worth a
	// warning: no token can.
	if grants, ok := f.fetchGlobalAdminGrants(ctx); ok {
		org.Admins = grants
		org.Available["adminGrants"] = true
	}

	f.logf("fetching licensed users")
	licensed := map[string]bool{}
	licensedUsers, err := getPaged[apiUser](ctx, f.client, "/api/1.0/users", url.Values{"permission": []string{"LICENSED_USER"}})
	if err == nil {
		org.Available["licensedUsers"] = true
		for _, u := range licensedUsers {
			licensed[u.Name] = true
			if u.Active {
				f.licensedActive++
			}
		}
		f.licensedKnown = true
	} else {
		org.Available["licensedUsers"] = false
		f.warn("licensed users could not be listed (%v); the dormant-account rule will report MANUAL", err)
	}

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
				Licensed:     licensed[u.Name],
				InactiveDays: -1,
				AgeDays:      -1,
			}
			if u.LastAuthenticationTimestamp != nil && *u.LastAuthenticationTimestamp > 0 {
				user.LastActivityEpoch = *u.LastAuthenticationTimestamp / 1000
				user.InactiveDays = f.daysSince(user.LastActivityEpoch)
				activityKnown = true
			}
			if u.CreatedTimestamp != nil && *u.CreatedTimestamp > 0 {
				user.CreatedEpoch = *u.CreatedTimestamp / 1000
				user.AgeDays = f.daysSince(user.CreatedEpoch)
			}
			org.Users = append(org.Users, user)
		}
		// One account with a timestamp shows the instance records them. Then,
		// and only then, an account without one has never authenticated —
		// measured on Bitbucket 10.4, where token use updates the time as a
		// sign-in does and the key is simply left out for an account that has
		// done neither.
		org.Available["userActivity"] = activityKnown
		if activityKnown {
			for i := range org.Users {
				org.Users[i].NeverSignedIn = org.Users[i].LastActivityEpoch == 0
			}
		} else if len(users) > 0 {
			f.warn("no user reports a last-authentication timestamp; dormant-account rules will report MANUAL")
		}
	case f.unreadable(err):
		org.Available["users"] = false
		org.Available["userActivity"] = false
		f.warn("the user directory is not readable (%v); dormant-account rules will report MANUAL", err)
	default:
		org.Available["users"] = false
		org.Available["userActivity"] = false
		f.warn("the user directory could not be read: %v", err)
	}

	return org, nil
}

// fetchGlobalAdminGrants reads the global permission table, groups as groups.
func (f *Fetcher) fetchGlobalAdminGrants(ctx context.Context) ([]scm.PrincipalPermission, bool) {
	userPerms, err := getPaged[apiUserPermission](ctx, f.client, "/api/1.0/admin/permissions/users", nil)
	if err != nil {
		return nil, false
	}
	groupPerms, err := getPaged[apiGroupPermission](ctx, f.client, "/api/1.0/admin/permissions/groups", nil)
	if err != nil {
		return nil, false
	}
	var grants []scm.PrincipalPermission
	for _, up := range userPerms {
		if isGlobalAdminPermission(up.Permission) {
			grants = append(grants, scm.PrincipalPermission{
				Name:        up.User.Name,
				DisplayName: up.User.DisplayName,
				Type:        "user",
				Permission:  up.Permission,
				Active:      up.User.Active,
			})
		}
	}
	for _, gp := range groupPerms {
		if isGlobalAdminPermission(gp.Permission) {
			grants = append(grants, scm.PrincipalPermission{Name: gp.Group.Name, Type: "group", Permission: gp.Permission})
		}
	}
	return grants, true
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

	// Projects are fetched concurrently, every repository of every project
	// drawing on one shared bound. One project at a time ran an instance of a
	// thousand small projects one or two requests wide however high
	// scan.concurrency was set: the bound only ever applied inside a project.
	f.repoSlots = make(chan struct{}, f.concurrency)
	projects := make([]scm.Project, len(apiProjects))
	projectSlots := make(chan struct{}, f.concurrency)
	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)
	for i, ap := range apiProjects {
		wg.Add(1)
		go func(i int, ap apiProject) {
			defer wg.Done()
			select {
			case projectSlots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-projectSlots }()

			repos, err := f.fetchRepositories(ctx, ap, want, scanPosition{project: i + 1, projects: len(apiProjects)})
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			projects[i] = scm.Project{
				Key:          ap.Key,
				Name:         ap.Name,
				Type:         ap.Type,
				Public:       ap.Public,
				Repositories: repos,
			}
		}(i, ap)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	found := map[string]bool{}
	for _, p := range projects {
		for _, repo := range p.Repositories {
			found[strings.ToLower(repo.FullName)] = true
		}
	}

	// A --repository that names nothing used to scan zero repositories, warn,
	// and exit 0: a CI gate on one repository went green the day it was
	// renamed. A target that does not exist is a typo in the invocation.
	var missing []string
	for lower, spelled := range want.repositories {
		if !found[lower] && !f.unlistedProject(strings.SplitN(lower, "/", 2)[0]) {
			missing = append(missing, spelled)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("--repository %s: no such repository, or this token cannot see it", strings.Join(missing, ", "))
	}
	return projects, nil
}

// unlistedProject reports whether a project's repositories could not be
// listed, so a missing --repository inside it is a gap, not a typo.
func (f *Fetcher) unlistedProject(key string) bool {
	f.warnMu.Lock()
	defer f.warnMu.Unlock()
	for _, k := range f.unlisted {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// fetchRepositories lists and then fully populates the repositories of one
// project, bounded by the configured concurrency.
func (f *Fetcher) fetchRepositories(ctx context.Context, project apiProject, want targets, pos scanPosition) ([]scm.Repository, error) {
	f.logf("listing repositories in %s", project.Key)
	apiRepos, err := getPaged[apiRepository](ctx, f.client, "/api/1.0/projects/"+url.PathEscape(project.Key)+"/repos", nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// One project that would not list its repositories used to abort the
		// whole scan with nothing written. Its repositories are now missing
		// from the snapshot — which no rule can report as MANUAL, since they
		// are not there to evaluate — so the gap is recorded by name and the
		// scan exits 2 unless scan.allowIncomplete accepts it.
		f.warnMu.Lock()
		f.unlisted = append(f.unlisted, project.Key)
		f.warnMu.Unlock()
		f.warn("the repositories of project %s could not be listed (%v); none of them is in this report", project.Key, err)
		return nil, nil
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
	sem := f.repoSlots
	if sem == nil {
		sem = make(chan struct{}, f.concurrency)
	}
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

			repo, err := f.fetchRepository(ctx, project, r)
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
func (f *Fetcher) fetchRepository(ctx context.Context, project apiProject, r apiRepository) (scm.Repository, error) {
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

	repo.Permissions.PublicAccess = repo.Public
	f.fetchBaseAccess(ctx, project.Key, r.Slug, &repo)

	if r.Archived {
		// An archived repository takes no pushes and no pull requests, so every
		// control about how a change arrives is not applicable and none of its
		// settings are fetched. Who can read the code still matters: that is the
		// public flag and the base access just resolved.
		return repo, ctx.Err()
	}

	// The branch list anchors nearly every other check: it settles whether the
	// repository is empty and which existing branch is the default.
	branches := f.listBranches(ctx, base, &repo)
	f.resolveDefaultBranch(ctx, base, branches, &repo)

	model := f.fetchBranchModel(ctx, project.Key, r.Slug)

	f.fetchPullRequestSettings(ctx, base, &repo)
	f.fetchBranchRestrictions(ctx, project.Key, r.Slug, model, &repo)
	f.fetchRequiredBuilds(ctx, project.Key, r.Slug, model, &repo)
	f.fetchHooks(ctx, base, &repo)
	f.fetchSecurityPolicy(ctx, base, &repo)
	f.fetchRepositoryAdmins(ctx, project.Key, r.Slug, &repo)

	if err := ctx.Err(); err != nil {
		return repo, err
	}
	return repo, nil
}

// resolveDefaultBranch settles which existing branch is the default.
//
// The branch list decides, through isDefault, because it describes what exists.
// /default-branch only says what is configured, and on Bitbucket 10.4 that is
// refs/heads/master for a repository whose one branch is main — the state any
// repository ends up in when git's default moved to main and the instance's did
// not. Believing it sent every default-branch rule at a branch that is not
// there: the security-policy probe looked on master, found nothing, and failed
// CIS-1.2.1 for a repository with SECURITY.md at its root; and an empty
// repository, which /default-branch answers just the same, was taken for one
// with commits, so its branch rules failed instead of reporting NA.
//
// The configured name is asked for only when no listed branch is the default,
// to say which branch is missing.
func (f *Fetcher) resolveDefaultBranch(ctx context.Context, base string, branches []apiBranch, repo *scm.Repository) {
	if !repo.Available["branches"] {
		repo.Available["defaultBranch"] = false
		repo.Errors = append(repo.Errors, "default branch unknown: the branch list could not be read")
		return
	}
	for _, b := range branches {
		if b.IsDefault {
			repo.DefaultBranch = b.ID
			repo.DefaultBranchDisplay = fallbackDisplay(apiRef{ID: b.ID, DisplayID: b.DisplayID})
			repo.Available["defaultBranch"] = true
			return
		}
	}

	configured, known := f.configuredDefaultBranch(ctx, base)
	if repo.Empty {
		// Nothing has been pushed yet. The configured name is all there is,
		// and the branch rules report NA without needing it.
		repo.DefaultBranch = configured.ID
		repo.DefaultBranchDisplay = fallbackDisplay(configured)
		repo.Available["defaultBranch"] = true
		return
	}

	// Branches exist and none of them is the default. Leaving DefaultBranch
	// empty is deliberate: a rule handed the configured name would judge the
	// protection of a branch nobody can push to or merge into.
	repo.Available["defaultBranch"] = false
	if known && configured.ID != "" {
		repo.Errors = append(repo.Errors, fmt.Sprintf(
			"the configured default branch %s does not exist in the repository; set an existing one at Repository settings -> Repository details",
			fallbackDisplay(configured)))
		return
	}
	repo.Errors = append(repo.Errors, "no branch is marked as the default and the configured default branch could not be read")
}

// configuredDefaultBranch reads the default branch a repository is configured
// with, whether or not it exists: /default-branch (Bitbucket 7.5+), then the
// legacy /branches/default, which answers 204 for an empty repository and 404
// when the configured branch does not exist.
func (f *Fetcher) configuredDefaultBranch(ctx context.Context, base string) (apiRef, bool) {
	for _, path := range []string{base + "/default-branch", base + "/branches/default"} {
		var out apiRef
		err := f.client.get(ctx, path, nil, &out)
		if err == nil && out.ID != "" {
			return out, true
		}
	}
	return apiRef{}, false
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
	// Recorded rather than inferred: an absent key and a false one lead to the
	// same verdict, but not to the same fix — one needs a checkbox ticked, the
	// other an app installed first — and the report has to say which.
	repo.Available["unapproveOnUpdate"] = settings.UnapproveOnUpdate != nil
	repo.PullRequestSettings = scm.PullRequestSettings{
		RequiredApprovers:        settings.RequiredApprovers.Int(),
		RequiredAllApprovers:     settings.RequiredAllApprovers.Bool(),
		RequiredAllTasksComplete: settings.RequiredAllTasksComplete.Bool(),
		RequiredSuccessfulBuilds: settings.RequiredSuccessfulBuilds.Int(),
		UnapproveOnUpdate:        settings.UnapproveOnUpdate != nil && settings.UnapproveOnUpdate.Bool(),
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
		matches, known := matchesDefaultBranch(r.Matcher, repo.DefaultBranch, repo.DefaultBranchDisplay, model)
		br := scm.BranchRestriction{
			ID:                   r.ID,
			Type:                 normalizeRestrictionType(r.Type.ID),
			MatcherID:            r.Matcher.ID,
			MatcherType:          strings.ToUpper(strings.TrimSpace(r.Matcher.Type.ID)),
			MatcherText:          r.Matcher.DisplayID,
			Scope:                r.Scope.Type,
			MatchesDefaultBranch: matches,
			MatchUnknown:         !known && repo.Available["defaultBranch"],
			ExemptAccessKeys:     len(r.AccessKeys),
		}
		for _, u := range r.Users {
			br.ExemptUsers = append(br.ExemptUsers, u.Name)
		}
		br.ExemptGroups = append(br.ExemptGroups, r.Groups...)
		for _, k := range r.AccessKeys {
			br.ExemptAccessKeyIDs = append(br.ExemptAccessKeyIDs, k.id())
		}
		// Expanding here rather than in the rule is the usual split: group
		// membership is an API call, and a policy may not make one. The
		// instance-wide group cache means the exemption groups cost nothing
		// beyond the ones no permission table already expanded.
		br.ExemptPrincipals = f.expandPrincipals(ctx, exemptGrants(br), true)
		repo.BranchRestrictions = append(repo.BranchRestrictions, br)
	}
}

// normalizeRestrictionType maps a restriction type to the hyphenated lower-case
// form Bitbucket 8+ uses ("pull-request-only"). Older documentation shows the
// enum spelling ("PULL_REQUEST_ONLY"), which lower-casing alone would leave as
// "pull_request_only" and no rule would recognise.
func normalizeRestrictionType(t string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(t)), "_", "-")
}

// exemptGrants renders a restriction's exempt users and groups as the grant
// list expandPrincipals consumes. The permission field is left empty: what
// matters here is which principals an entry names and whether it is a group,
// not what level of access it carries.
func exemptGrants(br scm.BranchRestriction) []scm.PrincipalPermission {
	grants := make([]scm.PrincipalPermission, 0, len(br.ExemptUsers)+len(br.ExemptGroups))
	for _, u := range br.ExemptUsers {
		grants = append(grants, scm.PrincipalPermission{Name: u, Type: "user"})
	}
	for _, g := range br.ExemptGroups {
		grants = append(grants, scm.PrincipalPermission{Name: g, Type: "group"})
	}
	return grants
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
		matches, known := matchesDefaultBranch(c.RefMatcher, repo.DefaultBranch, repo.DefaultBranchDisplay, model)
		rb := scm.RequiredBuild{
			ID:                   c.ID,
			BuildParentKeys:      c.BuildParentKeys,
			MatcherID:            c.RefMatcher.ID,
			MatcherType:          strings.ToUpper(strings.TrimSpace(c.RefMatcher.Type.ID)),
			MatcherText:          c.RefMatcher.DisplayID,
			MatchesDefaultBranch: matches,
		}
		if c.ExemptRefMatcher != nil {
			rb.ExemptMatcherID = c.ExemptRefMatcher.ID
			exempt, exemptKnown := matchesDefaultBranch(*c.ExemptRefMatcher, repo.DefaultBranch, repo.DefaultBranchDisplay, model)
			switch {
			case exempt:
				// An exemption covering the default branch cancels the condition.
				matches = false
			case !exemptKnown:
				// It may cancel it: the condition cannot count as gating the
				// default branch until somebody can tell.
				known = false
			}
			rb.MatchesDefaultBranch = matches && known
		}
		rb.MatchUnknown = !known && repo.Available["defaultBranch"]
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

// listBranches reads every branch with the age of its tip commit, and decides
// whether the repository is empty: no branch at all.
//
// details=true is what carries the tip commit's time. Bitbucket 10.4 refuses
// it — 404 NoDefaultBranchException — for a repository whose configured
// default branch does not exist, because the ahead/behind metadata is computed
// against it. The plain listing still answers, and the per-commit lookup below
// supplies the times.
func (f *Fetcher) listBranches(ctx context.Context, base string, repo *scm.Repository) []apiBranch {
	branches, err := getPaged[apiBranch](ctx, f.client, base+"/branches", url.Values{"details": []string{"true"}})
	if err != nil && IsNotFound(err) {
		branches, err = getPaged[apiBranch](ctx, f.client, base+"/branches", nil)
	}
	if err != nil {
		repo.Available["branches"] = false
		repo.Available["branchAges"] = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("branches: %v", err))
		return nil
	}
	repo.Available["branches"] = true
	repo.Empty = len(branches) == 0

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
			// The listing carried no commit metadata; ask for the commit.
			branch.LatestCommitEpoch = f.fetchCommitEpoch(ctx, base, b.LatestCommit)
		}
		if branch.LatestCommitEpoch > 0 {
			branch.AgeDays = f.daysSince(branch.LatestCommitEpoch)
		} else {
			agesComplete = false
		}
		repo.Branches = append(repo.Branches, branch)
	}
	repo.Available["branchAges"] = agesComplete
	if !agesComplete {
		repo.Errors = append(repo.Errors, "commit timestamps unavailable for some branches; stale-branch rule reports MANUAL")
	}
	return branches
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

// fetchBaseAccess finds the highest permission every licensed user holds on
// the repository — its base permission, which is what CIS-1.3.8 judges.
//
// It used to be the project's default permission, probed through
// /projects/{key}/permissions/{perm}/all. That needs a project-admin token, so
// the read-only token the README recommends answered 401 and the control was
// MANUAL everywhere; and it never saw a grant to a group that holds everyone,
// which hands out access exactly as a default permission does.
//
// Bitbucket answers instead. /users with permission.1=LICENSED_USER and
// permission.2=<repository permission> lists the licensed users holding it, a
// subset of the licensed users; asking for the entry at the position of the
// last licensed user says whether the subset is all of them, in one request.
// The AND with LICENSED_USER is load-bearing: a permission list on its own
// also holds unlicensed accounts with a direct grant (measured on 10.4), and
// counting those would let a list missing real users look complete.
//
// The levels are walked upward and stop at the first one not everybody
// holds, so the snapshot records the exact base permission whatever ceiling a
// later evaluation applies: one request for a repository nobody has blanket
// access to, two when everybody can read it.
func (f *Fetcher) fetchBaseAccess(ctx context.Context, projectKey, slug string, repo *scm.Repository) {
	if !f.licensedKnown || f.licensedActive == 0 {
		repo.Permissions.DefaultPermissionKnown = false
		repo.Errors = append(repo.Errors, "licensed users unknown, so whether every one of them can reach this repository is unknown")
		return
	}
	highest := ""
	for _, perm := range []string{"REPO_READ", "REPO_WRITE", "REPO_ADMIN"} {
		query := url.Values{
			"permission.1":                []string{"LICENSED_USER"},
			"permission.2":                []string{perm},
			"permission.2.projectKey":     []string{projectKey},
			"permission.2.repositorySlug": []string{slug},
			// Counted against active licensed users: if Bitbucket listed an
			// inactive account here too, the subset could only reach the
			// count early — reporting a base permission that is broader than
			// the real one, never narrower.
			"start": []string{strconv.Itoa(f.licensedActive - 1)},
			"limit": []string{"1"},
		}
		var p page
		if err := f.client.get(ctx, "/api/1.0/users", query, &p); err != nil {
			repo.Permissions.DefaultPermissionKnown = false
			repo.Errors = append(repo.Errors, fmt.Sprintf("base access (%s): %v", perm, err))
			return
		}
		if len(p.Values) == 0 {
			break
		}
		highest = perm
	}
	repo.Permissions.DefaultPermission = highest
	repo.Permissions.DefaultPermissionKnown = true
}

// fetchRepositoryAdmins resolves who administers this repository, as
// Bitbucket itself decides it — repository, project and global grants, groups
// expanded — and keeps the people the repository has of its own: instance
// administrators administer every repository, so counting them made
// CIS-1.3.7 pass everywhere on any instance that satisfies CIS-1.3.3.
//
// The answer is complete only when the instance administrators are known too,
// since without them the set may still hold some, and a count that may be
// inflated proves neither a pass nor a fail.
func (f *Fetcher) fetchRepositoryAdmins(ctx context.Context, projectKey, slug string, repo *scm.Repository) {
	query := url.Values{
		"permission.1":                []string{"REPO_ADMIN"},
		"permission.1.projectKey":     []string{projectKey},
		"permission.1.repositorySlug": []string{slug},
	}
	users, err := getPaged[apiUser](ctx, f.client, "/api/1.0/users", query)
	if err != nil {
		repo.Available["admins"] = false
		repo.Errors = append(repo.Errors, fmt.Sprintf("repository administrators: %v", err))
		return
	}
	admins := scm.EffectivePrincipals{Complete: f.orgAdminsKnown}
	for _, u := range users {
		if u.Active && !f.orgAdmins[u.Name] {
			admins.Users = append(admins.Users, u.Name)
		}
	}
	sort.Strings(admins.Users)
	admins.Count = len(admins.Users)
	repo.Admins = admins
	repo.Available["admins"] = admins.Complete
	if !admins.Complete {
		repo.Errors = append(repo.Errors, "instance administrators unknown, so they could not be told apart from this repository's own")
	}
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
		f.warn("group %q could not be expanded (%v); counts derived from it are lower bounds", group, err)
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
	// repositories holds lowercased "project/slug" entries from --repository,
	// mapped to the spelling the user gave, for error messages.
	repositories map[string]string
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
	_, ok := t.repositories[strings.ToLower(projectKey+"/"+slug)]
	return ok
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
		repositories:  map[string]string{},
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
		t.repositories[strings.ToLower(r)] = r
		// Naming a repository implies scanning its project.
		addProject(key)
	}
	return t, nil
}
