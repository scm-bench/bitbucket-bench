package scmbench.rules.cis_1_1_11

import rego.v1

import data.scmbench.lib

enabled := lib.pr_setting("requiredAllTasksComplete", false)

result := {
	"status": "MANUAL",
	"details": "Pull request merge checks could not be read, so task completion enforcement is unknown.",
} if {
	not lib.available("pullRequestSettings")
} else := {
	"status": "PASS",
	"details": "All pull request tasks must be resolved before merging.",
} if {
	enabled == true
} else := {
	"status": "FAIL",
	"details": "Pull requests can be merged with open tasks, so review findings can be silently dropped.",
	"evidence": ["requiredAllTasksComplete = false"],
}
