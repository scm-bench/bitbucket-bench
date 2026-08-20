# Helpers shared by every control.
#
# The contract each control implements is a single `result` document:
#
#   result := {"status": "PASS"|"FAIL"|"MANUAL"|"NA", "details": "...", "evidence": [...]}
#
# MANUAL means the instance did not expose enough data to decide — never a
# guess dressed up as a verdict. NA means the control does not apply to this
# resource at all. Only PASS and FAIL affect the score.
package scmbench.lib

import rego.v1

# resource is the repository or organization under evaluation.
resource := input.resource

# cfg holds the thresholds, so no control hard-codes a number.
cfg := input.config

# available reports whether a given part of the snapshot was fetched
# successfully. A missing key is treated as unavailable.
available(key) if {
	object.get(resource, ["available", key], false) == true
}

# list reads a list-valued field, treating an explicit JSON null exactly like a
# missing one.
#
# This is load-bearing. Go marshals a nil slice to null rather than [], and
# object.get only substitutes its default when the key is *absent* — a key
# present with a null value comes back as null. Passing that null to concat or
# sort raises a type error, which makes the whole rule undefined and the control
# report nothing at all. Always read lists through this.
list(path) := value if {
	value := object.get(resource, path, [])
	is_array(value)
} else := []

# empty_repository is true for a repository with no commits yet.
empty_repository if {
	object.get(resource, "empty", false) == true
}

# has_default_branch is false for empty repositories and for any repository
# whose default branch could not be resolved.
has_default_branch if {
	object.get(resource, "defaultBranch", "") != ""
}

# default_branch_name is the short branch name, for use in messages.
default_branch_name := name if {
	name := object.get(resource, "defaultBranchDisplay", "")
	name != ""
} else := "the default branch"

# restrictions returns every branch restriction of the given type that covers
# the default branch. Matcher resolution — globs, branch models — is done by
# the fetcher, so controls only read the resolved boolean.
restrictions(kind) := [r |
	some r in list("branchRestrictions")
	r.type == kind
	r.matchesDefaultBranch == true
]

# as_list reads a list already in hand the way list() reads one by path:
# an explicit null is treated exactly like a missing field. Go marshals a nil
# slice to null, and object.get substitutes its default only when the key is
# absent — a present-but-null value would reach array.concat and error the
# whole rule out of existence.
as_list(x) := [] if {
	x == null
} else := x

# exemptions lists the principals allowed to bypass the given restrictions.
# A restriction still counts as configured when it has exemptions, but the
# report names them so the hole is visible.
exemptions(rs) := sort({e |
	some r in rs
	some e in array.concat(
		as_list(object.get(r, "exemptUsers", [])),
		as_list(object.get(r, "exemptGroups", [])),
	)
})

# exempt_access_keys totals the SSH keys allowed to bypass the restrictions.
#
# They are counted rather than named because the API returns keys, not people,
# and a key's label says nothing useful in a report. Counting them at all is
# the point: an access key that can push past a branch restriction is a bypass
# exactly like an exempt user is, and leaving it out of the note described a
# protection as tighter than it was.
exempt_access_keys(rs) := sum([n |
	some r in rs
	n := object.get(r, "exemptAccessKeys", 0)
	is_number(n)
])

# exemption_note renders a trailing clause naming any bypass principals.
exemption_note(rs) := sprintf(" (bypass allowed for: %s; %s)", [concat(", ", exemptions(rs)), keys_clause(rs)]) if {
	count(exemptions(rs)) > 0
	exempt_access_keys(rs) > 0
} else := sprintf(" (bypass allowed for: %s)", [concat(", ", exemptions(rs))]) if {
	count(exemptions(rs)) > 0
} else := sprintf(" (%s can bypass it)", [keys_clause(rs)]) if {
	exempt_access_keys(rs) > 0
} else := ""

keys_clause(rs) := sprintf("%d access key(s)", [exempt_access_keys(rs)])

# merge_strategies is the configured merge strategy list.
merge_strategies := list(["pullRequestSettings", "mergeStrategies"])

enabled_merge_strategies := [s.id |
	some s in merge_strategies
	s.enabled == true
]

# pr_setting reads one pull request merge check with a default.
pr_setting(key, fallback) := object.get(resource, ["pullRequestSettings", key], fallback)

# branch_protection_na is the shared "nothing to protect" outcome: an empty
# repository, or one whose default branch could not be determined.
branch_protection_na := {
	"status": "NA",
	"details": "Repository has no commits yet, so there is no default branch to protect.",
} if {
	empty_repository
}

# joined renders a list for a message, capping the length so a repository with
# hundreds of stale branches does not produce an unreadable line. The is_array
# guards keep a null from erroring the rule that calls it.
joined(items, limit) := msg if {
	is_array(items)
	count(items) > limit
	shown := array.slice(sort(items), 0, limit)
	msg := sprintf("%s and %d more", [concat(", ", shown), count(items) - limit])
} else := msg if {
	is_array(items)
	msg := concat(", ", sort(items))
} else := ""
