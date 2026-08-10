package scmbench.rules.cis_1_3_3

import rego.v1

import data.scmbench.lib

minimum := object.get(lib.cfg, ["thresholds", "minOrgAdmins"], 2)

maximum := object.get(lib.cfg, ["thresholds", "maxOrgAdmins"], 5)

# effectiveAdmins expands admin groups to their members. When a group could not
# be expanded, complete is false and the count is a lower bound.
total := object.get(lib.resource, ["effectiveAdmins", "count"], 0)

complete := object.get(lib.resource, ["effectiveAdmins", "complete"], false)

admins := lib.list(["effectiveAdmins", "users"])

decidable if {
	lib.available("adminUsers")
	complete
}

# Ordering matters: an over-count is conclusive even from a lower bound, so it
# is decided before the completeness gate. An under-count is not.
result := {
	"status": "FAIL",
	"details": sprintf("%d accounts hold instance administrator rights (at most %d recommended), so the blast radius of any one compromised admin is large: %s.", [total, maximum, lib.joined(admins, 10)]),
	"evidence": sort(admins),
} if {
	total > maximum
} else := {
	"status": "MANUAL",
	"details": "Global permissions could not be fully read (the token may lack admin rights, or an admin group could not be expanded), so the administrator count is unknown.",
} if {
	not decidable
} else := {
	"status": "PASS",
	"details": sprintf("%d instance administrators, within the recommended range of %d to %d.", [total, minimum, maximum]),
} if {
	total >= minimum
} else := {
	"status": "FAIL",
	"details": sprintf("Only %d instance administrator(s); at least %d are needed so that losing one account does not lock the organisation out.", [total, minimum]),
	"evidence": sort(admins),
}
