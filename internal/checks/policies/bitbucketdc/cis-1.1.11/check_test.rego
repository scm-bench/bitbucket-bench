package scmbench.rules.cis_1_1_11_test

import rego.v1

import data.scmbench.rules.cis_1_1_11
import data.scmbench.testdata

test_passes_when_all_tasks_must_be_resolved if {
	r := cis_1_1_11.result with input as testdata.repo_input({"pullRequestSettings": {"requiredAllTasksComplete": true}})
	r.status == "PASS"
}

test_fails_when_open_tasks_do_not_block_a_merge if {
	r := cis_1_1_11.result with input as testdata.repo_input({"pullRequestSettings": {"requiredAllTasksComplete": false}})
	r.status == "FAIL"
}

test_manual_when_merge_checks_are_unreadable if {
	r := cis_1_1_11.result with input as testdata.repo_input({"available": testdata.without("pullRequestSettings")})
	r.status == "MANUAL"
}

# An archived repository takes no pushes and no pull requests: nothing about how
# a change arrives applies, and none of its settings were read to say otherwise.
test_not_applicable_when_archived if {
	r := cis_1_1_11.result with input as testdata.archived_input
	r.status == "NA"
	contains(r.details, "archived")
}
