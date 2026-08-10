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
