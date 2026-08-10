package scmbench.rules.cis_1_1_13_test

import rego.v1

import data.scmbench.rules.cis_1_1_13
import data.scmbench.testdata

test_passes_when_only_linear_strategies_are_enabled if {
	r := cis_1_1_13.result with input as testdata.repo_input({"pullRequestSettings": {"mergeStrategies": [
		{"id": "squash", "enabled": true},
		{"id": "ff", "enabled": true},
	]}})
	r.status == "PASS"
}

test_fails_when_a_merge_commit_strategy_is_enabled if {
	r := cis_1_1_13.result with input as testdata.repo_input({"pullRequestSettings": {"mergeStrategies": [
		{"id": "squash", "enabled": true},
		{"id": "no-ff", "enabled": true},
	]}})
	r.status == "FAIL"
	contains(r.details, "no-ff")
}

# A strategy that exists but is switched off cannot introduce a merge commit.
test_passes_when_the_offending_strategy_is_disabled if {
	r := cis_1_1_13.result with input as testdata.repo_input({"pullRequestSettings": {"mergeStrategies": [
		{"id": "squash", "enabled": true},
		{"id": "no-ff", "enabled": false},
	]}})
	r.status == "PASS"
}

test_manual_when_merge_strategies_are_unreadable if {
	r := cis_1_1_13.result with input as testdata.repo_input({
		"pullRequestSettings": {"mergeStrategies": []},
		"available": testdata.without("mergeStrategies"),
	})
	r.status == "MANUAL"
}
