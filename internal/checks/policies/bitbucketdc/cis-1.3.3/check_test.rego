package scmbench.rules.cis_1_3_3_test

import rego.v1

import data.scmbench.rules.cis_1_3_3
import data.scmbench.testdata

org(admins, complete) := testdata.input_for({
	"effectiveAdmins": {"users": admins, "count": count(admins), "complete": complete},
	"available": {"adminUsers": true, "adminGroups": true},
})

test_passes_inside_the_range if {
	r := cis_1_3_3.result with input as org(["alice", "bob", "carol"], true)
	r.status == "PASS"
}

test_fails_below_the_minimum if {
	r := cis_1_3_3.result with input as org(["alice"], true)
	r.status == "FAIL"
	contains(r.details, "at least 2")
}

test_fails_above_the_maximum if {
	r := cis_1_3_3.result with input as org(["a", "b", "c", "d", "e", "f"], true)
	r.status == "FAIL"
	contains(r.details, "at most 5")
}

# An over-count is conclusive even from a lower bound: expanding the group that
# failed could only add more administrators, never remove any.
test_an_over_count_is_conclusive_even_when_incomplete if {
	r := cis_1_3_3.result with input as org(["a", "b", "c", "d", "e", "f"], false)
	r.status == "FAIL"
}

# An under-count is not conclusive, which is the asymmetry worth pinning.
test_an_under_count_from_an_incomplete_set_is_manual if {
	r := cis_1_3_3.result with input as org(["alice"], false)
	r.status == "MANUAL"
}

test_manual_when_global_permissions_are_unreadable if {
	r := cis_1_3_3.result with input as testdata.input_for({
		"effectiveAdmins": {"users": [], "count": 0, "complete": false},
		"available": {"adminUsers": false, "adminGroups": false},
	})
	r.status == "MANUAL"
}

# Zero means "no upper limit", matching what config.Validate has always
# assumed. Read the other way it meant "at most zero administrators", which
# failed every instance that had any.
test_zero_maximum_means_no_upper_limit if {
	cfg := object.union(testdata.config, {"thresholds": object.union(
		testdata.config.thresholds,
		{"maxOrgAdmins": 0},
	)})
	r := cis_1_3_3.result with input as {
		"resource": {
			"effectiveAdmins": {"users": ["a", "b", "c", "d", "e", "f", "g"], "count": 7, "complete": true},
			"available": {"adminUsers": true, "adminGroups": true},
		},
		"config": cfg,
	}
	r.status == "PASS"
	not contains(r.details, "2 to 0")
}
