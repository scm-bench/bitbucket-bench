package scmbench.rules.cis_1_2_3

import rego.v1

# Repository deletion follows from repository and project administrator rights,
# which Bitbucket does not expose as a separately restrictable capability.
# CIS-1.3.7 reports who those administrators are; whether that set is
# appropriately small is a judgement this release leaves to a human.
result := {
	"status": "MANUAL",
	"details": "Repository deletion follows from administrator rights, which Bitbucket does not gate separately. Review the administrator sets reported by CIS-1.3.3 and CIS-1.3.7 and confirm they are appropriately small.",
}
