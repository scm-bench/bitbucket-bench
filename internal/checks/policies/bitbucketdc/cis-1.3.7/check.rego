package scmbench.rules.cis_1_3_7

import rego.v1

import data.scmbench.lib

minimum := object.get(lib.cfg, ["thresholds", "minRepositoryAdmins"], 2)

# admins unions repository and project administrator grants, with groups
# expanded. complete is false when a group could not be expanded.
total := object.get(lib.resource, ["admins", "count"], 0)

complete := object.get(lib.resource, ["admins", "complete"], false)

admins := lib.list(["admins", "users"])

decidable if {
	lib.available("permissions")
	complete
}

# A lower bound that already meets the minimum is conclusive, so PASS is
# decided before the completeness gate.
result := {
	"status": "PASS",
	"details": sprintf("%d administrator(s) can manage this repository: %s.", [total, lib.joined(admins, 10)]),
} if {
	total >= minimum
} else := {
	"status": "MANUAL",
	"details": "Repository permissions could not be fully read (or an admin group could not be expanded), so the administrator count is unknown.",
} if {
	not decidable
} else := {
	"status": "FAIL",
	"details": sprintf("Only %d administrator(s) can manage this repository; at least %d are needed so settings stay maintainable if one leaves.", [total, minimum]),
	"evidence": sort(admins),
}
