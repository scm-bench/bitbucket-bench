package scmbench.rules.cis_1_1_12

import rego.v1

import data.scmbench.lib

# A hook counts only when its full key is configured and it can stop a change:
# a pre-receive hook rejects the push, a merge check refuses the merge. A
# post-receive hook runs after the commits are in.
#
# Exact keys, never substrings: Bitbucket bundles "Verify Committer"
# (verify-committer-hook), which checks who pushed and verifies no signature,
# and a substring list containing "verify-commit" accepted it as one.
blocking_types := {"PRE_RECEIVE", "PRE_PULL_REQUEST_MERGE"}

keys := {k | some k in lib.as_list(object.get(lib.cfg, "signatureHookKeys", []))}

matching := {h.key |
	some h in lib.list("hooks")
	h.enabled == true
	h.key in keys
	object.get(h, "type", "") in blocking_types
}

result := lib.archived_na if {
	lib.archived_repository
} else := {
	"status": "MANUAL",
	"details": "Repository hooks could not be read, so signature verification cannot be confirmed.",
} if {
	not lib.available("hooks")
} else := {
	"status": "PASS",
	"details": sprintf("Commit signature verification is enforced by an enabled hook: %s.", [concat(", ", sort(matching))]),
} if {
	count(matching) > 0
} else := {
	"status": "FAIL",
	"details": "No enabled hook verifies commit signatures, so commit authorship cannot be trusted.",
	"evidence": [sprintf("%d hook(s) enabled, none of them a configured signature hook (signatureHookKeys)", [count([h | some h in lib.list("hooks"); h.enabled == true])])],
}
