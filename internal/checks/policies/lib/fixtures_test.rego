# Shared fixtures for the control unit tests.
#
# The configuration here mirrors config.Default() in internal/config. It is
# restated rather than derived because that is the point: if someone changes a
# default in Go without meaning to change what the rules assert, the tests that
# pin the behaviour keep asserting the old numbers and the difference shows up
# as a failure rather than as a quietly different verdict.
package scmbench.testdata

import rego.v1

config := {
	"thresholds": {
		"minApprovers": 2,
		"minRepositoryAdmins": 2,
		"minOrgAdmins": 2,
		"maxOrgAdmins": 5,
		"staleBranchDays": 90,
		"maxStaleBranches": 0,
		"inactiveUserDays": 90,
		"maxBypassPrincipals": 0,
	},
	"signatureHookKeys": ["signature", "signed-commit", "gpg", "verify-commit", "commit-signing"],
	"nonLinearMergeStrategies": ["no-ff", "rebase-no-ff"],
	"securityPolicyPaths": ["SECURITY.md", ".github/SECURITY.md"],
	"allowedBypassPrincipals": [],
	"maxDefaultPermission": "REPO_READ",
	"allowPublicRepositories": false,
	"skipArchivedRepositories": true,
	"permissionRank": {
		"": 0,
		"LICENSED_USER": 1,
		"REPO_READ": 10,
		"PROJECT_VIEW": 10,
		"PROJECT_READ": 10,
		"REPO_WRITE": 20,
		"PROJECT_WRITE": 20,
		"REPO_ADMIN": 30,
		"PROJECT_ADMIN": 30,
		"PROJECT_CREATE": 35,
		"ADMIN": 40,
		"SYS_ADMIN": 50,
	},
}

# every_available lists the per-repository fetches a rule may ask about. Tests
# start from all of them succeeding and switch off the one under examination,
# so a MANUAL case says which fetch failed rather than which twelve did not.
every_available := {
	"defaultBranch": true,
	"pullRequestSettings": true,
	"mergeStrategies": true,
	"branchRestrictions": true,
	"requiredBuilds": true,
	"hooks": true,
	"branches": true,
	"branchAges": true,
	"files": true,
	"permissions": true,
	"admins": true,
}

# repo builds a repository with a resolved default branch, everything readable,
# and the given fields merged over the top.
repo(fields) := object.union(
	{
		"fullName": "PRJ/app",
		"defaultBranch": "refs/heads/main",
		"defaultBranchDisplay": "main",
		"available": every_available,
	},
	fields,
)

# input_for wraps a resource as the engine does.
input_for(resource) := {"resource": resource, "config": config}

# repo_input is the common case: a repository, wrapped.
repo_input(fields) := input_for(repo(fields))

# without marks one availability key as failed, leaving the rest readable.
without(key) := object.union(every_available, {key: false})

# input_with_config is input_for with config keys overridden, for the tests
# that are about a threshold rather than about a repository.
input_with_config(resource, overrides) := {
	"resource": resource,
	"config": object.union(config, overrides),
}

# repo_input_with_config is repo_input's equivalent.
repo_input_with_config(fields, overrides) := input_with_config(repo(fields), overrides)

# restriction builds a branch restriction that covers the default branch.
restriction(kind, extra) := object.union(
	{"type": kind, "matchesDefaultBranch": true},
	extra,
)

# exempt renders what the fetcher leaves behind for a restriction that exempts
# the given users: both the raw grant and the resolved set. Writing the two
# together is deliberate — a fixture carrying one without the other describes
# a snapshot the fetcher cannot produce.
exempt(users) := {
	"exemptUsers": users,
	"exemptPrincipals": {"users": users, "count": count(users), "complete": true},
}

# exempt_unresolved is a restriction exempting a group the scan could not
# expand. The members are unknown, so the resolved set is a lower bound and
# every count derived from it is too.
exempt_unresolved(group) := {
	"exemptGroups": [group],
	"exemptPrincipals": {"users": [], "groups": [group], "count": 0, "complete": false},
}
