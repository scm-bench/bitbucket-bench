package scmbench.rules.cis_1_1_8

import rego.v1

import data.scmbench.lib

threshold := object.get(lib.cfg, ["thresholds", "staleBranchDays"], 90)

allowed := object.get(lib.cfg, ["thresholds", "maxStaleBranches"], 0)

# The default branch is excluded: a quiet default branch means a quiet project,
# not an abandoned branch that should be pruned.
stale := [b.displayId |
	some b in object.get(lib.resource, "branches", [])
	object.get(b, "isDefault", false) == false
	age := object.get(b, "ageDays", -1)
	age >= threshold
]

decidable if {
	lib.available("branches")
	lib.available("branchAges")
}

result := {
	"status": "MANUAL",
	"details": "Branch listing or commit timestamps could not be read, so branch staleness is unknown.",
} if {
	not decidable
} else := {
	"status": "PASS",
	"details": sprintf("No branch other than the default has been untouched for %d days or more.", [threshold]),
} if {
	count(stale) <= allowed
} else := {
	"status": "FAIL",
	"details": sprintf("%d branch(es) have had no commits for %d days or more: %s.", [count(stale), threshold, lib.joined(stale, 10)]),
	"evidence": sort(stale),
}
