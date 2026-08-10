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
	"status": "PASS",
	"details": sprintf("History rewrites on %s are blocked%s.", [lib.default_branch_name, lib.exemption_note(blocked)]),
} if {
	count(blocked) > 0
} else := {
	"status": "FAIL",
	"details": sprintf("%s can be force pushed, letting anyone with write access rewrite or erase merged history.", [lib.default_branch_name]),
	"evidence": [sprintf("no fast-forward-only or read-only restriction covers %s", [lib.default_branch_name])],
}
