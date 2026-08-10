package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// The table renderer wraps to console.Width, which reads COLUMNS. Pinning it
// keeps these assertions from depending on the width of whatever terminal the
// suite happens to run under; a test that cares about a different width sets it
// with t.Setenv.
func TestMain(m *testing.M) {
	os.Setenv("COLUMNS", "80")
	os.Exit(m.Run())
}

func sampleReport() *engine.Report {
	findings := []engine.Finding{
		{
			CheckID: "CIS-1.1.15", CISID: "1.1.15", Severity: "HIGH", Status: engine.StatusFail,
			Title:    "Ensure pushing is restricted",
			Resource: "PRJ/app", ResourceType: engine.ResourceRepository,
			Description: "Direct pushes bypass review entirely.",
			Details:     "Anyone with write access can push directly to main.",
			Evidence:    []string{"no restriction covers main"},
			Remediation: "Repository settings -> Branch permissions -> Add restriction",
			FixSummary:  "Enable Prevent changes without a pull request.",
			References:  []string{"https://example.invalid/cis"},
			Automated:   true,
		},
		{
			CheckID: "CIS-1.3.5", CISID: "1.3.5", Severity: "HIGH", Status: engine.StatusManual,
			Title: "Ensure MFA is enforced", Resource: engine.InstanceResourceName,
			ResourceType: engine.ResourceOrganization,
			Details:      "MFA is enforced by the identity provider.",
			Remediation:  "Enforce MFA at the IdP",
		},
		{
			CheckID: "CIS-1.1.3", CISID: "1.1.3", Severity: "HIGH", Status: engine.StatusPass,
			Title: "Ensure two approvals", Resource: "PRJ/app", ResourceType: engine.ResourceRepository,
			Details: "Pull requests require 2 approvals.", Remediation: "n/a",
			// Carried even on a pass, because metadata belongs to the control
			// rather than the verdict. The renderer is what decides not to show
			// it, which is what TestTableShowPassedIncludesPassingControls
			// checks.
			FixSummary: "Set Minimum approvals to 2 at Repository settings.",
			Automated:  true,
		},
	}

	return &engine.Report{
		Metadata: scm.Metadata{
			Tool: "scm-bench", ToolVersion: "1.2.3", Platform: scm.PlatformBitbucketDC,
			BaseURL: "https://bitbucket.example.com", GeneratedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			Warnings: []string{"the user directory is not readable"},
		},
		Findings: findings,
		Score:    engine.Compute(findings),
	}
}

func render(t *testing.T, opts Options) string {
	t.Helper()
	return renderReport(t, sampleReport(), opts)
}

// reportWithUnreadableResource models the case the Not evaluated section exists
// for: one repository the token could not read, costing n controls a verdict.
// They are MANUAL and Automated, which is what separates "this run came up
// short" from "no API can answer this".
func reportWithUnreadableResource(t *testing.T, ids ...string) *engine.Report {
	t.Helper()

	findings := []engine.Finding{{
		CheckID: "CIS-1.3.5", CISID: "1.3.5", Severity: "HIGH", Status: engine.StatusManual,
		Title: "Ensure MFA is enforced", Resource: engine.InstanceResourceName,
		ResourceType: engine.ResourceOrganization,
		Details:      "MFA is enforced by the identity provider.",
		Remediation:  "Enforce MFA at the IdP -> Authentication",
		Automated:    false,
	}}
	for _, id := range ids {
		findings = append(findings, engine.Finding{
			CheckID: "CIS-" + id, CISID: id, Severity: "HIGH", Status: engine.StatusManual,
			Title: "Ensure " + id, Resource: "PRJ/locked", ResourceType: engine.ResourceRepository,
			Details:     "Branch permissions could not be read.",
			Remediation: "Repository settings -> Branch permissions -> " + id,
			Automated:   true,
		})
	}

	return &engine.Report{
		Metadata: scm.Metadata{Tool: "scm-bench", Platform: scm.PlatformBitbucketDC},
		Findings: findings,
		Score:    engine.Compute(findings),
	}
}

func renderReport(t *testing.T, rep *engine.Report, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, rep, opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.String()
}

// reportWithRepeatedFinding models the case the grouping exists for: one
// control failing identically across n repositories.
func reportWithRepeatedFinding(t *testing.T, n int) *engine.Report {
	t.Helper()

	findings := make([]engine.Finding, 0, n)
	for i := range n {
		findings = append(findings, engine.Finding{
			CheckID: "CIS-1.1.15", CISID: "1.1.15", Severity: "HIGH", Status: engine.StatusFail,
			Title:        "Ensure pushing is restricted",
			Resource:     fmt.Sprintf("PRJ/repo-%02d", i),
			ResourceType: engine.ResourceRepository,
			Details:      "Anyone with write access can push directly to main.",
			Evidence:     []string{"no restriction covers main"},
			Remediation:  "Repository settings -> Branch permissions",
			Automated:    true,
		})
	}

	return &engine.Report{
		Metadata: scm.Metadata{Tool: "scm-bench", Platform: scm.PlatformBitbucketDC},
		Findings: findings,
		Score:    engine.Compute(findings),
	}
}

func TestTableIncludesFindingsRemediationAndWarnings(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	for _, want := range []string{
		"CIS-1.1.15",
		"PRJ/app",
		"Anyone with write access can push directly to main.",
		"Repository settings -> Branch permissions -> Add restriction",
		"== Needs manual review",
		"== Scan warnings",
		"the user directory is not readable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output is missing %q\n---\n%s", want, out)
		}
	}

	// Passing controls are summarised but not listed unless asked for.
	if strings.Contains(out, "Ensure two approvals") {
		t.Error("passing controls should be hidden without --show-passed")
	}
	if !strings.Contains(out, "1 passed") {
		t.Errorf("the summary should still count the passing control\n---\n%s", out)
	}
}

// The summary is the first thing on the page, not the last.
//
// A CI log is read from its end, but a terminal is read from its top, and a
// score that arrived after four screens of findings answered "how bad is this"
// long after the reader had started guessing. The scan warnings come next
// because they say how much of the report to believe.
func TestSummaryAndWarningsComeBeforeTheFindings(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	score := strings.Index(out, "SCORE")
	warnings := strings.Index(out, "== Scan warnings")
	failed := strings.Index(out, "== Failed")
	remediations := strings.Index(out, "== Remediations")

	for _, step := range []struct {
		name       string
		before, at int
	}{
		{"SCORE before the scan warnings", score, warnings},
		{"scan warnings before the findings", warnings, failed},
		{"findings before the remediations", failed, remediations},
	} {
		if step.before < 0 || step.at < 0 {
			t.Fatalf("%s: a section is missing entirely\n---\n%s", step.name, out)
		}
		if step.before > step.at {
			t.Errorf("%s: order is wrong\n---\n%s", step.name, out)
		}
	}
}

// One unreadable repository used to produce one entry per control, each with
// its own heading and its own remediation paragraph, and every one of them was
// the same 403. Named once, it is a few lines and points at the thing to fix.
func TestUnevaluatedControlsCollapseByResource(t *testing.T) {
	ids := []string{"1.1.3", "1.1.4", "1.1.9", "1.1.15", "1.1.16", "1.1.17", "1.2.1"}
	out := renderReport(t, reportWithUnreadableResource(t, ids...), Options{Format: FormatTable})

	if !strings.Contains(out, fmt.Sprintf("== Not evaluated (%d)", len(ids))) {
		t.Errorf("no Not evaluated section\n---\n%s", out)
	}
	if n := strings.Count(out, "PRJ/locked"); n != 1 {
		t.Errorf("the resource is named %d times, want once\n---\n%s", n, out)
	}
	// Every control is named. A truncated list would leave the reader unable to
	// tell whether the ones they care about are among them.
	for _, id := range ids {
		if !strings.Contains(out, "CIS-"+id) {
			t.Errorf("control CIS-%s went unlisted\n---\n%s", id, out)
		}
	}
	if strings.Contains(out, "and 2 more") {
		t.Errorf("the control list was summarised\n---\n%s", out)
	}

	// The control no API can answer is a different question and keeps its own
	// section, with its per-control heading intact.
	manual := strings.Index(out, "== Needs manual review (1)")
	if manual < 0 {
		t.Errorf("the non-automated control lost its section\n---\n%s", out)
	}
	if !strings.Contains(out[manual:], "CIS-1.3.5") {
		t.Errorf("CIS-1.3.5 is not under Needs manual review\n---\n%s", out)
	}
}

// A control the scan never saw is not known to be misconfigured. Printing how
// to change its settings would say the opposite; what it needs is access, and
// that is what the Not evaluated section asks for.
func TestUnevaluatedControlsAreLeftOutOfTheRemediations(t *testing.T) {
	out := renderReport(t, reportWithUnreadableResource(t, "1.1.15", "1.1.16"), Options{Format: FormatTable})

	remediations := strings.Index(out, "== Remediations")
	if remediations < 0 {
		t.Fatalf("no remediations section\n---\n%s", out)
	}
	tail := out[remediations:]
	for _, unwanted := range []string{"Branch permissions -> 1.1.15", "Branch permissions -> 1.1.16"} {
		if strings.Contains(tail, unwanted) {
			t.Errorf("an unevaluated control was given a remediation: %q\n---\n%s", unwanted, tail)
		}
	}
	if !strings.Contains(tail, "Enforce MFA at the IdP") {
		t.Errorf("the control that does need a person lost its remediation\n---\n%s", tail)
	}
	if !strings.Contains(out, "== Remediations (1)") {
		t.Errorf("remediations should count only the controls that have one\n---\n%s", out)
	}
}

// The one-line fix rides with the verdict so the reader can act without
// scrolling; the paragraph stays in its own section so the list stays a list.
func TestFixSummaryRidesWithTheVerdictAndTheParagraphDoesNot(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	failed := strings.Index(out, "== Failed")
	remediations := strings.Index(out, "== Remediations")
	if failed < 0 || remediations < 0 {
		t.Fatalf("sections missing\n---\n%s", out)
	}
	findings, fixes := out[failed:remediations], out[remediations:]

	if !strings.Contains(findings, "fix: Enable Prevent changes without a pull request.") {
		t.Errorf("the one-line fix is not beside the verdict\n---\n%s", findings)
	}
	if strings.Contains(findings, "Add restriction") {
		t.Errorf("the full remediation leaked into the findings list\n---\n%s", findings)
	}
	if !strings.Contains(fixes, "Add restriction") {
		t.Errorf("the full remediation is missing from its section\n---\n%s", fixes)
	}
}

// Grouping by control is right for deciding what is wrong and wrong for
// deciding who fixes it. This line is the tally the reader would otherwise
// keep by hand.
func TestMostAffectedNamesAResourceOnlyWhenFailuresConcentrate(t *testing.T) {
	// Four repositories with one failure each is not a pattern, and naming one
	// of them would invent a culprit.
	out := renderReport(t, reportWithRepeatedFinding(t, 4), Options{Format: FormatTable})
	if strings.Contains(out, "most affected") {
		t.Errorf("a culprit was named when the failures were spread evenly\n---\n%s", out)
	}

	rep := sampleReport()
	rep.Findings = append(rep.Findings, engine.Finding{
		CheckID: "CIS-1.1.16", CISID: "1.1.16", Severity: "HIGH", Status: engine.StatusFail,
		Title: "Ensure force pushing is denied", Resource: "PRJ/app",
		ResourceType: engine.ResourceRepository, Details: "main can be force pushed.",
		Remediation: "Repository settings -> Branch permissions", Automated: true,
	})
	rep.Score = engine.Compute(rep.Findings)
	if got := renderReport(t, rep, Options{Format: FormatTable}); !strings.Contains(got, "most affected: PRJ/app (2 failures)") {
		t.Errorf("two failures on one resource should be named\n---\n%s", got)
	}
}

// A left edge that moves is one the eye has to find again on every line.
func TestResourceColumnIsAlignedAcrossASection(t *testing.T) {
	rep := sampleReport()
	rep.Findings = append(rep.Findings, engine.Finding{
		CheckID: "CIS-1.3.1", CISID: "1.3.1", Severity: "MEDIUM", Status: engine.StatusFail,
		Title: "Ensure inactive users are removed", Resource: engine.InstanceResourceName,
		ResourceType: engine.ResourceOrganization, Details: "One account is dormant.",
		Remediation: "Administration -> Users", Automated: true,
	})
	rep.Score = engine.Compute(rep.Findings)
	out := renderReport(t, rep, Options{Format: FormatTable})

	// "PRJ/app" and "instance" differ in length; their verdicts must still
	// start in the same column.
	var columns []int
	for _, line := range strings.Split(out, "\n") {
		for _, name := range []string{"PRJ/app", "instance"} {
			if i := strings.Index(line, name+" "); i > 0 && strings.HasPrefix(line, "[INFO]     ") {
				columns = append(columns, i+len(name)+countSpaces(line[i+len(name):]))
			}
		}
	}
	if len(columns) < 2 {
		t.Fatalf("expected both resources to be rendered inline\n---\n%s", out)
	}
	for _, c := range columns[1:] {
		if c != columns[0] {
			t.Errorf("verdict text starts at columns %v, want one column\n---\n%s", columns, out)
			break
		}
	}
}

func countSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' {
			break
		}
		n++
	}
	return n
}

// Nothing may run past the width, at any width, whatever the escape sequences
// would have measured.
func TestEveryLineFitsTheTerminalWidth(t *testing.T) {
	for _, columns := range []string{"60", "80", "100", "300"} {
		t.Setenv("COLUMNS", columns)
		limit := min(max(atoi(t, columns), 60), 100)

		for _, colour := range []bool{false, true} {
			out := renderReport(t, reportWithUnreadableResource(t, "1.1.3", "1.1.15", "1.1.16"),
				Options{Format: FormatTable, Color: colour})
			for _, line := range strings.Split(out, "\n") {
				if n := utf8.RuneCountInString(stripANSI(line)); n > limit {
					t.Errorf("COLUMNS=%s colour=%v: %d columns: %q", columns, colour, n, line)
				}
			}
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestTableShowPassedIncludesPassingControls(t *testing.T) {
	out := render(t, Options{Format: FormatTable, ShowPassed: true})
	if !strings.Contains(out, "Ensure two approvals") {
		t.Error("--show-passed should list passing controls")
	}

	// Not with a fix under them. Telling someone how to change a setting that
	// is already right reads as an instruction to go and break it.
	passed := strings.Index(out, "== Passed")
	if passed < 0 {
		t.Fatalf("no Passed section\n---\n%s", out)
	}
	tail := out[passed:]
	if next := strings.Index(tail, "\n[INFO] == "); next > 0 {
		tail = tail[:next]
	}
	if strings.Contains(tail, "fix:") {
		t.Errorf("a passing control was given a fix\n---\n%s", tail)
	}
}

// The same misconfiguration across many repositories has to read as one
// problem. Grouping by control alone did not achieve that: the verdict was
// printed once per repository with only the name changing.
func TestTableCollapsesRepeatedVerdicts(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 20)
	out := renderReport(t, rep, Options{Format: FormatTable, MaxResources: DefaultMaxResources})

	if n := strings.Count(out, "Anyone with write access can push directly to main."); n != 1 {
		t.Errorf("the shared verdict was printed %d times, want once\n---\n%s", n, out)
	}
	if !strings.Contains(out, "20 repositories:") {
		t.Errorf("the affected count is missing\n---\n%s", out)
	}
	if !strings.Contains(out, "and 15 more") {
		t.Errorf("the tail was not summarised\n---\n%s", out)
	}
	// Evidence is part of the shared verdict and must not repeat either.
	if n := strings.Count(out, "no restriction covers main"); n != 1 {
		t.Errorf("evidence was printed %d times, want once", n)
	}
}

// Distinct outcomes are different problems and must not be merged just because
// they belong to the same control.
func TestTableKeepsDistinctVerdictsApart(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 4)
	rep.Findings[2].Details = "Branch permissions could not be read."
	rep.Findings[2].Evidence = nil

	out := renderReport(t, rep, Options{Format: FormatTable, MaxResources: DefaultMaxResources})

	if !strings.Contains(out, "Branch permissions could not be read.") {
		t.Errorf("a differing verdict was swallowed\n---\n%s", out)
	}
	if !strings.Contains(out, "3 repositories:") {
		t.Errorf("the remaining three should still be grouped\n---\n%s", out)
	}
}

// A single resource stays on one line with its verdict: that is the common
// case on a small instance and splitting it over two lines reads worse.
func TestTableKeepsSingleResourceInline(t *testing.T) {
	out := render(t, Options{Format: FormatTable})
	if !strings.Contains(out, "PRJ/app  Anyone with write access can push directly to main.") {
		t.Errorf("a lone resource should share the line with its verdict\n---\n%s", out)
	}
	if strings.Contains(out, "1 repositories") {
		t.Error("a single resource should not be rendered as a count")
	}
}

func TestMaxResourcesZeroListsEveryResource(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 20)
	out := renderReport(t, rep, Options{Format: FormatTable, MaxResources: 0})

	if strings.Contains(out, "more") {
		t.Errorf("--max-resources 0 should summarise nothing\n---\n%s", out)
	}
	if !strings.Contains(out, "PRJ/repo-19") {
		t.Errorf("the last resource is missing\n---\n%s", out)
	}
}

func TestTableIsPlainWithoutColor(t *testing.T) {
	out := render(t, Options{Format: FormatTable, Color: false})
	if strings.Contains(out, "\033[") {
		t.Error("colour was disabled but ANSI escapes were emitted")
	}
}

// The tag column is a contract, not decoration: `scm-bench scan | grep
// '^\[FAIL\]'` is the obvious thing to reach for, so every line has to carry a
// tag and every tag has to be the same width.
func TestEveryTableLineStartsWithAFixedWidthTag(t *testing.T) {
	out := render(t, Options{Format: FormatTable, ShowPassed: true})

	// Four tags, the same four kube-bench uses. Adding a fifth is a decision,
	// not an accident, so this list is where it has to be made.
	known := map[string]bool{
		"[PASS]": true, "[FAIL]": true, "[WARN]": true, "[INFO]": true,
	}

	for i, line := range strings.Split(out, "\n") {
		// Blank lines separate blocks and deliberately carry no tag.
		if line == "" {
			continue
		}
		if len(line) < 6 || !known[line[:6]] {
			t.Errorf("line %d does not start with a known tag: %q", i+1, line)
		}
	}
}

// Each verdict has to reach column one, or the colour scheme is the only way
// to tell them apart — which fails the moment output is piped or NO_COLOR is
// set. MANUAL appears as NOTE, the word docker-bench uses for the same idea.
func TestVerdictsAppearInTheTagColumn(t *testing.T) {
	out := render(t, Options{Format: FormatTable, ShowPassed: true})

	for tag, want := range map[string]string{
		"[FAIL]": "CIS-1.1.15",
		// MANUAL surfaces as WARN, kube-bench's word for a control a human
		// still has to decide.
		"[WARN]": "CIS-1.3.5",
		"[PASS]": "Ensure two approvals",
	} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, tag) && strings.Contains(line, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a %s line containing %q\n---\n%s", tag, want, out)
		}
	}
}

func TestJSONRoundTrips(t *testing.T) {
	out := render(t, Options{Format: FormatJSON})

	var decoded engine.Report
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("JSON output does not parse: %v\n%s", err, out)
	}
	if len(decoded.Findings) != 3 {
		t.Errorf("decoded %d findings, want 3", len(decoded.Findings))
	}
	if decoded.Score.Value != 50 {
		t.Errorf("decoded score = %d, want 50 (one HIGH pass, one HIGH fail)", decoded.Score.Value)
	}
	// Remediation arrows must survive intact rather than becoming >.
	if !strings.Contains(out, "Branch permissions -> Add restriction") {
		t.Error("remediation text should not be HTML-escaped")
	}
}

func TestSARIFStructure(t *testing.T) {
	out := render(t, Options{Format: FormatSARIF, ToolVersion: "1.2.3"})

	var log struct {
		Schema  string `json:"$schema"`
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name    string `json:"name"`
					Version string `json:"version"`
					Rules   []struct {
						ID               string `json:"id"`
						ShortDescription struct {
							Text string `json:"text"`
						} `json:"shortDescription"`
						FullDescription struct {
							Text string `json:"text"`
						} `json:"fullDescription"`
						DefaultConfiguration struct {
							Level string `json:"level"`
						} `json:"defaultConfiguration"`
						Properties struct {
							SecuritySeverity string `json:"security-severity"`
						} `json:"properties"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					LogicalLocations []struct {
						Name string `json:"name"`
					} `json:"logicalLocations"`
				} `json:"locations"`
				PartialFingerprints map[string]string `json:"partialFingerprints"`
			} `json:"results"`
			Invocations []struct {
				ExecutionSuccessful        bool `json:"executionSuccessful"`
				ToolExecutionNotifications []struct {
					Level string `json:"level"`
				} `json:"toolExecutionNotifications"`
			} `json:"invocations"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &log); err != nil {
		t.Fatalf("SARIF output does not parse: %v\n%s", err, out)
	}

	if log.Version != "2.1.0" || log.Schema == "" {
		t.Errorf("SARIF version = %q schema = %q", log.Version, log.Schema)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("expected one run, got %d", len(log.Runs))
	}
	run := log.Runs[0]

	if run.Tool.Driver.Name != "scm-bench" || run.Tool.Driver.Version != "1.2.3" {
		t.Errorf("driver = %+v", run.Tool.Driver)
	}
	// Passing controls carry no action and are omitted; the failure and the
	// manual review remain.
	if len(run.Results) != 2 {
		t.Fatalf("expected 2 results (1 fail + 1 manual), got %d", len(run.Results))
	}

	byRule := map[string]string{}
	for _, r := range run.Results {
		byRule[r.RuleID] = r.Level
		if len(r.Locations) == 0 || len(r.Locations[0].LogicalLocations) == 0 {
			t.Errorf("result %s has no logical location", r.RuleID)
		}
		if r.PartialFingerprints["scmBenchFindingV1"] == "" {
			t.Errorf("result %s has no fingerprint", r.RuleID)
		}
		if !strings.Contains(r.Message.Text, "Remediation:") {
			t.Errorf("result %s message carries no remediation", r.RuleID)
		}
	}

	if byRule["CIS-1.1.15"] != "error" {
		t.Errorf("a HIGH failure should be level error, got %q", byRule["CIS-1.1.15"])
	}

	// SARIF keeps the two apart: shortDescription is the label in a list,
	// fullDescription is what a viewer shows when the reader wants to know what
	// the rule is about. Repeating the title in both wastes the field — and a
	// control without a description must still fall back to something.
	for _, r := range run.Tool.Driver.Rules {
		switch r.ID {
		case "CIS-1.1.15":
			if r.FullDescription.Text != "Direct pushes bypass review entirely." {
				t.Errorf("fullDescription = %q, want the control's description", r.FullDescription.Text)
			}
			if r.FullDescription.Text == r.ShortDescription.Text {
				t.Error("fullDescription merely repeats shortDescription")
			}
		case "CIS-1.3.5":
			if r.FullDescription.Text != r.ShortDescription.Text {
				t.Errorf("a control with no description should fall back to its title, got %q", r.FullDescription.Text)
			}
		}
	}
	// A control nobody could evaluate is not an assertion that something broke.
	if byRule["CIS-1.3.5"] != "note" {
		t.Errorf("a MANUAL finding should be level note, got %q", byRule["CIS-1.3.5"])
	}

	for _, rule := range run.Tool.Driver.Rules {
		if rule.Properties.SecuritySeverity == "" {
			t.Errorf("rule %s has no security-severity", rule.ID)
		}
	}

	if len(run.Invocations) != 1 || len(run.Invocations[0].ToolExecutionNotifications) != 1 {
		t.Error("scan warnings should surface as invocation notifications")
	}
}

func TestUnknownFormatIsRejected(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sampleReport(), Options{Format: "yaml"}); err == nil {
		t.Error("an unknown format should be rejected")
	}
}

// Remediation is a paragraph naming a settings path. Printed under every
// control it turned the findings list into prose you had to read past to reach
// the next verdict, so it moved to its own section — kube-bench's arrangement.
func TestRemediationLivesInItsOwnSection(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	remedy := "Repository settings -> Branch permissions -> Add restriction"
	findings, remediations, ok := strings.Cut(out, "== Remediations")
	if !ok {
		t.Fatalf("no remediation section\n---\n%s", out)
	}
	if strings.Contains(findings, remedy) {
		t.Errorf("remediation is still inline in the findings list\n---\n%s", findings)
	}
	if !strings.Contains(remediations, remedy) {
		t.Errorf("remediation missing from its own section\n---\n%s", remediations)
	}
}

// A control that failed on one repository and could not be read on another is
// still one control, so its settings path is stated once.
func TestRemediationIsListedOncePerControl(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 4)
	rep.Findings[2].Status = engine.StatusManual
	rep.Score = engine.Compute(rep.Findings)

	out := renderReport(t, rep, Options{Format: FormatTable, MaxResources: DefaultMaxResources})
	if n := strings.Count(out, "Repository settings -> Branch permissions"); n != 1 {
		t.Errorf("remediation printed %d times, want once\n---\n%s", n, out)
	}
}

func TestNoRemediationsDropsTheSection(t *testing.T) {
	out := render(t, Options{Format: FormatTable, NoRemediations: true})

	if strings.Contains(out, "== Remediations") {
		t.Errorf("--no-remediations left the section in\n---\n%s", out)
	}
	// The findings themselves are still there; only the fixes are gone.
	if !strings.Contains(out, "CIS-1.1.15") {
		t.Errorf("--no-remediations dropped the findings too\n---\n%s", out)
	}
}

// The counts are the last thing printed and the first thing read.
//
// All four states are counted, on one line, in the state names the rest of the
// tool uses.
//
// PASS/FAIL/MANUAL/NA, not the four tags: the tag column answers "what should I
// do with this line", and this line is arithmetic. Borrowing WARN for MANUAL
// here would have the summary and the report counting in two vocabularies.
func TestSummaryReportsAllFourStates(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	for _, want := range []string{"1 passed", "1 failed", "1 manual", "0 n/a"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q\n---\n%s", want, out)
		}
	}
	// One line, not kube-bench's four.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "SCORE") {
			if !strings.Contains(line, "1 passed") || !strings.Contains(line, "0 n/a") {
				t.Errorf("the counts should sit beside the score, got %q", line)
			}
			return
		}
	}
	t.Errorf("no SCORE line\n---\n%s", out)
}

// SARIF types run.results as an array, and a nil Go slice marshals to null.
// The case that reaches it is the good one — an instance where nothing failed
// and nothing needed a human — so the failure mode was that a clean scan
// produced a file GitHub's SARIF upload rejects, while every dirty scan worked.
func TestSARIFResultsIsAnArrayWhenThereIsNothingToReport(t *testing.T) {
	rep := &engine.Report{
		Metadata: scm.Metadata{Tool: "scm-bench", Platform: scm.PlatformBitbucketDC},
		Findings: []engine.Finding{{
			CheckID: "CIS-1.3.9", CISID: "1.3.9", Title: "Ensure the organization is verified",
			Severity: "LOW", Status: engine.StatusNA,
			Resource: engine.InstanceResourceName, ResourceType: engine.ResourceOrganization,
			Details: "Not applicable.",
		}},
	}
	out := renderReport(t, rep, Options{Format: FormatSARIF, ToolVersion: "1.2.3"})

	if strings.Contains(out, `"results": null`) {
		t.Fatalf("run.results serialised as null, which is not a SARIF array:\n%s", out)
	}

	// Asserted through the raw JSON rather than a typed struct, because
	// unmarshalling turns both null and [] into the same nil slice and would
	// pass either way.
	var log struct {
		Runs []struct {
			Results *[]json.RawMessage `json:"results"`
			Tool    struct {
				Driver struct {
					Rules *[]json.RawMessage `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &log); err != nil {
		t.Fatalf("unmarshal SARIF: %v", err)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(log.Runs))
	}
	if log.Runs[0].Results == nil {
		t.Error("run.results is null, want []")
	} else if len(*log.Runs[0].Results) != 0 {
		t.Errorf("run.results has %d entries, want 0", len(*log.Runs[0].Results))
	}
	if log.Runs[0].Tool.Driver.Rules == nil {
		t.Error("tool.driver.rules is null, want []")
	}
}

// The score excludes MANUAL from both sides, so a scan that could see very
// little scores high off a tiny denominator — 100/100 is reachable from three
// decided controls. The arithmetic is right; what was missing was any line
// saying how small the sample was.
func TestSummaryStatesHowMuchWasActuallyScored(t *testing.T) {
	out := render(t, Options{Format: FormatTable, ToolVersion: "1.2.3"})
	if !strings.Contains(out, "could not be evaluated") {
		t.Errorf("summary does not report evaluation coverage:\n%s", out)
	}

	// With nothing unevaluated there is nothing to caveat, so the line is
	// absent rather than reading "0 could not be evaluated".
	rep := &engine.Report{
		Metadata: scm.Metadata{Tool: "scm-bench", Platform: scm.PlatformBitbucketDC},
		Findings: []engine.Finding{{
			CheckID: "CIS-1.1.15", CISID: "1.1.15", Title: "t", Severity: "HIGH",
			Status: engine.StatusPass, Resource: "PRJ/app", ResourceType: engine.ResourceRepository,
			Details: "fine",
		}},
	}
	rep.Score = engine.Compute(rep.Findings)
	if got := renderReport(t, rep, Options{Format: FormatTable}); strings.Contains(got, "could not be evaluated") {
		t.Errorf("coverage line shown when everything was evaluated:\n%s", got)
	}
}

// GitHub takes an alert's displayed severity from the rule's security-severity,
// not from the result's level. A control that can only ever report MANUAL —
// CIS-1.3.5, where multi-factor authentication is enforced somewhere Bitbucket
// cannot be asked about it — therefore arrived in the Security panel as an 8.0
// High alert asserting a setting was broken, while `--fail-on high` locally did
// not fail on it at all. Two severities for one finding, and the louder one was
// the wrong one.
func TestSARIFRuleSeverityFollowsWhetherAnythingActuallyFailed(t *testing.T) {
	manualOnly := engine.Finding{
		CheckID: "CIS-1.3.5", CISID: "1.3.5", Title: "MFA", Severity: "HIGH",
		Status: engine.StatusManual, Resource: engine.InstanceResourceName,
		ResourceType: engine.ResourceOrganization, Details: "ask your IdP",
	}
	failing := engine.Finding{
		CheckID: "CIS-1.1.15", CISID: "1.1.15", Title: "Restrict pushes", Severity: "HIGH",
		Status: engine.StatusFail, Resource: "PRJ/app",
		ResourceType: engine.ResourceRepository, Details: "anyone can push",
	}
	// Same control, MANUAL first: the escalation must not depend on ordering.
	mixedManualFirst := engine.Finding{
		CheckID: "CIS-1.1.15", CISID: "1.1.15", Title: "Restrict pushes", Severity: "HIGH",
		Status: engine.StatusManual, Resource: "PRJ/other",
		ResourceType: engine.ResourceRepository, Details: "cannot read",
	}

	for _, tc := range []struct {
		name             string
		findings         []engine.Finding
		rule             string
		wantLevel        string
		wantSecuritySeve string
	}{
		{"manual only stays a note", []engine.Finding{manualOnly}, "CIS-1.3.5", "note", "3.0"},
		{"a real failure keeps its severity", []engine.Finding{failing}, "CIS-1.1.15", "error", "8.0"},
		{"mixed escalates regardless of order", []engine.Finding{mixedManualFirst, failing}, "CIS-1.1.15", "error", "8.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := &engine.Report{
				Metadata: scm.Metadata{Tool: "scm-bench", Platform: scm.PlatformBitbucketDC},
				Findings: tc.findings,
			}
			out := renderReport(t, rep, Options{Format: FormatSARIF, ToolVersion: "1.2.3"})

			var log struct {
				Runs []struct {
					Tool struct {
						Driver struct {
							Rules []struct {
								ID                   string `json:"id"`
								DefaultConfiguration struct {
									Level string `json:"level"`
								} `json:"defaultConfiguration"`
								Properties struct {
									SecuritySeverity string `json:"security-severity"`
								} `json:"properties"`
							} `json:"rules"`
						} `json:"driver"`
					} `json:"tool"`
				} `json:"runs"`
			}
			if err := json.Unmarshal([]byte(out), &log); err != nil {
				t.Fatalf("unmarshal SARIF: %v", err)
			}

			var found bool
			for _, r := range log.Runs[0].Tool.Driver.Rules {
				if r.ID != tc.rule {
					continue
				}
				found = true
				if r.DefaultConfiguration.Level != tc.wantLevel {
					t.Errorf("level = %q, want %q", r.DefaultConfiguration.Level, tc.wantLevel)
				}
				if r.Properties.SecuritySeverity != tc.wantSecuritySeve {
					t.Errorf("security-severity = %q, want %q", r.Properties.SecuritySeverity, tc.wantSecuritySeve)
				}
			}
			if !found {
				t.Fatalf("rule %s absent from the SARIF", tc.rule)
			}
		})
	}
}
