package cli

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scm-bench/scm-bench/internal/scm/bitbucketdc"
)

func get(path, scope string, status int) bitbucketdc.RequestEvent {
	return bitbucketdc.RequestEvent{
		Method: http.MethodGet, Path: path, Scope: scope,
		Status: status, Duration: 12 * time.Millisecond,
	}
}

// The closing line is the reason this exists: the operator handed over a token
// that can read every repository, and this is where they are told what it was
// used for.
func TestSummaryAccountsForEveryRequest(t *testing.T) {
	tr := newTracer(&bytes.Buffer{}, false, false)
	for i := 0; i < 59; i++ {
		tr.record(get("/api/1.0/projects", "", 200))
	}

	summary, _ := tr.summary()
	for _, want := range []string{"59 requests", "59 GET", "0 writes", "read-only"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary = %q, missing %q", summary, want)
		}
	}
	if !strings.HasPrefix(summary, "✓") {
		t.Errorf("summary = %q, want it to open with a tick", summary)
	}
}

// The count comes from the requests that were actually sent, not from the
// promise that they are all GET. If that ever stops being true, this is what
// says so — quietly reporting "read-only" anyway would be the worst outcome.
func TestSummaryFlagsANonReadRequest(t *testing.T) {
	tr := newTracer(&bytes.Buffer{}, false, false)
	tr.record(get("/api/1.0/projects", "", 200))
	tr.record(bitbucketdc.RequestEvent{Method: http.MethodPost, Path: "/api/1.0/projects", Status: 201})

	summary, _ := tr.summary()
	if strings.Contains(summary, "read-only") {
		t.Errorf("summary = %q, must not claim read-only after a POST", summary)
	}
	for _, want := range []string{"NOT READ-ONLY", "1 POST", "✗"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary = %q, missing %q", summary, want)
		}
	}
}

func TestSummaryIsEmptyWhenNothingWasSent(t *testing.T) {
	if s, _ := newTracer(&bytes.Buffer{}, false, false).summary(); s != "" {
		t.Errorf("summary = %q, want nothing when no request was made", s)
	}
}

func TestSummaryCountsRetries(t *testing.T) {
	tr := newTracer(&bytes.Buffer{}, false, false)
	tr.record(get("/api/1.0/projects", "", 500))
	e := get("/api/1.0/projects", "", 200)
	e.Attempt = 1
	tr.record(e)

	if s, _ := tr.summary(); !strings.Contains(s, "1 retried") {
		t.Errorf("summary = %q, want the retry counted", s)
	}
}

// Requests arrive interleaved across concurrent repositories, so a repository's
// requests are held and printed together once it finishes.
func TestRequestsAreGroupedByRepository(t *testing.T) {
	var buf bytes.Buffer
	tr := newTracer(&buf, false, true)

	tr.record(get("/api/1.0/projects", "", 200))
	tr.record(get("/api/1.0/projects/PRJ/repos/a/branches", "PRJ/a", 200))
	tr.record(get("/api/1.0/projects/PRJ/repos/b/branches", "PRJ/b", 200))
	tr.record(get("/api/1.0/projects/PRJ/repos/a/settings/hooks", "PRJ/a", 200))

	// Nothing repository-scoped has been printed yet.
	if strings.Contains(buf.String(), "PRJ/a") {
		t.Errorf("a repository was printed before it finished:\n%s", buf.String())
	}
	// The instance-level request has, since it has nothing to wait for.
	if !strings.Contains(buf.String(), "/projects") {
		t.Errorf("an unscoped request was withheld:\n%s", buf.String())
	}

	tr.repositoryDone("PRJ/a")
	out := buf.String()

	if !strings.Contains(out, "PRJ/a") {
		t.Errorf("the finished repository was not printed:\n%s", out)
	}
	if strings.Contains(out, "PRJ/b") {
		t.Errorf("an unfinished repository was printed:\n%s", out)
	}
	// Both of that repository's requests belong to the block.
	if !strings.Contains(out, "…/branches") || !strings.Contains(out, "…/settings/hooks") {
		t.Errorf("the group is missing requests:\n%s", out)
	}
}

// Accounting must happen whether or not anything is being displayed: the
// closing line is printed even when the request log is off.
func TestQuietTracerStillCounts(t *testing.T) {
	var buf bytes.Buffer
	tr := newTracer(&buf, false, false)

	tr.record(get("/api/1.0/projects/PRJ/repos/a/branches", "PRJ/a", 200))
	tr.repositoryDone("PRJ/a")

	if buf.Len() != 0 {
		t.Errorf("a quiet tracer wrote %q", buf.String())
	}
	if s, _ := tr.summary(); !strings.Contains(s, "1 requests") {
		t.Errorf("summary = %q, want the request counted anyway", s)
	}
}

func TestShortenPath(t *testing.T) {
	for _, tc := range []struct {
		path, scope, want string
	}{
		{"/api/1.0/projects/PRJ/repos/app/branches", "PRJ/app", "…/branches"},
		{"/branch-permissions/2.0/projects/PRJ/repos/app/restrictions", "PRJ/app", "…/restrictions"},
		{"/required-builds/latest/projects/PRJ/repos/app/conditions", "PRJ/app", "…/conditions"},
		{"/api/1.0/projects/PRJ", "", "/projects/PRJ"},
		// A path that does not belong to the scope keeps its full form rather
		// than being misleadingly abbreviated.
		{"/api/1.0/admin/users", "PRJ/app", "/admin/users"},
	} {
		if got := shortenPath(tc.path, tc.scope); got != tc.want {
			t.Errorf("shortenPath(%q, %q) = %q, want %q", tc.path, tc.scope, got, tc.want)
		}
	}
}

// record and repositoryDone are called from the fetch goroutines.
func TestTracerIsSafeForConcurrentUse(t *testing.T) {
	tr := newTracer(&bytes.Buffer{}, false, true)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scope := "PRJ/repo"
			tr.record(get("/api/1.0/projects/PRJ/repos/repo/branches", scope, 200))
			tr.repositoryDone(scope)
		}(i)
	}
	wg.Wait()

	if s, _ := tr.summary(); !strings.Contains(s, "20 requests") {
		t.Errorf("summary = %q, want all 20 counted", s)
	}
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		12 * time.Millisecond:   "12ms",
		1500 * time.Millisecond: "1.5s",
		2 * time.Second:         "2.0s",
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

// The reported symptom: twenty-two identical "GET /admin/users" lines and
// sixteen identical "GET /admin/groups/more-members" lines, which read as the
// tool hammering one endpoint rather than paging a directory and expanding
// sixteen different groups.
func TestQueryContextDistinguishesOtherwiseIdenticalRequests(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query url.Values
		want  string
	}{
		{"no query", nil, ""},
		// limit rides on every paged request and separates nothing.
		{"limit only", url.Values{"limit": {"100"}}, ""},
		// Nor does the first page, which is the only page most requests have.
		{"first page", url.Values{"limit": {"100"}, "start": {"0"}}, ""},
		{"later page", url.Values{"limit": {"100"}, "start": {"200"}}, "?start=200"},
		{"group expansion", url.Values{"limit": {"100"}, "context": {"bitbucket-admins"}}, "?context=bitbucket-admins"},
		{"sorted", url.Values{"context": {"g"}, "start": {"100"}}, "?context=g&start=100"},
	} {
		if got := queryContext(tc.query); got != tc.want {
			t.Errorf("%s: queryContext(%v) = %q, want %q", tc.name, tc.query, got, tc.want)
		}
	}
}

// The path column is padded by hand so the dimmed query does not push the
// status and duration out of line. Colour must not change where they land.
func TestRequestColumnsLineUpWithAndWithoutColour(t *testing.T) {
	e := get("/api/1.0/admin/groups/more-members", "", 200)
	e.Query = url.Values{"limit": {"100"}, "context": {"bitbucket-admins"}}

	plain := newTracer(&bytes.Buffer{}, false, true).formatRequest(e, "")
	coloured := newTracer(&bytes.Buffer{}, true, true).formatRequest(e, "")

	if !strings.Contains(plain, "?context=bitbucket-admins") {
		t.Fatalf("the group is missing from the line: %q", plain)
	}
	if stripANSI(coloured) != plain {
		t.Errorf("colour changed the layout:\n plain    %q\n coloured %q", plain, stripANSI(coloured))
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
