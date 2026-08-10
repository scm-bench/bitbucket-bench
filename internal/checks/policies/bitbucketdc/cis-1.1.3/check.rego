package scmbench.rules.cis_1_1_3

import rego.v1

import data.scmbench.lib

required := lib.pr_setting("requiredApprovers", 0)

minimum := object.get(lib.cfg, ["thresholds", "minApprovers"], 2)

result := {
	"status": "MANUAL",
	"details": "Pull request merge checks could not be read, so the required approval count is unknown.",
} if {
	not lib.available("pullRequestSettings")
} else := {
	"status": "PASS",
	"details": sprintf("Pull requests require %d approval(s), meeting the minimum of %d.", [required, minimum]),
} if {
	required >= minimum
} else := {
	"status": "FAIL",
	"details": sprintf("Pull requests require %d approval(s); at least %d independent approvals are needed.", [required, minimum]),
	"evidence": [sprintf("requiredApprovers = %d", [required])],
}
