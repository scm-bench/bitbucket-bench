// Package bitbucketdc fetches a normalized snapshot from a Bitbucket Data
// Center instance. It only ever issues GET requests: scanning must never be
// able to change the instance it is auditing.
package bitbucketdc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// pageLimit is the page size requested from paginated endpoints. Bitbucket
// caps most of them at 1000; 100 keeps responses small without being chatty.
const pageLimit = 100

// maxPages bounds pagination so a misbehaving instance cannot loop forever.
const maxPages = 1000

// Client is a read-only Bitbucket Data Center REST client.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client

	// Exactly one auth mode is used: a bearer HTTP access token, or basic auth.
	token    string
	username string
	password string

	maxRetries int
	onRequest  func(RequestEvent)
	// Logf receives progress and warning lines; nil discards them.
	// Repositories are fetched concurrently, so this is called from multiple
	// goroutines and must be safe for concurrent use.
	Logf func(format string, args ...any)
	// Warnf receives lines about what the scan could not see, as opposed to
	// what it is doing. Separate from Logf so a caller can tag the two
	// differently without sniffing the text for a "warning:" prefix. Also
	// called from multiple goroutines.
	Warnf func(format string, args ...any)

	// transportWarnings records protections the caller opted out of. They are
	// copied into the snapshot so an archived scan still says what it was
	// exposed to at capture time.
	transportWarnings []string
}

// TransportWarnings returns the protections this client was told to give up.
// Empty for the ordinary case of https with a verified certificate.
func (c *Client) TransportWarnings() []string {
	return append([]string(nil), c.transportWarnings...)
}

// RequestEvent describes one HTTP request the scan made.
//
// Method is carried rather than assumed. The tool only ever issues GET, and a
// test enforces that — but a caller showing an operator what a token was used
// for should be reporting what actually went out, not repeating the promise.
type RequestEvent struct {
	Method string
	// Path is relative to the instance root, without the /rest prefix.
	Path string
	// Scope names the repository this request belongs to, when it belongs to
	// one. Empty for instance-level and project-level requests.
	Scope    string
	Status   int
	Duration time.Duration
	Err      error
	// Attempt is 0 for the first try; higher values are retries.
	Attempt int
	// Query is what actually went out on the URL.
	//
	// Without it the trace collapses genuinely different requests into
	// identical lines: paging the user directory printed "GET /admin/users"
	// twenty-two times, and expanding sixteen distinct groups printed
	// "GET /admin/groups/more-members" sixteen times. Both read as the tool
	// hammering one endpoint in a loop. An account of what a token was used for
	// is only worth having if the requests in it can be told apart.
	Query url.Values
}

// scopeKey carries the repository a request belongs to. It rides on the
// context because the client has no other way to know: fetchRepository issues
// a dozen requests through the same client, concurrently with other
// repositories doing the same.
type scopeKey struct{}

func withScope(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, scopeKey{}, name)
}

func scopeFrom(ctx context.Context) string {
	name, _ := ctx.Value(scopeKey{}).(string)
	return name
}

// Options configures a Client.
type Options struct {
	BaseURL  string
	Token    string
	Username string
	Password string
	// Timeout bounds a single HTTP request. Zero uses 30s.
	Timeout time.Duration
	// Insecure disables TLS verification. Only for instances with a private CA
	// that the caller cannot install; off by default and never implied.
	Insecure bool
	// AllowPlaintext permits an http:// base URL to a non-loopback host, which
	// puts the credential on the wire in the clear. Off by default and never
	// implied.
	AllowPlaintext bool
	// MaxRetries bounds retries of 429/5xx responses. Zero uses 3.
	MaxRetries int
	// Logf is called from multiple goroutines during a scan and must be safe
	// for concurrent use.
	Logf func(format string, args ...any)
	// Warnf receives what the scan could not read. Nil falls back to Logf.
	Warnf func(format string, args ...any)
	// OnRequest receives every request as it completes, including retries.
	// Called concurrently; nil discards them.
	OnRequest func(RequestEvent)
}

// NewClient validates the options and returns a ready client.
func NewClient(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, errors.New("base URL is required")
	}
	raw := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	// A bare host is almost certainly meant as https.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// url.Error embeds the URL it failed on, credentials and all, so the
		// inner cause is unwrapped and the URL is redacted separately.
		reason := err
		var parseErr *url.Error
		if errors.As(err, &parseErr) {
			reason = parseErr.Err
		}
		return nil, fmt.Errorf("invalid base URL %q: %w", redactURL(opts.BaseURL), reason)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q: no host", redactURL(opts.BaseURL))
	}

	// Credentials embedded in the URL are stripped here, at the one place a URL
	// enters the client, rather than at each of the places it leaves.
	//
	// They are never used for authentication — that is what --token and
	// --username/--password are for — but url.URL.String() writes userinfo back
	// out verbatim (only Redacted() does not), and this URL is stamped into the
	// snapshot file, the report header and the SARIF uploaded to a code
	// scanning service. A password reaching any of those is the exact failure
	// SECURITY.md names first, so it is removed before it can be stored.
	urlHadCredentials := u.User != nil
	u.User = nil

	// Tolerate a URL that already points at /rest so both forms work.
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/rest")

	if opts.Token == "" && opts.Username == "" {
		return nil, errors.New("an access token or username/password is required")
	}

	if err := checkTransport(u, opts.AllowPlaintext); err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	retries := opts.MaxRetries
	if retries <= 0 {
		retries = 3
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	var warnings []string
	if opts.Insecure {
		transport.TLSClientConfig = tlsInsecureConfig()
		warnings = append(warnings, fmt.Sprintf(
			"TLS certificate verification was disabled (--insecure) for %s; this scan could have been intercepted", u.Host))
	}
	if u.Scheme == "http" && !isLoopback(u) {
		warnings = append(warnings, fmt.Sprintf(
			"credentials were sent in cleartext over http:// to %s (--allow-plaintext)", u.Host))
	}
	if urlHadCredentials {
		// Said out loud rather than dropped in silence: someone who put a
		// credential in the URL expected it to authenticate them, and needs to
		// know it did not — otherwise the eventual 401 sends them looking at
		// the password they just typed.
		warnings = append(warnings, "credentials embedded in the base URL were ignored and removed; "+
			"authentication uses --token or --username/--password")
	}

	return &Client{
		baseURL:           u,
		httpClient:        &http.Client{Timeout: timeout, Transport: transport},
		token:             opts.Token,
		username:          opts.Username,
		password:          opts.Password,
		maxRetries:        retries,
		onRequest:         opts.OnRequest,
		Logf:              opts.Logf,
		Warnf:             opts.Warnf,
		transportWarnings: warnings,
	}, nil
}

// checkTransport refuses to put a credential on the wire in the clear.
//
// This is not a hypothetical: the token this tool asks for can read every
// repository on the instance, and the admin-read variant can enumerate the
// user directory. Sending it over http:// hands all of that to anyone on the
// path, and unlike a misconfiguration the tool reports, it cannot be undone
// afterwards — by the time a warning is printed the credential is already out.
//
// Loopback is exempt: that traffic does not leave the machine, so the threat
// model does not apply, and it keeps local test servers usable.
func checkTransport(u *url.URL, allowPlaintext bool) error {
	if u.Scheme != "http" || isLoopback(u) || allowPlaintext {
		return nil
	}
	// Redacted() rather than the URL itself: NewClient strips userinfo before
	// calling this, but an error message about protecting a credential is the
	// last place that should depend on someone upstream having remembered to.
	return fmt.Errorf("refusing to send credentials in cleartext to %s\n"+
		"use https://, or pass --allow-plaintext if this network is genuinely trusted", u.Redacted())
}

// redactURL renders a URL for an error message with any password removed.
//
// It takes a string rather than a *url.URL because the callers that need it
// most are on the path where parsing has already failed, and an unparseable
// URL is exactly as capable of carrying a password as a valid one.
func redactURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	// Unparseable, so locate the userinfo by hand: it can only sit between the
	// scheme separator and the "@" that ends the authority's credential part.
	start := 0
	if scheme := strings.Index(raw, "://"); scheme >= 0 {
		start = scheme + 3
	}
	authority := raw[start:]
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return raw
	}
	return raw[:start] + "xxxxx@" + raw[start+at+1:]
}

// isLoopback reports whether the host resolves to this machine by name or by
// literal address, without performing DNS: a name that merely happens to point
// at 127.0.0.1 today is not a property the transport check can rely on.
func isLoopback(u *url.URL) bool {
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// BaseURL returns the instance root, without the /rest suffix.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// emit reports a completed request. The method is taken from the request that
// was actually sent.
func (c *Client) emit(ctx context.Context, method, path string, query url.Values, status int, took time.Duration, attempt int, err error) {
	if c.onRequest == nil {
		return
	}
	c.onRequest(RequestEvent{
		Method:   method,
		Path:     path,
		Scope:    scopeFrom(ctx),
		Status:   status,
		Duration: took,
		Err:      err,
		Attempt:  attempt,
		Query:    query,
	})
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Client) warnf(format string, args ...any) {
	if c.Warnf != nil {
		c.Warnf(format, args...)
		return
	}
	// Without a dedicated sink a warning is better shown as narration than
	// dropped entirely.
	c.logf("warning: "+format, args...)
}

// APIError describes a non-2xx response.
type APIError struct {
	StatusCode int
	Path       string
	Messages   []string
}

func (e *APIError) Error() string {
	msg := strings.Join(e.Messages, "; ")
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("GET %s: %d %s", e.Path, e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404. For optional features this means
// "this instance does not have it" rather than a real failure.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsForbidden reports whether err is a 403, i.e. the credential is valid but
// lacks the permission this endpoint needs. Rules turn this into MANUAL, not
// FAIL: the question could not be asked, so it must not be answered.
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

// IsUnauthorized reports whether err is a 401.
//
// A 401 means two different things depending on when it arrives, which is why
// this is not part of IsUnavailable and why the fetcher decides with
// Fetcher.unreadable rather than calling this directly.
//
// Before the credential has been proven, a 401 means the instance rejected it
// outright, and degrading that to MANUAL would turn a mistyped token into a
// report full of "could not determine" and a score of 0 — reading like a
// finding about the instance rather than a mistake in the invocation.
//
// After the preflight has succeeded, a 401 cannot mean that: the same
// credential just worked. Bitbucket answers 401 rather than 403 on the admin
// endpoints when the user is authenticated but is not an instance
// administrator — "You are not permitted to access this resource" — so at that
// point it carries exactly the meaning of a 403.
func IsUnauthorized(err error) bool { return hasStatus(err, http.StatusUnauthorized) }

// IsUnavailable reports whether the data simply could not be read for a reason
// that is not fatal to the scan: the endpoint is absent on this version, or
// this credential may not see it.
func IsUnavailable(err error) bool {
	return IsNotFound(err) || IsForbidden(err) || hasStatus(err, http.StatusMethodNotAllowed)
}

func hasStatus(err error, code int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == code
}

// get issues a single GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.raw(ctx, path, query)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: decode response: %w", path, err)
	}
	return nil
}

// raw issues a GET and returns the response body, retrying throttled and
// transient responses with exponential backoff.
func (c *Client) raw(ctx context.Context, path string, query url.Values) ([]byte, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/rest" + path
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoff(attempt)
			c.logf("retrying %s in %s (attempt %d/%d)", path, delay, attempt, c.maxRetries)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build request for %s: %w", path, err)
		}
		req.Header.Set("Accept", "application/json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		} else {
			req.SetBasicAuth(c.username, c.password)
		}

		started := time.Now()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Transport errors are worth retrying; a cancelled context is not.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			c.emit(ctx, req.Method, path, query, 0, time.Since(started), attempt, err)
			lastErr = fmt.Errorf("GET %s: %w", path, err)
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		c.emit(ctx, req.Method, path, query, resp.StatusCode, time.Since(started), attempt, readErr)
		if readErr != nil {
			lastErr = fmt.Errorf("GET %s: read body: %w", path, readErr)
			continue
		}

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return body, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = &APIError{StatusCode: resp.StatusCode, Path: path, Messages: parseErrorMessages(body)}
			if wait, ok := retryAfter(resp); ok {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(wait):
				}
			}
			continue
		default:
			// 4xx other than throttling is deterministic: do not retry.
			return nil, &APIError{StatusCode: resp.StatusCode, Path: path, Messages: parseErrorMessages(body)}
		}
	}
	return nil, lastErr
}

// page is the envelope Bitbucket wraps every paginated collection in.
type page struct {
	Size          int               `json:"size"`
	Limit         int               `json:"limit"`
	IsLastPage    bool              `json:"isLastPage"`
	Start         int               `json:"start"`
	NextPageStart *int              `json:"nextPageStart"`
	Values        []json.RawMessage `json:"values"`
}

// getPaged walks every page of a collection endpoint, decoding each element
// into a fresh T and appending it.
func getPaged[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	if query == nil {
		query = url.Values{}
	}
	var out []T
	start := 0
	for i := 0; i < maxPages; i++ {
		q := cloneValues(query)
		q.Set("limit", strconv.Itoa(pageLimit))
		if start > 0 {
			q.Set("start", strconv.Itoa(start))
		}

		var p page
		if err := c.get(ctx, path, q, &p); err != nil {
			return out, err
		}
		for _, rawItem := range p.Values {
			var item T
			if err := json.Unmarshal(rawItem, &item); err != nil {
				return out, fmt.Errorf("GET %s: decode item: %w", path, err)
			}
			out = append(out, item)
		}
		// A last page ends the walk.
		if p.IsLastPage {
			return out, nil
		}

		// Not the last page, so there is more to read. Bitbucket normally says
		// where to resume; some add-on endpoints do not, and treating a missing
		// nextPageStart as the end quietly returned the first hundred results
		// as though they were all of them — with Available still true, so the
		// verdict was drawn from a list nobody knew was short. A truncated
		// branch-permission list reads as a branch nobody protected.
		next := start + len(p.Values)
		if p.NextPageStart != nil && *p.NextPageStart > start {
			next = *p.NextPageStart
		}
		if next <= start {
			// The page said there was more and offered no way to reach it.
			// Erroring makes the caller mark the data unavailable, which is
			// what turns this into MANUAL instead of a confident wrong answer.
			return out, fmt.Errorf("GET %s: the instance reported more results after %d but no way to reach them", path, start)
		}
		start = next
	}
	return out, fmt.Errorf("GET %s: exceeded %d pages", path, maxPages)
}

func cloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in)+2)
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// parseErrorMessages pulls the human-readable text out of Bitbucket's error
// envelope, falling back to a truncated body when it is not JSON.
func parseErrorMessages(body []byte) []string {
	var envelope struct {
		Errors []struct {
			Message       string `json:"message"`
			ExceptionName string `json:"exceptionName"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			if e.Message != "" {
				msgs = append(msgs, e.Message)
			} else if e.ExceptionName != "" {
				msgs = append(msgs, e.ExceptionName)
			}
		}
		if len(msgs) > 0 {
			return msgs
		}
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil
	}
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return []string{text}
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		// Cap it: an instance asking us to sleep for an hour should surface as
		// an error rather than a hung scan.
		if secs > 60 {
			secs = 60
		}
		return time.Duration(secs) * time.Second, true
	}
	return 0, false
}

// backoff grows exponentially, with jitter.
//
// The jitter is not decoration. A scan fetches repositories concurrently, so a
// throttled instance rejects a batch of requests at once; without jitter every
// one of them would sleep for exactly the same interval and retry in the same
// instant, reproducing the burst that caused the throttling. Subtracting rather
// than adding keeps the documented ceiling a real ceiling.
func backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > 16*time.Second {
		d = 16 * time.Second
	}
	return d - time.Duration(rand.Int64N(int64(d/4)))
}
