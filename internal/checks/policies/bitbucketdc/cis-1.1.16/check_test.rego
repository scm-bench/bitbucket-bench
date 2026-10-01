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

# An archived repository takes no pushes and no pull requests: nothing about how
# a change arrives applies, and none of its settings were read to say otherwise.
test_not_applicable_when_archived if {
	r := cis_1_1_16.result with input as testdata.archived_input
	r.status == "NA"
	contains(r.details, "archived")
}

force_push_hook := {
	"key": "com.atlassian.bitbucket.server.bitbucket-bundled-hooks:force-push-hook",
	"type": "PRE_RECEIVE",
	"enabled": true,
}

# Measured on Bitbucket 10.4: the bundled Reject Force Push hook refuses every
# force push on every branch, and a repository relying on it — no branch
# restriction at all — failed this control.
test_passes_with_the_reject_force_push_hook_and_no_restriction if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [],
		"hooks": [force_push_hook],
	})
	r.status == "PASS"
	contains(r.details, "Reject Force Push")
}

# The hook binds everyone, so it settles the question even where the branch
# restrictions could not be read, or a restriction exempts the whole team.
test_the_hook_settles_it_whatever_the_restrictions_say if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [testdata.restriction("fast-forward-only", testdata.exempt(["alice", "bob"]))],
		"hooks": [force_push_hook],
		"available": testdata.without("branchRestrictions"),
	})
	r.status == "PASS"
}

test_a_disabled_hook_protects_nothing if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [],
		"hooks": [object.union(force_push_hook, {"enabled": false})],
	})
	r.status == "FAIL"
}

# No restriction blocks force pushes, and the hooks could not be read: the
# Reject Force Push hook may or may not be what protects the branch.
test_manual_when_unprotected_by_restrictions_and_hooks_are_unreadable if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [],
		"available": testdata.without("hooks"),
	})
	r.status == "MANUAL"
	contains(r.details, "hooks could not be read")
}

# A restriction binding everyone is a PASS whatever the hooks say.
test_a_binding_restriction_passes_with_hooks_unreadable if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [testdata.restriction("fast-forward-only", {})],
		"available": testdata.without("hooks"),
	})
	r.status == "PASS"
}

# The restriction exempts the team, and the hooks are unread: if Reject Force
# Push were enabled the exemption would not matter, so this cannot fail yet.
test_manual_when_bypassed_and_hooks_are_unreadable if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [testdata.restriction("fast-forward-only", testdata.exempt(["alice", "bob"]))],
		"available": testdata.without("hooks"),
	})
	r.status == "MANUAL"
}

test_fails_without_any_restriction_or_hook_even_when_the_default_branch_is_unknown if {
	r := cis_1_1_16.result with input as testdata.input_for({
		"fullName": "PRJ/app",
		"defaultBranch": "",
		"branchRestrictions": [],
		"hooks": [],
		"available": testdata.without("defaultBranch"),
	})
	r.status == "FAIL"
	contains(r.details, "any branch")
}

test_manual_when_a_restriction_exists_but_the_default_branch_is_unknown if {
	r := cis_1_1_16.result with input as testdata.input_for({
		"fullName": "PRJ/app",
		"defaultBranch": "",
		"branchRestrictions": [{"type": "fast-forward-only", "matchesDefaultBranch": false}],
		"hooks": [],
		"available": testdata.without("defaultBranch"),
	})
	r.status == "MANUAL"
}

# The restriction exists, on another branch: the default branch can still be
# force pushed, and the message names it rather than "any branch".
test_fails_when_the_restriction_covers_another_branch if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [{"type": "fast-forward-only", "matchesDefaultBranch": false}],
		"hooks": [],
	})
	r.status == "FAIL"
	contains(r.details, "main can be force pushed")
}

# The only restriction of the right kind names a model branch, and the branch
# model could not be read: it may cover the default branch or not. The fetcher
# used to guess "development is the default branch", which passed a gitflow
# repository whose development branch is develop.
test_manual_when_the_only_restriction_cannot_be_matched if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [{"type": "fast-forward-only", "matchesDefaultBranch": false, "matchUnknown": true}],
		"hooks": [],
	})
	r.status == "MANUAL"
	contains(r.details, "could not be decided")
}

# One restriction binds everyone on the default branch; another, unresolved,
# changes nothing — it can only add protection.
test_a_resolved_binding_restriction_passes_beside_an_unresolved_one if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [
			testdata.restriction("fast-forward-only", {}),
			{"type": "fast-forward-only", "matchesDefaultBranch": false, "matchUnknown": true},
		],
		"hooks": [],
	})
	r.status == "PASS"
}

# The known restriction exempts the team; the unresolved one may cover the
# branch and bind them after all, so this is not yet a hole.
test_a_bypass_is_not_a_hole_while_another_restriction_is_unresolved if {
	r := cis_1_1_16.result with input as testdata.repo_input({
		"branchRestrictions": [
			testdata.restriction("fast-forward-only", testdata.exempt(["alice", "bob"])),
			{"type": "fast-forward-only", "matchesDefaultBranch": false, "matchUnknown": true},
		],
		"hooks": [],
	})
	r.status == "MANUAL"
}
