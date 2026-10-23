package bitbucketdc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// edgeClient is a client for a loopback test server.
func edgeClient(t *testing.T, srv *httptest.Server, opts Options) *Client {
	t.Helper()
	opts.BaseURL = srv.URL
	if opts.Token == "" {
		opts.Token = "t"
	}
	c, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func respond(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRedirectChainsAreCut(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://bitbucket.example.com/a", nil)
	via := make([]*http.Request, 5)
	for i := range via {
		via[i] = req
	}
	if err := sameOriginRedirects(req, via); err == nil || !strings.Contains(err.Error(), "stopped after 5 redirects") {
		t.Errorf("err = %v", err)
	}
}

func TestRedactURLWithoutCredentials(t *testing.T) {
	// Unparseable, and nothing to hide.
	if got := redactURL("https://host%zz/path"); got != "https://host%zz/path" {
		t.Errorf("redactURL = %q", got)
	}
}

func TestLogAndWarnSinks(t *testing.T) {
	var logged, warned []string
	c := &Client{
		Logf:  func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) },
		Warnf: func(f string, a ...any) { warned = append(warned, fmt.Sprintf(f, a...)) },
	}
	c.logf("hello %d", 1)
	c.warnf("careful %d", 2)
	if len(logged) != 1 || logged[0] != "hello 1" || len(warned) != 1 || warned[0] != "careful 2" {
		t.Errorf("logged %q, warned %q", logged, warned)
	}
	// With no warning sink, a warning is narrated rather than dropped.
	c.Warnf = nil
	c.warnf("careful %d", 3)
	if len(logged) != 2 || logged[1] != "warning: careful 3" {
		t.Errorf("logged %q", logged)
	}
}

func TestAPIErrorWithoutMessagesNamesTheStatus(t *testing.T) {
	err := &APIError{StatusCode: http.StatusBadGateway, Path: "/x"}
	if got := err.Error(); got != "GET /x: 502 Bad Gateway" {
		t.Errorf("Error() = %q", got)
	}
}

func TestUndecodableResponseIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `not json`) }))
	defer srv.Close()
	var out map[string]any
	if err := edgeClient(t, srv, Options{}).get(context.Background(), "/x", nil, &out); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("err = %v", err)
	}
}

func TestUnbuildableRequestIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := edgeClient(t, srv, Options{})
	c.baseURL = &url.URL{Scheme: "https", Host: "bad host"}
	if err := c.get(context.Background(), "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "build request for") {
		t.Errorf("err = %v", err)
	}
}

// A context cancelled while the client waits to retry ends the wait at once.
func TestCancelDuringBackoffStopsTheRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := edgeClient(t, srv, Options{OnRequest: func(RequestEvent) { cancel() }})
	start := time.Now()
	if err := c.get(ctx, "/x", nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("the backoff was waited out despite the cancellation")
	}
}

// The same holds for a wait the instance asked for with Retry-After.
func TestCancelDuringRetryAfterStopsTheRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := edgeClient(t, srv, Options{OnRequest: func(RequestEvent) { cancel() }})
	start := time.Now()
	if err := c.get(ctx, "/x", nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the Retry-After was waited out despite the cancellation")
	}
}

// A transport failure caused by cancellation reports the cancellation, not
// the transport error, and is not retried.
func TestTransportErrorAfterCancelReportsTheCancellation(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := edgeClient(t, srv, Options{})
	var calls atomic.Int32
	c.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		cancel()
		return nil, errors.New("connection reset")
	})
	if err := c.get(ctx, "/x", nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if calls.Load() != 1 {
		t.Errorf("%d attempts, want 1", calls.Load())
	}
}

// A transient transport failure and a body cut off mid-read are both retried.
func TestTransientFailuresAreRetried(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	var events []RequestEvent
	c := edgeClient(t, srv, Options{OnRequest: func(e RequestEvent) { events = append(events, e) }})
	var calls atomic.Int32
	c.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		switch calls.Add(1) {
		case 1:
			return nil, errors.New("connection reset")
		case 2:
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(io.MultiReader(strings.NewReader(`{"a"`), errReader{}))}, nil
		default:
			return respond(http.StatusOK, `{"a":1}`, nil), nil
		}
	})
	var out struct{ A int }
	if err := c.get(context.Background(), "/x", nil, &out); err != nil || out.A != 1 {
		t.Fatalf("err = %v, out = %+v", err, out)
	}
	if len(events) != 3 || events[0].Err == nil || events[1].Err == nil || events[2].Err != nil {
		t.Errorf("events = %+v", events)
	}
}

// Every attempt failing reports the last failure.
func TestExhaustedRetriesReportTheLastFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := edgeClient(t, srv, Options{MaxRetries: 1})
	c.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(errReader{})}, nil
	})
	if err := c.get(context.Background(), "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "read body") {
		t.Errorf("err = %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("unexpected EOF") }

func TestUndecodablePageItemIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"isLastPage":true,"values":["not an object"]}`)
	}))
	defer srv.Close()
	if _, err := getPaged[apiProject](context.Background(), edgeClient(t, srv, Options{}), "/x", nil); err == nil || !strings.Contains(err.Error(), "decode item") {
		t.Errorf("err = %v", err)
	}
}

// An instance that pages forever is cut off rather than followed.
func TestEndlessPaginationIsCutOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		fmt.Fprintf(w, `{"isLastPage":false,"nextPageStart":%d,"values":[]}`, start+1)
	}))
	defer srv.Close()
	got, err := getPaged[apiProject](context.Background(), edgeClient(t, srv, Options{}), "/x", nil)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeded %d pages", maxPages)) || len(got) != 0 {
		t.Errorf("got %d, err = %v", len(got), err)
	}
}

func TestErrorEnvelopeFallsBackToTheExceptionName(t *testing.T) {
	got := parseErrorMessages([]byte(`{"errors":[{"exceptionName":"com.atlassian.Boom"},{}]}`))
	if len(got) != 1 || got[0] != "com.atlassian.Boom" {
		t.Errorf("messages = %q", got)
	}
}
