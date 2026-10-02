package scmbench.rules.cis_1_1_13_test

import rego.v1

import data.scmbench.rules.cis_1_1_13
import data.scmbench.testdata

test_passes_when_only_linear_strategies_are_enabled if {
	r := cis_1_1_13.result with input as testdata.repo_input({"pullRequestSettings": {"mergeStrategies": [
		{"id": "squash", "enabled": true},
		{"id": "ff-only", "enabled": true},
		{"id": "rebase-ff-only", "enabled": true},
	]}})
	r.status == "PASS"
}

# "Fast-forward" creates a merge commit whenever the target has moved, which on
# a busy repository is most merges: it allows linear history, it does not
# require it.
test_fails_when_fast_forward_with_fallback_is_enabled if {
	r := cis_1_1_13.result with input as testdata.repo_input({"pullRequestSettings": {"mergeStrategies": [
		{"id": "squash", "enabled": true},
		{"id": "ff", "enabled": true},
	]}})
	r.status == "FAIL"
	contains(r.details, "ff")
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

# An archived repository takes no pushes and no pull requests: nothing about how
# a change arrives applies, and none of its settings were read to say otherwise.
test_not_applicable_when_archived if {
	r := cis_1_1_13.result with input as testdata.archived_input
	r.status == "NA"
	contains(r.details, "archived")
}
