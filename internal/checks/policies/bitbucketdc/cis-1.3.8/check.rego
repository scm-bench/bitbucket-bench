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

# defaultPermission is the highest permission every licensed user holds on this
# repository, however they came by it: a project's default permission, or a
# grant to a group that holds everyone, which hands access out just the same.
default_violation := [sprintf("every licensed user holds %s on it (ceiling is %s)", [granted, ceiling])] if {
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
	"details": "Whether every licensed user can reach this repository could not be determined, so its base permission is unknown.",
} if {
	not default_known
} else := {
	"status": "MANUAL",
	"details": sprintf("Every licensed user holds %q, which is not listed in permissionRank, so it cannot be compared against the %q ceiling. Add it to permissionRank in bitbucket-bench's config.", [granted, ceiling]),
	"evidence": [sprintf("unknown permission %q", [granted])],
} if {
	not granted_known
} else := {
	"status": "PASS",
	"details": sprintf("The repository is not anonymously readable, and no permission above %s is held by every licensed user.", [ceiling]),
}
