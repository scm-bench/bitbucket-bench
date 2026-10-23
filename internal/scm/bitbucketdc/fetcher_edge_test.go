package bitbucketdc

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scm-bench/bitbucket-bench/internal/config"
	"github.com/scm-bench/bitbucket-bench/internal/scm"
)

// badRequest answers 400, which the client neither retries nor treats as
// "unavailable": the path for an unexpected refusal.
func badRequest(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, `{"errors":[{"message":"bad request"}]}`)
}

func forbidden(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprint(w, `{"errors":[{"message":"forbidden"}]}`)
}

// fetchEdge runs a scan against f and returns what Fetch returned.
func fetchEdge(t *testing.T, f *fakeInstance, ctx context.Context, cfg config.Config, client Options, opts FetchOptions) (*scm.Snapshot, error) {
	t.Helper()
	server := f.start()
	client.BaseURL, client.Token, client.Timeout = server.URL, "t", 5*time.Second
	c, err := NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Now.IsZero() {
		opts.Now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return NewFetcher(c, cfg).Fetch(ctx, opts)
}

// usersExcept answers /users like f.users does, except that a query whose
// permission.1 or permission.2 is perm fails.
func usersExcept(f *fakeInstance, perm string, fail http.HandlerFunc) {
	inner := f.handlers["/api/1.0/users"]
	f.handle("/api/1.0/users", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("permission.1") == perm || q.Get("permission.2") == perm || q.Get("permission") == perm {
			fail(w, r)
			return
		}
		inner(w, r)
	})
}

func TestWarningsAreRecordedOnce(t *testing.T) {
	var sent []string
	c := &Client{Warnf: func(f string, a ...any) { sent = append(sent, fmt.Sprintf(f, a...)) }}
	f := NewFetcher(c, config.Default())
	f.warn("same %d", 1)
	f.warn("same %d", 1)
	if len(f.warnings) != 1 || len(sent) != 1 {
		t.Errorf("warnings %q, sent %q", f.warnings, sent)
	}
}

// A preflight that fails for any reason other than a refusal still fails the
// scan, and says the credentials could not be verified.
func TestUnverifiableCredentialsFailTheScan(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/users", badRequest)
	if _, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{}); err == nil || !strings.Contains(err.Error(), "could not verify the credentials") {
		t.Errorf("err = %v", err)
	}

	// Also when what failed was the connection, not the instance.
	server := standardInstance(t).start()
	c, err := NewClient(Options{BaseURL: server.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	c.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, x509.UnknownAuthorityError{}
	})
	if _, err := NewFetcher(c, config.Default()).Fetch(context.Background(), FetchOptions{}); err == nil || !strings.Contains(err.Error(), "could not verify the credentials") {
		t.Errorf("err = %v", err)
	}
}

// The organization-level reads each degrade on their own: administrators
// refused, the grant table half-readable, the user directory broken.
func TestOrganizationReadsDegradeIndependently(t *testing.T) {
	f := standardInstance(t)
	usersExcept(f, "ADMIN", forbidden)
	f.json("/api/1.0/admin/permissions/users", pageOf(`{"user":{"name":"carol","active":true},"permission":"LICENSED_USER"}`))
	f.handle("/api/1.0/admin/permissions/groups", forbidden)
	f.handle("/api/1.0/admin/users", badRequest)

	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	org := snapshot.Organization
	if org.Available["admins"] || org.Available["adminGrants"] || org.Available["users"] || org.Available["userActivity"] {
		t.Errorf("available = %v", org.Available)
	}
	joined := strings.Join(snapshot.Metadata.Warnings, "\n")
	for _, want := range []string{"CIS-1.3.3 will report MANUAL", "the user directory could not be read"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
}

// A global grant that is not an administrator's is not counted as one.
func TestOnlyAdministratorGrantsAreRecorded(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/admin/permissions/users", `{"size":2,"isLastPage":true,"values":[
		{"user":{"name":"alice","active":true},"permission":"SYS_ADMIN"},
		{"user":{"name":"carol","active":true},"permission":"LICENSED_USER"}]}`)
	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range snapshot.Organization.Admins {
		if g.Name == "carol" {
			t.Errorf("a LICENSED_USER grant was recorded as an administrator: %+v", g)
		}
	}
}

func TestMalformedTargetFailsTheScan(t *testing.T) {
	_, err := fetchEdge(t, standardInstance(t), context.Background(), config.Default(), Options{}, FetchOptions{Repositories: []string{"no-slash"}})
	if err == nil || !strings.Contains(err.Error(), "PROJECT/slug") {
		t.Errorf("err = %v", err)
	}
}

func TestUnlistableProjectsFailTheScan(t *testing.T) {
	f := standardInstance(t)
	f.handle("/api/1.0/projects", badRequest)
	if _, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{}); err == nil || !strings.Contains(err.Error(), "list projects") {
		t.Errorf("err = %v", err)
	}
}

// A --repository inside a project whose repositories could not be listed is a
// gap in the scan, recorded as such, not a typo to fail on.
func TestRepositoryTargetInAnUnlistableProjectIsAGap(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/LOCKED", `{"key":"LOCKED","id":2,"name":"Locked"}`)
	f.handle("/api/1.0/projects/LOCKED/repos", badRequest)
	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{Repositories: []string{"LOCKED/app"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Metadata.Unlisted, []string{"LOCKED"}) {
		t.Errorf("unlisted = %v", snapshot.Metadata.Unlisted)
	}
}

// Progress names the project as well once there is more than one, and only
// the repositories named by --repository are fetched.
func TestProgressAcrossProjectsAndRepositoryFilter(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ", `{"key":"PRJ","id":1,"name":"Project"}`)
	f.json("/api/1.0/projects/OTHER", `{"key":"OTHER","id":2,"name":"Other"}`)
	f.json("/api/1.0/projects/OTHER/repos", pageOf(`{"slug":"svc","id":20,"name":"svc","project":{"key":"OTHER"}}`))
	f.json("/api/1.0/projects/PRJ/repos", `{"size":2,"isLastPage":true,"values":[
		{"slug":"app","id":10,"name":"app","project":{"key":"PRJ"}},
		{"slug":"skipped","id":11,"name":"skipped","project":{"key":"PRJ"}}]}`)

	var mu sync.Mutex
	var lines []string
	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{
		Projects:     []string{"OTHER"},
		Repositories: []string{"PRJ/app"},
		Progress: func(s string) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, s)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := countRepositories(snapshot.Projects); got != 2 {
		t.Errorf("scanned %d repositories, want PRJ/app and OTHER/svc", got)
	}
	if !slices.ContainsFunc(lines, func(s string) bool { return strings.Contains(s, "project 1/2") || strings.Contains(s, "project 2/2") }) {
		t.Errorf("progress = %q, want the project position", lines)
	}
}

// Every repository-level read that fails in an unexpected way is recorded
// against the repository, and the scan goes on.
func TestRepositoryReadsRecordUnexpectedFailures(t *testing.T) {
	f := standardInstance(t)
	f.handle("/branch-utils/latest/projects/PRJ/repos/app/branchmodel", badRequest)
	f.handle("/required-builds/latest/projects/PRJ/repos/app/conditions", badRequest)
	f.handle("/api/1.0/projects/PRJ/repos/app/browse/SECURITY.md", badRequest)
	usersExcept(f, "REPO_ADMIN", badRequest)
	usersExcept(f, "REPO_READ", badRequest)
	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repo := firstRepository(t, snapshot)
	errs := strings.Join(repo.Errors, "\n")
	for _, want := range []string{"required builds: GET", "browse SECURITY.md", "repository administrators", "base access (REPO_READ)"} {
		if !strings.Contains(errs, want) {
			t.Errorf("errors lack %q:\n%s", want, errs)
		}
	}
	if repo.Available["requiredBuilds"] || repo.Available["files"] || repo.Available["admins"] || repo.Permissions.DefaultPermissionKnown {
		t.Errorf("available = %v, base access known = %v", repo.Available, repo.Permissions.DefaultPermissionKnown)
	}
	if !slices.ContainsFunc(snapshot.Metadata.Warnings, func(w string) bool { return strings.Contains(w, "branch model for PRJ/app failed") }) {
		t.Errorf("warnings = %q", snapshot.Metadata.Warnings)
	}
}

// A policy file missing at the first path is looked for at the next.
func TestSecurityPolicyIsLookedForAtEveryPath(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos/app/browse/docs/SECURITY.md", `{"lines":[{"text":"# Security"}]}`)
	cfg := config.Default()
	cfg.SecurityPolicyPaths = []string{"missing.md", "docs/SECURITY.md"}
	snapshot, err := fetchEdge(t, f, context.Background(), cfg, Options{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstRepository(t, snapshot).Files.SecurityPolicyPaths; !slices.Equal(got, []string{"docs/SECURITY.md"}) {
		t.Errorf("found %v", got)
	}
}

// An exemption decides whether a required build gates the default branch: one
// that covers it cancels the condition, one nobody can resolve makes it
// unknown, and one elsewhere leaves it standing.
func TestRequiredBuildExemptions(t *testing.T) {
	f := standardInstance(t)
	f.json("/required-builds/latest/projects/PRJ/repos/app/conditions", `{"size":3,"isLastPage":true,"values":[
		{"id":1,"refMatcher":{"id":"ANY_REF_MATCHER_ID"},"exemptRefMatcher":{"id":"refs/heads/main","type":{"id":"BRANCH"}}},
		{"id":2,"refMatcher":{"id":"ANY_REF_MATCHER_ID"},"exemptRefMatcher":{"id":"elsewhere","type":{"id":"SOMETHING_NEW"}}},
		{"id":3,"refMatcher":{"id":"ANY_REF_MATCHER_ID"},"exemptRefMatcher":{"id":"refs/heads/release","type":{"id":"BRANCH"}}}]}`)
	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	builds := firstRepository(t, snapshot).RequiredBuilds
	if len(builds) != 3 {
		t.Fatalf("builds = %+v", builds)
	}
	for i, want := range []struct{ matches, unknown bool }{{false, false}, {false, true}, {true, false}} {
		if builds[i].MatchesDefaultBranch != want.matches || builds[i].MatchUnknown != want.unknown {
			t.Errorf("condition %d: matches=%v unknown=%v, want %v/%v", builds[i].ID, builds[i].MatchesDefaultBranch, builds[i].MatchUnknown, want.matches, want.unknown)
		}
	}
}

// Without commit metadata in the listing, the commit itself is asked; its
// author time stands in for a missing committer time.
func TestBranchAgeFallsBackToTheCommit(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects/PRJ/repos/app/branches", `{"size":2,"isLastPage":true,"values":[
		{"id":"refs/heads/main","displayId":"main","isDefault":true,"latestCommit":"aaa"},
		{"id":"refs/heads/old","displayId":"old","latestCommit":"bbb"}]}`)
	f.json("/api/1.0/projects/PRJ/repos/app/commits/aaa", `{"authorTimestamp":1766620800000}`)
	f.json("/api/1.0/projects/PRJ/repos/app/commits/bbb", `{}`)
	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repo := firstRepository(t, snapshot)
	if repo.Branches[0].AgeDays != 7 || repo.Branches[1].AgeDays != -1 || repo.Available["branchAges"] {
		t.Errorf("branches = %+v, branchAges = %v", repo.Branches, repo.Available["branchAges"])
	}
}

// Exempt groups are expanded once per scan, failures included, and a person
// named both directly and through a group is counted once.
func TestExemptGroupExpansionIsMemoised(t *testing.T) {
	f := standardInstance(t)
	f.json("/branch-permissions/2.0/projects/PRJ/repos/app/restrictions", `{"size":3,"isLastPage":true,"values":[
		{"id":1,"type":{"id":"read-only"},"matcher":{"id":"refs/heads/main","type":{"id":"BRANCH"}},"users":[{"name":"bob"}],"groups":["devs"]},
		{"id":2,"type":{"id":"no-deletes"},"matcher":{"id":"refs/heads/main","type":{"id":"BRANCH"}},"groups":["devs","broken"]},
		{"id":3,"type":{"id":"fast-forward-only"},"matcher":{"id":"refs/heads/main","type":{"id":"BRANCH"}},"groups":["broken"]}]}`)
	var mu sync.Mutex
	asked := map[string]int{}
	f.handle("/api/1.0/admin/groups/more-members", func(w http.ResponseWriter, r *http.Request) {
		group := r.URL.Query().Get("context")
		mu.Lock()
		asked[group]++
		mu.Unlock()
		if group == "broken" {
			badRequest(w, r)
			return
		}
		fmt.Fprint(w, pageOf(`{"name":"bob","active":true},{"name":"dave","active":true}`))
	})
	snapshot, err := fetchEdge(t, f, context.Background(), config.Default(), Options{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if asked["devs"] != 1 || asked["broken"] != 1 {
		t.Errorf("asked = %v, want each group once", asked)
	}
	r := firstRepository(t, snapshot).BranchRestrictions
	if got := r[0].ExemptPrincipals.Users; !slices.Equal(got, []string{"bob", "dave"}) || !r[0].ExemptPrincipals.Complete {
		t.Errorf("first restriction = %+v", r[0].ExemptPrincipals)
	}
	if r[1].ExemptPrincipals.Complete || r[2].ExemptPrincipals.Complete {
		t.Error("a restriction exempting an unexpandable group cannot be complete")
	}
}

func TestDaysSince(t *testing.T) {
	f := &Fetcher{now: time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		epoch int64
		want  int
	}{
		{0, -1},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), 10},
		{time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix(), 0}, // clock skew reads as fresh
	} {
		if got := f.daysSince(tc.epoch); got != tc.want {
			t.Errorf("daysSince(%d) = %d, want %d", tc.epoch, got, tc.want)
		}
	}
}

func TestParseTargetsSkipsBlanks(t *testing.T) {
	got, err := parseTargets(FetchOptions{Projects: []string{" ", "PRJ"}, Repositories: []string{"", "  ", "PRJ/app"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.projects) != 1 || len(got.repositories) != 1 {
		t.Errorf("targets = %+v", got)
	}
}

// Cancelling while repositories are being listed ends the scan with the
// cancellation.
func TestCancelWhileListingRepositories(t *testing.T) {
	f := standardInstance(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.handle("/api/1.0/projects/PRJ/repos", func(w http.ResponseWriter, r *http.Request) {
		cancel()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	if _, err := fetchEdge(t, f, ctx, config.Default(), Options{}, FetchOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// Cancelling mid-scan stops the projects and repositories still waiting for a
// slot as well as the one in flight.
func TestCancelWhileProjectsAndRepositoriesWait(t *testing.T) {
	f := standardInstance(t)
	f.json("/api/1.0/projects", `{"size":2,"isLastPage":true,"values":[{"key":"PRJ","id":1},{"key":"OTHER","id":2}]}`)
	two := `{"size":2,"isLastPage":true,"values":[{"slug":"app","id":10},{"slug":"lib","id":11}]}`
	f.json("/api/1.0/projects/PRJ/repos", two)
	f.json("/api/1.0/projects/OTHER/repos", two)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	logf := func(format string, args ...any) {
		if strings.HasPrefix(format, "scanning %s") {
			once.Do(func() {
				cancel()
				// Holding both slots while everything queued behind them
				// sees the cancellation.
				time.Sleep(100 * time.Millisecond)
			})
		}
	}
	if _, err := fetchEdge(t, f, ctx, config.Default(), Options{Logf: logf}, FetchOptions{Concurrency: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// Cancelled just as the last repository finished, the scan still reports the
// cancellation rather than returning as though it had completed.
func TestCancelAfterTheLastRepository(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := fetchEdge(t, standardInstance(t), ctx, config.Default(), Options{}, FetchOptions{OnRepositoryDone: func(string) { cancel() }})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
