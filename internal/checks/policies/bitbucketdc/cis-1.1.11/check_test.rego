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
