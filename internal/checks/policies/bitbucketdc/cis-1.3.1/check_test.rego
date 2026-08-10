package scmbench.rules.cis_1_3_1_test

import rego.v1

import data.scmbench.rules.cis_1_3_1
import data.scmbench.testdata

org(fields) := testdata.input_for(object.union(
	{"available": {"users": true, "userActivity": true}},
	fields,
))

test_passes_when_everyone_has_signed_in_recently if {
	r := cis_1_3_1.result with input as org({"users": [{
		"name": "alice",
		"active": true,
		"hasRepositoryAccess": true,
		"inactiveDays": 3,
	}]})
	r.status == "PASS"
}

test_fails_past_the_threshold if {
	r := cis_1_3_1.result with input as org({"users": [{
		"name": "dana",
		"active": true,
		"hasRepositoryAccess": true,
		"inactiveDays": 120,
	}]})
	r.status == "FAIL"
	contains(r.details, "dana")
}

# A dormant account with no grants is untidy; a dormant account with write
# access is a credential nobody is watching. Only the second is this control.
test_ignores_dormant_accounts_that_cannot_reach_code if {
	r := cis_1_3_1.result with input as org({"users": [{
		"name": "auditor",
		"active": true,
		"hasRepositoryAccess": false,
		"inactiveDays": 900,
	}]})
	r.status == "PASS"
}

test_ignores_deactivated_accounts if {
	r := cis_1_3_1.result with input as org({"users": [{
		"name": "leaver",
		"active": false,
		"hasRepositoryAccess": true,
		"inactiveDays": 900,
	}]})
	r.status == "PASS"
}

# An account with no last-authentication time is unknown, not fresh. Letting it
# reach PASS would turn a hole in the data into a clean bill of health for
# exactly the accounts nobody is watching.
test_unknown_last_authentication_is_manual if {
	r := cis_1_3_1.result with input as org({"users": [{
		"name": "mystery",
		"active": true,
		"hasRepositoryAccess": true,
		"inactiveDays": -1,
	}]})
	r.status == "MANUAL"
	contains(r.details, "mystery")
}

# A confirmed dormant account settles the question, so unknowns alongside it do
# not soften the verdict.
test_a_confirmed_dormant_account_wins_over_unknowns if {
	r := cis_1_3_1.result with input as org({"users": [
		{"name": "dana", "active": true, "hasRepositoryAccess": true, "inactiveDays": 120},
		{"name": "mystery", "active": true, "hasRepositoryAccess": true, "inactiveDays": -1},
	]})
	r.status == "FAIL"
}

test_manual_when_the_instance_reports_no_activity_data if {
	r := cis_1_3_1.result with input as testdata.input_for({
		"users": [],
		"available": {"users": true, "userActivity": false},
	})
	r.status == "MANUAL"
}

test_null_user_list_does_not_break_the_rule if {
	r := cis_1_3_1.result with input as org({"users": null})
	r.status == "PASS"
}
