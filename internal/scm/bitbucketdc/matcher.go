package bitbucketdc

import (
	"strings"
)

// Bitbucket ref matcher type IDs.
const (
	matcherBranch        = "BRANCH"
	matcherPattern       = "PATTERN"
	matcherModelBranch   = "MODEL_BRANCH"
	matcherModelCategory = "MODEL_CATEGORY"
	matcherAnyRef        = "ANY_REF"
)

// anyRefMatcherID is the sentinel Bitbucket uses for the "all branches" matcher.
const anyRefMatcherID = "ANY_REF_MATCHER_ID"

// branchModel is the repository's branching model, which is what gives
// MODEL_BRANCH and MODEL_CATEGORY matchers their meaning. Without it we cannot
// tell whether a "development branch" restriction covers the default branch.
type branchModel struct {
	Development *apiRef `json:"development"`
	Production  *apiRef `json:"production"`
	Types       []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		Prefix      string `json:"prefix"`
	} `json:"types"`
	// resolved is false when the branch model endpoint was unavailable, in
	// which case model matchers fall back to documented defaults.
	resolved bool
}

// matchesDefaultBranch reports whether a ref matcher selects the repository's
// default branch.
//
// This resolution lives in Go rather than in Rego on purpose: glob semantics
// and branch-model indirection are fiddly, version-dependent, and have nothing
// to do with policy. Rules read the resulting boolean.
func matchesDefaultBranch(m apiMatcher, defaultRef, defaultDisplay string, model branchModel) bool {
	if defaultRef == "" && defaultDisplay == "" {
		return false
	}
	typeID := strings.ToUpper(strings.TrimSpace(m.Type.ID))
	// Some versions omit the matcher type on the "all branches" entry.
	if m.ID == anyRefMatcherID || typeID == matcherAnyRef {
		return true
	}

	switch typeID {
	case matcherBranch:
		return sameRef(m.ID, defaultRef, defaultDisplay) || sameRef(m.DisplayID, defaultRef, defaultDisplay)

	case matcherPattern:
		pattern := m.ID
		if pattern == "" {
			pattern = m.DisplayID
		}
		return antMatch(pattern, defaultRef) || antMatch(pattern, defaultDisplay)

	case matcherModelBranch:
		return modelBranchMatches(m.ID, defaultRef, defaultDisplay, model)

	case matcherModelCategory:
		return modelCategoryMatches(m.ID, defaultDisplay, model)

	default:
		// Unknown matcher type: fall back to the comparisons that are always
		// safe. Guessing "matches" here would invent protection that may not
		// exist, so an unrecognised matcher only counts on an exact hit.
		return sameRef(m.ID, defaultRef, defaultDisplay) || antMatch(m.ID, defaultDisplay)
	}
}

// modelBranchMatches resolves a "development"/"production" model branch.
func modelBranchMatches(id, defaultRef, defaultDisplay string, model branchModel) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "development":
		if model.resolved && model.Development != nil {
			return sameRef(model.Development.ID, defaultRef, defaultDisplay)
		}
		// Bitbucket's development branch defaults to the repository default
		// branch, so without the model that is the correct assumption.
		return true
	case "production":
		if model.resolved && model.Production != nil {
			return sameRef(model.Production.ID, defaultRef, defaultDisplay)
		}
		// The production branch is unset unless configured; assuming it covers
		// the default branch would manufacture protection.
		return false
	default:
		return false
	}
}

// modelCategoryMatches resolves a branch-type category ("feature", "release",
// ...) to its prefix and tests the default branch against it.
func modelCategoryMatches(id, defaultDisplay string, model branchModel) bool {
	if defaultDisplay == "" {
		return false
	}
	want := strings.ToUpper(strings.TrimSpace(id))
	if model.resolved {
		for _, t := range model.Types {
			if strings.EqualFold(t.ID, want) && t.Prefix != "" {
				return strings.HasPrefix(defaultDisplay, t.Prefix)
			}
		}
		return false
	}
	// Fall back to Bitbucket's stock prefixes.
	defaults := map[string]string{
		"FEATURE": "feature/",
		"BUGFIX":  "bugfix/",
		"HOTFIX":  "hotfix/",
		"RELEASE": "release/",
	}
	prefix, ok := defaults[want]
	return ok && strings.HasPrefix(defaultDisplay, prefix)
}

// sameRef compares a matcher value against the default branch in both its full
// ref and short forms, since matchers store either.
func sameRef(value, defaultRef, defaultDisplay string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if defaultRef != "" && value == defaultRef {
		return true
	}
	if defaultDisplay != "" && value == defaultDisplay {
		return true
	}
	// "main" vs "refs/heads/main" in either direction.
	return normalizeRef(value) == normalizeRef(defaultRef) && normalizeRef(defaultRef) != ""
}

func normalizeRef(ref string) string {
	return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
}

// antMatch implements the Ant-style glob Bitbucket uses for PATTERN matchers:
//
//	?  one character, not a separator
//	*  zero or more characters, not a separator
//	** zero or more characters, separators included
//
// A pattern without a "refs/" prefix is also tried against the short branch
// name, which is how the UI presents it.
func antMatch(pattern, name string) bool {
	pattern = strings.TrimSpace(pattern)
	name = strings.TrimSpace(name)
	if pattern == "" || name == "" {
		return false
	}
	if pattern == name {
		return true
	}
	if globMatch(pattern, name) {
		return true
	}
	// Compare on equal footing when only one side carries the refs/heads prefix.
	if strings.HasPrefix(name, "refs/heads/") && !strings.HasPrefix(pattern, "refs/") {
		return globMatch(pattern, normalizeRef(name))
	}
	if strings.HasPrefix(pattern, "refs/heads/") && !strings.HasPrefix(name, "refs/") {
		return globMatch(normalizeRef(pattern), name)
	}
	return false
}

// globMatch runs the Ant glob with backtracking. Patterns here are short and
// come from configuration, so the simple recursive form is fast enough.
func globMatch(pattern, name string) bool {
	return matchFrom([]rune(pattern), []rune(name), 0, 0)
}

func matchFrom(pattern, name []rune, pi, ni int) bool {
	for pi < len(pattern) {
		switch pattern[pi] {
		case '*':
			// "**" crosses separators; a single "*" stops at one.
			doubled := pi+1 < len(pattern) && pattern[pi+1] == '*'
			if doubled {
				pi += 2
				// "**/" may also match zero segments, so skip an optional slash.
				if pi < len(pattern) && pattern[pi] == '/' {
					if matchFrom(pattern, name, pi+1, ni) {
						return true
					}
				}
				for i := ni; i <= len(name); i++ {
					if matchFrom(pattern, name, pi, i) {
						return true
					}
				}
				return false
			}
			pi++
			for i := ni; i <= len(name); i++ {
				if i > ni && name[i-1] == '/' {
					break
				}
				if matchFrom(pattern, name, pi, i) {
					return true
				}
			}
			return false

		case '?':
			if ni >= len(name) || name[ni] == '/' {
				return false
			}
			pi++
			ni++

		default:
			if ni >= len(name) || name[ni] != pattern[pi] {
				return false
			}
			pi++
			ni++
		}
	}
	return ni == len(name)
}
