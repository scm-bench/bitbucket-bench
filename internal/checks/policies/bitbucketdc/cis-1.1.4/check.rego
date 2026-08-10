package scmbench.rules.cis_1_1_4

import rego.v1

import data.scmbench.lib

enabled := lib.pr_setting("unapproveOnUpdate", false)

result := {
	"status": "MANUAL",
	"details": "Pull request merge checks could not be read, so approval reset behaviour is unknown.",
} if {
	not lib.available("pullRequestSettings")
} else := {
	"status": "PASS",
	"details": "Existing approvals are dismissed when the source branch is updated.",
} if {
	enabled == true
} else := {
	"status": "FAIL",
	"details": "Approvals survive updates to the source branch, so code can be merged that nobody reviewed.",
	"evidence": ["unapproveOnUpdate = false"],
}
