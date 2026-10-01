package scmbench.rules.cis_1_1_4

import rego.v1

import data.scmbench.lib

enabled := lib.pr_setting("unapproveOnUpdate", false)

result := lib.archived_na if {
	lib.archived_repository
} else := {
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
	# Bitbucket reports the setting only when the app that provides it is
	# installed. Without it there is no mechanism that resets an approval, so
	# this is a FAIL — with a different first move than an unticked box.
	"status": "FAIL",
	"details": "Approvals survive updates to the source branch, so code can be merged that nobody reviewed: Bitbucket reports no approval-reset setting, which comes from Atlassian's Auto Unapprove app.",
	"evidence": ["unapproveOnUpdate not reported (the Auto Unapprove app is not installed or not enabled)"],
} if {
	not lib.available("unapproveOnUpdate")
} else := {
	"status": "FAIL",
	"details": "Approvals survive updates to the source branch, so code can be merged that nobody reviewed.",
	"evidence": ["unapproveOnUpdate = false"],
}
