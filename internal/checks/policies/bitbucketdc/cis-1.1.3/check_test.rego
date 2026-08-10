package scmbench.rules.cis_1_1_3_test

import rego.v1

import data.scmbench.rules.cis_1_1_3
import data.scmbench.testdata

test_passes_at_the_configured_minimum if {
	r := cis_1_1_3.result with input as testdata.repo_input({"pullRequestSettings": {"requiredApprovers": 2}})
	r.status == "PASS"
}

test_passes_above_the_minimum if {
	r := cis_1_1_3.result with input as testdata.repo_input({"pullRequestSettings": {"requiredApprovers": 3}})
	r.status == "PASS"
}

test_fails_one_below_the_minimum if {
	r := cis_1_1_3.result with input as testdata.repo_input({"pullRequestSettings": {"requiredApprovers": 1}})
	r.status == "FAIL"
}

# The default when Bitbucket reports nothing is zero approvals, which is the
# permissive reading and therefore the one that must fail.
test_fails_when_no_approval_count_is_configured if {
	r := cis_1_1_3.result with input as testdata.repo_input({"pullRequestSettings": {}})
	r.status == "FAIL"
	contains(r.details, "require 0 approval")
}

test_manual_when_merge_checks_are_unreadable if {
	r := cis_1_1_3.result with input as testdata.repo_input({"available": testdata.without("pullRequestSettings")})
	r.status == "MANUAL"
}
