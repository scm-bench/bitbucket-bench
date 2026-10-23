package bitbucketdc

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every representation a merge-check count has had across releases decodes,
// and none of the unusable ones decodes to a confident zero by accident.
func TestFlexIntAcceptsEveryRepresentation(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{`null`, 7}, // untouched
		{`3`, 3},
		{`"4"`, 4},
		{`"+4"`, 4},   // not a JSON number, but a number all the same
		{`"many"`, 7}, // a non-numeric string carries no count
		{`{"count":2}`, 2},
		{`{"value":5}`, 5},
		{`{"enabled":true}`, 1},
		{`{"enabled":false}`, 7},
		{`[1]`, 7},
	} {
		f := flexInt(7)
		if err := json.Unmarshal([]byte(tc.in), &f); err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if f.Int() != tc.want {
			t.Errorf("%s decoded to %d, want %d", tc.in, f.Int(), tc.want)
		}
	}
	// A number too large for a float is an error, not a silent zero.
	f := flexInt(7)
	if err := f.UnmarshalJSON([]byte(`1e400`)); err == nil {
		t.Error("an out-of-range number must be an error")
	}
	if err := f.UnmarshalJSON(nil); err != nil || f != 7 {
		t.Errorf("empty input: %v, %d", err, f)
	}
}

func TestFlexBoolAcceptsEveryRepresentation(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{`null`, true},
		{`false`, false},
		{`"false"`, false},
		{`"perhaps"`, true}, // unparseable: left alone
		{`{"enabled":false}`, false},
		{`{"value":false}`, false},
		{`{}`, true},
	} {
		f := flexBool(true)
		if err := json.Unmarshal([]byte(tc.in), &f); err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if f.Bool() != tc.want {
			t.Errorf("%s decoded to %v, want %v", tc.in, f.Bool(), tc.want)
		}
	}
}

func TestFlexIDAcceptsNull(t *testing.T) {
	f := flexID{ID: "kept"}
	if err := f.UnmarshalJSON([]byte(` null `)); err != nil || f.ID != "kept" {
		t.Errorf("null: %v, %+v", err, f)
	}
}

// The tip commit time is dug out of whichever metadata key carries it, by
// suffix, falling back to the author timestamp.
func TestLatestCommitEpoch(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta map[string]json.RawMessage
		want int64
	}{
		{"no metadata", nil, 0},
		{"unrelated key", map[string]json.RawMessage{"com.example:other": json.RawMessage(`{"committerTimestamp":1000}`)}, 0},
		{"malformed", map[string]json.RawMessage{"x:latest-commit-metadata": json.RawMessage(`[]`)}, 0},
		{"author only", map[string]json.RawMessage{"x:latest-commit-metadata": json.RawMessage(`{"authorTimestamp":5000}`)}, 5},
		{"neither", map[string]json.RawMessage{"x:latest-commit-metadata": json.RawMessage(`{}`)}, 0},
	} {
		if got := (apiBranch{Metadata: tc.meta}).latestCommitEpoch(); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestBrowsedDirectoryIsNotAFile(t *testing.T) {
	children := json.RawMessage(`{}`)
	if (apiBrowseResponse{Children: &children, Lines: []json.RawMessage{}}).isFile() {
		t.Error("a response with children is a directory")
	}
}

func TestMatcherEdgeCases(t *testing.T) {
	m := func(typ, id, display string) apiMatcher {
		var a apiMatcher
		a.ID, a.DisplayID, a.Type.ID = id, display, typ
		return a
	}
	resolved := branchModel{resolved: true}
	for _, tc := range []struct {
		name                   string
		matcher                apiMatcher
		ref, display           string
		model                  branchModel
		wantMatches, wantKnown bool
	}{
		{"display only", m(matcherBranch, "main", ""), "", "main", resolved, true, true},
		{"pattern in display id", m(matcherPattern, "", "ma*"), "refs/heads/main", "main", resolved, true, true},
		{"unknown model branch", m(matcherModelBranch, "hotfix", ""), "refs/heads/main", "main", resolved, false, false},
		{"unknown type, no exact hit", m("SOMETHING_NEW", "release", ""), "refs/heads/main", "main", resolved, false, false},
		{"unknown type, exact hit", m("SOMETHING_NEW", "main", ""), "refs/heads/main", "main", resolved, true, true},
		{"blank branch value", m(matcherBranch, " ", ""), "refs/heads/main", "main", resolved, false, true},
	} {
		matches, known := matchesDefaultBranch(tc.matcher, tc.ref, tc.display, tc.model)
		if matches != tc.wantMatches || known != tc.wantKnown {
			t.Errorf("%s: (%v, %v), want (%v, %v)", tc.name, matches, known, tc.wantMatches, tc.wantKnown)
		}
	}
}

func TestGlobMatchEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"**x", "y", false},   // "**" runs out of name without a match
		{"a?", "a", false},    // "?" needs a character
		{"a?b", "a/b", false}, // and never a separator
		{"a?b", "acb", true},
	} {
		if got := globMatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// A platform with no enumerable system pool still trusts the bundle.
func TestCAFileWithoutASystemPool(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	defer func(orig func() (*x509.CertPool, error)) { systemCertPool = orig }(systemCertPool)
	systemCertPool = func() (*x509.CertPool, error) { return nil, errors.New("no system pool") }

	cfg, err := tlsWithCAFile(writeCA(t, server))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs == nil {
		t.Error("the bundle must still be trusted")
	}
}
