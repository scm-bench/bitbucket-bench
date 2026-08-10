package scmbench.rules.cis_1_1_17_test

import rego.v1

import data.scmbench.rules.cis_1_1_17
import data.scmbench.testdata

test_passes_with_a_no_deletes_restriction if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "no-deletes",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

test_passes_with_a_read_only_restriction if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "read-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

# Preventing history rewrites does not prevent removing the branch outright.
test_fails_when_only_history_rewrites_are_blocked if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "fast-forward-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "FAIL"
}

test_manual_when_branch_permissions_are_unreadable if {
	r := cis_1_1_17.result with input as testdata.repo_input({
		"branchRestrictions": [],
		"available": testdata.without("branchRestrictions"),
	})
	r.status == "MANUAL"
}

test_not_applicable_for_an_empty_repository if {
	r := cis_1_1_17.result with input as testdata.input_for({
		"fullName": "PRJ/empty",
		"empty": true,
		"available": testdata.every_available,
	})
	r.status == "NA"
}
