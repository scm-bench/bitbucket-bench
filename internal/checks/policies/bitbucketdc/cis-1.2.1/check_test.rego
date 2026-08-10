package scmbench.rules.cis_1_2_1_test

import rego.v1

import data.scmbench.rules.cis_1_2_1
import data.scmbench.testdata

test_passes_when_a_policy_file_was_found if {
	r := cis_1_2_1.result with input as testdata.repo_input({"files": {
		"probed": ["SECURITY.md", ".github/SECURITY.md"],
		"securityPolicyPaths": ["SECURITY.md"],
	}})
	r.status == "PASS"
	contains(r.details, "SECURITY.md")
}

test_fails_when_every_probed_path_was_absent if {
	r := cis_1_2_1.result with input as testdata.repo_input({"files": {
		"probed": ["SECURITY.md", ".github/SECURITY.md"],
		"securityPolicyPaths": [],
	}})
	r.status == "FAIL"
	contains(r.evidence[0], "SECURITY.md")
}

test_manual_when_the_default_branch_could_not_be_browsed if {
	r := cis_1_2_1.result with input as testdata.repo_input({
		"files": {"probed": [], "securityPolicyPaths": []},
		"available": testdata.without("files"),
	})
	r.status == "MANUAL"
}

test_not_applicable_for_an_empty_repository if {
	r := cis_1_2_1.result with input as testdata.input_for({
		"fullName": "PRJ/empty",
		"empty": true,
		"available": testdata.every_available,
	})
	r.status == "NA"
}

# Go marshals a nil slice to null, and null reaching concat makes the rule
# undefined — a control that produces no verdict at all.
test_null_file_lists_do_not_break_the_rule if {
	r := cis_1_2_1.result with input as testdata.repo_input({"files": {"probed": null, "securityPolicyPaths": null}})
	r.status == "FAIL"
}
