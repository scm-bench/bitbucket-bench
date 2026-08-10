package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/scm"
)

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
			Details: "Pull requests require 2 approvals.", Remediation: "n/a", Automated: true,
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
	if !strings.Contains(out, "1 finding PASS") {
		t.Errorf("the summary should still count the passing control\n---\n%s", out)
	}
}

func TestTableShowPassedIncludesPassingControls(t *testing.T) {
	out := render(t, Options{Format: FormatTable, ShowPassed: true})
	if !strings.Contains(out, "Ensure two approvals") {
		t.Error("--show-passed should list passing controls")
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
// They count findings — one control against one resource — not controls, which
// is why the noun matters: a reader who had just seen `list-checks` report "20
// controls" was being asked to reconcile that with a summary whose four lines
// added up to forty-eight.
func TestSummaryReportsAllFourStates(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	for _, want := range []string{"1 finding PASS", "1 finding FAIL", "1 finding WARN", "0 findings INFO"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q\n---\n%s", want, out)
		}
	}
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
