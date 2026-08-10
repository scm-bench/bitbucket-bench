package scmbench.rules.cis_1_1_13

import rego.v1

import data.scmbench.lib

non_linear := object.get(lib.cfg, "nonLinearMergeStrategies", [])

enabled := lib.enabled_merge_strategies

offending := [s |
	some s in enabled
	s in non_linear
]

result := {
	"status": "MANUAL",
	"details": "Merge strategy configuration could not be read, so linear history cannot be confirmed.",
} if {
	not lib.available("mergeStrategies")
} else := {
	"status": "PASS",
	"details": sprintf("Only linear merge strategies are enabled: %s.", [concat(", ", sort(enabled))]),
} if {
	count(offending) == 0
} else := {
	"status": "FAIL",
	"details": sprintf("Merge strategies that create merge commits are enabled: %s.", [concat(", ", sort(offending))]),
	"evidence": [sprintf("enabled strategies: %s", [concat(", ", sort(enabled))])],
}
