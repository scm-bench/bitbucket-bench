package scmbench.rules.cis_1_2_2

import rego.v1

# Deciding this needs the global "Project Creator" permission read together with
# every group's membership and each project's own create grants. The judgement
# of what counts as "limited" is deployment-specific, so v0.1 reports MANUAL
# rather than encoding one interpretation as a verdict.
result := {
	"status": "MANUAL",
	"details": "Confirm by hand that repository and project creation is limited to a small, named set of users or groups rather than granted broadly.",
}
