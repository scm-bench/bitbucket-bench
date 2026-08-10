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

	tests := []struct {
		name    string
		matcher apiMatcher
		model   branchModel
		want    bool
	}{
		{"exact ref", matcher(matcherBranch, "refs/heads/main", "main"), model, true},
		{"short name only", matcher(matcherBranch, "main", "main"), model, true},
		{"different branch", matcher(matcherBranch, "refs/heads/develop", "develop"), model, false},
		{"any ref sentinel", apiMatcher{ID: anyRefMatcherID}, model, true},
		{"any ref type", matcher(matcherAnyRef, "any", "any"), model, true},

		{"pattern star matches", matcher(matcherPattern, "ma*", "ma*"), model, true},
		{"pattern star does not cross separator", matcher(matcherPattern, "refs/*", "refs/*"), model, false},
		{"pattern double star crosses separator", matcher(matcherPattern, "refs/**", "refs/**"), model, true},
		{"pattern question mark", matcher(matcherPattern, "mai?", "mai?"), model, true},
		{"pattern non-match", matcher(matcherPattern, "release/*", "release/*"), model, false},

		{"model development resolves to main", matcher(matcherModelBranch, "development", "Development"), model, true},
		{"model production is another branch", matcher(matcherModelBranch, "production", "Production"), model, false},
		{"model category does not match main", matcher(matcherModelCategory, "FEATURE", "Feature"), model, false},

		// Without the branch model, development defaults to the repository
		// default branch, while production stays unset.
		{"unresolved model development assumed default", matcher(matcherModelBranch, "development", ""), branchModel{}, true},
		{"unresolved model production not assumed", matcher(matcherModelBranch, "production", ""), branchModel{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesDefaultBranch(tc.matcher, ref, display, tc.model); got != tc.want {
				t.Errorf("matchesDefaultBranch(%+v) = %v, want %v", tc.matcher, got, tc.want)
			}
		})
	}
}

func TestMatchesDefaultBranchWithoutDefaultBranch(t *testing.T) {
	if matchesDefaultBranch(matcher(matcherBranch, "refs/heads/main", "main"), "", "", branchModel{}) {
		t.Error("a repository with no default branch must not match any restriction")
	}
}

func TestModelCategoryMatchesPrefixedBranch(t *testing.T) {
	if !matchesDefaultBranch(matcher(matcherModelCategory, "RELEASE", "Release"), "refs/heads/release/2.0", "release/2.0", branchModel{}) {
		t.Error("a release/* default branch should match the RELEASE category via the stock prefix")
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"main", "main", true},
		{"*", "main", true},
		{"*", "a/b", false},
		{"**", "a/b/c", true},
		{"release/*", "release/1.0", true},
		{"release/*", "release/1.0/rc", false},
		{"release/**", "release/1.0/rc", true},
		{"**/hotfix", "a/b/hotfix", true},
		{"**/hotfix", "hotfix", true},
		{"?at", "cat", true},
		{"?at", "at", false},
		{"", "main", false},
	}

	for _, tc := range tests {
		if got := antMatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("antMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
