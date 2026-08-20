package scmbench.rules.cis_1_1_9

import rego.v1

import data.scmbench.lib

# Bitbucket can gate a merge on builds two different ways: a minimum count of
# green builds (a merge check) or a required-builds condition naming specific
# CI plans. Either one satisfies the control.
conditions := [c |
	some c in lib.list("requiredBuilds")
	c.matchesDefaultBranch == true
]

minimum_builds := lib.pr_setting("requiredSuccessfulBuilds", 0)

gated if {
	count(conditions) > 0
}

gated if {
	minimum_builds > 0
}

# Every source must be readable before a clean FAIL can be claimed: a missing
# required-builds add-on looks exactly like a repository with no conditions —
# and a repository whose default branch could not be resolved makes every
# condition's matchesDefaultBranch false, which looks exactly like no
# condition covering it. (A PASS through the repository-wide minimum-builds
# check needs no default branch, which is why gated is decided first.)
fully_known if {
	lib.available("requiredBuilds")
	lib.available("pullRequestSettings")
	lib.has_default_branch
}

result := lib.branch_protection_na if {
	lib.empty_repository
} else := {
	"status": "PASS",
	"details": sprintf("Merging into %s is gated on CI: %d required-build condition(s) and a minimum of %d successful build(s).", [lib.default_branch_name, count(conditions), minimum_builds]),
} if {
	gated
} else := {
	"status": "MANUAL",
	"details": "Required builds, merge checks or the default branch could not be read, so CI gating cannot be confirmed.",
} if {
	not fully_known
} else := {
	"status": "FAIL",
	"details": sprintf("Nothing prevents a merge into %s while checks are failing or absent.", [lib.default_branch_name]),
	"evidence": ["requiredSuccessfulBuilds = 0", "no required-build condition covers the default branch"],
}
