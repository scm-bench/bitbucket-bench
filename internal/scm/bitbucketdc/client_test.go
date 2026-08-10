package bitbucketdc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// retryAfter caps what an instance can ask for. An instance answering
// "Retry-After: 3600" should surface as an error rather than a scan that looks
// hung for an hour.
func TestRetryAfterIsHonouredAndCapped(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{"", 0, false},
		{"5", 5 * time.Second, true},
		{"0", 0, true},
		{"3600", 60 * time.Second, true},
		// A date-formatted Retry-After is legal HTTP but not parsed here; the
		// backoff schedule takes over rather than the request being dropped.
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0, false},
		{"-1", 0, false},
	} {
		resp := &http.Response{Header: http.Header{}}
		if tc.header != "" {
			resp.Header.Set("Retry-After", tc.header)
		}
		got, ok := retryAfter(resp)
		if got != tc.want || ok != tc.ok {
			t.Errorf("retryAfter(%q) = (%v, %v), want (%v, %v)", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

// The jitter is subtracted, never added, so the documented ceiling is a real
// ceiling — a scan cannot sleep longer than the schedule says it can.
func TestBackoffGrowsAndStaysUnderTheCeiling(t *testing.T) {
	var previous time.Duration
	for attempt := 1; attempt <= 8; attempt++ {
		// Sampled rather than called once: the jitter is random, and a single
		// call could pass while the bound is wrong.
		for range 50 {
			d := backoff(attempt)
			if d <= 0 {
				t.Fatalf("backoff(%d) = %v, must be positive", attempt, d)
			}
			if d > 16*time.Second {
				t.Fatalf("backoff(%d) = %v, over the 16s ceiling", attempt, d)
			}
		}
		// Growth is checked on the ceiling rather than a sample, since jittered
		// draws from adjacent attempts can legitimately overlap.
		if attempt <= 5 && backoff(attempt) <= previous/4 {
			t.Errorf("backoff(%d) did not grow past attempt %d", attempt, attempt-1)
		}
		previous = backoff(attempt)
	}
}

// A throttled instance must be retried rather than failing the scan, and the
// retry has to be reported: a scan that quietly took four attempts per request
// is a different measurement from one that took one.
func TestThrottledRequestIsRetriedAndReported(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			// Zero seconds keeps the test fast while still exercising the
			// Retry-After path rather than the backoff schedule.
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"8.19.0"}`)
	}))
	t.Cleanup(server.Close)

	var events []RequestEvent
	client, err := NewClient(Options{
		BaseURL: server.URL, Token: "t", Timeout: 5 * time.Second,
		OnRequest: func(e RequestEvent) { events = append(events, e) },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := client.get(context.Background(), "/api/1.0/application-properties", nil, nil); err != nil {
		t.Fatalf("a 429 should have been retried, got: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("made %d attempts, want 2", got)
	}
	if len(events) != 2 {
		t.Fatalf("reported %d requests, want both attempts", len(events))
	}
	if events[0].Status != http.StatusTooManyRequests || events[1].Attempt != 1 {
		t.Errorf("the retry was not reported as one: %+v", events)
	}
}

// The tracer groups a repository's requests together, and this is the only
// thing that tells it which repository a request belongs to — the client
// issues a dozen concurrently through the same connection pool.
func TestRequestEventsCarryTheRepositoryScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)

	var scopes []string
	client, err := NewClient(Options{
		BaseURL: server.URL, Token: "t", Timeout: 5 * time.Second,
		OnRequest: func(e RequestEvent) { scopes = append(scopes, e.Scope) },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx := context.Background()
	if err := client.get(ctx, "/api/1.0/projects", nil, nil); err != nil {
		t.Fatalf("unscoped get: %v", err)
	}
	if err := client.get(withScope(ctx, "PRJ/app"), "/api/1.0/projects/PRJ/repos/app", nil, nil); err != nil {
		t.Fatalf("scoped get: %v", err)
	}

	if len(scopes) != 2 || scopes[0] != "" || scopes[1] != "PRJ/app" {
		t.Errorf("scopes = %q, want [\"\", \"PRJ/app\"]", scopes)
	}
}

// A password in the base URL must never survive into anything the client hands
// back, because everything it hands back gets persisted: BaseURL() is stamped
// into the snapshot file, the report header and the SARIF that a pipeline
// uploads to a code scanning service. url.URL.String() writes userinfo out
// verbatim — only Redacted() does not — so this is a property of the client,
// not something each consumer can be expected to remember.
func TestCredentialsInTheBaseURLNeverReachOutput(t *testing.T) {
	const secret = "SuperSecret123"

	for _, raw := range []string{
		"https://svc:" + secret + "@bitbucket.example.com",
		"https://svc:" + secret + "@bitbucket.example.com/rest",
		"http://svc:" + secret + "@127.0.0.1:7990",
	} {
		client, err := NewClient(Options{BaseURL: raw, Token: "t", Timeout: time.Second})
		if err != nil {
			t.Fatalf("NewClient(%q): %v", raw, err)
		}
		if got := client.BaseURL(); strings.Contains(got, secret) {
			t.Errorf("NewClient(%q).BaseURL() = %q, still carries the password", raw, got)
		}
		if got := client.BaseURL(); strings.Contains(got, "svc") {
			t.Errorf("NewClient(%q).BaseURL() = %q, still carries the username", raw, got)
		}
		// Silently dropping it would send someone hunting through the password
		// they just typed when the scan comes back 401.
		var told bool
		for _, w := range client.TransportWarnings() {
			if strings.Contains(w, "embedded in the base URL") {
				told = true
			}
			if strings.Contains(w, secret) {
				t.Errorf("transport warning leaks the password: %q", w)
			}
		}
		if !told {
			t.Errorf("NewClient(%q) removed the credentials without saying so", raw)
		}
	}
}

// The error paths are the easy ones to miss: they are reached before any
// stripping has happened, and both url.Error and a bare %q of the option would
// print the password back out.
func TestCredentialsDoNotLeakThroughURLErrors(t *testing.T) {
	const secret = "SuperSecret123"

	for _, tc := range []struct{ name, raw string }{
		{"unparseable", "https://svc:" + secret + "@bitbucket.example.com/%zz"},
		{"no host", "https://svc:" + secret + "@"},
		{"cleartext refused", "http://svc:" + secret + "@bitbucket.example.com"},
	} {
		_, err := NewClient(Options{BaseURL: tc.raw, Token: "t", Timeout: time.Second})
		if err == nil {
			t.Fatalf("%s: NewClient(%q) succeeded, want an error", tc.name, tc.raw)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: error leaks the password: %v", tc.name, err)
		}
	}
}

// redactURL is the fallback for the path where parsing already failed, so it
// cannot lean on net/url to find the userinfo for it.
func TestRedactURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://bitbucket.example.com", "https://bitbucket.example.com"},
		{"https://user:pw@bitbucket.example.com", "https://user:xxxxx@bitbucket.example.com"},
		// Unparseable: the manual path drops the whole userinfo rather than
		// trying to tell username from password in a string it cannot parse.
		{"https://user:pw@bitbucket.example.com/%zz", "https://xxxxx@bitbucket.example.com/%zz"},
		{"://user:pw@host/%zz", "://xxxxx@host/%zz"},
		// An "@" after the authority is not a credential.
		{"https://bitbucket.example.com/a@b", "https://bitbucket.example.com/a@b"},
	} {
		if got := redactURL(tc.in); got != tc.want {
			t.Errorf("redactURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
