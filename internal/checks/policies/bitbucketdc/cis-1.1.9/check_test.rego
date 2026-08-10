package scmbench.rules.cis_1_1_9_test

import rego.v1

import data.scmbench.rules.cis_1_1_9
import data.scmbench.testdata

test_passes_with_a_required_build_condition if {
	r := cis_1_1_9.result with input as testdata.repo_input({
		"requiredBuilds": [{"matchesDefaultBranch": true}],
		"pullRequestSettings": {"requiredSuccessfulBuilds": 0},
	})
	r.status == "PASS"
}

# The merge check is the weaker of the two mechanisms but still satisfies it.
test_passes_with_a_minimum_successful_build_count if {
	r := cis_1_1_9.result with input as testdata.repo_input({
		"requiredBuilds": [],
		"pullRequestSettings": {"requiredSuccessfulBuilds": 1},
	})
	r.status == "PASS"
}

test_fails_with_neither if {
	r := cis_1_1_9.result with input as testdata.repo_input({
		"requiredBuilds": [],
		"pullRequestSettings": {"requiredSuccessfulBuilds": 0},
	})
	r.status == "FAIL"
}

test_fails_when_the_condition_covers_another_branch if {
	r := cis_1_1_9.result with input as testdata.repo_input({
		"requiredBuilds": [{"matchesDefaultBranch": false}],
		"pullRequestSettings": {"requiredSuccessfulBuilds": 0},
	})
	r.status == "FAIL"
}

# A missing required-builds add-on looks exactly like a repository with no
# conditions, so a clean FAIL needs both sources readable.
test_manual_when_required_builds_are_unreadable if {
	r := cis_1_1_9.result with input as testdata.repo_input({
		"requiredBuilds": [],
		"pullRequestSettings": {"requiredSuccessfulBuilds": 0},
		"available": testdata.without("requiredBuilds"),
	})
	r.status == "MANUAL"
}

test_not_applicable_for_an_empty_repository if {
	r := cis_1_1_9.result with input as testdata.input_for({
		"fullName": "PRJ/empty",
		"empty": true,
		"available": testdata.every_available,
	})
	r.status == "NA"
}
