package scmbench.rules.cis_1_3_8_test

import rego.v1

import data.scmbench.rules.cis_1_3_8
import data.scmbench.testdata

test_passes_at_the_ceiling if {
	r := cis_1_3_8.result with input as testdata.repo_input({
		"public": false,
		"permissions": {"defaultPermission": "REPO_READ", "defaultPermissionKnown": true},
	})
	r.status == "PASS"
}

test_passes_when_nothing_is_granted_by_default if {
	r := cis_1_3_8.result with input as testdata.repo_input({
		"public": false,
		"permissions": {"defaultPermission": "", "defaultPermissionKnown": true},
	})
	r.status == "PASS"
}

test_fails_above_the_ceiling if {
	r := cis_1_3_8.result with input as testdata.repo_input({
		"public": false,
		"permissions": {"defaultPermission": "PROJECT_WRITE", "defaultPermissionKnown": true},
	})
	r.status == "FAIL"
	contains(r.details, "PROJECT_WRITE")
}

test_fails_on_anonymous_access if {
	r := cis_1_3_8.result with input as testdata.repo_input({
		"public": true,
		"permissions": {"defaultPermission": "", "defaultPermissionKnown": true},
	})
	r.status == "FAIL"
	contains(r.details, "anonymous")
}

# Some instances publish code on purpose, which is a decision rather than a
# misconfiguration once it has been stated.
test_anonymous_access_can_be_allowed_by_configuration if {
	cfg := object.union(testdata.config, {"allowPublicRepositories": true})
	r := cis_1_3_8.result with input as {
		"resource": testdata.repo({
			"public": true,
			"permissions": {"defaultPermission": "", "defaultPermissionKnown": true},
		}),
		"config": cfg,
	}
	r.status == "PASS"
}

# An already-detected violation is conclusive even from a partial probe.
test_a_violation_wins_over_an_unknown_default if {
	r := cis_1_3_8.result with input as testdata.repo_input({
		"public": true,
		"permissions": {"defaultPermission": "", "defaultPermissionKnown": false},
	})
	r.status == "FAIL"
}

test_manual_when_the_default_permission_is_unknown if {
	r := cis_1_3_8.result with input as testdata.repo_input({
		"public": false,
		"permissions": {"defaultPermission": "", "defaultPermissionKnown": false},
	})
	r.status == "MANUAL"
}

# An unrecognised permission used to default to rank 0 — below everything — so
# the rule passed and announced that nothing above REPO_READ was granted, about
# a permission it had never heard of.
test_an_unknown_permission_is_manual_not_a_pass if {
	r := cis_1_3_8.result with input as testdata.repo_input({
		"public": false,
		"permissions": {"defaultPermission": "REPO_CREATE", "defaultPermissionKnown": true},
	})
	r.status == "MANUAL"
	contains(r.details, "REPO_CREATE")
}
