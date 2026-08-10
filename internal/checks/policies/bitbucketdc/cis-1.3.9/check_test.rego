package scmbench.rules.cis_1_3_9_test

import rego.v1

import data.scmbench.rules.cis_1_3_9
import data.scmbench.testdata

# Domain verification is a hosted-SaaS concept with no counterpart on a
# self-hosted instance. NA rather than MANUAL: there is nothing for a human to
# go and check either.
test_always_not_applicable if {
	r := cis_1_3_9.result with input as testdata.input_for({"available": {}})
	r.status == "NA"
}
