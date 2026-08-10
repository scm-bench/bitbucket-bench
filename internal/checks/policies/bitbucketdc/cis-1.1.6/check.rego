package scmbench.rules.cis_1_1_6

import rego.v1

# Bitbucket Data Center has no CODEOWNERS equivalent. Default reviewers are the
# closest mechanism, but they are advisory unless paired with an approval count,
# so mapping them onto this control would overstate what is enforced. The
# control is reported as MANUAL rather than guessed at.
result := {
	"status": "MANUAL",
	"details": "Bitbucket Data Center has no native code-owners mechanism. Confirm by hand that changes to sensitive paths require review by their owners, typically via default reviewers combined with a minimum approval count.",
}
