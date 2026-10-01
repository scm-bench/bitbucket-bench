package bitbucketdc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scm-bench/bitbucket-bench/internal/config"
	"github.com/scm-bench/bitbucket-bench/internal/scm"
	"sync/atomic"
)

// fakeInstance is a stand-in Bitbucket Data Center. Handlers are keyed by the
// path below /rest, and anything unhandled returns 404 — which is exactly how
// an instance missing an optional add-on behaves, so the tests exercise that
// path for free.
type fakeInstance struct {
	t        *testing.T
	handlers map[string]http.HandlerFunc

	mu       sync.Mutex
	requests []string
	methods  map[string]bool
}

func newFakeInstance(t *testing.T) *fakeInstance {
	return &fakeInstance{t: t, handlers: map[string]http.HandlerFunc{}, methods: map[string]bool{}}
}

func (f *fakeInstance) handle(path string, h http.HandlerFunc) { f.handlers[path] = h }

func (f *fakeInstance) json(path string, body string) {
	f.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
}

// users answers /users the way Bitbucket does: filtered by whichever
// permission the query names, and paged by start/limit. Keys are the global
// permission ("ADMIN", "LICENSED_USER"), "PERM:PROJECT/slug" for a repository
// permission, "LICENSED_USER+PERM:PROJECT/slug" for the two ANDed, or "*" for
// an unfiltered list (the credential preflight). Values are a JSON array body.
func (f *fakeInstance) users(byPermission map[string]string) {
	f.handle("/api/1.0/users", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		key := q.Get("permission")
		if p := q.Get("permission.1"); p != "" {
			key = p + ":" + q.Get("permission.1.projectKey") + "/" + q.Get("permission.1.repositorySlug")
			if p2 := q.Get("permission.2"); p2 != "" {
				key = p + "+" + p2 + ":" + q.Get("permission.2.projectKey") + "/" + q.Get("permission.2.repositorySlug")
			}
		}
		if key == "" {
			key = "*"
		}
		var values []json.RawMessage
		if body := byPermission[key]; body != "" {
			if err := json.Unmarshal([]byte("["+body+"]"), &values); err != nil {
				f.t.Errorf("bad users fixture for %s: %v", key, err)
			}
		}
		start, limit := 0, len(values)
		fmt.Sscan(q.Get("start"), &start)
		fmt.Sscan(q.Get("limit"), &limit)
		if start > len(values) {
			start = len(values)
		}
		end := start + limit
		if end > len(values) {
			end = len(values)
		}
		page, _ := json.Marshal(map[string]any{
			"size": end - start, "limit": limit, "start": start,
			"isLastPage": end == len(values), "values": values[start:end],
		})
		w.Header().Set("Content-Type", "application/json")
		w.Write(page)
	})
}

// page wraps values in Bitbucket's pagination envelope.
func pageOf(values string) string {
	return fmt.Sprintf(`{"size":1,"limit":100,"isLastPage":true,"start":0,"values":[%s]}`, values)
}

func (f *fakeInstance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path)
	f.methods[r.Method] = true
	f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/rest")
	if h, ok := f.handlers[path]; ok {
		h(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"errors":[{"message":"not found"}]}`)
}

func (f *fakeInstance) start() *httptest.Server {
	server := httptest.NewServer(f)
	f.t.Cleanup(server.Close)
	return server
}

// standardInstance wires up a complete, well-configured instance.
func standardInstance(t *testing.T) *fakeInstance {
	f := newFakeInstance(t)

	// /users answers the preflight and every permission question. alice holds
	// SYS_ADMIN and bob ADMIN through bitbucket-admins; Bitbucket resolves the
	// group itself. carol administers PRJ/app of her own.
	f.users(map[string]string{
		"*":                  `{"name":"scanner","displayName":"Scanner","active":true}`,
		"ADMIN":              `{"name":"alice","active":true},{"name":"bob","active":true}`,
		"LICENSED_USER":      `{"name":"alice","active":true},{"name":"bob","active":true},{"name":"carol","active":true}`,
		"REPO_ADMIN:PRJ/app": `{"name":"alice","active":true},{"name":"bob","active":true},{"name":"carol","active":true}`,
	})
	f.json("/api/1.0/admin/permissions/users", pageOf(`{"user":{"name":"alice","displayName":"Alice","active":true},"permission":"SYS_ADMIN"}`))
	f.json("/api/1.0/admin/permissions/groups", pageOf(`{"group":{"name":"bitbucket-admins"},"permission":"ADMIN"}`))
	f.json("/api/1.0/admin/groups/more-members", pageOf(`{"name":"bob","displayName":"Bob","active":true}`))
	f.json("/api/1.0/admin/users", pageOf(`{"name":"alice","displayName":"Alice","active":true,"lastAuthenticationTimestamp":1767225600000}`))

	f.json("/api/1.0/projects", pageOf(`{"key":"PRJ","id":1,"name":"Project","public":false,"type":"NORMAL"}`))
	f.json("/api/1.0/projects/PRJ/permissions/users", pageOf(`{"user":{"name":"alice","active":true},"permission":"PROJECT_ADMIN"}`))
	f.json("/api/1.0/projects/PRJ/permissions/groups", pageOf(``))
	for _, perm := range []string{"PROJECT_ADMIN", "PROJECT_WRITE", "PROJECT_READ"} {
		f.json("/api/1.0/projects/PRJ/permissions/"+perm+"/all", `{"permitted":false}`)
	}

	f.json("/api/1.0/projects/PRJ/repos", pageOf(`{"slug":"app","id":10,"name":"app","public":false,"archived":false,"forkable":true,"project":{"key":"PRJ"}}`))
	f.json("/api/1.0/projects/PRJ/repos/app/default-branch", `{"id":"refs/heads/main","displayId":"main"}`)
	f.json("/branch-utils/latest/projects/PRJ/repos/app/branchmodel", `{"development":{"id":"refs/heads/main","displayId":"main"},"types":[]}`)

	// requiredApprovers arrives as an object here, the shape some Bitbucket
	// versions use, to prove the tolerant decoding does not silently zero it.
	f.json("/api/1.0/projects/PRJ/repos/app/settings/pull-requests", `{
		"requiredApprovers": {"count": 2, "enabled": true},
		"requiredAllTasksComplete": true,
		"requiredSuccessfulBuilds": 1,
		"unapproveOnUpdate": "true",
		"mergeConfig": {
			"defaultStrategy": {"id": "squash"},
			"strategies": [
				{"id": "squash", "name": "Squash"},
				{"id": "no-ff", "name": "Merge commit", "enabled": false}
			]
		}
	}`)

	f.json("/branch-permissions/2.0/projects/PRJ/repos/app/restrictions", pageOf(`{
		"id": 1,
		"type": {"id": "fast-forward-only", "name": "Prevent rewriting history"},
		"matcher": {"id": "refs/heads/main", "displayId": "main", "type": {"id": "BRANCH"}},
		"scope": {"type": "REPOSITORY", "resourceId": 10},
		"users": [{"name": "build-bot"}],
		"groups": []
	}`))

	f.json("/required-builds/latest/projects/PRJ/repos/app/conditions", pageOf(`{
		"id": 5,
		"buildParentKeys": ["CI-BUILD"],
		"refMatcher": {"id": "refs/heads/main", "displayId": "main", "type": {"id": "BRANCH"}}
	}`))

	f.json("/api/1.0/projects/PRJ/repos/app/settings/hooks", pageOf(`{
		"details": {"key": "com.example.gpg", "name": "GPG signature check", "type": "PRE_RECEIVE"},
		"enabled": true,
		"configured": true,
		"scope": {"type": "REPOSITORY"}
	}`))

	f.json("/api/1.0/projects/PRJ/repos/app/branches", pageOf(`{
		"id": "refs/heads/main",
		"displayId": "main",
		"isDefault": true,
		"latestCommit": "abc123",
		"metadata": {
			"com.atlassian.bitbucket.server.bitbucket-branch:latest-commit-metadata": {"committerTimestamp": 1767225600000}
		}
	}`))

	f.json("/api/1.0/projects/PRJ/repos/app/browse/SECURITY.md", `{"lines":[{"text":"# Security"}],"isLastPage":true}`)
	f.json("/api/1.0/projects/PRJ/repos/app/permissions/users", pageOf(`{"user":{"name":"carol","active":true},"permission":"REPO_ADMIN"}`))
	f.json("/api/1.0/projects/PRJ/repos/app/permissions/groups", pageOf(``))

	return f
}

func fetchSnapshot(t *testing.T, f *fakeInstance) (*fakeInstance, *scm.Snapshot) {
	t.Helper()
	return fetchSnapshotWithConfig(t, f, config.Default())
}

func fetchSnapshotWithConfig(t *testing.T, f *fakeInstance, cfg config.Config) (*fakeInstance, *scm.Snapshot) {
	t.Helper()
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Token: "test-token", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	fetcher := NewFetcher(client, cfg)
	snapshot, err := fetcher.Fetch(context.Background(), FetchOptions{
		ToolVersion: "test",
		Now:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Concurrency: 2,
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return f, snapshot
}

func TestFetchBuildsCompleteSnapshot(t *testing.T) {
	f, snapshot := fetchSnapshot(t, standardInstance(t))

	if len(snapshot.Projects) != 1 || len(snapshot.Projects[0].Repositories) != 1 {
		t.Fatalf("expected one project with one repository, got %+v", snapshot.Projects)
	}
	repo := snapshot.Projects[0].Repositories[0]

	if repo.FullName != "PRJ/app" {
		t.Errorf("FullName = %q, want PRJ/app", repo.FullName)
	}
	if repo.DefaultBranch != "refs/heads/main" || repo.DefaultBranchDisplay != "main" {
		t.Errorf("default branch = %q/%q", repo.DefaultBranch, repo.DefaultBranchDisplay)
	}

	// The object-shaped requiredApprovers must survive decoding: a zero here
	// would be a silent false FAIL on the most important control.
	if repo.PullRequestSettings.RequiredApprovers != 2 {
		t.Errorf("RequiredApprovers = %d, want 2", repo.PullRequestSettings.RequiredApprovers)
	}
	if !repo.PullRequestSettings.UnapproveOnUpdate {
		t.Error("UnapproveOnUpdate should decode from the string \"true\"")
	}
	if !repo.PullRequestSettings.RequiredAllTasksComplete {
		t.Error("RequiredAllTasksComplete = false, want true")
	}

	// A strategy with no "enabled" field is one Bitbucket has switched on.
	var squashEnabled, noFFEnabled bool
	for _, s := range repo.PullRequestSettings.MergeStrategies {
		switch s.ID {
		case "squash":
			squashEnabled = s.Enabled
		case "no-ff":
			noFFEnabled = s.Enabled
		}
	}
	if !squashEnabled {
		t.Error("squash strategy should default to enabled when the flag is absent")
	}
	if noFFEnabled {
		t.Error("no-ff strategy is explicitly disabled and must stay disabled")
	}

	if len(repo.BranchRestrictions) != 1 {
		t.Fatalf("expected one branch restriction, got %d", len(repo.BranchRestrictions))
	}
	restriction := repo.BranchRestrictions[0]
	if restriction.Type != "fast-forward-only" {
		t.Errorf("restriction type = %q", restriction.Type)
	}
	if !restriction.MatchesDefaultBranch {
		t.Error("a BRANCH matcher on refs/heads/main must resolve to the default branch")
	}
	if len(restriction.ExemptUsers) != 1 || restriction.ExemptUsers[0] != "build-bot" {
		t.Errorf("exempt users = %v, want [build-bot]", restriction.ExemptUsers)
	}

	if len(repo.RequiredBuilds) != 1 || !repo.RequiredBuilds[0].MatchesDefaultBranch {
		t.Errorf("required builds = %+v", repo.RequiredBuilds)
	}
	if len(repo.Hooks) != 1 || !repo.Hooks[0].Enabled {
		t.Errorf("hooks = %+v", repo.Hooks)
	}

	if len(repo.Branches) != 1 || repo.Branches[0].AgeDays != 0 {
		t.Errorf("branches = %+v, want one branch aged 0 days", repo.Branches)
	}
	if len(repo.Files.SecurityPolicyPaths) != 1 || repo.Files.SecurityPolicyPaths[0] != "SECURITY.md" {
		t.Errorf("security policy paths = %v", repo.Files.SecurityPolicyPaths)
	}

	// Bitbucket names alice, bob and carol as able to administer PRJ/app;
	// alice and bob are instance administrators, so the repository has one
	// administrator of its own.
	if repo.Admins.Count != 1 || !repo.Admins.Complete || !slices.Equal(repo.Admins.Users, []string{"carol"}) {
		t.Errorf("repository admins = %+v, want carol alone, complete", repo.Admins)
	}

	for key, want := range map[string]bool{
		"pullRequestSettings": true,
		"branchRestrictions":  true,
		"requiredBuilds":      true,
		"hooks":               true,
		"branches":            true,
		"branchAges":          true,
		"files":               true,
		"admins":              true,
	} {
		if repo.Available[key] != want {
			t.Errorf("Available[%q] = %v, want %v", key, repo.Available[key], want)
		}
	}

	org := snapshot.Organization
	if org.EffectiveAdmins.Count != 2 || !org.EffectiveAdmins.Complete {
		t.Errorf("organization admins = %+v, want alice + bob complete", org.EffectiveAdmins)
	}
	for key, want := range map[string]bool{"admins": true, "users": true, "userActivity": true, "licensedUsers": true} {
		if org.Available[key] != want {
			t.Errorf("Organization.Available[%q] = %v, want %v", key, org.Available[key], want)
		}
	}

	// Scanning must never mutate the instance it audits.
	f.mu.Lock()
	defer f.mu.Unlock()
	for method := range f.methods {
		if method != http.MethodGet {
			t.Errorf("fetcher issued a %s request; scanning must be read-only", method)
		}
	}
}

// A missing add-on must leave the control undecidable rather than looking like
// a repository that simply has no conditions configured.
func TestMissingRequiredBuildsApiMarksUnavailable(t *testing.T) {
	f := standardInstance(t)
	delete(f.handlers, "/required-builds/latest/projects/PRJ/repos/app/conditions")

	_, snapshot := fetchSnapshot(t, f)
	repo := snapshot.Projects[0].Repositories[0]

	if repo.Available["requiredBuilds"] {
		t.Error("requiredBuilds should be unavailable when both endpoint spellings 404")
	}
	if len(repo.Errors) == 0 {
		t.Error("an unavailable API should be recorded in the repository's errors")
	}
}

// The collection endpoint was renamed across releases; the older spelling must
// still be found.
func TestRequiredBuildsFallsBackToLegacyEndpoint(t *testing.T) {
	f := standardInstance(t)
	delete(f.handlers, "/required-builds/latest/projects/PRJ/repos/app/conditions")
	f.json("/required-builds/latest/projects/PRJ/repos/app/condition", pageOf(`{
		"id": 5,
		"buildParentKeys": ["CI-BUILD"],
		"refMatcher": {"id": "refs/heads/main", "displayId": "main", "type": {"id": "BRANCH"}}
	}`))

	_, snapshot := fetchSnapshot(t, f)
	repo := snapshot.Projects[0].Repositories[0]

	if !repo.Available["requiredBuilds"] || len(repo.RequiredBuilds) != 1 {
		t.Errorf("legacy endpoint not used: available=%v builds=%+v", repo.Available["requiredBuilds"], repo.RequiredBuilds)
	}
}

// A rejected credential must stop the scan. Degrading it the way a 403 is
// degraded would produce a full report in which every control reports MANUAL
// and the score is 0 — indistinguishable from a real audit result unless the
// reader notices that nothing at all could be read.
func TestRejectedCredentialsFailTheScan(t *testing.T) {
	f := newFakeInstance(t)
	f.handle("/api/1.0/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"Authentication failed"}]}`)
	})
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Token: "wrong", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{ToolVersion: "test"})
	if err == nil {
		t.Fatal("Fetch succeeded with credentials the instance rejected")
	}
	if !strings.Contains(err.Error(), "did not accept the credentials") {
		t.Errorf("error = %v, want it to name the credentials as the cause", err)
	}

	// The scan must give up immediately rather than walking the whole instance
	// with a credential that cannot work.
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 1 {
		t.Errorf("made %d requests after a 401 (%v); want only the credential check", len(f.requests), f.requests)
	}
}

// Measured on Bitbucket 10.4.1: a bearer token the instance does not recognise
// is not refused, it is served as anonymous. /application-properties — what
// the preflight used to ask — answers anonymous callers with a 200, so a
// revoked token sailed through it and every later 401 was filed as a missing
// permission. The preflight now asks an endpoint anonymous callers cannot read.
func TestUnrecognisedTokenServedAsAnonymousFailsTheScan(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/application-properties", `{"version":"10.4.1","displayName":"Bitbucket"}`)
	f.handle("/api/1.0/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource","exceptionName":"com.atlassian.plugins.rest.api.security.exception.AuthenticationRequiredException"}]}`)
	})
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Token: "revoked", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "did not accept the credentials") {
		t.Fatalf("err = %v, want the scan refused for a credential served as anonymous", err)
	}
}

// Bitbucket 10 ships with password authentication disabled on the REST API and
// answers a basic-auth request with a 403 and one sentence. The scan has to
// say what to do instead, not just repeat the sentence.
func TestDisabledBasicAuthenticationNamesTheFix(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Basic Authentication has been disabled on this instance."}`)
	})
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Username: "admin", Password: "pw", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{})
	if err == nil {
		t.Fatal("Fetch succeeded although the instance refused basic authentication")
	}
	for _, want := range []string{"does not accept passwords", "HTTP access token", "--token", "Basic Authentication has been disabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A base URL missing its context path reaches a web server that is not
// Bitbucket's REST API. That is a typo in --url, and saying so beats the
// generic 404 every later request would produce.
func TestWrongBaseURLIsReportedAsSuch(t *testing.T) {
	f := newFakeInstance(t) // nothing handled: every path is a 404
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Token: "t", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "check --url") {
		t.Fatalf("err = %v, want it to point at --url", err)
	}
}

// This is where TestUnauthorizedIsNotTreatedAsUnavailable used to sit. It
// asserted that a 401 partway through was fatal, on the reasoning that a 401
// always means the credential was rejected. A real instance disproved that: a
// project administrator's credential passed the preflight and was answered 401
// by /admin/permissions/users, so "fatal" cost them the entire report.
//
// The rule it was protecting is real, and is now split across the two tests
// below by when the 401 arrives rather than by status code alone — fatal
// before the credential is proven, MANUAL after.

// A token without admin rights must degrade to MANUAL-able gaps, not an error.
func TestMissingAdminAccessDegradesGracefully(t *testing.T) {
	f := standardInstance(t)
	forbidden := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource"}]}`)
	}
	f.handle("/api/1.0/admin/permissions/users", forbidden)
	f.handle("/api/1.0/admin/permissions/groups", forbidden)
	f.handle("/api/1.0/admin/users", forbidden)

	_, snapshot := fetchSnapshot(t, f)

	if snapshot.Organization.Available["adminGrants"] || snapshot.Organization.Available["users"] {
		t.Error("forbidden admin endpoints must be marked unavailable")
	}
	if len(snapshot.Metadata.Warnings) == 0 {
		t.Error("a forbidden admin endpoint should produce a scan warning")
	}
	// The repository scan must still have completed.
	if len(snapshot.Projects[0].Repositories) != 1 {
		t.Error("repository scanning should continue without admin rights")
	}
}

// Reported from a real instance: a project administrator got two requests in
// and no report at all.
//
//	GET /application-properties   200
//	GET /admin/permissions/users  401  You are not permitted to access this resource
//
// Bitbucket answers 401 rather than 403 on the admin endpoints for a user who
// is authenticated but is not an instance administrator, and 401 was fatal
// everywhere. The preflight had already proved the credential works, so this
// 401 could only ever have been an authorization decision.
func TestUnauthorizedAdminEndpointsDegradeOnceCredentialsAreProven(t *testing.T) {
	f := standardInstance(t)
	unauthorized := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource"}]}`)
	}
	f.handle("/api/1.0/admin/permissions/users", unauthorized)
	f.handle("/api/1.0/admin/permissions/groups", unauthorized)
	f.handle("/api/1.0/admin/users", unauthorized)

	_, snapshot := fetchSnapshot(t, f)

	if snapshot.Organization.Available["adminGrants"] || snapshot.Organization.Available["users"] {
		t.Error("unauthorized admin endpoints must be marked unavailable, so the controls report MANUAL")
	}
	if len(snapshot.Metadata.Warnings) == 0 {
		t.Error("an unauthorized admin endpoint should produce a scan warning")
	}
	// The whole point: the report the user asked for still gets written.
	if len(snapshot.Projects[0].Repositories) != 1 {
		t.Error("repository scanning should continue when only the admin endpoints are refused")
	}
}

// The other half of the same rule. A 401 before the credential has been proven
// is the credential being rejected, and must stay fatal — degrading it would
// turn a mistyped token into an all-MANUAL report that reads like a finding
// about the instance.
func TestUnauthorizedPreflightIsStillFatal(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"Authentication failed"}]}`)
	})

	server := f.start()
	client, err := NewClient(Options{BaseURL: server.URL, Token: "wrong", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{}); err == nil {
		t.Fatal("a 401 from the preflight must fail the scan, not degrade to MANUAL")
	}
}

// A large instance spends minutes in fetchRepositories with nothing to show
// for it. The callback is what turns that into a sign of life, so it has to
// fire once per repository and be safe to call from the fetch goroutines.
func TestProgressReportsEveryRepository(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PLAT/repos", `{"size":3,"limit":100,"isLastPage":true,"start":0,"values":[
		{"slug":"app","id":10,"name":"app","project":{"key":"PRJ"}},
		{"slug":"web","id":11,"name":"web","project":{"key":"PRJ"}},
		{"slug":"api","id":12,"name":"api","project":{"key":"PRJ"}}
	]}`)
	f.json("/api/1.0/projects/PRJ/repos", `{"size":3,"limit":100,"isLastPage":true,"start":0,"values":[
		{"slug":"app","id":10,"name":"app","project":{"key":"PRJ"}},
		{"slug":"web","id":11,"name":"web","project":{"key":"PRJ"}},
		{"slug":"api","id":12,"name":"api","project":{"key":"PRJ"}}
	]}`)
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Token: "test-token", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var mu sync.Mutex
	var lines []string
	_, err = NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{
		ToolVersion: "test",
		Concurrency: 3,
		Progress: func(line string) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, line)
		},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if len(lines) != 3 {
		t.Fatalf("got %d progress lines for 3 repositories: %v", len(lines), lines)
	}
	// The counter is what makes the line useful, so the last one must show the
	// scan reaching its end rather than stalling partway.
	var sawFinal bool
	for _, l := range lines {
		if strings.Contains(l, "3/3 repositories") {
			sawFinal = true
		}
	}
	if !sawFinal {
		t.Errorf("no line reported the final count: %v", lines)
	}
}

// Nothing should be formatted when nobody is watching.
func TestProgressIsOptional(t *testing.T) {
	f := standardInstance(t)
	if _, snapshot := fetchSnapshot(t, f); len(snapshot.Projects) == 0 {
		t.Error("a fetch without a progress callback should still work")
	}
}

func TestExhaustivePaginationFollowsNextPageStart(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/projects/PRJ/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("start") == "" {
			fmt.Fprint(w, `{"size":1,"limit":100,"isLastPage":false,"start":0,"nextPageStart":1,"values":[
				{"slug":"app","id":10,"name":"app","project":{"key":"PRJ"}}
			]}`)
			return
		}
		fmt.Fprint(w, `{"size":1,"limit":100,"isLastPage":true,"start":1,"values":[
			{"slug":"second","id":11,"name":"second","project":{"key":"PRJ"}}
		]}`)
	})

	_, snapshot := fetchSnapshot(t, f)
	if got := len(snapshot.Projects[0].Repositories); got != 2 {
		t.Fatalf("fetched %d repositories, want 2 across two pages", got)
	}
}

// An archived repository is reported rather than dropped, so the report
// accounts for every repository the token can see — but none of its settings
// are fetched: every control about changes is NA for it, and the read-access
// control needs only the public flag and the project's default permission.
func TestArchivedRepositoriesAreReportedWithoutFetchingTheirSettings(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos", pageOf(`{"slug":"app","id":10,"name":"app","archived":true,"public":true,"project":{"key":"PRJ"}}`))

	_, snapshot := fetchSnapshot(t, f)
	repos := snapshot.Projects[0].Repositories
	if len(repos) != 1 || !repos[0].Archived {
		t.Fatalf("repositories = %+v, want the archived one reported", repos)
	}
	if !repos[0].Permissions.PublicAccess {
		t.Error("an archived public repository must still say it is public")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, path := range f.requests {
		if strings.Contains(path, "/repos/app/") || strings.Contains(path, "/repos/app?") {
			t.Errorf("fetched %s for an archived repository", path)
		}
	}
}

func TestArchivedRepositoriesCanBeLeftOut(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos", pageOf(`{"slug":"app","id":10,"name":"app","archived":true,"project":{"key":"PRJ"}}`))

	cfg := config.Default()
	cfg.SkipArchivedRepositories = true
	_, snapshot := fetchSnapshotWithConfig(t, f, cfg)
	if len(snapshot.Projects[0].Repositories) != 0 {
		t.Error("archived repositories should be left out when skipArchivedRepositories is on")
	}
}

func TestPublicProjectMakesRepositoryPublic(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects", pageOf(`{"key":"PRJ","id":1,"name":"Project","public":true,"type":"NORMAL"}`))

	_, snapshot := fetchSnapshot(t, f)
	if !snapshot.Projects[0].Repositories[0].Public {
		t.Error("a repository in a public project is anonymously readable and must be marked public")
	}
}

func TestClientRejectsMissingCredentials(t *testing.T) {
	if _, err := NewClient(Options{BaseURL: "https://bitbucket.example.com"}); err == nil {
		t.Error("a client with no token and no username should be rejected")
	}
	if _, err := NewClient(Options{Token: "x"}); err == nil {
		t.Error("a client with no base URL should be rejected")
	}
}

func TestClientNormalizesBaseURL(t *testing.T) {
	for _, in := range []string{
		"https://bitbucket.example.com",
		"https://bitbucket.example.com/",
		"https://bitbucket.example.com/rest",
		"bitbucket.example.com",
	} {
		client, err := NewClient(Options{BaseURL: in, Token: "x"})
		if err != nil {
			t.Fatalf("NewClient(%q): %v", in, err)
		}
		if got := client.BaseURL(); got != "https://bitbucket.example.com" {
			t.Errorf("NewClient(%q).BaseURL() = %q", in, got)
		}
	}
}

// The credential this tool asks for can read every repository on the instance,
// so putting it on the wire in the clear is refused rather than warned about:
// by the time a warning could be printed the token is already gone.
func TestPlaintextURLIsRefused(t *testing.T) {
	for _, in := range []string{
		"http://bitbucket.example.com",
		"http://bitbucket.example.com:7990/rest",
		"http://192.0.2.10:7990",
	} {
		if _, err := NewClient(Options{BaseURL: in, Token: "x"}); err == nil {
			t.Errorf("NewClient(%q) succeeded; cleartext credentials must be refused", in)
		} else if !strings.Contains(err.Error(), "--allow-plaintext") {
			t.Errorf("NewClient(%q) error = %v; it should name the flag that overrides it", in, err)
		}
	}
}

// Loopback traffic does not leave the machine, so the threat the check exists
// for does not apply — and refusing it would make local servers unusable.
func TestLoopbackPlaintextIsAllowedWithoutAWarning(t *testing.T) {
	for _, in := range []string{
		"http://localhost:7990",
		"http://127.0.0.1:7990",
		"http://[::1]:7990",
	} {
		client, err := NewClient(Options{BaseURL: in, Token: "x"})
		if err != nil {
			t.Errorf("NewClient(%q): %v", in, err)
			continue
		}
		if w := client.TransportWarnings(); len(w) != 0 {
			t.Errorf("NewClient(%q) warned about loopback plaintext: %v", in, w)
		}
	}
}

// Opting out of a protection has to leave a mark in the report, and in any
// snapshot archived from it: a scan captured over cleartext or without
// certificate verification is not the same evidence as one that was not.
func TestOptedOutProtectionsAreRecordedAsWarnings(t *testing.T) {
	plaintext, err := NewClient(Options{BaseURL: "http://bitbucket.example.com", Token: "x", AllowPlaintext: true})
	if err != nil {
		t.Fatalf("NewClient with AllowPlaintext: %v", err)
	}
	if w := plaintext.TransportWarnings(); len(w) != 1 || !strings.Contains(w[0], "cleartext") {
		t.Errorf("TransportWarnings() = %v, want one naming cleartext", w)
	}

	insecure, err := NewClient(Options{BaseURL: "https://bitbucket.example.com", Token: "x", Insecure: true})
	if err != nil {
		t.Fatalf("NewClient with Insecure: %v", err)
	}
	if w := insecure.TransportWarnings(); len(w) != 1 || !strings.Contains(w[0], "intercepted") {
		t.Errorf("TransportWarnings() = %v, want one naming interception", w)
	}

	clean, err := NewClient(Options{BaseURL: "https://bitbucket.example.com", Token: "x"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if w := clean.TransportWarnings(); len(w) != 0 {
		t.Errorf("an ordinary https client warned about nothing in particular: %v", w)
	}
}

// The warnings have to reach the snapshot, not just the client, or a snapshot
// re-evaluated later would look like it was captured safely.
func TestTransportWarningsReachTheSnapshot(t *testing.T) {
	f := standardInstance(t)
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Token: "test-token", Insecure: true, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	snapshot, err := NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{ToolVersion: "test"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	var found bool
	for _, w := range snapshot.Metadata.Warnings {
		if strings.Contains(w, "intercepted") {
			found = true
		}
	}
	if !found {
		t.Errorf("snapshot warnings = %v, want one recording that --insecure was used", snapshot.Metadata.Warnings)
	}
}

func TestErrorMessagesAreExtractedFromEnvelope(t *testing.T) {
	body := []byte(`{"errors":[{"context":null,"message":"Repository does not exist","exceptionName":"x"}]}`)
	msgs := parseErrorMessages(body)
	if len(msgs) != 1 || msgs[0] != "Repository does not exist" {
		t.Errorf("parseErrorMessages = %v", msgs)
	}
}

func TestFlexibleFieldDecoding(t *testing.T) {
	var settings apiPullRequestSettings
	raw := `{"requiredApprovers":"3","requiredAllTasksComplete":{"enabled":true},"unapproveOnUpdate":1}`
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if settings.RequiredApprovers.Int() != 3 {
		t.Errorf("numeric string requiredApprovers = %d, want 3", settings.RequiredApprovers.Int())
	}
	if !settings.RequiredAllTasksComplete.Bool() {
		t.Error("object-shaped requiredAllTasksComplete should decode to true")
	}
}

// A repository with commits whose default branch cannot be resolved was never
// browsed. Marking the file probe "available" would let the security-policy
// control report "nothing found" having looked nowhere.
func TestUnresolvedDefaultBranchMarksFilesUnavailable(t *testing.T) {
	f := standardInstance(t)
	forbidden := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource"}]}`)
	}
	f.handle("/api/1.0/projects/PRJ/repos/app/default-branch", forbidden)
	f.handle("/api/1.0/projects/PRJ/repos/app/branches/default", forbidden)
	// The repository has branches, so it is not empty — but none is flagged as
	// the default, so the default branch stays unknown.
	f.json("/api/1.0/projects/PRJ/repos/app/branches", pageOf(`{
		"id": "refs/heads/main", "displayId": "main", "isDefault": false, "latestCommit": "abc123"
	}`))

	_, snapshot := fetchSnapshot(t, f)
	repo := snapshot.Projects[0].Repositories[0]

	if repo.Empty {
		t.Fatal("a repository with branches must not be reported as empty")
	}
	if repo.DefaultBranch != "" {
		t.Fatalf("default branch = %q, want unresolved for this fixture", repo.DefaultBranch)
	}
	if repo.Available["files"] {
		t.Error("files must be unavailable when no path could be browsed")
	}
	if len(repo.Files.SecurityPolicyPaths) != 0 || len(repo.Files.Probed) != 0 {
		t.Errorf("nothing should have been probed: %+v", repo.Files)
	}
}

// An empty repository is a known state, not an unknown one: the control
// reports NA rather than MANUAL.
func TestEmptyRepositoryMarksFilesAvailable(t *testing.T) {
	f := standardInstance(t)
	notFound := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"errors":[{"message":"Repository is empty"}]}`)
	}
	f.handle("/api/1.0/projects/PRJ/repos/app/default-branch", notFound)
	f.handle("/api/1.0/projects/PRJ/repos/app/branches/default", notFound)
	f.json("/api/1.0/projects/PRJ/repos/app/branches", pageOf(``))

	_, snapshot := fetchSnapshot(t, f)
	repo := snapshot.Projects[0].Repositories[0]

	if !repo.Empty {
		t.Fatal("a repository with no branches should be reported as empty")
	}
	if !repo.Available["files"] {
		t.Error("an empty repository is a known state, so files should be available")
	}
}

// The shapes below are Bitbucket 10.4.1's, recorded against a real instance.
// /default-branch reports the configured branch whether or not anything was
// ever pushed, so an empty repository answers 200 refs/heads/master — and was
// taken for one with commits, failing its branch rules instead of reporting NA.
func TestEmptyRepositoryIsDecidedFromTheBranchList(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos/app/default-branch", `{"id":"refs/heads/master","displayId":"master","type":"BRANCH"}`)
	f.handle("/api/1.0/projects/PRJ/repos/app/branches/default", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	f.json("/api/1.0/projects/PRJ/repos/app/branches", `{"size":0,"limit":100,"isLastPage":true,"values":[],"start":0}`)

	_, snapshot := fetchSnapshot(t, f)
	repo := snapshot.Projects[0].Repositories[0]

	if !repo.Empty {
		t.Fatal("a repository with no branches must be reported as empty, whatever /default-branch says")
	}
	if !repo.Available["defaultBranch"] || repo.DefaultBranchDisplay != "master" {
		t.Errorf("default branch = %q (available %v), want the configured master, known",
			repo.DefaultBranchDisplay, repo.Available["defaultBranch"])
	}
	if !repo.Available["files"] {
		t.Error("an empty repository is a known state, so files should be available")
	}
}

// A repository configured with a default branch nobody pushed: the instance
// default stayed "master" while git's moved to "main". Bitbucket 10.4 answers
// /default-branch with the missing master and refuses branches?details=true
// outright, because ahead/behind is computed against the default branch.
//
// Before: the scan judged protection on master, probed master for SECURITY.md
// and failed CIS-1.2.1 for a repository that has one, and lost every branch age
// with the details listing.
func TestConfiguredDefaultBranchThatDoesNotExist(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos/app/default-branch", `{"id":"refs/heads/master","displayId":"master","type":"BRANCH"}`)
	f.handle("/api/1.0/projects/PRJ/repos/app/branches", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("details") == "true" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errors":[{"message":"No default branch is defined","exceptionName":"com.atlassian.bitbucket.repository.NoDefaultBranchException"}]}`)
			return
		}
		fmt.Fprint(w, pageOf(`{"id":"refs/heads/main","displayId":"main","type":"BRANCH","latestCommit":"abc123","isDefault":false}`))
	})
	f.json("/api/1.0/projects/PRJ/repos/app/commits/abc123", `{"id":"abc123","committerTimestamp":1767139200000,"authorTimestamp":1767139200000}`)

	_, snapshot := fetchSnapshot(t, f)
	repo := snapshot.Projects[0].Repositories[0]

	if repo.Empty {
		t.Fatal("a repository with a branch is not empty")
	}
	if repo.Available["defaultBranch"] || repo.DefaultBranch != "" {
		t.Errorf("default branch = %q (available %v); a branch that does not exist must not be judged",
			repo.DefaultBranch, repo.Available["defaultBranch"])
	}
	if !slices.ContainsFunc(repo.Errors, func(e string) bool { return strings.Contains(e, "master does not exist") }) {
		t.Errorf("errors %q do not say the configured branch is missing", repo.Errors)
	}
	if repo.Available["files"] {
		t.Error("no default branch exists to look for a security policy on, so files must be unavailable")
	}
	if !repo.Available["branchAges"] || len(repo.Branches) != 1 || repo.Branches[0].AgeDays != 1 {
		t.Errorf("branches = %+v (ages available %v), want main dated through the commit lookup",
			repo.Branches, repo.Available["branchAges"])
	}
}

// --project is the flow most scans actually use, and it had no coverage at
// all: the narrowing path fetches each named project directly instead of
// listing them, which is a different code path from a full scan.
func TestScanNarrowedToProjectsFetchesOnlyThose(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ", `{"key":"PRJ","id":1,"name":"Project","public":false,"type":"NORMAL"}`)
	f.json("/api/1.0/projects/OTHER", `{"key":"OTHER","id":2,"name":"Other","public":false,"type":"NORMAL"}`)
	f.json("/api/1.0/projects/OTHER/repos", `{"size":0,"limit":100,"isLastPage":true,"start":0,"values":[]}`)

	server := f.start()
	client, err := NewClient(Options{BaseURL: server.URL, Token: "t", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	// Deliberately out of order: the keys are sorted before fetching so a scan
	// of the same projects issues its requests in the same order every time,
	// which is what makes a request trace comparable between runs.
	snapshot, err := NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{
		Projects: []string{"OTHER", "PRJ"},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	var keys []string
	for _, p := range snapshot.Projects {
		keys = append(keys, p.Key)
	}
	if len(keys) != 2 || keys[0] != "OTHER" || keys[1] != "PRJ" {
		t.Errorf("projects = %v, want [OTHER PRJ] in sorted order", keys)
	}

	// The listing endpoint must not be touched: asking for two projects and
	// enumerating the whole instance are very different requests to make with
	// somebody's token.
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if strings.HasSuffix(r, "/rest/api/1.0/projects") {
			t.Errorf("narrowed scan listed every project anyway:\n%v", f.requests)
		}
	}
}

// A project that cannot be read is fatal when it was asked for by name: there
// is nothing left to audit, and a report full of MANUAL would read like a
// finding about the instance rather than a permissions problem.
func TestNarrowedScanFailsWhenTheProjectCannotBeRead(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/projects/PRJ", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource"}]}`)
	})

	server := f.start()
	client, err := NewClient(Options{BaseURL: server.URL, Token: "t", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{Projects: []string{"PRJ"}})
	if err == nil {
		t.Fatal("a named project that cannot be read must fail the scan")
	}
	if !strings.Contains(err.Error(), "PRJ") {
		t.Errorf("error should name the project: %v", err)
	}
}

// --project and --repository are both additive includes, and they used to
// share one filter. `--project PLATFORM --repository OTHER/app` therefore
// scanned nothing in PLATFORM: naming any repository switched the filter on
// for every project, and PLATFORM had no entry in it. The project was still
// fetched and still landed in the snapshot, just empty — so its controls did
// not report MANUAL, they disappeared, and what came back read as a clean scan
// of a project nobody had looked at.
func TestProjectAndRepositoryFiltersAreAdditive(t *testing.T) {
	whole := targets{
		projects:      map[string]string{"platform": "PLATFORM", "other": "OTHER"},
		wholeProjects: map[string]bool{"platform": true},
		repositories:  map[string]string{"other/app": "OTHER/app"},
	}
	for _, tc := range []struct {
		project, slug string
		want          bool
	}{
		// Named by --project: everything under it is in scope.
		{"PLATFORM", "api", true},
		{"PLATFORM", "web", true},
		// Named by --repository: only that repository.
		{"OTHER", "app", true},
		{"OTHER", "unrelated", false},
		// Bitbucket's REST paths are case-insensitive, so the comparison is too.
		{"platform", "api", true},
		{"other", "APP", true},
	} {
		if got := whole.selects(tc.project, tc.slug); got != tc.want {
			t.Errorf("selects(%q, %q) = %v, want %v", tc.project, tc.slug, got, tc.want)
		}
	}

	// No --repository at all leaves every repository of the named projects in.
	none := targets{projects: map[string]string{"p": "P"}, wholeProjects: map[string]bool{"p": true}}
	if !none.selects("P", "anything") {
		t.Error("with no --repository, every repository of a named project is in scope")
	}
}

// `-p PRJ -r prj/app` names one project, not two. Treating the spellings as
// distinct fetched it twice, put it in the snapshot twice, and doubled every
// finding and every request under it.
func TestParseTargetsFoldsProjectKeyCase(t *testing.T) {
	got, err := parseTargets(FetchOptions{
		Projects:     []string{"PRJ", "prj", " PRJ "},
		Repositories: []string{"pRj/app"},
	})
	if err != nil {
		t.Fatalf("parseTargets: %v", err)
	}
	if len(got.projects) != 1 {
		t.Errorf("projects = %v, want a single entry", got.projects)
	}
	if keys := got.keys(); len(keys) != 1 || keys[0] != "PRJ" {
		t.Errorf("keys() = %v, want [PRJ]: the first spelling given is the one requested", keys)
	}
}

// A --repository that names no project used to be dropped in silence, which
// left the repository filter empty. An empty filter does not mean "that one
// repository", it means no filter — so asking for one repository scanned the
// whole instance.
func TestParseTargetsRejectsMalformedRepository(t *testing.T) {
	for _, bad := range []string{"payments-api", "PRJ/", "/app", "   /   "} {
		if _, err := parseTargets(FetchOptions{Repositories: []string{bad}}); err == nil {
			t.Errorf("parseTargets accepted --repository %q", bad)
		}
	}
	if _, err := parseTargets(FetchOptions{Repositories: []string{"PRJ/app"}}); err != nil {
		t.Errorf("parseTargets rejected a valid entry: %v", err)
	}
}

// A scan that covered no repository renders as a clean instance: the
// repository-scope controls simply have nothing to report, the instance-scope
// ones carry the score alone, and the summary looks like an audit. One
// mistyped --project key produces it, and nothing else in the output says so.
func TestScanningNoRepositoriesIsWarnedAbout(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos", pageOf(``))

	_, snapshot := fetchSnapshot(t, f)

	var warned bool
	for _, w := range snapshot.Metadata.Warnings {
		if strings.Contains(w, "0 repositories") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning that the scan covered nothing: %v", snapshot.Metadata.Warnings)
	}
}

// The ordinary case must not carry the warning, or it stops meaning anything.
func TestScanningRepositoriesIsNotWarnedAbout(t *testing.T) {
	_, snapshot := fetchSnapshot(t, standardInstance(t))
	for _, w := range snapshot.Metadata.Warnings {
		if strings.Contains(w, "0 repositories") {
			t.Errorf("warned about an empty scan that was not empty: %q", w)
		}
	}
}

// firstRepository is the repository every standardInstance test operates on.
func firstRepository(t *testing.T, snapshot *scm.Snapshot) scm.Repository {
	t.Helper()
	for _, p := range snapshot.Projects {
		for _, r := range p.Repositories {
			return r
		}
	}
	t.Fatal("snapshot carries no repositories")
	return scm.Repository{}
}

// A restriction exempting a group hides how many people it lets through: the
// group name is what the API returns, and the count that decides whether the
// protection still binds anyone is the membership behind it. Expanding happens
// here because a policy may not make an API call.
func TestExemptGroupsAreExpandedToPeople(t *testing.T) {
	f := standardInstance(t)
	f.json("/branch-permissions/2.0/projects/PRJ/repos/app/restrictions", pageOf(`{
		"id": 1,
		"type": {"id": "no-deletes", "name": "Prevent deletion"},
		"matcher": {"id": "refs/heads/main", "displayId": "main", "type": {"id": "BRANCH"}},
		"scope": {"type": "REPOSITORY", "resourceId": 10},
		"users": [{"name": "build-bot"}],
		"groups": ["developers"],
		"accessKeys": [
			{"key": {"id": 7, "label": "deploy-one", "text": "ssh-ed25519 AAAA deploy-one"}},
			{"key": {"id": 9, "label": "deploy-two", "text": "ssh-ed25519 AAAA deploy-two"}}
		]
	}`))
	f.handle("/api/1.0/admin/groups/more-members", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("context") {
		case "developers":
			fmt.Fprint(w, pageOf(`{"name":"eve","displayName":"Eve","active":true}`))
		default:
			fmt.Fprint(w, pageOf(`{"name":"bob","displayName":"Bob","active":true}`))
		}
	})

	_, snapshot := fetchSnapshot(t, f)
	repo := firstRepository(t, snapshot)
	if len(repo.BranchRestrictions) != 1 {
		t.Fatalf("got %d restrictions, want 1", len(repo.BranchRestrictions))
	}
	br := repo.BranchRestrictions[0]

	if !br.ExemptPrincipals.Complete {
		t.Error("every exempt group expanded, but the set is marked incomplete")
	}
	for _, want := range []string{"build-bot", "eve"} {
		if !slices.Contains(br.ExemptPrincipals.Users, want) {
			t.Errorf("exempt principals %v do not include %q", br.ExemptPrincipals.Users, want)
		}
	}
	if !slices.Contains(br.ExemptPrincipals.Groups, "developers") {
		t.Errorf("exempt groups %v do not name developers", br.ExemptPrincipals.Groups)
	}
	// The identities, not only the total: a rule asks whether the same key
	// bypasses every restriction covering the branch.
	if !slices.Equal(br.ExemptAccessKeyIDs, []int{7, 9}) {
		t.Errorf("exempt access key ids = %v, want [7 9]", br.ExemptAccessKeyIDs)
	}
	if br.ExemptAccessKeys != 2 {
		t.Errorf("exempt access keys = %d, want 2", br.ExemptAccessKeys)
	}
}

// A group the token cannot expand makes the bypass set a lower bound. The
// fetcher records that rather than reporting the members it happened to see as
// though they were all of them — the rule turns it into MANUAL from here.
func TestUnexpandableExemptGroupLeavesTheBypassSetIncomplete(t *testing.T) {
	f := standardInstance(t)
	f.json("/branch-permissions/2.0/projects/PRJ/repos/app/restrictions", pageOf(`{
		"id": 1,
		"type": {"id": "no-deletes", "name": "Prevent deletion"},
		"matcher": {"id": "refs/heads/main", "displayId": "main", "type": {"id": "BRANCH"}},
		"scope": {"type": "REPOSITORY", "resourceId": 10},
		"users": [],
		"groups": ["contractors"]
	}`))
	f.handle("/api/1.0/admin/groups/more-members", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("context") == "contractors" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, pageOf(`{"name":"bob","displayName":"Bob","active":true}`))
	})

	_, snapshot := fetchSnapshot(t, f)
	br := firstRepository(t, snapshot).BranchRestrictions[0]

	if br.ExemptPrincipals.Complete {
		t.Error("a group that could not be expanded left the bypass set marked complete")
	}
	if len(br.ExemptPrincipals.Users) != 0 {
		t.Errorf("exempt principals = %v, want none resolved", br.ExemptPrincipals.Users)
	}
	if !slices.Contains(br.ExemptPrincipals.Groups, "contractors") {
		t.Error("the unexpandable group is not named in the snapshot")
	}
}

// Bitbucket 10.4.1's /settings/pull-requests, verbatim apart from the merge
// strategies: no unapproveOnUpdate key at all, because the setting belongs to
// the separately installed Auto Unapprove app. Absence is recorded so the
// report can name the app instead of an unticked box.
func TestUnreportedApprovalResetIsRecorded(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos/app/settings/pull-requests", `{
		"mergeConfig": {"defaultStrategy": {"id": "no-ff"}, "strategies": [{"id": "no-ff", "enabled": true}], "type": "DEFAULT"},
		"com.atlassian.bitbucket.server.bitbucket-bundled-hooks:requiredApprovers": {"enable": true, "count": 2},
		"requiredAllApprovers": false,
		"needsWork": false,
		"requiredApprovers": 2,
		"requiredAllTasksComplete": false,
		"com.atlassian.bitbucket.server.bitbucket-build:requiredBuilds": {"enable": false, "count": 0},
		"requiredSuccessfulBuilds": 0
	}`)

	_, snapshot := fetchSnapshot(t, f)
	repo := snapshot.Projects[0].Repositories[0]
	if !repo.Available["pullRequestSettings"] || repo.Available["unapproveOnUpdate"] {
		t.Errorf("available = %v, want pull request settings read and the approval reset recorded as unreported", repo.Available)
	}
	if repo.PullRequestSettings.RequiredApprovers != 2 {
		t.Errorf("requiredApprovers = %d, want 2", repo.PullRequestSettings.RequiredApprovers)
	}
}

// No HTTP access token can carry a global permission, and Bitbucket 10 refuses
// passwords on its REST API by default, so the global grant table is out of
// reach of every credential the README recommends. Bitbucket answers "who
// holds ADMIN" itself, for any authenticated caller, groups resolved and
// SYS_ADMIN included. An inactive account administers nothing.
func TestInstanceAdministratorsAreResolvedByBitbucket(t *testing.T) {
	f := standardInstance(t)
	f.users(map[string]string{
		"*":                  `{"name":"scanner","active":true}`,
		"ADMIN":              `{"name":"alice","active":true},{"name":"gone","active":false},{"name":"bob","active":true}`,
		"LICENSED_USER":      `{"name":"alice","active":true}`,
		"REPO_ADMIN:PRJ/app": `{"name":"alice","active":true}`,
	})
	unauthorized := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource"}]}`)
	}
	f.handle("/api/1.0/admin/permissions/users", unauthorized)
	f.handle("/api/1.0/admin/permissions/groups", unauthorized)

	_, snapshot := fetchSnapshot(t, f)
	org := snapshot.Organization
	if !org.Available["admins"] || !org.EffectiveAdmins.Complete || !slices.Equal(org.EffectiveAdmins.Users, []string{"alice", "bob"}) {
		t.Errorf("effective admins = %+v (available %v), want alice and bob, complete", org.EffectiveAdmins, org.Available["admins"])
	}
	if org.Available["adminGrants"] {
		t.Error("the grant table answered 401 and must not be marked readable")
	}
	for _, w := range snapshot.Metadata.Warnings {
		if strings.Contains(w, "permissions/users") {
			t.Errorf("an unreadable grant table is not worth a warning when no token can read it: %q", w)
		}
	}
}

// A repository's administrators can only be told from the instance's once the
// instance's are known; without them the set may be inflated, and the
// repository's count is marked unknown rather than trusted.
func TestRepositoryAdminsAreUnknownWithoutTheInstanceAdministrators(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Query().Get("permission") == "ADMIN":
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"errors":[{"message":"try later"}]}`)
		case r.URL.Query().Get("permission.1") == "REPO_ADMIN":
			fmt.Fprint(w, pageOf(`{"name":"alice","active":true},{"name":"carol","active":true}`))
		default:
			fmt.Fprint(w, pageOf(`{"name":"scanner","active":true}`))
		}
	})

	_, snapshot := fetchSnapshot(t, f)
	if snapshot.Organization.Available["admins"] {
		t.Fatal("instance administrators answered 503 and must be unavailable")
	}
	repo := snapshot.Projects[0].Repositories[0]
	if repo.Available["admins"] || repo.Admins.Complete {
		t.Errorf("repository admins = %+v (available %v); with the instance administrators unknown they must be too",
			repo.Admins, repo.Available["admins"])
	}
}

// Bitbucket 10.4 leaves lastAuthenticationTimestamp out for an account that has
// never authenticated — by password, token or session — and fills it in for
// everyone who has. Once one account shows the instance records them, a
// missing time means "never"; the creation time says whether that is a new
// account or a dormant one.
func TestSignInHistoryAndLicensing(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/admin/users", pageOf(`
		{"name":"alice","active":true,"createdTimestamp":1735689600000,"lastAuthenticationTimestamp":1767139200000},
		{"name":"eve","active":true,"createdTimestamp":1750000000000},
		{"name":"carol","active":true,"createdTimestamp":1735689600000,"lastAuthenticationTimestamp":1767139200000}`))
	f.users(map[string]string{
		"*":             `{"name":"scanner","active":true}`,
		"ADMIN":         `{"name":"alice","active":true}`,
		"LICENSED_USER": `{"name":"alice","active":true},{"name":"eve","active":true}`,
	})

	_, snapshot := fetchSnapshot(t, f)
	users := map[string]scm.User{}
	for _, u := range snapshot.Organization.Users {
		users[u.Name] = u
	}
	if !snapshot.Organization.Available["userActivity"] || !snapshot.Organization.Available["licensedUsers"] {
		t.Fatalf("available = %v, want activity and licensing known", snapshot.Organization.Available)
	}
	if eve := users["eve"]; !eve.NeverSignedIn || !eve.Licensed || eve.AgeDays != 199 || eve.InactiveDays != -1 {
		t.Errorf("eve = %+v, want licensed, never signed in, 199 days old", eve)
	}
	if alice := users["alice"]; alice.NeverSignedIn || alice.InactiveDays != 1 || alice.AgeDays != 365 {
		t.Errorf("alice = %+v, want last authenticated a day ago, a year old", alice)
	}
	if carol := users["carol"]; carol.Licensed {
		t.Errorf("carol = %+v, want unlicensed", carol)
	}
}

// An instance that reports no times at all is one that does not record them:
// nobody is "never signed in" then, everybody is unknown.
func TestNoActivityDataIsUnknownNotNever(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/admin/users", pageOf(`{"name":"alice","active":true,"createdTimestamp":1735689600000},{"name":"eve","active":true}`))

	_, snapshot := fetchSnapshot(t, f)
	if snapshot.Organization.Available["userActivity"] {
		t.Error("no account carries a time, so the instance must not be taken to record them")
	}
	for _, u := range snapshot.Organization.Users {
		if u.NeverSignedIn {
			t.Errorf("%s marked never signed in on an instance that reports no times", u.Name)
		}
	}
}

// licensedThree is three active licensed users and one deactivated one: the
// base-access probe counts against the active three.
const licensedThree = `{"name":"alice","active":true},{"name":"bob","active":true},{"name":"carol","active":true},{"name":"gone","active":false}`

// The base permission is the highest one every licensed user holds, found by
// asking Bitbucket for the licensed users holding each level and checking
// whether the list reaches the last of them — one request per level, walked
// upward until a level is not everyone's.
func TestBaseAccessIsWhatEveryLicensedUserHolds(t *testing.T) {
	f := standardInstance(t)
	f.users(map[string]string{
		"*":                                `{"name":"scanner","active":true}`,
		"ADMIN":                            `{"name":"alice","active":true}`,
		"LICENSED_USER":                    licensedThree,
		"LICENSED_USER+REPO_READ:PRJ/app":  `{"name":"alice"},{"name":"bob"},{"name":"carol"}`,
		"LICENSED_USER+REPO_WRITE:PRJ/app": `{"name":"alice"},{"name":"bob"},{"name":"carol"}`,
		"LICENSED_USER+REPO_ADMIN:PRJ/app": `{"name":"alice"}`,
		"REPO_ADMIN:PRJ/app":               `{"name":"alice","active":true}`,
	})

	_, snapshot := fetchSnapshot(t, f)
	perms := snapshot.Projects[0].Repositories[0].Permissions
	if perms.DefaultPermission != "REPO_WRITE" || !perms.DefaultPermissionKnown {
		t.Errorf("base access = %q (known %v), want REPO_WRITE, known", perms.DefaultPermission, perms.DefaultPermissionKnown)
	}
}

// A repository nobody has blanket access to costs one request, and records
// that nothing is held by all.
func TestBaseAccessStopsAtTheFirstLevelNotEveryoneHolds(t *testing.T) {
	f := standardInstance(t)
	f.users(map[string]string{
		"*":                               `{"name":"scanner","active":true}`,
		"ADMIN":                           `{"name":"alice","active":true}`,
		"LICENSED_USER":                   licensedThree,
		"LICENSED_USER+REPO_READ:PRJ/app": `{"name":"alice"},{"name":"bob"}`,
	})

	_, snapshot := fetchSnapshot(t, f)
	perms := snapshot.Projects[0].Repositories[0].Permissions
	if perms.DefaultPermission != "" || !perms.DefaultPermissionKnown {
		t.Errorf("base access = %q (known %v), want none, known", perms.DefaultPermission, perms.DefaultPermissionKnown)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	asked := 0
	for _, path := range f.requests {
		if path == "/rest/api/1.0/users" {
			asked++
		}
	}
	// preflight, ADMIN, LICENSED_USER, REPO_ADMIN for the repository's
	// administrators, and one base-access probe.
	if asked != 5 {
		t.Errorf("made %d /users requests, want 5", asked)
	}
}

// Without the licensed users there is nothing to compare against.
func TestBaseAccessIsUnknownWithoutTheLicensedUsers(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("permission") == "LICENSED_USER" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"errors":[{"message":"You are not permitted to access this resource"}]}`)
			return
		}
		fmt.Fprint(w, pageOf(`{"name":"scanner","active":true}`))
	})

	_, snapshot := fetchSnapshot(t, f)
	if perms := snapshot.Projects[0].Repositories[0].Permissions; perms.DefaultPermissionKnown {
		t.Errorf("base access = %q, known; with the licensed users unread it must be unknown", perms.DefaultPermission)
	}
}

// An archived repository still has its base access judged: who can read the
// code matters after it stops changing.
func TestArchivedRepositoriesStillHaveTheirBaseAccessResolved(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos", pageOf(`{"slug":"app","id":10,"name":"app","archived":true,"project":{"key":"PRJ"}}`))
	f.users(map[string]string{
		"*":                                `{"name":"scanner","active":true}`,
		"ADMIN":                            `{"name":"alice","active":true}`,
		"LICENSED_USER":                    licensedThree,
		"LICENSED_USER+REPO_READ:PRJ/app":  `{"name":"alice"},{"name":"bob"},{"name":"carol"}`,
		"LICENSED_USER+REPO_WRITE:PRJ/app": `{"name":"alice"}`,
	})

	_, snapshot := fetchSnapshot(t, f)
	perms := snapshot.Projects[0].Repositories[0].Permissions
	if perms.DefaultPermission != "REPO_READ" || !perms.DefaultPermissionKnown {
		t.Errorf("archived base access = %q (known %v), want REPO_READ, known", perms.DefaultPermission, perms.DefaultPermissionKnown)
	}
}

// Bitbucket 10.4 nests an exempt key's identity: accessKeys[].key.id. Reading a
// top-level id made every key 0, and two restrictions exempting two different
// keys then intersected to "one key can bypass both" — a FAIL for a branch
// nobody could get past. The top-level shape is still read when it is the only
// one present.
func TestExemptAccessKeysKeepTheirIdentity(t *testing.T) {
	f := standardInstance(t)
	f.json("/branch-permissions/2.0/projects/PRJ/repos/app/restrictions", `{"size":2,"limit":100,"isLastPage":true,"start":0,"values":[
		{"id": 1, "type": "pull-request-only",
		 "matcher": {"id": "refs/heads/main", "displayId": "main", "type": {"id": "BRANCH", "name": "Branch"}, "active": true},
		 "scope": {"type": "REPOSITORY", "resourceId": 10}, "users": [], "groups": [],
		 "accessKeys": [{"key": {"id": 1, "label": "deploy-one", "text": "ssh-ed25519 AAAA deploy-one"}}]},
		{"id": 2, "type": "fast-forward-only",
		 "matcher": {"id": "refs/heads/main", "displayId": "main", "type": {"id": "BRANCH", "name": "Branch"}, "active": true},
		 "scope": {"type": "REPOSITORY", "resourceId": 10}, "users": [], "groups": [],
		 "accessKeys": [{"id": 2}]}
	]}`)

	_, snapshot := fetchSnapshot(t, f)
	restrictions := firstRepository(t, snapshot).BranchRestrictions
	if len(restrictions) != 2 {
		t.Fatalf("got %d restrictions, want 2", len(restrictions))
	}
	if got := restrictions[0].ExemptAccessKeyIDs; !slices.Equal(got, []int{1}) {
		t.Errorf("nested key ids = %v, want [1]", got)
	}
	if got := restrictions[1].ExemptAccessKeyIDs; !slices.Equal(got, []int{2}) {
		t.Errorf("top-level key ids = %v, want [2]", got)
	}
}

// A project that will not list its repositories used to abort the whole scan,
// with nothing written. It is now recorded by name — its repositories are not
// in the snapshot, so nothing else could say they were missed — and the rest
// of the scan goes on.
func TestUnlistableProjectIsRecordedAndTheScanGoesOn(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects", `{"size":2,"limit":100,"isLastPage":true,"start":0,"values":[
		{"key":"PRJ","id":1,"name":"Project","public":false,"type":"NORMAL"},
		{"key":"LOCKED","id":2,"name":"Locked","public":false,"type":"NORMAL"}
	]}`)
	f.handle("/api/1.0/projects/LOCKED/repos", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"errors":[{"message":"boom"}]}`)
	})

	_, snapshot := fetchSnapshot(t, f)
	if !slices.Equal(snapshot.Metadata.Unlisted, []string{"LOCKED"}) {
		t.Errorf("unlisted = %v, want [LOCKED]", snapshot.Metadata.Unlisted)
	}
	if got := countRepositories(snapshot.Projects); got != 1 {
		t.Errorf("scanned %d repositories, want PRJ/app still scanned", got)
	}
}

// A --repository naming nothing used to scan zero repositories and exit 0: a
// CI gate on one repository went green the day it was renamed.
func TestMissingRepositoryTargetIsAnError(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ", `{"key":"PRJ","id":1,"name":"Project","public":false,"type":"NORMAL"}`)
	server := f.start()
	client, err := NewClient(Options{BaseURL: server.URL, Token: "t", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{Repositories: []string{"PRJ/app", "PRJ/renamed"}})
	if err == nil || !strings.Contains(err.Error(), "PRJ/renamed") || !strings.Contains(err.Error(), "no such repository") {
		t.Fatalf("err = %v, want the missing repository named", err)
	}
}

// An instance of many one-repository projects used to be scanned one request
// wide whatever scan.concurrency said: projects ran one after another and the
// bound applied only inside each. Repositories of different projects now run
// side by side under one shared bound.
func TestRepositoriesOfDifferentProjectsAreFetchedConcurrently(t *testing.T) {
	f := standardInstance(t)
	var projects, users []string
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("P%d", i)
		projects = append(projects, fmt.Sprintf(`{"key":"%s","id":%d,"name":"%s","type":"NORMAL"}`, key, i+1, key))
		f.json("/api/1.0/projects/"+key+"/repos", pageOf(fmt.Sprintf(`{"slug":"app","id":%d,"name":"app","project":{"key":"%s"}}`, 100+i, key)))
		users = append(users, key)
	}
	f.json("/api/1.0/projects", fmt.Sprintf(`{"size":8,"limit":100,"isLastPage":true,"start":0,"values":[%s]}`, strings.Join(projects, ",")))

	var inFlight, peak atomic.Int64
	slow := func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"requiredApprovers":2,"mergeConfig":{"strategies":[]}}`)
	}
	for _, key := range users {
		f.handle("/api/1.0/projects/"+key+"/repos/app/settings/pull-requests", slow)
	}

	server := f.start()
	client, err := NewClient(Options{BaseURL: server.URL, Token: "t", Timeout: 5 * time.Second, Concurrency: 8})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	snapshot, err := NewFetcher(client, config.Default()).Fetch(context.Background(), FetchOptions{Concurrency: 8})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := countRepositories(snapshot.Projects); got != 8 {
		t.Fatalf("fetched %d repositories, want 8", got)
	}
	if peak.Load() < 2 {
		t.Errorf("at most %d repository fetch ran at once across 8 projects; projects are still serialised", peak.Load())
	}
	for i, p := range snapshot.Projects {
		if p.Key != fmt.Sprintf("P%d", i) {
			t.Errorf("project %d = %s; order must follow the listing", i, p.Key)
		}
	}
}
