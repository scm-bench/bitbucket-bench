package scmbench.rules.cis_1_1_17

import rego.v1

import data.scmbench.lib

matching := array.concat(
	lib.restrictions("no-deletes"),
	lib.restrictions("read-only"),
)

decidable if {
	lib.available("branchRestrictions")
	lib.has_default_branch
}

result := lib.branch_protection_na if {
	lib.empty_repository
} else := {
	"status": "MANUAL",
	"details": "Branch permissions could not be read, so deletion protection on the default branch is unknown.",
} if {
	not decidable
} else := {
	"status": "PASS",
	"details": sprintf("Deletion of %s is blocked%s.", [lib.default_branch_name, lib.exemption_note(matching)]),
} if {
	count(matching) > 0
} else := {
	"status": "FAIL",
	"details": sprintf("%s can be deleted by anyone with write access.", [lib.default_branch_name]),
	"evidence": [sprintf("no no-deletes or read-only restriction covers %s", [lib.default_branch_name])],
}
