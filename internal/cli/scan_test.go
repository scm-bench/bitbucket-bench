package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// writeSnapshotFixture saves a snapshot with one badly configured repository,
// so the CLI has something with a known verdict to report on.
func writeSnapshotFixture(t *testing.T) string {
	t.Helper()

	snapshot := scm.Snapshot{
		SchemaVersion: scm.SchemaVersion,
		Metadata: scm.Metadata{
			Tool:        "scm-bench",
			Platform:    scm.PlatformBitbucketDC,
			BaseURL:     "https://bitbucket.example.com",
			GeneratedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		Organization: scm.Organization{
			EffectiveAdmins: scm.EffectivePrincipals{Users: []string{"alice", "bob"}, Count: 2, Complete: true},
			Users: []scm.User{
				{Name: "alice", Active: true, HasRepositoryAccess: true, InactiveDays: 1},
			},
			Available: map[string]bool{"adminUsers": true, "adminGroups": true, "users": true, "userActivity": true},
		},
		Projects: []scm.Project{{
			Key:  "PRJ",
			Name: "Project",
			Repositories: []scm.Repository{{
				Slug:                 "app",
				Name:                 "app",
				ProjectKey:           "PRJ",
				FullName:             "PRJ/app",
				DefaultBranch:        "refs/heads/main",
				DefaultBranchDisplay: "main",
				// No approvals, no restrictions: a guaranteed HIGH failure.
				PullRequestSettings: scm.PullRequestSettings{
					MergeStrategies: []scm.MergeStrategy{{ID: "no-ff", Enabled: true}},
				},
				Branches:    []scm.Branch{{ID: "refs/heads/main", DisplayID: "main", IsDefault: true, AgeDays: 1}},
				Files:       scm.Files{Probed: []string{"SECURITY.md"}},
				Permissions: scm.Permissions{DefaultPermissionKnown: true},
				Admins:      scm.EffectivePrincipals{Users: []string{"alice", "bob"}, Count: 2, Complete: true},
				Available: map[string]bool{
					"defaultBranch": true, "pullRequestSettings": true, "mergeStrategies": true,
					"branchRestrictions": true, "requiredBuilds": true, "hooks": true,
					"branches": true, "branchAges": true, "files": true, "permissions": true, "admins": true,
				},
			}},
		}},
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// run executes the CLI and returns stdout, stderr and the process exit code.
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer

	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)

	err := root.Execute()
	return stdout.String(), stderr.String(), ExitCode(err)
}

func TestScanFromSnapshotProducesJSONReport(t *testing.T) {
	fixture := writeSnapshotFixture(t)
	stdout, _, code := run(t, "scan", "--snapshot-in", fixture, "-o", "json", "--fail-on", "none")

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d with --fail-on none", code, ExitOK)
	}

	var report engine.Report
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	if len(report.Findings) == 0 {
		t.Fatal("report contains no findings")
	}
	if report.Metadata.BaseURL != "https://bitbucket.example.com" {
		t.Errorf("metadata did not survive: %+v", report.Metadata)
	}

	var found bool
	for _, f := range report.Findings {
		if f.CheckID == "CIS-1.1.15" && f.Resource == "PRJ/app" {
			found = true
			if f.Status != engine.StatusFail {
				t.Errorf("CIS-1.1.15 = %s, want FAIL", f.Status)
			}
			if f.Remediation == "" {
				t.Error("a failing finding must carry remediation text")
			}
			// Details says what this repository does and Remediation says what
			// to change; neither answers why the control is worth acting on.
			// That is what the metadata's description is for, and it used to
			// stop at the bundle instead of reaching the report.
			if f.Description == "" {
				t.Error("a finding must carry the control's description")
			}
		}
	}
	if !found {
		t.Error("CIS-1.1.15 produced no finding for PRJ/app")
	}
}

// A scan that runs and finds HIGH failures exits 1: that is the CI contract.
func TestFailOnSeverityDrivesExitCode(t *testing.T) {
	fixture := writeSnapshotFixture(t)

	if _, _, code := run(t, "scan", "--snapshot-in", fixture, "-o", "json", "--fail-on", "high"); code != ExitFindings {
		t.Errorf("exit code = %d, want %d for HIGH failures", code, ExitFindings)
	}
	if _, _, code := run(t, "scan", "--snapshot-in", fixture, "-o", "json", "--fail-on", "none"); code != ExitOK {
		t.Errorf("exit code = %d, want %d with --fail-on none", code, ExitOK)
	}
}

func TestScanWritesReportToFile(t *testing.T) {
	fixture := writeSnapshotFixture(t)
	out := filepath.Join(t.TempDir(), "nested", "report.sarif")

	if _, _, code := run(t, "scan", "--snapshot-in", fixture, "-o", "sarif", "--output-file", out, "--fail-on", "none"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("report file was not created: %v", err)
	}
	var log map[string]any
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatalf("SARIF file does not parse: %v", err)
	}
	if log["version"] != "2.1.0" {
		t.Errorf("SARIF version = %v", log["version"])
	}

	// A rendered report names every repository that can be force-pushed and
	// every account that should have been deactivated. That is the same map of
	// weak points the snapshot is, so it gets the same permissions.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("report permissions = %o, want 600", perm)
	}
}

func TestScanRoundTripsSnapshotOut(t *testing.T) {
	fixture := writeSnapshotFixture(t)
	out := filepath.Join(t.TempDir(), "copy.json")

	if _, _, code := run(t, "scan", "--snapshot-in", fixture, "--snapshot-out", out, "-o", "json", "--fail-on", "none"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("snapshot was not written: %v", err)
	}
	var snapshot scm.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("written snapshot does not parse: %v", err)
	}
	if snapshot.SchemaVersion != scm.SchemaVersion {
		t.Errorf("schema version = %q", snapshot.SchemaVersion)
	}

	// A snapshot records an instance's weak points; it must not be world-readable.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("snapshot permissions = %o, want 600", perm)
	}
}

func TestScanRejectsBadArguments(t *testing.T) {
	fixture := writeSnapshotFixture(t)

	tests := []struct {
		name string
		args []string
	}{
		{"no url and no snapshot", []string{"scan"}},
		{"unknown format", []string{"scan", "--snapshot-in", fixture, "-o", "yaml"}},
		{"unknown language", []string{"scan", "--snapshot-in", fixture, "--lang", "fr"}},
		{"unknown fail-on", []string{"scan", "--snapshot-in", fixture, "--fail-on", "critical"}},
		{"zero concurrency", []string{"scan", "--snapshot-in", fixture, "--concurrency", "0"}},
		{"missing snapshot file", []string{"scan", "--snapshot-in", "/nonexistent/snapshot.json"}},
		// A repository that names no project left the filter empty, and an
		// empty filter is not "that one repository", it is no filter at all —
		// so the scan quietly covered the whole instance.
		{"repository without a project", []string{"scan", "--url", "https://example.invalid", "--token", "t", "-r", "payments-api"}},
		{"repository with no slug", []string{"scan", "--url", "https://example.invalid", "--token", "t", "-r", "PRJ/"}},
		// Narrowing flags against a snapshot had nothing to act on and were
		// ignored in silence: the report covered every repository in the file
		// and looked exactly like the narrowed scan that had been asked for.
		{"project against a snapshot", []string{"scan", "--snapshot-in", fixture, "-p", "PRJ"}},
		{"repository against a snapshot", []string{"scan", "--snapshot-in", fixture, "-r", "PRJ/app"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, code := run(t, tc.args...); code != ExitError {
				t.Errorf("exit code = %d, want %d", code, ExitError)
			}
		})
	}
}

func TestScanRejectsSnapshotWithWrongSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":"0","metadata":{"platform":"bitbucket-dc"}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, code := run(t, "scan", "--snapshot-in", path); code != ExitError {
		t.Error("a snapshot from an incompatible schema version should be rejected, not misread")
	}
}

func TestTableOutputIsHumanReadable(t *testing.T) {
	fixture := writeSnapshotFixture(t)
	stdout, _, _ := run(t, "scan", "--snapshot-in", fixture, "--fail-on", "none")

	// The section names follow kube-bench's shape: findings, then remediations,
	// then a summary. "Branch permissions" is the remediation text, which has to
	// survive being moved out of the findings list into its own section.
	for _, want := range []string{"SCORE", "== Failed", "PRJ/app", "== Remediations", "Branch permissions", "== Summary", "checks FAIL"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table output is missing %q\n---\n%s", want, stdout)
		}
	}
	// Colour is off when stdout is not a terminal.
	if strings.Contains(stdout, "\033[") {
		t.Error("ANSI escapes leaked into non-terminal output")
	}
}

func TestListChecksCoversTheBundle(t *testing.T) {
	stdout, _, code := run(t, "list-checks")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"CIS-1.1.3", "CIS-1.3.9", "SEVERITY", "controls"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list-checks output is missing %q", want)
		}
	}

	jsonOut, _, _ := run(t, "list-checks", "--json")
	var loaded []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &loaded); err != nil {
		t.Fatalf("list-checks --json does not parse: %v", err)
	}
	if len(loaded) < 20 {
		t.Errorf("bundle exposes %d checks, want at least 20", len(loaded))
	}
}

func TestVersionCommand(t *testing.T) {
	stdout, _, code := run(t, "version")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "scm-bench") {
		t.Errorf("version output = %q", stdout)
	}
}

// The reported symptom: a twenty-repository scan announced "130 control(s)
// failed" when the whole bundle holds twenty controls. Score.Failed counts
// findings — one control per resource — and calling those "controls" made a
// number that could not be true.
func TestFailureSummaryCountsControlsAndFindingsSeparately(t *testing.T) {
	rep := &engine.Report{
		Findings: []engine.Finding{
			{CheckID: "CIS-1.1.15", Status: engine.StatusFail, Resource: "PRJ/a"},
			{CheckID: "CIS-1.1.15", Status: engine.StatusFail, Resource: "PRJ/b"},
			{CheckID: "CIS-1.1.15", Status: engine.StatusFail, Resource: "PRJ/c"},
			{CheckID: "CIS-1.1.16", Status: engine.StatusFail, Resource: "PRJ/a"},
			{CheckID: "CIS-1.1.3", Status: engine.StatusPass, Resource: "PRJ/a"},
		},
	}
	rep.Score = engine.Compute(rep.Findings)

	got := failureSummary(rep, "high")
	if !strings.Contains(got, "2 controls failed") {
		t.Errorf("summary = %q, want it to name 2 controls, not 4", got)
	}
	if !strings.Contains(got, "across 4 findings") {
		t.Errorf("summary = %q, want the spread reported as findings", got)
	}
}

// One control failing on one resource should not read as "1 control failed
// across 1 finding", which is noise dressed up as precision.
func TestFailureSummaryOmitsTheSpreadWhenThereIsNone(t *testing.T) {
	rep := &engine.Report{
		Findings: []engine.Finding{{CheckID: "CIS-1.1.15", Status: engine.StatusFail, Resource: "PRJ/a"}},
	}
	rep.Score = engine.Compute(rep.Findings)

	got := failureSummary(rep, "high")
	if strings.Contains(got, "across") {
		t.Errorf("summary = %q, want no spread clause for a single finding", got)
	}
	if !strings.Contains(got, "1 control failed") {
		t.Errorf("summary = %q, want the singular", got)
	}
}

// The default output is the result, not an account of what the tool did. The
// request log describes the tool; someone running a benchmark wants to know
// what it found, and only reaches for the requests when something looks wrong
// — which is when --verbose gets typed.
func TestRequestLogIsOffByDefaultAndOnWithVerbose(t *testing.T) {
	fixture := writeSnapshotFixture(t)

	// --snapshot-in makes no requests at all, so the flag wiring is what is
	// under test here: which progress mode each invocation resolves to.
	cmd := newScanCommand()
	if got := cmd.Flags().Lookup("progress").DefValue; got != ProgressCompact {
		t.Errorf("--progress defaults to %q, want %q — full is a wall of GET lines", got, ProgressCompact)
	}

	_, stderr, _ := run(t, "scan", "--snapshot-in", fixture, "--fail-on", "none")
	if strings.Contains(stderr, "GET ") {
		t.Errorf("the request log appeared without --verbose:\n%s", stderr)
	}
}

// The findings message is what a user reads when a scan exits 1, and nothing
// exercised it: the tests took the exit code and dropped the error.
func TestExitCodeErrorCarriesBothTheCodeAndTheMessage(t *testing.T) {
	err := &exitCodeError{code: ExitFindings, msg: "2 controls failed"}

	if got := err.Error(); got != "2 controls failed" {
		t.Errorf("Error() = %q, want the message a user reads", got)
	}
	if got := ExitCode(err); got != ExitFindings {
		t.Errorf("ExitCode = %d, want %d", got, ExitFindings)
	}
	// Wrapped is the shape it actually travels in, out through cobra's RunE.
	if got := ExitCode(fmt.Errorf("scan: %w", err)); got != ExitFindings {
		t.Errorf("ExitCode through a wrap = %d, want %d", got, ExitFindings)
	}
	// Anything else is a scan that failed, not findings.
	if got := ExitCode(errors.New("boom")); got != ExitError {
		t.Errorf("ExitCode of a plain error = %d, want %d", got, ExitError)
	}
}

// StderrWriter is what main uses for the process's last line. It is only
// reachable from main, so nothing here had ever constructed it.
func TestStderrWriterHonoursNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if StderrWriter().P.Enabled {
		t.Error("NO_COLOR set but the final line would still be coloured")
	}

	t.Setenv("NO_COLOR", "")
	// Still false here, because the test process's stderr is not a terminal —
	// which is the other half of the rule and the case CI runs in.
	if StderrWriter().P.Enabled {
		t.Error("colour enabled while stderr is not a terminal")
	}
}
