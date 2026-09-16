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

# A restriction that exempts people does not stop them rewriting history, and
# reporting it as protection described the branch as safer than it is.
test_fails_when_people_are_exempt_from_the_restriction if {
	r := cis_1_1_16.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("fast-forward-only", testdata.exempt(["alice", "bob"])),
	]})
	r.status == "FAIL"
	contains(r.details, "alice")
}

# Protection is the union of the restrictions covering the branch: somebody
# exempt from "Prevent all changes" but still subject to "Prevent rewriting
# history" cannot force push, so they are not a hole.
test_passes_when_another_restriction_catches_the_exempt_principal if {
	r := cis_1_1_16.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("read-only", testdata.exempt(["release-manager"])),
		testdata.restriction("fast-forward-only", {}),
	]})
	r.status == "PASS"
}

test_an_allowlisted_service_account_still_passes if {
	r := cis_1_1_16.result with input as testdata.repo_input_with_config(
		{"branchRestrictions": [testdata.restriction("fast-forward-only", testdata.exempt(["build-bot"]))]},
		{"allowedBypassPrincipals": ["build-bot"]},
	)
	r.status == "PASS"
}

test_manual_when_an_exempt_group_could_not_be_expanded if {
	r := cis_1_1_16.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("fast-forward-only", testdata.exempt_unresolved("contractors")),
	]})
	r.status == "MANUAL"
}
