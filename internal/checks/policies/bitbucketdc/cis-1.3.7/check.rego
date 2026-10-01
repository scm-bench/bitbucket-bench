package scmbench.rules.cis_1_3_7

import rego.v1

import data.scmbench.lib

minimum := object.get(lib.cfg, ["thresholds", "minRepositoryAdmins"], 2)

# admins is the people Bitbucket says can administer this repository, less the
# instance administrators: they can administer every repository on the
# instance, so counting them made this control pass everywhere on any instance
# that satisfies CIS-1.3.3. What is left is who the repository has of its own —
# repository and project administrators, groups resolved.
total := object.get(lib.resource, ["admins", "count"], 0)

admins := lib.list(["admins", "users"])

# The set is only trustworthy once the instance administrators are known, since
# without them it may still hold some: a count that may be inflated proves
# neither a pass nor a fail. The fetcher marks "admins" available only then.
result := lib.archived_na if {
	lib.archived_repository
} else := {
	"status": "MANUAL",
	"details": "The repository's administrators could not be resolved, or could not be told apart from the instance administrators, so their number is unknown.",
} if {
	not lib.available("admins")
} else := {
	"status": "PASS",
	"details": sprintf("%d administrator(s) of its own (instance administrators not counted): %s.", [total, lib.joined(admins, 10)]),
} if {
	total >= minimum
} else := {
	"status": "FAIL",
	"details": sprintf("Only %d administrator(s) of its own (instance administrators not counted); at least %d are needed so settings stay maintainable if one leaves.", [total, minimum]),
	"evidence": sort(admins),
}
