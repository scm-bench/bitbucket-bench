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

# max_bypass is how many principals may hold an exemption from every
# restriction protecting the default branch — how many people the protection
# does not actually bind. -1 turns the check off.
max_bypass := n if {
	n := object.get(cfg, ["thresholds", "maxBypassPrincipals"], 0)
	is_number(n)
} else := 0

# allowed_bypass names the service accounts an exemption is expected on, so
# they do not count against the threshold.
allowed_bypass := {p | some p in as_list(object.get(cfg, "allowedBypassPrincipals", []))}

# exempt_users_of is one restriction's exempt principals, groups already
# expanded to people by the fetcher.
exempt_users_of(r) := {u | some u in as_list(object.get(r, ["exemptPrincipals", "users"], []))}

exempt_keys_of(r) := {k | some k in as_list(object.get(r, "exemptAccessKeyIds", []))}

# bypass_principals is who can get past EVERY restriction in rs — the
# intersection of the exempt sets, not their union.
#
# The intersection is the whole point, and it is what makes a read-only
# restriction read correctly. Protection on a branch is the union of the
# restrictions covering it, so a principal can only actually delete, force
# push or push directly when no restriction stops them: somebody exempt from
# "Prevent all changes" but still subject to "Prevent deletion" cannot delete
# the branch, and naming them as a hole would be wrong. Exempting the release
# managers who may write to an otherwise frozen branch is how that restriction
# is meant to be used — it becomes a bypass only when nothing else covers them.
bypass_principals(rs) := principals if {
	count(rs) > 0
	principals := intersection({s |
		some r in rs
		s := exempt_users_of(r)
	}) - allowed_bypass
} else := set()

# bypass_access_keys counts the keys that get past every restriction, by the
# same intersection rule. Two restrictions exempting one key each is not one
# key exempt from both, which is why the identities are carried rather than
# only the totals.
bypass_access_keys(rs) := count(intersection({s |
	some r in rs
	s := exempt_keys_of(r)
})) if {
	count(rs) > 0
} else := 0

# bypass_count totals the principals and keys the protection does not bind.
bypass_count(rs) := count(bypass_principals(rs)) + bypass_access_keys(rs)

# no_exemptions is true for a restriction that names nobody at all.
no_exemptions(r) if {
	count(as_list(object.get(r, "exemptUsers", []))) == 0
	count(as_list(object.get(r, "exemptGroups", []))) == 0
	object.get(r, "exemptAccessKeys", 0) == 0
}

# keys_identified is false for a snapshot captured before exemptAccessKeyIds
# existed: the total is known, the identities are not, so the keys cannot be
# intersected across restrictions.
keys_identified(r) if {
	object.get(r, "exemptAccessKeys", 0) == count(exempt_keys_of(r))
}

restriction_resolved(r) if {
	object.get(r, ["exemptPrincipals", "complete"], false) == true
	keys_identified(r)
}

# bypass_complete reports that the intersection is exact rather than a lower
# bound.
#
# One restriction naming nobody settles it on its own: intersecting with an
# empty set is empty whatever the other sets hold, so a group that could not be
# expanded elsewhere cannot add anyone. Failing that, every restriction has to
# be fully resolved, because a member the scan could not see might be in all of
# them.
bypass_complete(rs) if {
	some r in rs
	no_exemptions(r)
}

bypass_complete(rs) if {
	every r in rs {
		restriction_resolved(r)
	}
}

# bypass_exceeded is true when more principals can bypass the protection than
# the configuration allows.
bypass_exceeded(rs) if {
	max_bypass >= 0
	bypass_count(rs) > max_bypass
}

# bypass_undecidable is true when the bypass set has not crossed the threshold
# but is only a lower bound, so this scan cannot tell whether it would. By the
# rule the whole tool follows, that is MANUAL rather than a PASS — a group the
# token could not expand is not evidence that nobody is in it.
bypass_undecidable(rs) if {
	max_bypass >= 0
	not bypass_exceeded(rs)
	not bypass_complete(rs)
}

bypass_principal_list(rs) := [p | some p in bypass_principals(rs)]

# bypass_detail names who can bypass, for a failing verdict.
bypass_detail(rs) := sprintf("%s and %d access key(s)", [joined(bypass_principal_list(rs), 4), bypass_access_keys(rs)]) if {
	count(bypass_principals(rs)) > 0
	bypass_access_keys(rs) > 0
} else := joined(bypass_principal_list(rs), 4) if {
	count(bypass_principals(rs)) > 0
} else := sprintf("%d access key(s)", [bypass_access_keys(rs)])

# bypass_evidence states the count and the threshold it was judged against, so
# the verdict can be checked rather than taken on faith.
bypass_evidence(rs) := [
	sprintf("exempt from every restriction covering %s: %s", [default_branch_name, bypass_detail(rs)]),
	sprintf("%d in total; thresholds.maxBypassPrincipals is %d", [bypass_count(rs), max_bypass]),
]

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
