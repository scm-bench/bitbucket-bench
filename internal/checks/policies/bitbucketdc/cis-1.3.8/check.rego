package scmbench.rules.cis_1_3_8

import rego.v1

import data.scmbench.lib

ranks := object.get(lib.cfg, "permissionRank", {})

ceiling := object.get(lib.cfg, "maxDefaultPermission", "REPO_READ")

granted := object.get(lib.resource, ["permissions", "defaultPermission"], "")

granted_rank := object.get(ranks, [granted], 0)

# A permission the rank table does not know cannot be compared to the ceiling.
#
# Defaulting an unrecognised grant to rank 0 ranked it below everything, so the
# rule answered PASS and said "grants no default permission above REPO_READ" —
# about a permission it had never heard of. Anything that introduces a name not
# in permissionRank reaches this: a newer Bitbucket, a snapshot from another
# producer, or a user who overrode permissionRank and left an entry out.
granted_known if {
	granted == ""
}

granted_known if {
	object.get(ranks, [granted], -1) >= 0
}

ceiling_rank := object.get(ranks, [ceiling], 0)

public := object.get(lib.resource, "public", false)

allow_public := object.get(lib.cfg, "allowPublicRepositories", false)

anonymous_violation := ["the repository is readable by anonymous users"] if {
	public
	not allow_public
} else := []

default_violation := [sprintf("every licensed user is granted %s by default (ceiling is %s)", [granted, ceiling])] if {
	granted_rank > ceiling_rank
} else := []

violations := array.concat(anonymous_violation, default_violation)

default_known := object.get(lib.resource, ["permissions", "defaultPermissionKnown"], false)

# An already-detected violation is conclusive even if the probe was partial,
# so only a would-be PASS needs the default permission to be known.
result := {
	"status": "FAIL",
	"details": sprintf("Repository access is broader than intended: %s.", [concat("; ", violations)]),
	"evidence": violations,
} if {
	count(violations) > 0
} else := {
	"status": "MANUAL",
	"details": "The project's default permission could not be read, so it is unknown whether every licensed user is granted access by default.",
} if {
	not default_known
} else := {
	"status": "MANUAL",
	"details": sprintf("The project grants %q by default, which is not listed in permissionRank, so it cannot be compared against the %q ceiling. Add it to permissionRank in scm-bench's config.", [granted, ceiling]),
	"evidence": [sprintf("unknown permission %q", [granted])],
} if {
	not granted_known
} else := {
	"status": "PASS",
	"details": sprintf("The repository is not anonymously readable and grants no default permission above %s.", [ceiling]),
}
