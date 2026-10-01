package scmbench.rules.cis_1_3_1

import rego.v1

import data.scmbench.lib

threshold := object.get(lib.cfg, ["thresholds", "inactiveUserDays"], 90)

# Every active, licensed account: each can sign in, read everything open to
# all users and create personal repositories, and each is a credential an
# attacker can use. The population used to be "accounts holding a grant to
# code", which needed every grant table and group expansion on the instance —
# reading none of which an HTTP access token is allowed — so the control was
# MANUAL for every token.
population := [u |
	some u in lib.list(["users"])
	object.get(u, "active", false) == true
	object.get(u, "licensed", false) == true
]

# Last authenticated too long ago.
idle := [sprintf("%s (last authenticated %d days ago)", [u.name, u.inactiveDays]) |
	some u in population
	object.get(u, "inactiveDays", -1) >= threshold
]

# Never authenticated at all, and not new: an account created this morning has
# not had the chance. The platform has to say "never" (neverSignedIn) — a
# missing time on its own is only unknown.
never := [sprintf("%s (never authenticated; created %d days ago)", [u.name, u.ageDays]) |
	some u in population
	object.get(u, "neverSignedIn", false) == true
	object.get(u, "ageDays", -1) >= threshold
]

dormant := array.concat(idle, never)

# An account the platform reported no time for — and did not say "never" — is
# unknown, not fresh. Letting it fall through to PASS would turn a hole in the
# data into a clean bill of health for exactly the accounts nobody is watching.
time_unknown := [sprintf("%s (no last-authentication time recorded)", [u.name]) |
	some u in population
	object.get(u, "neverSignedIn", false) != true
	object.get(u, "inactiveDays", -1) < 0
]

# "Never", with no creation time to measure it against, is unknown too: a new
# account and a long-dormant one look the same. Measured on Bitbucket 8.19 and
# 9.4, which report no creation time for any account; 10.4 does. The reason
# travels with each name, because it changes what the reviewer has to check.
age_unknown := [sprintf("%s (never authenticated; creation date not reported)", [u.name]) |
	some u in population
	object.get(u, "neverSignedIn", false) == true
	object.get(u, "inactiveDays", -1) < 0
	object.get(u, "ageDays", -1) < 0
]

unknown := array.concat(age_unknown, time_unknown)

age_note := sprintf(" An account that never authenticated counts as dormant once it is %d days old, and this Bitbucket version does not report when an account was created.", [threshold]) if {
	count(age_unknown) > 0
} else := ""

decidable if {
	lib.available("users")
	lib.available("userActivity")
	lib.available("licensedUsers")
}

result := {
	"status": "MANUAL",
	"details": sprintf("The user directory, the licensed users or their last-authentication times could not be read, so dormant accounts cannot be detected automatically. Review users who have not signed in for %d days under Administration -> Users and revoke access that is no longer needed.", [threshold]),
} if {
	not decidable
} else := {
	"status": "FAIL",
	"details": sprintf("%d active, licensed account(s) have not authenticated in %d days or more: %s.", [count(dormant), threshold, lib.joined(dormant, 10)]),
	"evidence": sort(dormant),
} if {
	count(dormant) > 0
} else := {
	"status": "MANUAL",
	"details": sprintf("%d active, licensed account(s) cannot be assessed for dormancy: %s.%s Check them under Administration -> Users.", [count(unknown), lib.joined(unknown, 10), age_note]),
	"evidence": sort(unknown),
} if {
	count(unknown) > 0
} else := {
	"status": "PASS",
	"details": sprintf("Every active, licensed account has authenticated in the last %d days, or was created less than %d days ago.", [threshold, threshold]),
}
