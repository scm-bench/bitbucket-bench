package scmbench.rules.cis_1_1_17_test

import rego.v1

import data.scmbench.rules.cis_1_1_17
import data.scmbench.testdata

test_passes_with_a_no_deletes_restriction if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "no-deletes",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

test_passes_with_a_read_only_restriction if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "read-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

# Preventing history rewrites does not prevent removing the branch outright.
test_fails_when_only_history_rewrites_are_blocked if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "fast-forward-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "FAIL"
}

test_manual_when_branch_permissions_are_unreadable if {
	r := cis_1_1_17.result with input as testdata.repo_input({
		"branchRestrictions": [],
		"available": testdata.without("branchRestrictions"),
	})
	r.status == "MANUAL"
}

test_not_applicable_for_an_empty_repository if {
	r := cis_1_1_17.result with input as testdata.input_for({
		"fullName": "PRJ/empty",
		"empty": true,
		"available": testdata.every_available,
	})
	r.status == "NA"
}

# The case the control existed to catch and did not: deletion is restricted,
# and the people the restriction was supposed to bind can still delete the
# branch — taking its protections with it.
test_fails_when_people_are_exempt_from_the_restriction if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("no-deletes", testdata.exempt(["alice", "bob"])),
	]})
	r.status == "FAIL"
	contains(r.details, "alice")
}

# Exempting the release managers who may write to an otherwise frozen branch is
# how read-only is meant to be used. It is only a bypass when nothing else
# covers them, and here "Prevent deletion" does.
test_passes_when_another_restriction_catches_the_exempt_principal if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("read-only", testdata.exempt(["release-manager"])),
		testdata.restriction("no-deletes", {}),
	]})
	r.status == "PASS"
}

test_an_allowlisted_service_account_still_passes if {
	r := cis_1_1_17.result with input as testdata.repo_input_with_config(
		{"branchRestrictions": [testdata.restriction("no-deletes", testdata.exempt(["build-bot"]))]},
		{"allowedBypassPrincipals": ["build-bot"]},
	)
	r.status == "PASS"
}

test_manual_when_an_exempt_group_could_not_be_expanded if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("no-deletes", testdata.exempt_unresolved("contractors")),
	]})
	r.status == "MANUAL"
}
