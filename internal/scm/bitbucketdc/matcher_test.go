package bitbucketdc

import "testing"

func matcher(typeID, id, display string) apiMatcher {
	return apiMatcher{ID: id, DisplayID: display, Active: true, Type: flexID{ID: typeID}}
}

func TestMatchesDefaultBranch(t *testing.T) {
	const (
		ref     = "refs/heads/main"
		display = "main"
	)

	model := branchModel{
		resolved:    true,
		Development: &apiRef{ID: "refs/heads/main", DisplayID: "main"},
		Production:  &apiRef{ID: "refs/heads/release", DisplayID: "release"},
	}
	model.Types = append(model.Types, struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		Prefix      string `json:"prefix"`
	}{ID: "FEATURE", DisplayName: "Feature", Prefix: "feature/"})

	// Bitbucket leaves production out of the model until it is configured.
	noProduction := branchModel{resolved: true, Development: &apiRef{ID: "refs/heads/main", DisplayID: "main"}}

	tests := []struct {
		name    string
		matcher apiMatcher
		model   branchModel
		want    bool
		known   bool
	}{
		{"exact ref", matcher(matcherBranch, "refs/heads/main", "main"), model, true, true},
		{"short name only", matcher(matcherBranch, "main", "main"), model, true, true},
		{"different branch", matcher(matcherBranch, "refs/heads/develop", "develop"), model, false, true},
		{"any ref sentinel", apiMatcher{ID: anyRefMatcherID}, model, true, true},
		{"any ref type", matcher(matcherAnyRef, "any", "any"), model, true, true},

		{"pattern star matches", matcher(matcherPattern, "ma*", "ma*"), model, true, true},
		{"pattern star does not cross separator", matcher(matcherPattern, "refs/*", "refs/*"), model, false, true},
		{"pattern double star crosses separator", matcher(matcherPattern, "refs/**", "refs/**"), model, true, true},
		{"pattern question mark", matcher(matcherPattern, "mai?", "mai?"), model, true, true},
		{"pattern non-match", matcher(matcherPattern, "release/*", "release/*"), model, false, true},
		// Atlassian's own example, which the full-ref-or-short-name comparison
		// never matched: patterns match a suffix of the qualified ref.
		{"pattern matches a ref suffix", matcher(matcherPattern, "heads/**/main", "heads/**/main"), model, true, true},

		{"model development resolves to main", matcher(matcherModelBranch, "development", "Development"), model, true, true},
		{"model production is another branch", matcher(matcherModelBranch, "production", "Production"), model, false, true},
		{"unconfigured production selects nothing", matcher(matcherModelBranch, "production", "Production"), noProduction, false, true},
		{"model category does not match main", matcher(matcherModelCategory, "FEATURE", "Feature"), model, false, true},
		{"model category the model does not list", matcher(matcherModelCategory, "HOTFIX", "Hotfix"), model, false, true},

		// Without the branch model the scan cannot say which branch a model
		// matcher names. It used to assume development was the default
		// branch, passing a gitflow repository whose development branch is
		// develop.
		{"unresolved model development is unknown", matcher(matcherModelBranch, "development", ""), branchModel{}, false, false},
		{"unresolved model production is unknown", matcher(matcherModelBranch, "production", ""), branchModel{}, false, false},
		{"unresolved model category is unknown", matcher(matcherModelCategory, "RELEASE", "Release"), branchModel{}, false, false},

		{"unknown matcher type, exact hit", matcher("FUTURE_TYPE", "refs/heads/main", "main"), model, true, true},
		{"unknown matcher type, no hit", matcher("FUTURE_TYPE", "something", "something"), model, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, known := matchesDefaultBranch(tc.matcher, ref, display, tc.model)
			if got != tc.want || known != tc.known {
				t.Errorf("matchesDefaultBranch(%+v) = (%v, known %v), want (%v, known %v)", tc.matcher, got, known, tc.want, tc.known)
			}
		})
	}
}

func TestMatchesDefaultBranchWithoutDefaultBranch(t *testing.T) {
	if got, known := matchesDefaultBranch(matcher(matcherBranch, "refs/heads/main", "main"), "", "", branchModel{}); got || known {
		t.Errorf("no default branch = (%v, known %v); nothing can be said to cover a branch that is not there", got, known)
	}
}

// Every example on Atlassian's branch-permission patterns page, plus the
// boundaries of the suffix rule.
func TestAntMatchFollowsBitbucketPatternRules(t *testing.T) {
	tests := []struct {
		pattern, ref string
		want         bool
	}{
		// Documented examples.
		{"*", "refs/heads/main", true},
		{"*", "refs/heads/a/b", true},
		{"PROJECT-*", "refs/heads/PROJECT-1234", true},
		{"PROJECT-*", "refs/heads/stable/PROJECT-new", true},
		{"PROJECT-*", "refs/tags/PROJECT-1.1", true},
		{"?.?", "refs/tags/1.1", true},
		{"?.?", "refs/heads/stable/2.X", true},
		{"tags/", "refs/tags/1.0", true},
		{"tags/**", "refs/tags/1.0", true},
		{"heads/**/master", "refs/heads/master", true},
		{"heads/**/master", "refs/heads/team/a/master", true},

		// The suffix starts on a segment boundary, never mid-name.
		{"ain", "refs/heads/main", false},
		{"main", "refs/heads/main", true},
		{"main", "main", true},
		{"release/*", "refs/heads/release/1.0", true},
		{"release/*", "refs/heads/release/1.0/rc", false},
		{"release/**", "refs/heads/release/1.0/rc", true},
		{"**/hotfix", "refs/heads/a/b/hotfix", true},
		{"**/hotfix", "refs/heads/hotfix", true},
		{"?at", "refs/heads/cat", true},
		{"?at", "refs/heads/at", false},
		{"refs/heads/main", "refs/heads/main", true},
		{"refs/heads/ma*", "refs/heads/main", true},

		// Patterns are case-sensitive, as git refs are.
		{"Main", "refs/heads/main", false},

		{"", "refs/heads/main", false},
		{"main", "", false},
	}

	for _, tc := range tests {
		if got := antMatch(tc.pattern, tc.ref); got != tc.want {
			t.Errorf("antMatch(%q, %q) = %v, want %v", tc.pattern, tc.ref, got, tc.want)
		}
	}
}

// Older documentation spells restriction types as enum constants; rules know
// the hyphenated form Bitbucket 8+ returns.
func TestRestrictionTypesAreNormalized(t *testing.T) {
	for in, want := range map[string]string{
		"pull-request-only":   "pull-request-only",
		"PULL_REQUEST_ONLY":   "pull-request-only",
		" Fast-Forward-Only ": "fast-forward-only",
		"READ_ONLY":           "read-only",
		"no-deletes":          "no-deletes",
	} {
		if got := normalizeRestrictionType(in); got != want {
			t.Errorf("normalizeRestrictionType(%q) = %q, want %q", in, got, want)
		}
	}
}
