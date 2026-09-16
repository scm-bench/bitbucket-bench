# Tests for the helpers every control leans on.
#
# They are exercised indirectly by the control tests, but the branches that
# only appear at the edges — a repository whose default branch could not be
# named, a list long enough to be truncated, both kinds of bypass at once — are
# reached from here, where the case can be stated plainly.
package scmbench.lib_test

import rego.v1

import data.scmbench.lib
import data.scmbench.testdata

# default_branch_name falls back rather than rendering an empty string, so a
# message never reads "pushes to  are blocked".
test_default_branch_name_falls_back_when_unnamed if {
	name := lib.default_branch_name with input as testdata.input_for({"defaultBranch": "refs/heads/main"})
	name == "the default branch"
}

test_default_branch_name_uses_the_display_id if {
	name := lib.default_branch_name with input as testdata.input_for({"defaultBranchDisplay": "trunk"})
	name == "trunk"
}

# Both kinds of bypass at once: named principals and access keys.
test_exemption_note_covers_principals_and_access_keys if {
	note := lib.exemption_note([{"exemptUsers": ["build-bot"], "exemptAccessKeys": 2}])
	contains(note, "build-bot")
	contains(note, "2 access key")
}

# Keys with nobody named: the note has to say so rather than render an empty
# "bypass allowed for:" clause. Reached from here because a passing verdict
# with only key exemptions needs two restrictions to arrange.
test_exemption_note_for_access_keys_alone if {
	note := lib.exemption_note([{"exemptAccessKeys": 2}])
	contains(note, "2 access key")
	not contains(note, "bypass allowed for")
}

test_exemption_note_is_empty_without_exemptions if {
	lib.exemption_note([{"type": "read-only"}]) == ""
}

# joined caps the list so a repository with hundreds of stale branches does not
# produce an unreadable line.
test_joined_truncates_past_the_limit if {
	msg := lib.joined(["a", "b", "c", "d", "e"], 3)
	msg == "a, b, c and 2 more"
}

test_joined_leaves_a_short_list_alone if {
	lib.joined(["b", "a"], 3) == "a, b"
}

# A nil Go slice marshals to null, and null reaching concat or sort makes the
# calling rule undefined — which the engine reports as a control that produced
# no verdict at all.
test_joined_tolerates_null if {
	lib.joined(null, 3) == ""
}

test_list_substitutes_an_empty_list_for_null if {
	got := lib.list(["branches"]) with input as testdata.input_for({"branches": null})
	got == []
}

test_list_substitutes_an_empty_list_for_a_missing_key if {
	got := lib.list(["branches"]) with input as testdata.input_for({})
	got == []
}

# A key present and false means "this fetch failed", which is not the same as
# a key that was never written.
test_available_treats_a_missing_key_as_unavailable if {
	not lib.available("hooks") with input as testdata.input_for({"available": {}})
}

test_available_reads_a_successful_fetch if {
	lib.available("hooks") with input as testdata.input_for({"available": {"hooks": true}})
}

# The bypass helpers. These decide whether a configured restriction actually
# binds anyone, so their edges are worth stating plainly rather than inferring
# from three controls that happen to agree.

# The intersection is the whole design. Protection on a branch is the union of
# the restrictions covering it, so somebody exempt from one but caught by
# another is not a hole.
test_bypass_principals_intersects_rather_than_unions if {
	rs := [
		testdata.restriction("read-only", testdata.exempt(["alice"])),
		testdata.restriction("no-deletes", testdata.exempt(["bob"])),
	]
	lib.bypass_principals(rs) == set() with input as testdata.repo_input({})
}

test_bypass_principals_names_who_every_restriction_exempts if {
	rs := [
		testdata.restriction("read-only", testdata.exempt(["alice", "bob"])),
		testdata.restriction("no-deletes", testdata.exempt(["alice"])),
	]
	lib.bypass_principals(rs) == {"alice"} with input as testdata.repo_input({})
}

test_bypass_principals_is_empty_without_restrictions if {
	lib.bypass_principals([]) == set() with input as testdata.repo_input({})
}

test_bypass_access_keys_is_zero_without_restrictions if {
	lib.bypass_access_keys([]) == 0 with input as testdata.repo_input({})
}

test_bypass_access_keys_intersects_by_identity if {
	rs := [
		testdata.restriction("read-only", {"exemptAccessKeys": 2, "exemptAccessKeyIds": [1, 2]}),
		testdata.restriction("no-deletes", {"exemptAccessKeys": 1, "exemptAccessKeyIds": [2]}),
	]
	lib.bypass_access_keys(rs) == 1 with input as testdata.repo_input({})
}

# A named service account is expected to hold an exemption; that is what the
# allowlist is for, and it is why the threshold can stay at zero.
test_allowed_bypass_principals_do_not_count if {
	rs := [testdata.restriction("no-deletes", testdata.exempt(["build-bot"]))]
	lib.bypass_principals(rs) == set() with input as testdata.repo_input_with_config(
		{},
		{"allowedBypassPrincipals": ["build-bot"]},
	)
}

# One restriction naming nobody settles completeness on its own: intersecting
# with an empty set is empty whatever an unexpandable group turns out to hold.
test_bypass_complete_is_settled_by_a_restriction_naming_nobody if {
	rs := [
		testdata.restriction("read-only", testdata.exempt_unresolved("release-managers")),
		testdata.restriction("no-deletes", {}),
	]
	lib.bypass_complete(rs) with input as testdata.repo_input({})
}

test_bypass_complete_when_every_exemption_resolved if {
	rs := [testdata.restriction("no-deletes", testdata.exempt(["alice"]))]
	lib.bypass_complete(rs) with input as testdata.repo_input({})
}

test_bypass_incomplete_when_a_group_could_not_be_expanded if {
	rs := [testdata.restriction("no-deletes", testdata.exempt_unresolved("contractors"))]
	not lib.bypass_complete(rs) with input as testdata.repo_input({})
}

# A snapshot from a build older than exemptAccessKeyIds knows how many keys
# bypass a restriction but not which, so they cannot be intersected.
test_bypass_incomplete_when_access_keys_are_unidentified if {
	rs := [testdata.restriction("no-deletes", {"exemptAccessKeys": 2})]
	not lib.bypass_complete(rs) with input as testdata.repo_input({})
}

# A lower bound that already crosses the threshold is still a sound FAIL:
# expanding the group can only add people, never remove them.
test_bypass_exceeded_on_a_lower_bound_is_still_decided if {
	rs := [testdata.restriction("no-deletes", {
		"exemptUsers": ["alice"],
		"exemptGroups": ["contractors"],
		"exemptPrincipals": {"users": ["alice"], "groups": ["contractors"], "count": 1, "complete": false},
	})]
	lib.bypass_exceeded(rs) with input as testdata.repo_input({})
	not lib.bypass_undecidable(rs) with input as testdata.repo_input({})
}

test_bypass_undecidable_below_the_threshold_with_an_unexpanded_group if {
	rs := [testdata.restriction("no-deletes", testdata.exempt_unresolved("contractors"))]
	lib.bypass_undecidable(rs) with input as testdata.repo_input({})
}

# -1 turns the check off, restoring the behaviour of releases before it existed.
test_bypass_check_disabled_by_a_negative_threshold if {
	rs := [testdata.restriction("no-deletes", testdata.exempt(["alice", "bob"]))]
	off := testdata.repo_input_with_config({}, {"thresholds": {"maxBypassPrincipals": -1}})
	not lib.bypass_exceeded(rs) with input as off
	not lib.bypass_undecidable(rs) with input as off
}

test_max_bypass_falls_back_when_not_a_number if {
	bad := testdata.repo_input_with_config({}, {"thresholds": {"maxBypassPrincipals": "two"}})
	lib.max_bypass == 0 with input as bad
}

test_bypass_detail_covers_principals_and_access_keys if {
	rs := [testdata.restriction("no-deletes", object.union(
		testdata.exempt(["alice"]),
		{"exemptAccessKeys": 1, "exemptAccessKeyIds": [7]},
	))]
	detail := lib.bypass_detail(rs) with input as testdata.repo_input({})
	contains(detail, "alice")
	contains(detail, "1 access key")
}

test_bypass_detail_for_access_keys_alone if {
	rs := [testdata.restriction("no-deletes", {"exemptAccessKeys": 1, "exemptAccessKeyIds": [7]})]
	detail := lib.bypass_detail(rs) with input as testdata.repo_input({})
	detail == "1 access key(s)"
}

# The evidence names the threshold the verdict was judged against, so a reader
# can check it rather than take it on faith.
test_bypass_evidence_states_the_threshold if {
	rs := [testdata.restriction("no-deletes", testdata.exempt(["alice"]))]
	ev := lib.bypass_evidence(rs) with input as testdata.repo_input({})
	contains(ev[0], "alice")
	contains(ev[1], "maxBypassPrincipals is 0")
}
