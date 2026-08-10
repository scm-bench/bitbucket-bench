package scmbench.rules.cis_1_1_8_test

import rego.v1

import data.scmbench.rules.cis_1_1_8
import data.scmbench.testdata

test_passes_when_every_branch_is_recent if {
	r := cis_1_1_8.result with input as testdata.repo_input({"branches": [
		{"displayId": "main", "isDefault": true, "ageDays": 400},
		{"displayId": "feature", "ageDays": 3},
	]})
	r.status == "PASS"
}

# A quiet default branch means a quiet project, not an abandoned branch.
test_the_default_branch_is_never_counted_as_stale if {
	r := cis_1_1_8.result with input as testdata.repo_input({"branches": [{
		"displayId": "main",
		"isDefault": true,
		"ageDays": 3650,
	}]})
	r.status == "PASS"
}

test_fails_past_the_threshold if {
	r := cis_1_1_8.result with input as testdata.repo_input({"branches": [
		{"displayId": "main", "isDefault": true, "ageDays": 1},
		{"displayId": "spike/rewrite", "ageDays": 120},
	]})
	r.status == "FAIL"
	contains(r.details, "spike/rewrite")
}

test_passes_one_day_below_the_threshold if {
	r := cis_1_1_8.result with input as testdata.repo_input({"branches": [{"displayId": "old", "ageDays": 89}]})
	r.status == "PASS"
}

# ageDays is -1 when the commit date could not be read. Comparing that against
# the threshold answered "not stale", which turned missing data into a pass.
test_unknown_age_is_manual_not_fresh if {
	r := cis_1_1_8.result with input as testdata.repo_input({"branches": [{"displayId": "mystery", "ageDays": -1}]})
	r.status == "MANUAL"
	contains(r.details, "mystery")
}

# A branch already over the threshold settles the question, so branches that
# could not be dated cannot change the answer.
test_a_conclusive_failure_wins_over_unknown_ages if {
	r := cis_1_1_8.result with input as testdata.repo_input({"branches": [
		{"displayId": "abandoned", "ageDays": 200},
		{"displayId": "mystery", "ageDays": -1},
	]})
	r.status == "FAIL"
}

test_manual_when_branch_ages_are_unreadable if {
	r := cis_1_1_8.result with input as testdata.repo_input({
		"branches": [],
		"available": testdata.without("branchAges"),
	})
	r.status == "MANUAL"
}

test_null_branches_do_not_break_the_rule if {
	r := cis_1_1_8.result with input as testdata.repo_input({"branches": null})
	r.status == "PASS"
}
