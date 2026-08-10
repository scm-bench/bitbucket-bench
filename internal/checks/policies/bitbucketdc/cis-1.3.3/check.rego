package scmbench.rules.cis_1_3_3

import rego.v1

import data.scmbench.lib

minimum := object.get(lib.cfg, ["thresholds", "minOrgAdmins"], 2)

maximum := object.get(lib.cfg, ["thresholds", "maxOrgAdmins"], 5)

# Zero means "no upper limit", which is what config.Validate has always assumed
# — it skips the min/max ordering check when either is zero. This rule did not,
# so `maxOrgAdmins: 0` read as "at most zero administrators" and failed every
# instance that had any. Of the two readings only one is ever useful: a policy
# of "no administrators at all" is not something anybody wants, and "however
# many we have is fine" is.
over_limit if {
	maximum > 0
	total > maximum
}

# The PASS message states the range it was judged against, which cannot be
# "2 to 0" when the upper bound is switched off.
range_note := sprintf("within the recommended range of %d to %d", [minimum, maximum]) if {
	maximum > 0
} else := sprintf("at or above the recommended minimum of %d (no upper limit configured)", [minimum])

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
	over_limit
} else := {
	"status": "MANUAL",
	"details": "Global permissions could not be fully read (the token may lack admin rights, or an admin group could not be expanded), so the administrator count is unknown.",
} if {
	not decidable
} else := {
	"status": "PASS",
	"details": sprintf("%d instance administrators, %s.", [total, range_note]),
} if {
	total >= minimum
} else := {
	"status": "FAIL",
	"details": sprintf("Only %d instance administrator(s); at least %d are needed so that losing one account does not lock the organisation out.", [total, minimum]),
	"evidence": sort(admins),
}
