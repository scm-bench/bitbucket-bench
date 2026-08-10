package scmbench.rules.cis_1_2_3_test

import rego.v1

import data.scmbench.rules.cis_1_2_3
import data.scmbench.testdata

# Bitbucket does not gate deletion separately from administrator rights, so
# there is nothing to read. CIS-1.3.7 reports who those administrators are.
test_always_manual if {
	r := cis_1_2_3.result with input as testdata.input_for({"admins": [], "available": {}})
	r.status == "MANUAL"
}
