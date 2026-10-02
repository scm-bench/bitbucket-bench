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
// default branch, and whether that could be decided at all.
//
// This resolution lives in Go rather than in Rego on purpose: glob semantics
// and branch-model indirection are fiddly, version-dependent, and have nothing
// to do with policy. Rules read the resulting booleans.
//
// known is false when the answer depends on something the scan could not
// read — a model matcher with the branch model unavailable, or a matcher type
// this code does not recognise. It used to guess instead: a "development
// branch" restriction was assumed to cover the default branch whenever the
// model was unread, which passed CIS-1.1.15 for a gitflow repository whose
// development branch is develop, with not a word of warning.
func matchesDefaultBranch(m apiMatcher, defaultRef, defaultDisplay string, model branchModel) (matches, known bool) {
	if defaultRef == "" && defaultDisplay == "" {
		return false, false
	}
	if defaultRef == "" {
		defaultRef = "refs/heads/" + defaultDisplay
	}
	typeID := strings.ToUpper(strings.TrimSpace(m.Type.ID))
	// Some versions omit the matcher type on the "all branches" entry.
	if m.ID == anyRefMatcherID || typeID == matcherAnyRef {
		return true, true
	}

	switch typeID {
	case matcherBranch:
		return sameRef(m.ID, defaultRef, defaultDisplay) || sameRef(m.DisplayID, defaultRef, defaultDisplay), true

	case matcherPattern:
		pattern := m.ID
		if pattern == "" {
			pattern = m.DisplayID
		}
		return antMatch(pattern, defaultRef), true

	case matcherModelBranch:
		return modelBranchMatches(m.ID, defaultRef, defaultDisplay, model)

	case matcherModelCategory:
		return modelCategoryMatches(m.ID, defaultDisplay, model)

	default:
		// Unknown matcher type: an exact hit is still a hit, but anything else
		// is a question about semantics this code does not know.
		if sameRef(m.ID, defaultRef, defaultDisplay) {
			return true, true
		}
		return false, false
	}
}

// modelBranchMatches resolves a "development"/"production" model branch. Only
// the model says which branch either one is.
func modelBranchMatches(id, defaultRef, defaultDisplay string, model branchModel) (matches, known bool) {
	if !model.resolved {
		return false, false
	}
	var branch *apiRef
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "development":
		branch = model.Development
	case "production":
		branch = model.Production
	default:
		return false, false
	}
	// An unset model branch selects nothing: Bitbucket leaves production out
	// of the model until someone configures it.
	if branch == nil {
		return false, true
	}
	return sameRef(branch.ID, defaultRef, defaultDisplay), true
}

// modelCategoryMatches resolves a branch-type category ("feature", "release",
// ...) to its prefix and tests the default branch against it.
func modelCategoryMatches(id, defaultDisplay string, model branchModel) (matches, known bool) {
	if !model.resolved {
		// The stock prefixes are only defaults; a repository can rename them.
		return false, false
	}
	want := strings.ToUpper(strings.TrimSpace(id))
	for _, t := range model.Types {
		if strings.EqualFold(t.ID, want) && t.Prefix != "" {
			return strings.HasPrefix(defaultDisplay, t.Prefix), true
		}
	}
	// A category the model does not list, or one switched off: it selects
	// nothing.
	return false, true
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

// antMatch implements Bitbucket's branch-permission pattern rules, as
// documented at confluence.atlassian.com/bitbucketserver/branch-permission-patterns-776639814.html:
//
//	?   one character, not a separator
//	*   zero or more characters, not a separator
//	**  zero or more path segments
//
// A pattern ending in "/" has "**" appended, and a pattern "only needs to
// match a suffix of the fully qualified branch or tag name" — on a segment
// boundary. So "main" matches refs/heads/main, "PROJECT-*" matches
// refs/heads/stable/PROJECT-new, and "heads/**/master" matches
// refs/heads/master.
//
// The suffix rule is the part that used to be missing: only the full ref and
// the short branch name were tried, so a pattern like "heads/**/main" never
// matched, and a required-build exemption written that way left the default
// branch counted as gated when it was exempt.
func antMatch(pattern, ref string) bool {
	pattern = strings.TrimSpace(pattern)
	ref = strings.TrimSpace(ref)
	if pattern == "" || ref == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}
	for i := 0; ; {
		if globMatch(pattern, ref[i:]) {
			return true
		}
		next := strings.IndexByte(ref[i:], '/')
		if next < 0 {
			return false
		}
		i += next + 1
	}
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
