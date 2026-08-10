package scmbench.rules.cis_1_1_6_test

import rego.v1

import data.scmbench.rules.cis_1_1_6
import data.scmbench.testdata

# Bitbucket Data Center has no code-owners mechanism, so this control has no
# automatable answer. The test exists to stop one appearing: default reviewers
# look like a match and are advisory unless paired with an approval count, and
# mapping them onto this control would overstate what is enforced.
test_always_manual if {
	r := cis_1_1_6.result with input as testdata.repo_input({"defaultReviewers": [{"name": "alice"}]})
	r.status == "MANUAL"
}
