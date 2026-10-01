package scmbench.rules.cis_1_1_16

import rego.v1

import data.scmbench.lib

# "fast-forward-only" is Bitbucket's "Prevent rewriting history": it rejects
# any push that is not a fast-forward, which is exactly a force push.
matching := lib.restrictions("fast-forward-only")

# A read-only branch cannot be force pushed either, since it cannot be written
# to at all.
blocked := array.concat(matching, lib.restrictions("read-only"))

kinds := {"fast-forward-only", "read-only"}

# The bundled Reject Force Push hook (or an add-on configured alongside it)
# refuses every non-fast-forward push on every branch, for everyone. Measured
# on Bitbucket 10.4: a repository protected that way, with no branch
# restriction, used to fail this control.
hook_keys := {k | some k in lib.as_list(object.get(lib.cfg, "forcePushHookKeys", []))}

hook_rejects_force_pushes if {
	some h in lib.list("hooks")
	h.enabled == true
	h.key in hook_keys
	object.get(h, "type", "") == "PRE_RECEIVE"
}

# Every FAIL below also needs the hooks to have been read: an unread hook list
# could hold the one hook that makes the restrictions beside the point.
hook_known_absent if {
	lib.available("hooks")
	not hook_rejects_force_pushes
}

result := lib.archived_na if {
	lib.archived_repository
} else := lib.branch_protection_na if {
	lib.empty_repository
} else := {
	"status": "PASS",
	"details": "Force pushes are rejected on every branch by the Reject Force Push hook.",
	"evidence": [concat(", ", sort({h.key | some h in lib.list("hooks"); h.enabled == true; h.key in hook_keys}))],
} if {
	lib.available("hooks")
	hook_rejects_force_pushes
} else := {
	"status": "MANUAL",
	"details": "Branch permissions could not be read, so history-rewrite protection on the default branch is unknown.",
} if {
	not lib.available("branchRestrictions")
} else := {
	"status": "FAIL",
	"details": sprintf("No branch restriction or hook blocks force pushes on any branch, so anyone with write access can rewrite or erase the history of %s.", [lib.default_branch_name]),
	"evidence": [
		sprintf("no %s restriction exists on any branch", [concat(" or ", sort(kinds))]),
		"no Reject Force Push hook is enabled",
	],
} if {
	count(lib.restrictions_of(kinds)) == 0
	hook_known_absent
} else := lib.default_branch_unknown if {
	not lib.default_branch_known
} else := lib.match_unknown if {
	count(blocked) == 0
	count(lib.unresolved(kinds)) > 0
} else := {
	"status": "FAIL",
	"details": sprintf("%s can be force pushed, letting anyone with write access rewrite or erase merged history.", [lib.default_branch_name]),
	"evidence": [sprintf("no fast-forward-only or read-only restriction covers %s, and no Reject Force Push hook is enabled", [lib.default_branch_name])],
} if {
	count(blocked) == 0
	hook_known_absent
} else := {
	"status": "FAIL",
	"details": sprintf("History rewrites on %s are restricted, but %s can still force push and erase the history reviewers approved.", [lib.default_branch_name, lib.bypass_detail(blocked)]),
	"evidence": lib.bypass_evidence(blocked),
} if {
	lib.bypass_exceeded(blocked)
	hook_known_absent
	count(lib.unresolved(kinds)) == 0
} else := {
	"status": "MANUAL",
	"details": sprintf("History rewrites on %s are restricted, but a group holding an exemption could not be expanded, so whether the restriction binds everyone is unknown.", [lib.default_branch_name]),
} if {
	count(blocked) > 0
	lib.bypass_undecidable(blocked)
} else := {
	"status": "PASS",
	"details": sprintf("History rewrites on %s are blocked%s.", [lib.default_branch_name, lib.exemption_note(blocked)]),
} if {
	count(blocked) > 0
	not lib.bypass_exceeded(blocked)
} else := {
	"status": "MANUAL",
	"details": sprintf("No restriction binding everyone blocks force pushes to %s, and the repository hooks could not be read to see whether the Reject Force Push hook does.", [lib.default_branch_name]),
}
