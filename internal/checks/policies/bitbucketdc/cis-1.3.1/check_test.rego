package scmbench.rules.cis_1_3_1_test

import rego.v1

import data.scmbench.rules.cis_1_3_1
import data.scmbench.testdata

readable := {"users": true, "userActivity": true, "licensedUsers": true}

org(users) := testdata.input_for({"users": users, "available": readable})

# A licensed, active account that last authenticated `days` ago, created a year ago.
person(name, days) := {
	"name": name, "active": true, "licensed": true,
	"inactiveDays": days, "lastActivityEpoch": 1, "ageDays": 365,
}

test_passes_when_everyone_has_signed_in_recently if {
	r := cis_1_3_1.result with input as org([person("alice", 3), person("bob", 40)])
	r.status == "PASS"
}

test_fails_past_the_threshold if {
	r := cis_1_3_1.result with input as org([person("alice", 3), person("bob", 120)])
	r.status == "FAIL"
	contains(r.details, "bob (last authenticated 120 days ago)")
}

# Measured on Bitbucket 10.4: an account that has never authenticated simply has
# no time, and once the instance shows it records them, that absence means
# "never". An old account that has never been used is the dormant account most
# worth finding.
test_fails_for_an_old_account_that_never_signed_in if {
	r := cis_1_3_1.result with input as org([
		person("alice", 3),
		{"name": "eve", "active": true, "licensed": true, "inactiveDays": -1, "neverSignedIn": true, "ageDays": 200},
	])
	r.status == "FAIL"
	contains(r.details, "eve (never authenticated; created 200 days ago)")
}

# The same account created this morning has not had the chance.
test_passes_for_a_new_account_that_never_signed_in if {
	r := cis_1_3_1.result with input as org([
		person("alice", 3),
		{"name": "eve", "active": true, "licensed": true, "inactiveDays": -1, "neverSignedIn": true, "ageDays": 0},
	])
	r.status == "PASS"
}

# "Never", with no creation time to measure it against, is still unknown.
test_never_signed_in_without_a_creation_time_is_manual if {
	r := cis_1_3_1.result with input as org([
		{"name": "eve", "active": true, "licensed": true, "inactiveDays": -1, "neverSignedIn": true, "ageDays": -1},
	])
	r.status == "MANUAL"
	contains(r.details, "eve")
}

# An unlicensed account cannot sign in at all, so its dormancy exposes nothing.
test_ignores_unlicensed_accounts if {
	r := cis_1_3_1.result with input as org([
		person("alice", 3),
		object.union(person("bob", 400), {"licensed": false}),
	])
	r.status == "PASS"
}

test_ignores_deactivated_accounts if {
	r := cis_1_3_1.result with input as org([
		person("alice", 3),
		object.union(person("bob", 400), {"active": false}),
	])
	r.status == "PASS"
}

# An account with no last-authentication time, and no statement that it never
# authenticated, is unknown, not fresh. Letting it reach PASS would turn a hole
# in the data into a clean bill of health for exactly the accounts nobody is
# watching.
test_unknown_last_authentication_is_manual if {
	r := cis_1_3_1.result with input as org([
		person("alice", 3),
		{"name": "mallory", "active": true, "licensed": true, "inactiveDays": -1, "ageDays": 365},
	])
	r.status == "MANUAL"
	contains(r.details, "mallory")
}

# A confirmed dormant account settles the question, so unknowns alongside it do
# not soften the verdict.
test_a_confirmed_dormant_account_wins_over_unknowns if {
	r := cis_1_3_1.result with input as org([
		person("bob", 400),
		{"name": "mallory", "active": true, "licensed": true, "inactiveDays": -1},
	])
	r.status == "FAIL"
}

test_manual_when_the_instance_reports_no_activity_data if {
	r := cis_1_3_1.result with input as testdata.input_for({
		"users": [person("alice", 3)],
		"available": object.union(readable, {"userActivity": false}),
	})
	r.status == "MANUAL"
}

# Who is licensed decides who is in the population; without it a quiet list
# proves nothing.
test_manual_when_licensed_users_are_unknown if {
	r := cis_1_3_1.result with input as testdata.input_for({
		"users": [person("alice", 3)],
		"available": object.union(readable, {"licensedUsers": false}),
	})
	r.status == "MANUAL"
}

test_null_user_list_does_not_break_the_rule if {
	r := cis_1_3_1.result with input as org(null)
	r.status == "PASS"
}
