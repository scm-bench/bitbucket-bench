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

# The exemption does not change the verdict — the restriction is configured —
# but it has to be visible, because each exemption reopens the bypass.
test_exemptions_are_named_in_a_pass if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "pull-request-only",
		"matchesDefaultBranch": true,
		"exemptUsers": ["build-bot"],
		"exemptGroups": ["release-managers"],
	}]})
	r.status == "PASS"
	contains(r.details, "build-bot")
	contains(r.details, "release-managers")
}

# An access key that can push past a restriction is a bypass exactly as an
# exempt user is, and was being left out of the note entirely.
test_exempt_access_keys_are_counted if {
	r := cis_1_1_15.result with input as testdata.repo_input({"branchRestrictions": [{
		"type": "pull-request-only",
		"matchesDefaultBranch": true,
		"exemptAccessKeys": 3,
	}]})
	r.status == "PASS"
	contains(r.details, "3 access key")
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
	}]})
	r.status == "PASS"
	contains(r.details, "release-bots")
}
