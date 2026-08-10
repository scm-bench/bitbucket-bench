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

decidable if {
	lib.available("branchRestrictions")
	lib.has_default_branch
}

result := lib.branch_protection_na if {
	lib.empty_repository
} else := {
	"status": "MANUAL",
	"details": "Branch permissions could not be read, so direct-push restrictions on the default branch are unknown.",
} if {
	not decidable
} else := {
	"status": "PASS",
	"details": sprintf("Direct pushes to %s are blocked by a branch restriction%s.", [lib.default_branch_name, lib.exemption_note(matching)]),
} if {
	count(matching) > 0
} else := {
	"status": "FAIL",
	"details": sprintf("Anyone with write access can push directly to %s, bypassing pull request review entirely.", [lib.default_branch_name]),
	"evidence": [sprintf("no read-only or pull-request-only restriction covers %s", [lib.default_branch_name])],
}
