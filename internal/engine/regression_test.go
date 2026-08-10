package engine_test

import (
	"context"
	"testing"

	"github.com/scm-bench/scm-bench/internal/checks"
	"github.com/scm-bench/scm-bench/internal/config"
	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// Every list in the snapshot can legitimately be empty on a real instance, and
// a nil Go slice marshals to JSON null rather than []. Rego's object.get only
// substitutes its default for an *absent* key, so a null reaching a builtin
// like concat or sort raises a type error, the rule becomes undefined, and the
// control silently produces no verdict at all.
//
// This is the guard for that whole class of bug: it exercises every control
// against a snapshot where nothing is populated. Any future field that forgets
// omitempty, or any policy that reads a list without lib.list, fails here.
func TestZeroValuedSnapshotProducesAVerdictForEveryControl(t *testing.T) {
	ctx := context.Background()
	eng, err := engine.New(ctx, config.Default(), scm.PlatformBitbucketDC)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	available := map[string]bool{}
	for _, k := range []string{
		"defaultBranch", "pullRequestSettings", "mergeStrategies", "branchRestrictions",
		"requiredBuilds", "hooks", "branches", "branchAges", "files", "permissions", "admins",
	} {
		available[k] = true
	}

	snapshot := &scm.Snapshot{
		SchemaVersion: scm.SchemaVersion,
		Metadata:      scm.Metadata{Platform: scm.PlatformBitbucketDC},
		Organization: scm.Organization{
			EffectiveAdmins: scm.EffectivePrincipals{Complete: true},
			Available: map[string]bool{
				"adminUsers": true, "adminGroups": true, "users": true, "userActivity": true,
			},
		},
		Projects: []scm.Project{{
			Key: "PRJ",
			Repositories: []scm.Repository{
				{FullName: "PRJ/bare", Available: available},
				// A repository with no Available map at all: every key reads as
				// unknown, which must degrade to MANUAL rather than error.
				{FullName: "PRJ/opaque"},
			},
		}},
	}

	rep, err := eng.Evaluate(ctx, snapshot)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	for _, e := range rep.Errors {
		t.Errorf("policy error on a zero-valued snapshot: %s", e)
	}

	bundle, err := checks.Load()
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range rep.Findings {
		seen[f.CheckID] = true
	}
	for _, c := range bundle.Checks {
		if !seen[c.ID] {
			t.Errorf("check %s produced no finding on a zero-valued snapshot", c.ID)
		}
	}
}

// An empty administrator list is a real state, not a broken one: it must reach
// a FAIL rather than erroring the rule out of existence.
func TestEmptyAdminListStillFails(t *testing.T) {
	t.Run("organization", func(t *testing.T) {
		org := healthyOrg()
		org.EffectiveAdmins = scm.EffectivePrincipals{Users: nil, Count: 0, Complete: true}
		got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
		assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{
			"CIS-1.3.3": engine.StatusFail,
		})
	})

	t.Run("repository", func(t *testing.T) {
		repo := hardenedRepo()
		repo.Admins = scm.EffectivePrincipals{Users: nil, Count: 0, Complete: true}
		got := evaluate(t, snapshotWith([]scm.Repository{repo}, healthyOrg()))
		assertStatuses(t, got, "PRJ/hardened", map[string]engine.Status{
			"CIS-1.3.7": engine.StatusFail,
		})
	})
}

// A repository whose default branch could not be resolved was never browsed,
// so reporting "no security policy found" would be a verdict about a search
// that never happened.
func TestUnresolvedDefaultBranchDoesNotFailSecurityPolicy(t *testing.T) {
	repo := hardenedRepo()
	repo.DefaultBranch = ""
	repo.DefaultBranchDisplay = ""
	repo.Empty = false
	repo.Files = scm.Files{}
	repo.Available["files"] = false

	got := evaluate(t, snapshotWith([]scm.Repository{repo}, healthyOrg()))
	assertStatuses(t, got, "PRJ/hardened", map[string]engine.Status{
		"CIS-1.2.1": engine.StatusManual,
	})
}

// A user with no last-authentication timestamp is unknown, not fresh. Counting
// them as active would turn a gap in the data into a clean bill of health for
// exactly the accounts nobody is watching.
func TestUsersWithUnknownActivityReportManualNotPass(t *testing.T) {
	org := healthyOrg()
	org.Users = append(org.Users, scm.User{
		Name: "ghost", Active: true, HasRepositoryAccess: true, InactiveDays: -1,
	})

	got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
	assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{
		"CIS-1.3.1": engine.StatusManual,
	})
}

// A confirmed dormant account is conclusive even when other accounts have no
// timestamp: the unknowns cannot make a known finding go away.
func TestConfirmedDormantUserOutranksUnknownActivity(t *testing.T) {
	org := healthyOrg()
	org.Users = append(org.Users,
		scm.User{Name: "ghost", Active: true, HasRepositoryAccess: true, InactiveDays: -1},
		scm.User{Name: "departed", Active: true, HasRepositoryAccess: true, InactiveDays: 400},
	)

	got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
	assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{
		"CIS-1.3.1": engine.StatusFail,
	})
}

// An account with no repository access is not worth reporting even when its
// activity is unknown.
func TestUnknownActivityWithoutAccessIsIgnored(t *testing.T) {
	org := healthyOrg()
	org.Users = append(org.Users, scm.User{
		Name: "build-bot", Active: true, HasRepositoryAccess: false, InactiveDays: -1,
	})

	got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
	assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{
		"CIS-1.3.1": engine.StatusPass,
	})
}
