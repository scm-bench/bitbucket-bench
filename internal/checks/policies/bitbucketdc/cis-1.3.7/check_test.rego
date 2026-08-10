package scmbench.rules.cis_1_3_7_test

import rego.v1

import data.scmbench.rules.cis_1_3_7
import data.scmbench.testdata

test_passes_at_the_minimum if {
	r := cis_1_3_7.result with input as testdata.repo_input({"admins": {
		"users": ["alice", "bob"],
		"count": 2,
		"complete": true,
	}})
	r.status == "PASS"
}

test_fails_below_the_minimum if {
	r := cis_1_3_7.result with input as testdata.repo_input({"admins": {
		"users": ["alice"],
		"count": 1,
		"complete": true,
	}})
	r.status == "FAIL"
}

# A lower bound that already meets the minimum settles the question, so an
# unexpandable group does not have to be resolved to answer it.
test_an_incomplete_count_still_passes_once_it_meets_the_minimum if {
	r := cis_1_3_7.result with input as testdata.repo_input({"admins": {
		"users": ["alice", "bob"],
		"count": 2,
		"complete": false,
	}})
	r.status == "PASS"
}

# The reverse does not hold: too few, from a count known to be short, is not a
# finding. This is the case that used to report a confident FAIL.
test_an_incomplete_count_below_the_minimum_is_manual if {
	r := cis_1_3_7.result with input as testdata.repo_input({"admins": {
		"users": ["alice"],
		"count": 1,
		"complete": false,
	}})
	r.status == "MANUAL"
}

test_manual_when_permissions_are_unreadable if {
	r := cis_1_3_7.result with input as testdata.repo_input({
		"admins": {"users": [], "count": 0, "complete": true},
		"available": testdata.without("permissions"),
	})
	r.status == "MANUAL"
}
