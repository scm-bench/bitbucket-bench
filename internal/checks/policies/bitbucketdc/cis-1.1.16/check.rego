package scmbench.rules.cis_1_1_16

import rego.v1

import data.scmbench.lib

# "fast-forward-only" is Bitbucket's "Prevent rewriting history": it rejects
# any push that is not a fast-forward, which is exactly a force push.
matching := lib.restrictions("fast-forward-only")

# A read-only branch cannot be force pushed either, since it cannot be written
# to at all.
blocked := array.concat(matching, lib.restrictions("read-only"))

decidable if {
	lib.available("branchRestrictions")
	lib.has_default_branch
}

result := lib.branch_protection_na if {
	lib.empty_repository
} else := {
	"status": "MANUAL",
	"details": "Branch permissions could not be read, so history-rewrite protection on the default branch is unknown.",
} if {
	not decidable
} else := {
	"status": "FAIL",
	"details": sprintf("%s can be force pushed, letting anyone with write access rewrite or erase merged history.", [lib.default_branch_name]),
	"evidence": [sprintf("no fast-forward-only or read-only restriction covers %s", [lib.default_branch_name])],
} if {
	count(blocked) == 0
} else := {
	"status": "FAIL",
	"details": sprintf("History rewrites on %s are restricted, but %s can still force push and erase the history reviewers approved.", [lib.default_branch_name, lib.bypass_detail(blocked)]),
	"evidence": lib.bypass_evidence(blocked),
} if {
	lib.bypass_exceeded(blocked)
} else := {
	"status": "MANUAL",
	"details": sprintf("History rewrites on %s are restricted, but a group holding an exemption could not be expanded, so whether the restriction binds everyone is unknown.", [lib.default_branch_name]),
} if {
	lib.bypass_undecidable(blocked)
} else := {
	"status": "PASS",
	"details": sprintf("History rewrites on %s are blocked%s.", [lib.default_branch_name, lib.exemption_note(blocked)]),
}
