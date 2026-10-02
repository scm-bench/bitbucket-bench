package scmbench.rules.cis_1_1_12_test

import rego.v1

import data.scmbench.rules.cis_1_1_12
import data.scmbench.testdata

bundled := "com.atlassian.bitbucket.server.bitbucket-bundled-hooks:verify-commit-signature-hook"

hook(key, type, enabled) := {"key": key, "type": type, "enabled": enabled}

test_passes_when_the_bundled_signature_hook_is_enabled if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [hook(bundled, "PRE_RECEIVE", true)]})
	r.status == "PASS"
	contains(r.details, bundled)
}

# An add-on's key, configured alongside the bundled one, counts the same way.
test_passes_with_a_configured_add_on_key if {
	r := cis_1_1_12.result with input as testdata.repo_input_with_config(
		{"hooks": [hook("com.example.signatures:verify", "PRE_RECEIVE", true)]},
		{"signatureHookKeys": [bundled, "com.example.signatures:verify"]},
	)
	r.status == "PASS"
}

# A merge check that refuses unsigned commits is enforcement before merging too.
test_passes_with_a_signature_merge_check if {
	r := cis_1_1_12.result with input as testdata.repo_input_with_config(
		{"hooks": [hook("com.example.signatures:merge-check", "PRE_PULL_REQUEST_MERGE", true)]},
		{"signatureHookKeys": ["com.example.signatures:merge-check"]},
	)
	r.status == "PASS"
}

# Measured on Bitbucket 10.4: the bundled "Verify Committer" checks that the
# pusher authored the commits and verifies no signature. Its key contains
# "verify-commit", a substring the old default list matched on, and it passed.
test_fails_with_only_verify_committer_enabled if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [hook(
		"com.atlassian.bitbucket.server.bitbucket-bundled-hooks:verify-committer-hook",
		"PRE_RECEIVE",
		true,
	)]})
	r.status == "FAIL"
}

# A post-receive hook runs after the commits are in: it cannot refuse them.
test_fails_when_the_configured_hook_cannot_block if {
	r := cis_1_1_12.result with input as testdata.repo_input_with_config(
		{"hooks": [hook("com.example.signatures:notify", "POST_RECEIVE", true)]},
		{"signatureHookKeys": ["com.example.signatures:notify"]},
	)
	r.status == "FAIL"
}

test_fails_when_the_signature_hook_is_installed_but_disabled if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [hook(bundled, "PRE_RECEIVE", false)]})
	r.status == "FAIL"
}

test_fails_when_only_unrelated_hooks_are_enabled if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [hook("com.example.jira:issue-check", "PRE_RECEIVE", true)]})
	r.status == "FAIL"
}

test_fails_with_no_hooks_at_all if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": []})
	r.status == "FAIL"
}

test_manual_when_hooks_are_unreadable if {
	r := cis_1_1_12.result with input as testdata.repo_input({
		"hooks": [],
		"available": testdata.without("hooks"),
	})
	r.status == "MANUAL"
}

# A present-but-null hook list must read as an empty one, not error the rule
# out of existence. hooks stay available, so the verdict is a real FAIL.
test_null_hooks_do_not_break_the_rule if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": null})
	r.status == "FAIL"
}

# An archived repository takes no pushes and no pull requests: nothing about how
# a change arrives applies, and none of its settings were read to say otherwise.
test_not_applicable_when_archived if {
	r := cis_1_1_12.result with input as testdata.archived_input
	r.status == "NA"
	contains(r.details, "archived")
}
