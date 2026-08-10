package scmbench.rules.cis_1_1_16_test

import rego.v1

import data.scmbench.rules.cis_1_1_16
import data.scmbench.testdata

# "fast-forward-only" is Bitbucket's "Prevent rewriting history".
test_passes_with_a_fast_forward_only_restriction if {
	r := cis_1_1_16.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "fast-forward-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

# A branch nobody can write to cannot be force pushed either.
test_passes_with_a_read_only_restriction if {
	r := cis_1_1_16.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "read-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

# Requiring a pull request says nothing about rewriting history behind it.
test_fails_when_only_pull_requests_are_required if {
	r := cis_1_1_16.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "pull-request-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "FAIL"
}

test_fails_with_no_restriction if {
	r := cis_1_1_16.result with input as testdata.repo_input({"branchRestrictions": []})
	r.status == "FAIL"
}

test_manual_when_branch_permissions_are_unreadable if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [],
		"available": testdata.without("branchRestrictions"),
	})
	r.status == "MANUAL"
}

test_not_applicable_for_an_empty_repository if {
	r := cis_1_1_16.result with input as testdata.input_for({
		"fullName": "PRJ/empty",
		"empty": true,
		"available": testdata.every_available,
	})
	r.status == "NA"
}
