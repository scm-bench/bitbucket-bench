package scmbench.rules.cis_1_1_17

import rego.v1

import data.scmbench.lib

matching := array.concat(
	lib.restrictions("no-deletes"),
	lib.restrictions("read-only"),
)

# kinds are the restriction types that answer this control.
kinds := {"no-deletes", "read-only"}

result := lib.archived_na if {
	lib.archived_repository
} else := lib.branch_protection_na if {
	lib.empty_repository
} else := {
	"status": "MANUAL",
	"details": "Branch permissions could not be read, so deletion protection on the default branch is unknown.",
} if {
	not lib.available("branchRestrictions")
} else := {
	"status": "FAIL",
	"details": sprintf("No branch restriction blocks deletion on any branch, so anyone with write access can delete %s.", [lib.default_branch_name]),
	"evidence": [sprintf("no %s restriction exists on any branch", [concat(" or ", sort(kinds))])],
} if {
	# Nothing of either kind exists anywhere, so the default branch is
	# unprotected whichever branch that turns out to be.
	count(lib.restrictions_of(kinds)) == 0
} else := lib.default_branch_unknown if {
	not lib.default_branch_known
} else := lib.match_unknown if {
	count(matching) == 0
	count(lib.unresolved(kinds)) > 0
} else := {
	"status": "FAIL",
	"details": sprintf("%s can be deleted by anyone with write access.", [lib.default_branch_name]),
	"evidence": [sprintf("no no-deletes or read-only restriction covers %s", [lib.default_branch_name])],
} if {
	count(matching) == 0
} else := {
	"status": "FAIL",
	"details": sprintf("Deletion of %s is restricted, but %s can still delete it, taking the branch and its protections with them.", [lib.default_branch_name, lib.bypass_detail(matching)]),
	"evidence": lib.bypass_evidence(matching),
} if {
	lib.bypass_exceeded(matching)

	# An unresolved restriction might cover the branch and bind the people
	# the others exempt, so it has to be ruled out before calling them a hole.
	count(lib.unresolved(kinds)) == 0
} else := {
	"status": "MANUAL",
	"details": sprintf("Deletion of %s is restricted, but a group holding an exemption could not be expanded, so whether the restriction binds everyone is unknown.", [lib.default_branch_name]),
} if {
	lib.bypass_undecidable(matching)
} else := {
	"status": "PASS",
	"details": sprintf("Deletion of %s is blocked%s.", [lib.default_branch_name, lib.exemption_note(matching)]),
} if {
	not lib.bypass_exceeded(matching)
} else := {
	"status": "MANUAL",
	"details": sprintf("The restriction covering %s lets %s past it, but another restriction may cover the branch too and could not be resolved, so whether they are bound is unknown.", [lib.default_branch_name, lib.bypass_detail(matching)]),
}
