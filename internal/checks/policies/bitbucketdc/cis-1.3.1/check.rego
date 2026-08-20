package scmbench.rules.cis_1_3_1

import rego.v1

import data.scmbench.lib

threshold := object.get(lib.cfg, ["thresholds", "inactiveUserDays"], 90)

# Only accounts that can actually reach code matter here. A dormant account
# with no grants is untidy; a dormant account with write access is a credential
# nobody is watching.
relevant := [u |
	some u in lib.list(["users"])
	object.get(u, "active", false) == true
	object.get(u, "hasRepositoryAccess", false) == true
]

dormant := [u.name |
	some u in relevant
	object.get(u, "inactiveDays", -1) >= threshold
]

# An account Bitbucket reported no last-authentication time for is unknown, not
# fresh. Letting it fall through to PASS would turn a hole in the data into a
# clean bill of health for exactly the accounts nobody is watching.
unknown := [u.name |
	some u in relevant
	object.get(u, "inactiveDays", -1) < 0
]

decidable if {
	lib.available("users")
	lib.available("userActivity")
}

result := {
	"status": "MANUAL",
	"details": sprintf("Bitbucket did not report last-authentication times for this instance, so dormant accounts cannot be detected automatically. Review users who have not signed in for %d days under Administration -> Users and revoke access that is no longer needed.", [threshold]),
} if {
	not decidable
} else := {
	"status": "FAIL",
	"details": sprintf("%d active user(s) with repository access have not authenticated for %d days or more: %s.", [count(dormant), threshold, lib.joined(dormant, 10)]),
	"evidence": sort(dormant),
} if {
	count(dormant) > 0
} else := {
	"status": "MANUAL",
	"details": sprintf("%d active user(s) with repository access have no last-authentication time recorded, so their dormancy cannot be assessed: %s. Check them under Administration -> Users.", [count(unknown), lib.joined(unknown, 10)]),
	"evidence": sort(unknown),
} if {
	count(unknown) > 0
} else := {
	"status": "MANUAL",
	"details": sprintf("Some grant tables or group expansions were unreadable, so the set of users with repository access is incomplete: a dormant account whose only grant sits in an unread table is invisible here. Review users inactive for %d days under Administration -> Users.", [threshold]),
} if {
	# Ordered after FAIL on purpose: the dormant accounts the scan did see are
	# real findings whatever it missed; the gap only forbids the clean PASS.
	not lib.available("repositoryAccess")
} else := {
	"status": "PASS",
	"details": sprintf("No active user with repository access has been dormant for %d days or more.", [threshold]),
}
