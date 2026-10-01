package scmbench.rules.cis_1_1_4_test

import rego.v1

import data.scmbench.rules.cis_1_1_4
import data.scmbench.testdata

test_passes_when_approvals_reset_on_update if {
	r := cis_1_1_4.result with input as testdata.repo_input({"pullRequestSettings": {"unapproveOnUpdate": true}})
	r.status == "PASS"
}

test_fails_when_approvals_survive_an_update if {
	r := cis_1_1_4.result with input as testdata.repo_input({"pullRequestSettings": {"unapproveOnUpdate": false}})
	r.status == "FAIL"
}

# Absent is not enabled: the setting defaults off in Bitbucket.
test_fails_when_the_setting_is_absent if {
	r := cis_1_1_4.result with input as testdata.repo_input({"pullRequestSettings": {}})
	r.status == "FAIL"
}

# Measured on Bitbucket 10.4: without Atlassian's Auto Unapprove app the key is
# never reported. Approvals are then never reset, so it fails — but the first
# move is installing the app, and the finding has to say so.
test_names_the_app_when_bitbucket_does_not_report_the_setting if {
	r := cis_1_1_4.result with input as testdata.repo_input({
		"pullRequestSettings": {},
		"available": testdata.without("unapproveOnUpdate"),
	})
	r.status == "FAIL"
	contains(r.details, "Auto Unapprove")
}

test_manual_when_merge_checks_are_unreadable if {
	r := cis_1_1_4.result with input as testdata.repo_input({"available": testdata.without("pullRequestSettings")})
	r.status == "MANUAL"
}

# An archived repository takes no pushes and no pull requests: nothing about how
# a change arrives applies, and none of its settings were read to say otherwise.
test_not_applicable_when_archived if {
	r := cis_1_1_4.result with input as testdata.archived_input
	r.status == "NA"
	contains(r.details, "archived")
}
