package scmbench.rules.cis_1_2_2_test

import rego.v1

import data.scmbench.rules.cis_1_2_2
import data.scmbench.testdata

# Deciding this needs the global "Project Creator" grant read together with
# every group's membership, and what counts as "limited" is deployment
# specific. The test pins that no interpretation gets encoded as a verdict.
test_always_manual if {
	r := cis_1_2_2.result with input as testdata.input_for({"admins": [], "available": {}})
	r.status == "MANUAL"
}
