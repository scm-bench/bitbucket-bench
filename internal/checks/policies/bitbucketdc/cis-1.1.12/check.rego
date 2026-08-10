package scmbench.rules.cis_1_1_12

import rego.v1

import data.scmbench.lib

# Bitbucket Data Center ships no built-in signature verification, so this is
# always an add-on hook. Which add-on counts is deployment-specific, hence the
# configurable key list rather than a hard-coded vendor name.
hook_matches(h) if {
	some pattern in object.get(lib.cfg, "signatureHookKeys", [])

	# Every string contains "", so a blank pattern would report the first
	# enabled hook — any hook — as a signature verifier. config.Validate
	# rejects one, and this is the second lock on the same door: a caller
	# building a Config in Go never passes through that check, and a PASS
	# nobody verified is the worst output this tool can produce.
	trim_space(pattern) != ""
	haystack := lower(concat(" ", [
		object.get(h, "key", ""),
		object.get(h, "name", ""),
	]))
	contains(haystack, lower(pattern))
}

matching := {h.key |
	some h in object.get(lib.resource, "hooks", [])
	h.enabled == true
	hook_matches(h)
}

result := {
	"status": "MANUAL",
	"details": "Repository hooks could not be read, so signature verification cannot be confirmed.",
} if {
	not lib.available("hooks")
} else := {
	"status": "PASS",
	"details": sprintf("Commit signature verification is enforced by an enabled hook: %s.", [concat(", ", sort(matching))]),
} if {
	count(matching) > 0
} else := {
	"status": "FAIL",
	"details": "No enabled hook verifies commit signatures, so commit authorship cannot be trusted.",
	"evidence": [sprintf("%d hook(s) enabled, none matching the configured signature-hook keys", [count([h | some h in object.get(lib.resource, "hooks", []); h.enabled == true])])],
}
