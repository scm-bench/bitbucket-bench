package bitbucketdc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scm-bench/scm-bench/internal/config"
	"github.com/scm-bench/scm-bench/internal/scm"
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
	server := f.start()

	client, err := NewClient(Options{BaseURL: server.URL, Token: "test-token", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	fetcher := NewFetcher(client, config.Default())
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

	// Repository REPO_ADMIN plus project PROJECT_ADMIN, both resolved.
	if repo.Admins.Count != 2 || !repo.Admins.Complete {
		t.Errorf("repository admins = %+v, want 2 complete", repo.Admins)
	}

	for key, want := range map[string]bool{
		"pullRequestSettings": true,
		"branchRestrictions":  true,
		"requiredBuilds":      true,
		"hooks":               true,
		"branches":            true,
		"branchAges":          true,
		"files":               true,
		"permissions":         true,
	} {
		if repo.Available[key] != want {
			t.Errorf("Available[%q] = %v, want %v", key, repo.Available[key], want)
		}
	}

	// The global admin group must be expanded into its members.
	if snapshot.Organization.EffectiveAdmins.Count != 2 || !snapshot.Organization.EffectiveAdmins.Complete {
		t.Errorf("organization admins = %+v, want alice + bob complete", snapshot.Organization.EffectiveAdmins)
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
	f.handle("/api/1.0/application-properties", func(w http.ResponseWriter, _ *http.Request) {
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
	if !strings.Contains(err.Error(), "rejected the credentials") {
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

	if snapshot.Organization.Available["adminUsers"] || snapshot.Organization.Available["users"] {
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

	if snapshot.Organization.Available["adminUsers"] || snapshot.Organization.Available["users"] {
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
	f.handle("/api/1.0/application-properties", func(w http.ResponseWriter, _ *http.Request) {
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

func TestArchivedRepositoriesAreSkippedByDefault(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos", pageOf(`{"slug":"app","id":10,"name":"app","archived":true,"project":{"key":"PRJ"}}`))

	_, snapshot := fetchSnapshot(t, f)
	if len(snapshot.Projects[0].Repositories) != 0 {
		t.Error("archived repositories should be skipped when skipArchivedRepositories is on")
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

func TestDefaultPermissionProbeReportsKnownState(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/permissions/PROJECT_WRITE/all", `{"permitted":true}`)

	_, snapshot := fetchSnapshot(t, f)
	perms := snapshot.Projects[0].Repositories[0].Permissions
	if perms.DefaultPermission != "PROJECT_WRITE" || !perms.DefaultPermissionKnown {
		t.Errorf("default permission = %q known=%v, want PROJECT_WRITE known", perms.DefaultPermission, perms.DefaultPermissionKnown)
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

// Access granted through a read-only group still counts as access. Expanding
// only the admin groups would let a dormant account escape review because its
// single grant happened to be non-admin.
func TestNonAdminGroupGrantsRepositoryAccess(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/permissions/groups", pageOf(`{"group":{"name":"developers"},"permission":"PROJECT_READ"}`))
	f.json("/api/1.0/admin/users", pageOf(`{"name":"eve","displayName":"Eve","active":true,"lastAuthenticationTimestamp":1767225600000}`))
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

	var eve *scm.User
	for i := range snapshot.Organization.Users {
		if snapshot.Organization.Users[i].Name == "eve" {
			eve = &snapshot.Organization.Users[i]
		}
	}
	if eve == nil {
		t.Fatal("eve is missing from the user directory")
	}
	if !eve.HasRepositoryAccess {
		t.Error("a user whose only grant is a read-only group still has access to code")
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
