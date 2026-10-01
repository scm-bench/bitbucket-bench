package scmbench.rules.cis_1_1_15

import rego.v1

import data.scmbench.lib

# Either restriction removes the ability to push straight to the branch:
# "pull-request-only" forces changes through review, "read-only" blocks all
# writes outright.
matching := array.concat(
	lib.restrictions("pull-request-only"),
	lib.restrictions("read-only"),
)

# kinds are the restriction types that answer this control.
kinds := {"pull-request-only", "read-only"}

result := lib.archived_na if {
	lib.archived_repository
} else := lib.branch_protection_na if {
	lib.empty_repository
} else := {
	"status": "MANUAL",
	"details": "Branch permissions could not be read, so direct-push restrictions on the default branch are unknown.",
} if {
	not lib.available("branchRestrictions")
} else := {
	"status": "FAIL",
	"details": sprintf("No branch restriction blocks direct pushes on any branch, so anyone with write access can push directly to %s, bypassing pull request review entirely.", [lib.default_branch_name]),
	"evidence": [sprintf("no %s restriction exists on any branch", [concat(" or ", sort(kinds))])],
} if {
	# Nothing of either kind exists anywhere, so the default branch is
	# unprotected whichever branch that turns out to be.
	count(lib.restrictions_of(kinds)) == 0
} else := lib.default_branch_unknown if {
	not lib.default_branch_known
} else := {
	"status": "FAIL",
	"details": sprintf("Anyone with write access can push directly to %s, bypassing pull request review entirely.", [lib.default_branch_name]),
	"evidence": [sprintf("no read-only or pull-request-only restriction covers %s", [lib.default_branch_name])],
} if {
	count(matching) == 0
} else := {
	"status": "FAIL",
	"details": sprintf("Direct pushes to %s are restricted, but %s can push straight past the restriction, so review is optional for them.", [lib.default_branch_name, lib.bypass_detail(matching)]),
	"evidence": lib.bypass_evidence(matching),
} if {
	lib.bypass_exceeded(matching)
} else := {
	"status": "MANUAL",
	"details": sprintf("Direct pushes to %s are restricted, but a group holding an exemption could not be expanded, so whether the restriction binds everyone is unknown.", [lib.default_branch_name]),
} if {
	lib.bypass_undecidable(matching)
} else := {
	"status": "PASS",
	"details": sprintf("Direct pushes to %s are blocked by a branch restriction%s.", [lib.default_branch_name, lib.exemption_note(matching)]),
}
