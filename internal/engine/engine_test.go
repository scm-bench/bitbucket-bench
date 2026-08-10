package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/scm-bench/scm-bench/internal/checks"
	"github.com/scm-bench/scm-bench/internal/config"
	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// evaluate runs the whole bundle against a snapshot and indexes the results by
// check ID and resource, so a test can assert on one control at a time.
func evaluate(t *testing.T, snapshot *scm.Snapshot) map[string]map[string]engine.Status {
	t.Helper()
	ctx := context.Background()

	eng, err := engine.New(ctx, config.Default(), scm.PlatformBitbucketDC)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	rep, err := eng.Evaluate(ctx, snapshot)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("policies reported errors: %v", rep.Errors)
	}

	out := map[string]map[string]engine.Status{}
	for _, f := range rep.Findings {
		if out[f.CheckID] == nil {
			out[f.CheckID] = map[string]engine.Status{}
		}
		if _, dup := out[f.CheckID][f.Resource]; dup {
			t.Fatalf("check %s produced two findings for %s", f.CheckID, f.Resource)
		}
		out[f.CheckID][f.Resource] = f.Status
		if f.Details == "" {
			t.Errorf("check %s on %s has an empty details string", f.CheckID, f.Resource)
		}
	}
	return out
}

func assertStatuses(t *testing.T, got map[string]map[string]engine.Status, resource string, want map[string]engine.Status) {
	t.Helper()
	for checkID, wantStatus := range want {
		byResource, ok := got[checkID]
		if !ok {
			t.Errorf("%s: check %s produced no finding at all", resource, checkID)
			continue
		}
		gotStatus, ok := byResource[resource]
		if !ok {
			t.Errorf("%s: check %s produced no finding for this resource", resource, checkID)
			continue
		}
		if gotStatus != wantStatus {
			t.Errorf("%s: check %s = %s, want %s", resource, checkID, gotStatus, wantStatus)
		}
	}
}

func snapshotWith(repos []scm.Repository, org scm.Organization) *scm.Snapshot {
	return &scm.Snapshot{
		SchemaVersion: scm.SchemaVersion,
		Metadata: scm.Metadata{
			Tool:        "scm-bench",
			Platform:    scm.PlatformBitbucketDC,
			BaseURL:     "https://bitbucket.example.com",
			GeneratedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		Organization: org,
		Projects: []scm.Project{{
			Key:          "PRJ",
			Name:         "Project",
			Repositories: repos,
		}},
	}
}

// hardenedRepo satisfies every automatable repository control.
func hardenedRepo() scm.Repository {
	restriction := func(kind string) scm.BranchRestriction {
		return scm.BranchRestriction{
			Type:                 kind,
			MatcherID:            "refs/heads/main",
			MatcherType:          "BRANCH",
			MatchesDefaultBranch: true,
		}
	}
	return scm.Repository{
		Slug:                 "hardened",
		Name:                 "hardened",
		ProjectKey:           "PRJ",
		FullName:             "PRJ/hardened",
		DefaultBranch:        "refs/heads/main",
		DefaultBranchDisplay: "main",
		PullRequestSettings: scm.PullRequestSettings{
			RequiredApprovers:        2,
			RequiredAllTasksComplete: true,
			RequiredSuccessfulBuilds: 1,
			UnapproveOnUpdate:        true,
			MergeStrategies: []scm.MergeStrategy{
				{ID: "ff-only", Enabled: true},
				{ID: "squash", Enabled: true},
				{ID: "no-ff", Enabled: false},
			},
		},
		BranchRestrictions: []scm.BranchRestriction{
			restriction("pull-request-only"),
			restriction("fast-forward-only"),
			restriction("no-deletes"),
		},
		RequiredBuilds: []scm.RequiredBuild{{
			BuildParentKeys:      []string{"CI-BUILD"},
			MatcherID:            "refs/heads/main",
			MatchesDefaultBranch: true,
		}},
		Hooks: []scm.Hook{
			{Key: "com.example.gpg-signature-check", Name: "GPG signature check", Enabled: true, Configured: true},
		},
		Branches: []scm.Branch{
			{ID: "refs/heads/main", DisplayID: "main", IsDefault: true, AgeDays: 2},
			{ID: "refs/heads/feature/x", DisplayID: "feature/x", AgeDays: 10},
		},
		Files: scm.Files{
			SecurityPolicyPaths: []string{"SECURITY.md"},
			Probed:              []string{"SECURITY.md"},
		},
		Permissions: scm.Permissions{DefaultPermissionKnown: true},
		Admins:      scm.EffectivePrincipals{Users: []string{"alice", "bob"}, Count: 2, Complete: true},
		Available:   allAvailable(true),
	}
}

// openRepo fails every automatable repository control.
func openRepo() scm.Repository {
	return scm.Repository{
		Slug:                 "open",
		Name:                 "open",
		ProjectKey:           "PRJ",
		FullName:             "PRJ/open",
		Public:               true,
		DefaultBranch:        "refs/heads/main",
		DefaultBranchDisplay: "main",
		PullRequestSettings: scm.PullRequestSettings{
			RequiredApprovers: 1,
			MergeStrategies: []scm.MergeStrategy{
				{ID: "no-ff", Enabled: true},
			},
		},
		Hooks: []scm.Hook{
			{Key: "com.example.unrelated-hook", Name: "Jira issue check", Enabled: true},
		},
		Branches: []scm.Branch{
			{ID: "refs/heads/main", DisplayID: "main", IsDefault: true, AgeDays: 3},
			{ID: "refs/heads/abandoned", DisplayID: "abandoned", AgeDays: 400},
		},
		Files:       scm.Files{Probed: []string{"SECURITY.md", ".github/SECURITY.md"}},
		Permissions: scm.Permissions{DefaultPermission: "PROJECT_WRITE", DefaultPermissionKnown: true},
		Admins:      scm.EffectivePrincipals{Users: []string{"alice"}, Count: 1, Complete: true},
		Available:   allAvailable(true),
	}
}

// unknownRepo is a repository whose settings could not be read at all.
func unknownRepo() scm.Repository {
	return scm.Repository{
		Slug:                 "unknown",
		Name:                 "unknown",
		ProjectKey:           "PRJ",
		FullName:             "PRJ/unknown",
		DefaultBranch:        "refs/heads/main",
		DefaultBranchDisplay: "main",
		Available:            allAvailable(false),
	}
}

func emptyRepo() scm.Repository {
	return scm.Repository{
		Slug:        "empty",
		Name:        "empty",
		FullName:    "PRJ/empty",
		Empty:       true,
		Available:   allAvailable(true),
		Permissions: scm.Permissions{DefaultPermissionKnown: true},
		Admins:      scm.EffectivePrincipals{Users: []string{"alice", "bob"}, Count: 2, Complete: true},
	}
}

func allAvailable(v bool) map[string]bool {
	keys := []string{
		"defaultBranch", "pullRequestSettings", "mergeStrategies", "branchRestrictions",
		"requiredBuilds", "hooks", "branches", "branchAges", "files", "permissions", "admins",
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = v
	}
	return out
}

func healthyOrg() scm.Organization {
	return scm.Organization{
		Admins: []scm.PrincipalPermission{
			{Name: "alice", Type: "user", Permission: "SYS_ADMIN"},
			{Name: "bob", Type: "user", Permission: "ADMIN"},
		},
		EffectiveAdmins: scm.EffectivePrincipals{Users: []string{"alice", "bob"}, Count: 2, Complete: true},
		Users: []scm.User{
			{Name: "alice", Active: true, HasRepositoryAccess: true, InactiveDays: 1},
			{Name: "bob", Active: true, HasRepositoryAccess: true, InactiveDays: 20},
		},
		Available: map[string]bool{"adminUsers": true, "adminGroups": true, "users": true, "userActivity": true},
	}
}

func TestHardenedRepositoryPassesEveryAutomatableControl(t *testing.T) {
	got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, healthyOrg()))

	assertStatuses(t, got, "PRJ/hardened", map[string]engine.Status{
		"CIS-1.1.3":  engine.StatusPass,
		"CIS-1.1.4":  engine.StatusPass,
		"CIS-1.1.8":  engine.StatusPass,
		"CIS-1.1.9":  engine.StatusPass,
		"CIS-1.1.11": engine.StatusPass,
		"CIS-1.1.12": engine.StatusPass,
		"CIS-1.1.13": engine.StatusPass,
		"CIS-1.1.15": engine.StatusPass,
		"CIS-1.1.16": engine.StatusPass,
		"CIS-1.1.17": engine.StatusPass,
		"CIS-1.2.1":  engine.StatusPass,
		"CIS-1.3.7":  engine.StatusPass,
		"CIS-1.3.8":  engine.StatusPass,
		// Documented as not answerable through the API.
		"CIS-1.1.6": engine.StatusManual,
	})
	assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{
		"CIS-1.3.1": engine.StatusPass,
		"CIS-1.3.3": engine.StatusPass,
		"CIS-1.2.2": engine.StatusManual,
		"CIS-1.2.3": engine.StatusManual,
		"CIS-1.3.5": engine.StatusManual,
		"CIS-1.3.9": engine.StatusNA,
	})
}

func TestOpenRepositoryFailsEveryAutomatableControl(t *testing.T) {
	got := evaluate(t, snapshotWith([]scm.Repository{openRepo()}, healthyOrg()))

	assertStatuses(t, got, "PRJ/open", map[string]engine.Status{
		"CIS-1.1.3":  engine.StatusFail,
		"CIS-1.1.4":  engine.StatusFail,
		"CIS-1.1.8":  engine.StatusFail,
		"CIS-1.1.9":  engine.StatusFail,
		"CIS-1.1.11": engine.StatusFail,
		"CIS-1.1.12": engine.StatusFail,
		"CIS-1.1.13": engine.StatusFail,
		"CIS-1.1.15": engine.StatusFail,
		"CIS-1.1.16": engine.StatusFail,
		"CIS-1.1.17": engine.StatusFail,
		"CIS-1.2.1":  engine.StatusFail,
		"CIS-1.3.7":  engine.StatusFail,
		"CIS-1.3.8":  engine.StatusFail,
	})
}

// An unreadable instance must never look compliant, and must never look broken
// either: every affected control has to say so out loud.
func TestUnreadableSettingsReportManualNotFail(t *testing.T) {
	org := scm.Organization{Available: map[string]bool{}}
	got := evaluate(t, snapshotWith([]scm.Repository{unknownRepo()}, org))

	assertStatuses(t, got, "PRJ/unknown", map[string]engine.Status{
		"CIS-1.1.3":  engine.StatusManual,
		"CIS-1.1.4":  engine.StatusManual,
		"CIS-1.1.8":  engine.StatusManual,
		"CIS-1.1.9":  engine.StatusManual,
		"CIS-1.1.11": engine.StatusManual,
		"CIS-1.1.12": engine.StatusManual,
		"CIS-1.1.13": engine.StatusManual,
		"CIS-1.1.15": engine.StatusManual,
		"CIS-1.1.16": engine.StatusManual,
		"CIS-1.1.17": engine.StatusManual,
		"CIS-1.2.1":  engine.StatusManual,
		"CIS-1.3.7":  engine.StatusManual,
		"CIS-1.3.8":  engine.StatusManual,
	})
	assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{
		"CIS-1.3.1": engine.StatusManual,
		"CIS-1.3.3": engine.StatusManual,
	})
}

func TestEmptyRepositorySkipsBranchProtectionControls(t *testing.T) {
	got := evaluate(t, snapshotWith([]scm.Repository{emptyRepo()}, healthyOrg()))

	assertStatuses(t, got, "PRJ/empty", map[string]engine.Status{
		"CIS-1.1.9":  engine.StatusNA,
		"CIS-1.1.15": engine.StatusNA,
		"CIS-1.1.16": engine.StatusNA,
		"CIS-1.1.17": engine.StatusNA,
		"CIS-1.2.1":  engine.StatusNA,
	})
}

func TestOrganizationAdministratorBounds(t *testing.T) {
	tests := []struct {
		name  string
		users []string
		want  engine.Status
	}{
		{"too few", []string{"alice"}, engine.StatusFail},
		{"within range", []string{"alice", "bob", "carol"}, engine.StatusPass},
		{"too many", []string{"a", "b", "c", "d", "e", "f"}, engine.StatusFail},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			org := healthyOrg()
			org.EffectiveAdmins = scm.EffectivePrincipals{Users: tc.users, Count: len(tc.users), Complete: true}
			got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
			assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{"CIS-1.3.3": tc.want})
		})
	}
}

// An over-count is conclusive even when a group could not be expanded, because
// expanding it could only add more administrators.
func TestOrganizationAdminOvercountIsConclusiveWhenIncomplete(t *testing.T) {
	org := healthyOrg()
	org.EffectiveAdmins = scm.EffectivePrincipals{
		Users:    []string{"a", "b", "c", "d", "e", "f"},
		Count:    6,
		Complete: false,
	}
	got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
	assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{"CIS-1.3.3": engine.StatusFail})
}

// Likewise, a lower bound that already meets the repository minimum settles the
// control without needing the unexpandable group.
func TestRepositoryAdminLowerBoundCanStillPass(t *testing.T) {
	repo := hardenedRepo()
	repo.Admins = scm.EffectivePrincipals{Users: []string{"alice", "bob"}, Count: 2, Complete: false}
	got := evaluate(t, snapshotWith([]scm.Repository{repo}, healthyOrg()))
	assertStatuses(t, got, "PRJ/hardened", map[string]engine.Status{"CIS-1.3.7": engine.StatusPass})
}

func TestDormantUserDetection(t *testing.T) {
	t.Run("dormant user with access fails", func(t *testing.T) {
		org := healthyOrg()
		org.Users = append(org.Users, scm.User{
			Name: "ghost", Active: true, HasRepositoryAccess: true, InactiveDays: 400,
		})
		got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
		assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{"CIS-1.3.1": engine.StatusFail})
	})

	t.Run("dormant user without repository access is ignored", func(t *testing.T) {
		org := healthyOrg()
		org.Users = append(org.Users, scm.User{
			Name: "service-account", Active: true, HasRepositoryAccess: false, InactiveDays: 400,
		})
		got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
		assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{"CIS-1.3.1": engine.StatusPass})
	})

	t.Run("missing timestamps report manual, not pass", func(t *testing.T) {
		org := healthyOrg()
		org.Available["userActivity"] = false
		for i := range org.Users {
			org.Users[i].InactiveDays = -1
		}
		got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo()}, org))
		assertStatuses(t, got, engine.InstanceResourceName, map[string]engine.Status{"CIS-1.3.1": engine.StatusManual})
	})
}

// A restriction that does not cover the default branch must not be credited.
func TestRestrictionOnAnotherBranchDoesNotCount(t *testing.T) {
	repo := hardenedRepo()
	for i := range repo.BranchRestrictions {
		repo.BranchRestrictions[i].MatchesDefaultBranch = false
	}
	got := evaluate(t, snapshotWith([]scm.Repository{repo}, healthyOrg()))
	assertStatuses(t, got, "PRJ/hardened", map[string]engine.Status{
		"CIS-1.1.15": engine.StatusFail,
		"CIS-1.1.16": engine.StatusFail,
		"CIS-1.1.17": engine.StatusFail,
	})
}

// A required-build condition exempting the default branch protects nothing.
func TestRequiredBuildExemptingDefaultBranchFails(t *testing.T) {
	repo := hardenedRepo()
	repo.PullRequestSettings.RequiredSuccessfulBuilds = 0
	repo.RequiredBuilds = []scm.RequiredBuild{{
		BuildParentKeys:      []string{"CI-BUILD"},
		MatcherID:            "refs/heads/main",
		ExemptMatcherID:      "refs/heads/main",
		MatchesDefaultBranch: false,
	}}
	got := evaluate(t, snapshotWith([]scm.Repository{repo}, healthyOrg()))
	assertStatuses(t, got, "PRJ/hardened", map[string]engine.Status{"CIS-1.1.9": engine.StatusFail})
}

func TestConfigThresholdsAreHonoured(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Thresholds.MinApprovers = 1

	eng, err := engine.New(ctx, cfg, scm.PlatformBitbucketDC)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	// openRepo requires one approver, which fails the default of two but meets
	// a configured minimum of one.
	rep, err := eng.Evaluate(ctx, snapshotWith([]scm.Repository{openRepo()}, healthyOrg()))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	for _, f := range rep.Findings {
		if f.CheckID == "CIS-1.1.3" && f.Resource == "PRJ/open" {
			if f.Status != engine.StatusPass {
				t.Fatalf("CIS-1.1.3 = %s, want PASS with minApprovers=1", f.Status)
			}
			return
		}
	}
	t.Fatal("CIS-1.1.3 produced no finding for PRJ/open")
}

func TestExcludedChecksDoNotRun(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Exclude = []string{"CIS-1.1.3"}

	eng, err := engine.New(ctx, cfg, scm.PlatformBitbucketDC)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	for _, c := range eng.Checks() {
		if c.ID == "CIS-1.1.3" {
			t.Fatal("CIS-1.1.3 was excluded but is still selected")
		}
	}
}

// An ID that names no control is a typo, and reading it silently is what makes
// it dangerous: an exclude that matches nothing leaves the control running, and
// the scan still looks like it honoured the configuration.
func TestUnknownCheckIDIsRejected(t *testing.T) {
	ctx := context.Background()

	for name, mutate := range map[string]func(*config.Config){
		"exclude": func(c *config.Config) { c.Exclude = []string{"CIS-9.9.9"} },
		"include": func(c *config.Config) { c.Include = []string{"CIS-1.1.3", "CIS-0.0.0"} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default()
			mutate(&cfg)
			if _, err := engine.New(ctx, cfg, scm.PlatformBitbucketDC); err == nil {
				t.Error("engine.New accepted a check ID that is not in the bundle")
			}
		})
	}

	// Case and surrounding space are tolerated by config.Selects, so validation
	// must tolerate them too or the two would disagree about what is known.
	cfg := config.Default()
	cfg.Exclude = []string{"  cis-1.1.3  "}
	if _, err := engine.New(ctx, cfg, scm.PlatformBitbucketDC); err != nil {
		t.Errorf("a valid ID with different case and padding was rejected: %v", err)
	}
}

func TestEveryBundledCheckIsExercised(t *testing.T) {
	bundle, err := checks.Load()
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	got := evaluate(t, snapshotWith([]scm.Repository{hardenedRepo(), openRepo(), unknownRepo(), emptyRepo()}, healthyOrg()))

	for _, c := range bundle.Checks {
		if _, ok := got[c.ID]; !ok {
			t.Errorf("check %s produced no findings; is its Rego package %q correct?", c.ID, c.Package)
		}
	}
	if len(bundle.Checks) != len(got) {
		t.Errorf("bundle has %d checks but %d produced findings", len(bundle.Checks), len(got))
	}
}
