package scmbench.rules.cis_1_3_5_test

import rego.v1

import data.scmbench.rules.cis_1_3_5
import data.scmbench.testdata

# MFA is enforced by the identity provider in front of Bitbucket and is not
# observable through its API. A PASS or a FAIL here would be a fabrication
# either way, and this is what stops one being introduced.
test_always_manual if {
	r := cis_1_3_5.result with input as testdata.input_for({"users": [], "available": {"users": true}})
	r.status == "MANUAL"
}
