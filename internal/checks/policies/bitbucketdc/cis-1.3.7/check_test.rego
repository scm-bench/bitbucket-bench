package scmbench.rules.cis_1_3_7_test

import rego.v1

import data.scmbench.rules.cis_1_3_7
import data.scmbench.testdata

test_passes_at_the_minimum if {
	r := cis_1_3_7.result with input as testdata.repo_input({"admins": {"users": ["alice", "bob"], "count": 2, "complete": true}})
	r.status == "PASS"
	contains(r.details, "instance administrators not counted")
}

test_fails_below_the_minimum if {
	r := cis_1_3_7.result with input as testdata.repo_input({"admins": {"users": ["alice"], "count": 1, "complete": true}})
	r.status == "FAIL"
	contains(r.details, "Only 1")
}

# Measured on Bitbucket 10.4: with the instance administrators counted, every
# repository of an instance holding two or more of them passed, whoever the
# repository's own administrators were. They are left out by the fetcher, and
# a repository administered by nobody of its own fails.
test_fails_with_no_administrator_of_its_own if {
	r := cis_1_3_7.result with input as testdata.repo_input({"admins": {"users": [], "count": 0, "complete": true}})
	r.status == "FAIL"
}

# The fetcher marks the set unavailable when it could not tell the repository's
# administrators from the instance's: a count that may be inflated proves
# neither a pass nor a fail.
test_manual_when_administrators_are_unresolved if {
	r := cis_1_3_7.result with input as testdata.repo_input({
		"admins": {"users": ["alice", "bob", "carol"], "count": 3, "complete": false},
		"available": testdata.without("admins"),
	})
	r.status == "MANUAL"
}

# An archived repository takes no pushes and no pull requests: nothing about how
# a change arrives applies, and none of its settings were read to say otherwise.
test_not_applicable_when_archived if {
	r := cis_1_3_7.result with input as testdata.archived_input
	r.status == "NA"
	contains(r.details, "archived")
}
