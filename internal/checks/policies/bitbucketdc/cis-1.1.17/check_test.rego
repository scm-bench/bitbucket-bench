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

# An archived repository takes no pushes and no pull requests: nothing about how
# a change arrives applies, and none of its settings were read to say otherwise.
test_not_applicable_when_archived if {
	r := cis_1_1_17.result with input as testdata.archived_input
	r.status == "NA"
	contains(r.details, "archived")
}

# Measured on Bitbucket 10.4: a repository can point its default at a branch
# nobody pushed. With no restriction of the right kind on any branch, whichever
# branch is the default is unprotected, so that much is still a FAIL.
test_fails_without_any_restriction_even_when_the_default_branch_is_unknown if {
	r := cis_1_1_17.result with input as testdata.input_for({
		"fullName": "PRJ/app",
		"defaultBranch": "",
		"branchRestrictions": [],
		"available": testdata.without("defaultBranch"),
	})
	r.status == "FAIL"
	contains(r.details, "any branch")
}

# A restriction exists, but which branch it should be compared against does
# not: whether it covers the default branch cannot be known.
test_manual_when_a_restriction_exists_but_the_default_branch_is_unknown if {
	r := cis_1_1_17.result with input as testdata.input_for({
		"fullName": "PRJ/app",
		"defaultBranch": "",
		"branchRestrictions": [{"type": "no-deletes", "matchesDefaultBranch": false}],
		"available": testdata.without("defaultBranch"),
	})
	r.status == "MANUAL"
	contains(r.details, "default branch could not be resolved")
}

# A deletion restriction exists, on some other branch: the default branch is
# still deletable, and the message names it rather than "any branch".
test_fails_when_the_restriction_covers_another_branch if {
	r := cis_1_1_17.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "no-deletes",
		"matchesDefaultBranch": false,
	}]})
	r.status == "FAIL"
	contains(r.details, "main can be deleted")
}

# The only restriction of the right kind names a model branch, and the branch
# model could not be read: it may cover the default branch or not. The fetcher
# used to guess "development is the default branch", which passed a gitflow
# repository whose development branch is develop.
test_manual_when_the_only_restriction_cannot_be_matched if {
	r := cis_1_1_17.result with input as testdata.repo_input({
		"branchRestrictions": [{"type": "no-deletes", "matchesDefaultBranch": false, "matchUnknown": true}],
	})
	r.status == "MANUAL"
	contains(r.details, "could not be decided")
}

# One restriction binds everyone on the default branch; another, unresolved,
# changes nothing — it can only add protection.
test_a_resolved_binding_restriction_passes_beside_an_unresolved_one if {
	r := cis_1_1_17.result with input as testdata.repo_input({
		"branchRestrictions": [
			testdata.restriction("no-deletes", {}),
			{"type": "no-deletes", "matchesDefaultBranch": false, "matchUnknown": true},
		],
	})
	r.status == "PASS"
}

# The known restriction exempts the team; the unresolved one may cover the
# branch and bind them after all, so this is not yet a hole.
test_a_bypass_is_not_a_hole_while_another_restriction_is_unresolved if {
	r := cis_1_1_17.result with input as testdata.repo_input({
		"branchRestrictions": [
			testdata.restriction("no-deletes", testdata.exempt(["alice", "bob"])),
			{"type": "no-deletes", "matchesDefaultBranch": false, "matchUnknown": true},
		],
	})
	r.status == "MANUAL"
}
