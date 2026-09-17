package scmbench.rules.cis_1_1_15_test

import rego.v1

import data.scmbench.rules.cis_1_1_15
import data.scmbench.testdata

test_fails_when_nothing_restricts_the_default_branch if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": []})
	r.status == "FAIL"
	contains(r.details, "push directly to main")
}

test_passes_with_a_pull_request_only_restriction if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "pull-request-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

# read-only blocks every write, which covers direct pushes too.
test_passes_with_a_read_only_restriction if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "read-only",
		"matchesDefaultBranch": true,
	}]})
	r.status == "PASS"
}

# A restriction on some other branch protects some other branch.
test_fails_when_the_restriction_misses_the_default_branch if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "pull-request-only",
		"matchesDefaultBranch": false,
	}]})
	r.status == "FAIL"
}

test_manual_when_branch_permissions_are_unreadable if {
	r := cis_1_1_15.result with input as testdata.repo_input({
		"branchRestrictions": [],
		"available": testdata.without("branchRestrictions"),
	})
	r.status == "MANUAL"
}

test_not_applicable_for_an_empty_repository if {
	r := cis_1_1_15.result with input as testdata.input_for({
		"fullName": "PRJ/empty",
		"empty": true,
		"available": testdata.every_available,
	})
	r.status == "NA"
}

# An exemption on a named service account is what the allowlist is for: the
# restriction still binds every person, so the verdict stays PASS and the note
# keeps naming who holds it.
test_an_allowlisted_service_account_still_passes if {
	r := cis_1_1_15.result with input as testdata.repo_input_with_config(
		{"branchRestrictions": [testdata.restriction("pull-request-only", testdata.exempt(["build-bot"]))]},
		{"allowedBypassPrincipals": ["build-bot"]},
	)
	r.status == "PASS"
	contains(r.details, "build-bot")
}

# The case this control used to call a pass: the restriction is configured, and
# the people it was supposed to bind can push straight past it.
test_fails_when_people_are_exempt_from_the_restriction if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("pull-request-only", testdata.exempt(["alice", "bob"])),
	]})
	r.status == "FAIL"
	contains(r.details, "alice")
}

# Protection is the union of the restrictions covering the branch, so an
# exemption on one of them is not a hole while another still catches it.
test_passes_when_another_restriction_catches_the_exempt_principal if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("read-only", testdata.exempt(["alice"])),
		testdata.restriction("pull-request-only", testdata.exempt(["bob"])),
	]})
	r.status == "PASS"
}

# A group the token could not expand is not evidence that nobody is in it.
test_manual_when_an_exempt_group_could_not_be_expanded if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("pull-request-only", testdata.exempt_unresolved("contractors")),
	]})
	r.status == "MANUAL"
}

# An access key that can push past a restriction is a bypass exactly as an
# exempt user is, and is counted as one.
test_exempt_access_keys_are_counted if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("pull-request-only", {
			"exemptAccessKeys": 3,
			"exemptAccessKeyIds": [1, 2, 3],
		}),
	]})
	r.status == "FAIL"
	contains(r.details, "3 access key")
}

# A key total with no keys behind it is a snapshot disagreeing with itself.
# That is a gap in the data, not a clean branch, and it is reported as one.
test_manual_when_exempt_access_keys_are_unidentified if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [
		testdata.restriction("pull-request-only", {"exemptAccessKeys": 3}),
	]})
	r.status == "MANUAL"
}

# A nil slice marshals to JSON null, and null reaching a builtin makes the whole
# rule undefined — which the engine reports as a control that produced no
# verdict at all. Every list has to be read through lib.list for this reason.
test_null_restrictions_do_not_break_the_rule if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": null})
	r.status == "FAIL"
}

# The same null rule applies one level down: a restriction whose exemptUsers is
# an explicit null must not erase the named exemptions beside it. Before
# as_list, the null reached array.concat, the note went undefined, and the
# report described the protection as tighter than it is.
test_null_exempt_users_do_not_erase_the_named_exemptions if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "read-only",
		"matchesDefaultBranch": true,
		"exemptUsers": null,
		"exemptGroups": ["release-bots"],
		"exemptPrincipals": {"users": null, "groups": ["release-bots"], "count": 0, "complete": false},
	}]})
	r.status == "MANUAL"
}
