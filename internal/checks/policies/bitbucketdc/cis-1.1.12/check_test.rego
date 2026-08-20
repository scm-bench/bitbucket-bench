package scmbench.rules.cis_1_1_12_test

import rego.v1

import data.scmbench.rules.cis_1_1_12
import data.scmbench.testdata

test_passes_when_a_signature_hook_is_enabled if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [{
		"key": "com.example.gpg-verify",
		"name": "GPG signature check",
		"enabled": true,
	}]})
	r.status == "PASS"
}

# The name is searched as well as the key, since add-ons vary in which one
# carries the recognisable word.
test_matches_on_the_hook_name_too if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [{
		"key": "com.example.opaque",
		"name": "Commit signature verification",
		"enabled": true,
	}]})
	r.status == "PASS"
}

test_fails_when_the_signature_hook_is_installed_but_disabled if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [{
		"key": "com.example.gpg-verify",
		"enabled": false,
	}]})
	r.status == "FAIL"
}

test_fails_when_only_unrelated_hooks_are_enabled if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": [{
		"key": "com.example.jira-issue-check",
		"name": "Require a Jira issue",
		"enabled": true,
	}]})
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

# Every string contains "", so a blank pattern would report the first enabled
# hook — any hook — as a signature verifier. config.Validate rejects one; this
# is the second lock, for callers that build a Config in Go and never pass
# through it.
test_a_blank_configured_key_matches_nothing if {
	blank := object.union(testdata.config, {"signatureHookKeys": ["", "gpg"]})
	r := cis_1_1_12.result with input as {
		"resource": testdata.repo({"hooks": [{"key": "com.example.jira-issue-check", "enabled": true}]}),
		"config": blank,
	}
	r.status == "FAIL"
}

# A present-but-null hook list must read as an empty one, not error the rule
# out of existence. hooks stay available, so the verdict is a real FAIL.
test_null_hooks_do_not_break_the_rule if {
	r := cis_1_1_12.result with input as testdata.repo_input({"hooks": null})
	r.status == "FAIL"
}
