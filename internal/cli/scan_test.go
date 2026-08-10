package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// writeSnapshotFixture saves a snapshot with one badly configured repository,
// so the CLI has something with a known verdict to report on.
func writeSnapshotFixture(t *testing.T) string {
	t.Helper()
	return writeSnapshotWith(t, nil)
}

// writeSnapshotWith writes the fixture with an optional mutation applied, so a test
// that needs one field different does not have to restate the whole instance.
func writeSnapshotWith(t *testing.T, mutate func(*scm.Snapshot)) string {
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

	if mutate != nil {
		mutate(&snapshot)
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
	assertOwnerOnly(t, out, "report")
}

// assertOwnerOnly checks that a file the tool wrote is readable by its owner
// alone.
//
// It is a no-op on Windows, and that is a statement about the platform rather
// than a way of getting the test to pass. Windows has no Unix permission bits:
// Go's os package maps the mode argument to the read-only attribute and
// nothing else, so a file created with 0600 reports 0666 and its real access
// control comes from the ACL it inherits from its directory. Restricting it
// properly means writing a Windows ACL through golang.org/x/sys/windows, which
// is a feature this has not implemented — so the honest thing is to skip the
// assertion here and say so in the documentation, rather than assert something
// weaker and let the promise look kept.
func assertOwnerOnly(t *testing.T, path, what string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", what, err)
	}
	if runtime.GOOS == "windows" {
		t.Logf("%s permissions are not enforced on Windows (got %o); see the note in the README",
			what, info.Mode().Perm())
		return
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s permissions = %o, want 600", what, perm)
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
	assertOwnerOnly(t, out, "snapshot")
}

func TestScanRejectsBadArguments(t *testing.T) {
	fixture := writeSnapshotFixture(t)

	tests := []struct {
		name string
		args []string
	}{
		{"no url and no snapshot", []string{"scan"}},
		{"unknown format", []string{"scan", "--snapshot-in", fixture, "-o", "yaml"}},
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
	for _, want := range []string{"SCORE", "== Failed", "PRJ/app", "== Remediations", "Branch permissions", "== Summary", "findings FAIL"} {
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

// MANUAL is excluded from both sides of the score, which is right control by
// control — an instance should not be marked down for a question its API
// cannot answer — and perverse in aggregate, because it shrinks the
// denominator. The fixture below makes the point: with nothing readable, three
// controls stay decidable, all three pass, and a scan that saw almost nothing
// reports a perfect 100 and exits 0.
//
// Note which threshold catches it. --fail-under cannot: the score is 100.
// Only --max-manual asks the question that matters here, which is not "is the
// score good" but "did the scan see enough for the score to mean anything".
func TestScanThresholds(t *testing.T) {
	normal := writeSnapshotFixture(t)
	blind := writeSnapshotWith(t, func(s *scm.Snapshot) {
		s.Organization.Available = map[string]bool{}
		for i := range s.Projects {
			for j := range s.Projects[i].Repositories {
				s.Projects[i].Repositories[j].Available = map[string]bool{}
			}
		}
	})

	// Stated rather than assumed, because every expectation below rests on it.
	if got := scoreOf(t, blind); got != 100 {
		t.Fatalf("blind fixture scores %d, want 100; the rest of this test assumes it", got)
	}
	if got := scoreOf(t, normal); got != 30 {
		t.Fatalf("readable fixture scores %d, want 30", got)
	}

	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"failures trip fail-on", []string{"scan", "--snapshot-in", normal}, ExitFindings},
		{"fail-on none clears them", []string{"scan", "--snapshot-in", normal, "--fail-on", "none"}, ExitOK},
		{"score below fail-under", []string{"scan", "--snapshot-in", normal, "--fail-on", "none", "--fail-under", "50"}, ExitFindings},
		{"score above fail-under", []string{"scan", "--snapshot-in", normal, "--fail-on", "none", "--fail-under", "20"}, ExitOK},

		// The pathology, and the only thing that catches it.
		{"blind scan passes by default", []string{"scan", "--snapshot-in", blind}, ExitOK},
		{"fail-under cannot catch a blind scan", []string{"scan", "--snapshot-in", blind, "--fail-under", "100"}, ExitOK},
		{"max-manual catches it", []string{"scan", "--snapshot-in", blind, "--max-manual", "50"}, ExitFindings},
		{"max-manual generous enough", []string{"scan", "--snapshot-in", blind, "--max-manual", "90"}, ExitOK},

		{"fail-under out of range", []string{"scan", "--snapshot-in", normal, "--fail-under", "101"}, ExitError},
		{"max-manual out of range", []string{"scan", "--snapshot-in", normal, "--max-manual", "-2"}, ExitError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, code := run(t, tc.args...); code != tc.want {
				t.Errorf("exit code = %d, want %d", code, tc.want)
			}
		})
	}
}

func scoreOf(t *testing.T, snapshotPath string) int {
	t.Helper()
	out, _, _ := run(t, "scan", "--snapshot-in", snapshotPath, "-o", "json", "--fail-on", "none")
	var rep struct {
		Score struct {
			Value int `json:"value"`
		} `json:"score"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	return rep.Score.Value
}

// A policy that could not run is a broken tool, not a finding about the
// instance. It degrades to MANUAL so the rest of the report survives, but
// MANUAL leaves the score's denominator — so a bundle that failed to evaluate
// raises the score and used to exit 0. A green pipeline is the one thing that
// must not come out of a scan that did not work.
func TestPolicyErrorsFailTheScan(t *testing.T) {
	rep := &engine.Report{
		Score:  engine.Score{Value: 100, Passed: 1},
		Errors: []string{"CIS-1.1.15 on PRJ/app: policy produced no result"},
	}
	err := exitStatus(rep, &scanOptions{failOn: "high", maxManual: -1})
	if err == nil {
		t.Fatal("exitStatus returned nil for a report carrying policy errors")
	}
	if code := ExitCode(err); code != ExitError {
		t.Errorf("exit code = %d, want %d", code, ExitError)
	}
	if !strings.Contains(err.Error(), "policy produced no result") {
		t.Errorf("error does not carry the reason: %v", err)
	}
}

// The credential flags default to the environment, so an exported
// BITBUCKET_TOKEN filled --token in before the command line was read — and the
// client prefers a token over basic auth. Someone with a stale token in their
// shell profile who typed --username and --password was authenticated with the
// token they never mentioned, then told "the instance rejected the
// credentials", which sent them to check the password they had just typed.
func TestExplicitCredentialsBeatInheritedOnes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		env          map[string]string
		args         []string
		wantToken    string
		wantUsername string
	}{
		{
			name:         "typed basic auth beats an inherited token",
			env:          map[string]string{"BITBUCKET_TOKEN": "stale"},
			args:         []string{"--username", "alice", "--password", "pw"},
			wantToken:    "",
			wantUsername: "alice",
		},
		{
			name:         "typed token beats an inherited username",
			env:          map[string]string{"BITBUCKET_USERNAME": "leftover"},
			args:         []string{"--token", "fresh"},
			wantToken:    "fresh",
			wantUsername: "",
		},
		{
			name:         "an inherited token is still used when nothing is typed",
			env:          map[string]string{"BITBUCKET_TOKEN": "inherited"},
			args:         nil,
			wantToken:    "inherited",
			wantUsername: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			// Built after the environment is set, because the flag defaults are
			// read from it at construction — which is the whole mechanism.
			var got *scanOptions
			cmd := newScanCommand()
			cmd.RunE = func(c *cobra.Command, _ []string) error {
				opts := scanOptionsFrom(t, c)
				resolveCredentials(c, opts)
				got = opts
				return nil
			}
			cmd.SetArgs(tc.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}

			if got.token != tc.wantToken {
				t.Errorf("token = %q, want %q", got.token, tc.wantToken)
			}
			if got.username != tc.wantUsername {
				t.Errorf("username = %q, want %q", got.username, tc.wantUsername)
			}
		})
	}
}

// scanOptionsFrom rebuilds the options from the parsed flags, so the test reads
// what the command actually bound rather than reaching into its closure.
func scanOptionsFrom(t *testing.T, cmd *cobra.Command) *scanOptions {
	t.Helper()
	get := func(name string) string {
		v, err := cmd.Flags().GetString(name)
		if err != nil {
			t.Fatalf("read --%s: %v", name, err)
		}
		return v
	}
	return &scanOptions{token: get("token"), username: get("username"), password: get("password")}
}

// Both typed out explicitly is a question, not something to settle by
// precedence: only one would be used and the user cannot tell which.
func TestBothCredentialKindsTypedIsRejected(t *testing.T) {
	if _, _, code := run(t, "scan", "--url", "https://example.invalid", "--token", "t", "--username", "alice"); code != ExitError {
		t.Errorf("exit code = %d, want %d", code, ExitError)
	}
}
