package scm_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scm-bench/bitbucket-bench/internal/scm"
)

// The JSON field names are the contract between the fetcher and every rule.
//
// Nothing in Go enforces it. Rego reads `resource.branchRestrictions` as a
// string, so renaming the tag on BranchRestrictions compiles cleanly, passes
// vet, and turns every branch protection control into MANUAL on every
// repository — a silent, total loss of coverage that looks like an instance
// nobody can read. A snapshot is also meant to be captured once and evaluated
// later, so a rename breaks archives that were valid when they were written.
//
// This test is deliberately a list of strings rather than anything clever. It
// is here to be edited on purpose: changing it means changing the schema, and
// changing the schema means bumping SchemaVersion and updating the rules.
func TestSnapshotJSONContract(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  []string
	}{
		{scm.Snapshot{}, []string{"schemaVersion", "metadata", "organization", "projects"}},
		{scm.Metadata{}, []string{"tool", "toolVersion", "platform", "baseUrl", "generatedAt", "warnings"}},
		{scm.Organization{}, []string{"admins", "effectiveAdmins", "users", "available"}},
		{scm.User{}, []string{"name", "displayName", "emailAddress", "active", "lastActivityEpoch", "inactiveDays", "hasRepositoryAccess"}},
		{scm.Project{}, []string{"key", "name", "type", "public", "permissions", "repositories"}},
		{scm.Repository{}, []string{
			"slug", "name", "projectKey", "fullName", "public", "archived", "forkable", "empty",
			"defaultBranch", "defaultBranchDisplay", "pullRequestSettings", "branchRestrictions",
			"requiredBuilds", "hooks", "branches", "files", "permissions", "admins", "available", "errors",
		}},
		{scm.BranchRestriction{}, []string{
			"id", "type", "matcherId", "matcherText", "matcherType", "scope",
			"matchesDefaultBranch", "exemptUsers", "exemptGroups", "exemptAccessKeys",
		}},
		{scm.EffectivePrincipals{}, []string{"users", "groups", "count", "complete"}},
		{scm.Permissions{}, []string{"users", "groups", "defaultPermission", "defaultPermissionKnown", "publicAccess"}},
		{scm.Branch{}, []string{"id", "displayId", "isDefault", "latestCommit", "latestCommitEpoch", "ageDays"}},
		{scm.Hook{}, []string{"key", "name", "type", "enabled", "configured", "scope"}},
	} {
		typ := reflect.TypeOf(tc.value)
		t.Run(typ.Name(), func(t *testing.T) {
			got := jsonFieldNames(t, typ)
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("JSON field names changed.\n got: %v\nwant: %v\n\n"+
					"Rego reads these by name, so a rename silently turns controls into MANUAL "+
					"and invalidates archived snapshots. If this is deliberate, bump scm.SchemaVersion, "+
					"update the rules, and edit this list.", got, want)
			}
		})
	}
}

// A version bump is what tells an old snapshot it can no longer be read, so it
// is worth one assertion of its own rather than living only in a struct tag.
func TestSchemaVersionIsPinned(t *testing.T) {
	if scm.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q; changing it is a breaking change for archived snapshots, "+
			"so update this test only alongside the rules that read them", scm.SchemaVersion)
	}
}

// Round-tripping catches the other half: a field that marshals but cannot be
// read back, which is exactly what `scan --snapshot-in` does with a file
// written by `--snapshot-out` weeks earlier.
func TestSnapshotRoundTrips(t *testing.T) {
	original := scm.Snapshot{
		SchemaVersion: scm.SchemaVersion,
		Metadata: scm.Metadata{
			Tool: "bitbucket-bench", Platform: scm.PlatformBitbucketDC,
			BaseURL: "https://bitbucket.example.com", Warnings: []string{"a group could not be expanded"},
		},
		Organization: scm.Organization{
			EffectiveAdmins: scm.EffectivePrincipals{Users: []string{"alice"}, Count: 1, Complete: true},
			Users:           []scm.User{{Name: "alice", Active: true, InactiveDays: -1}},
			Available:       map[string]bool{"users": true},
		},
		Projects: []scm.Project{{Key: "PRJ", Repositories: []scm.Repository{{
			FullName:           "PRJ/app",
			BranchRestrictions: []scm.BranchRestriction{{Type: "read-only", MatchesDefaultBranch: true, ExemptAccessKeys: 2}},
			Available:          map[string]bool{"branchRestrictions": true},
		}}}},
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back scm.Snapshot
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, back) {
		t.Errorf("snapshot did not survive a round trip\n got: %+v\nwant: %+v", back, original)
	}
}

func jsonFieldNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	var out []string
	for _, f := range reflect.VisibleFields(typ) {
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			t.Errorf("%s.%s has no json tag; the rules read this struct by name", typ.Name(), f.Name)
			continue
		}
		out = append(out, name)
	}
	return out
}
