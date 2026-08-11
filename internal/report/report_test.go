package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
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

// reportWithUnreadableResource models the case UNREAD exists for: one
// repository the token could not read, costing n controls a verdict. They are
// MANUAL and Automated, which is what separates "this run came up short" from
// "no API can answer this".
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
		"Report Summary",
		"Findings",
		"CIS-1.1.15",
		"PRJ/app",
		"Ensure pushing is restricted",
		"Repository settings -> Branch permissions -> Add restriction",
		"Remediations",
		"Scan warnings",
		"the user directory is not readable",
	} {
		if !containsText(out, want) {
			t.Errorf("table output is missing %q\n---\n%s", want, out)
		}
	}

	// The default is the overview: no per-resource sections, and none of the
	// per-finding narration that belongs to them.
	if strings.Contains(out, "Total: ") {
		t.Errorf("the default output should not draw per-resource sections\n---\n%s", out)
	}
	if containsText(out, "Anyone with write access can push directly to main.") {
		t.Errorf("per-finding details belong to --details\n---\n%s", out)
	}

	// Passing controls are summarised but not listed unless asked for.
	if strings.Contains(out, "Ensure two approvals") {
		t.Error("passing controls should be hidden without --show-passed")
	}
	if !strings.Contains(out, "1 passed") {
		t.Errorf("the summary should still count the passing control\n---\n%s", out)
	}
}

func TestDetailsIncludesPerResourceSections(t *testing.T) {
	out := render(t, Options{Format: FormatTable, Details: true})

	for _, want := range []string{
		"Report Summary",
		"CIS-1.1.15",
		"PRJ/app",
		"Total: ",
		"Anyone with write access can push directly to main.",
		"Repository settings -> Branch permissions -> Add restriction",
		"Remediations",
		"Scan warnings",
		"the user directory is not readable",
	} {
		if !containsText(out, want) {
			t.Errorf("details output is missing %q\n---\n%s", want, out)
		}
	}
}

// The summary is the first thing on the page, not the last.
//
// A CI log is read from its end, but a terminal is read from its top, and a
// score that arrived after four screens of findings answered "how bad is this"
// long after the reader had started guessing. In the detail layout the scan
// warnings come before the findings because they say how much of the report to
// believe; the overview is one screen, so there they sit under the sentence
// that points at them.
func TestSummaryAndWarningsComeBeforeTheFindings(t *testing.T) {
	out := render(t, Options{Format: FormatTable, Details: true})

	score := strings.Index(out, "SCORE")
	summary := strings.Index(out, "Report Summary")
	warnings := strings.Index(out, "Scan warnings")
	findings := strings.Index(out, "Total: ")
	remediations := strings.Index(out, "Remediations (")

	for _, step := range []struct {
		name       string
		before, at int
	}{
		{"SCORE before the report summary", score, summary},
		{"report summary before the scan warnings", summary, warnings},
		{"scan warnings before the findings", warnings, findings},
		{"findings before the remediations", findings, remediations},
	} {
		if step.before < 0 || step.at < 0 {
			t.Fatalf("%s: a section is missing entirely\n---\n%s", step.name, out)
		}
		if step.before > step.at {
			t.Errorf("%s: order is wrong\n---\n%s", step.name, out)
		}
	}
}

func TestOverviewSectionOrder(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	score := strings.Index(out, "SCORE")
	summary := strings.Index(out, "Report Summary")
	findings := strings.Index(out, "Findings")
	warnings := strings.Index(out, "Scan warnings")
	remediations := strings.Index(out, "Remediations (")
	hint := strings.Index(out, "Details: rerun with --details")

	for _, step := range []struct {
		name       string
		before, at int
	}{
		{"SCORE before the report summary", score, summary},
		{"report summary before the findings", summary, findings},
		{"findings before the scan warnings", findings, warnings},
		{"scan warnings before the remediations", warnings, remediations},
		{"remediations before the hint", remediations, hint},
	} {
		if step.before < 0 || step.at < 0 {
			t.Fatalf("%s: a section is missing entirely\n---\n%s", step.name, out)
		}
		if step.before > step.at {
			t.Errorf("%s: order is wrong\n---\n%s", step.name, out)
		}
	}
}

// Both are MANUAL, and reading them as one state is what made the sample
// instance report nineteen controls needing a person when six of them did. The
// other thirteen were one unreadable repository. The Status column is where
// that difference survives now that the sections are gone.
func TestUnreadIsDistinctFromManualInTheStatusColumn(t *testing.T) {
	ids := []string{"1.1.3", "1.1.4", "1.1.9", "1.1.15", "1.1.16", "1.1.17", "1.2.1"}
	out := renderReport(t, reportWithUnreadableResource(t, ids...), Options{Format: FormatTable, Details: true})

	if n := strings.Count(out, statusUnread); n < len(ids) {
		t.Errorf("only %d of %d unreadable controls are marked %s\n---\n%s", n, len(ids), statusUnread, out)
	}
	// The control no API can answer is a different question and must not be
	// swept into the same word.
	unreadAt := strings.Index(out, "CIS-1.3.5")
	if unreadAt < 0 {
		t.Fatalf("the non-automated control is missing entirely\n---\n%s", out)
	}
	row := out[unreadAt:]
	if end := strings.Index(row, "\n"); end > 0 {
		row = row[:end]
	}
	if strings.Contains(row, statusUnread) {
		t.Errorf("a control no API can answer was reported as %s: %q", statusUnread, row)
	}

	// Every control is still named. A truncated list would leave the reader
	// unable to tell whether the ones they care about are among them.
	for _, id := range ids {
		if !strings.Contains(out, "CIS-"+id) {
			t.Errorf("control CIS-%s went unlisted\n---\n%s", id, out)
		}
	}
}

// A control the scan never saw is not known to be misconfigured. Printing how
// to change its settings would say the opposite; what it needs is access.
func TestUnreadControlsAreLeftOutOfTheRemediations(t *testing.T) {
	out := renderReport(t, reportWithUnreadableResource(t, "1.1.15", "1.1.16"), Options{Format: FormatTable})

	remediations := strings.Index(out, "Remediations (")
	if remediations < 0 {
		t.Fatalf("no remediations section\n---\n%s", out)
	}
	tail := out[remediations:]
	for _, unwanted := range []string{"Branch permissions -> 1.1.15", "Branch permissions -> 1.1.16"} {
		if strings.Contains(tail, unwanted) {
			t.Errorf("an unread control was given a remediation: %q\n---\n%s", unwanted, tail)
		}
	}
	if !strings.Contains(tail, "Enforce MFA at the IdP") {
		t.Errorf("the control that does need a person lost its remediation\n---\n%s", tail)
	}
	if !strings.Contains(out, "Remediations (1)") {
		t.Errorf("remediations should count only the controls that have one\n---\n%s", out)
	}
}

// The one-line fix rides in the finding's cell so the reader can act without
// scrolling; the paragraph stays in its own section so the table stays a table.
func TestFixSummaryRidesWithTheVerdictAndTheParagraphDoesNot(t *testing.T) {
	out := render(t, Options{Format: FormatTable, Details: true})

	remediations := strings.Index(out, "Remediations (")
	if remediations < 0 {
		t.Fatalf("no remediations section\n---\n%s", out)
	}
	tables, fixes := out[:remediations], out[remediations:]

	if !containsText(tables, "fix: Enable Prevent changes without a pull request.") {
		t.Errorf("the one-line fix is not in the finding's cell\n---\n%s", tables)
	}
	if containsText(tables, "Add restriction") {
		t.Errorf("the full remediation leaked into a table\n---\n%s", tables)
	}
	if !containsText(fixes, "Add restriction") {
		t.Errorf("the full remediation is missing from its section\n---\n%s", fixes)
	}
}

// The report proper groups by resource, which says everything about one
// repository and nothing about how it compares. The summary is the comparison,
// so its order is the answer to "where do I start".
func TestReportSummaryRanksResourcesWorstFirst(t *testing.T) {
	rep := sampleReport()
	rep.Findings = append(rep.Findings, engine.Finding{
		CheckID: "CIS-1.1.16", CISID: "1.1.16", Severity: "HIGH", Status: engine.StatusFail,
		Title: "Ensure force pushing is denied", Resource: "PRJ/app",
		ResourceType: engine.ResourceRepository, Details: "main can be force pushed.",
		Remediation: "Repository settings -> Branch permissions", Automated: true,
	})
	rep.Findings = append(rep.Findings, engine.Finding{
		CheckID: "CIS-1.2.1", CISID: "1.2.1", Severity: "LOW", Status: engine.StatusPass,
		Title: "Ensure a security policy exists", Resource: "PRJ/quiet",
		ResourceType: engine.ResourceRepository, Details: "SECURITY.md is present.",
		Remediation: "Add a SECURITY.md", Automated: true,
	})
	rep.Score = engine.Compute(rep.Findings)
	out := renderReport(t, rep, Options{Format: FormatTable})

	summary := strings.Index(out, "Report Summary")
	if summary < 0 {
		t.Fatalf("no report summary\n---\n%s", out)
	}
	// Every resource appears, including the clean one: trivy lists its targets
	// with zero findings too, and "nothing wrong here" is a result.
	head := out[summary:]
	app, quiet := strings.Index(head, "PRJ/app"), strings.Index(head, "PRJ/quiet")
	if app < 0 || quiet < 0 {
		t.Fatalf("a resource is missing from the summary\n---\n%s", head)
	}
	if app > quiet {
		t.Errorf("the resource with two failures should be listed first\n---\n%s", head)
	}

	// A count of zero reads as a dash, so the eye stops on the digits.
	if !strings.Contains(head, "-") {
		t.Errorf("a zero count should render as a dash\n---\n%s", head)
	}
}

// Nothing may run past the width, at any width, whatever the escape sequences
// would have measured.
func TestEveryLineFitsTheTerminalWidth(t *testing.T) {
	for _, columns := range []string{"60", "80", "100", "300"} {
		t.Setenv("COLUMNS", columns)
		limit := min(max(atoi(t, columns), 60), 120)

		for _, colour := range []bool{false, true} {
			for _, details := range []bool{false, true} {
				out := renderReport(t, reportWithUnreadableResource(t, "1.1.3", "1.1.15", "1.1.16"),
					Options{Format: FormatTable, Color: colour, Details: details})
				for _, line := range strings.Split(out, "\n") {
					if n := utf8.RuneCountInString(stripANSI(line)); n > limit {
						t.Errorf("COLUMNS=%s colour=%v details=%v: %d columns: %q", columns, colour, details, n, line)
					}
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

// tableCells rebuilds every cell of every table in out, joining the lines a
// cell was wrapped across back into one string.
//
// Assertions need it because the renderer wraps to the terminal: a sentence in
// a cell is several lines with a border between them, so strings.Contains over
// the raw output cannot find it. Rebuilding by column boundary rather than by
// splitting on the border keeps a cell's own lines together instead of
// interleaving them with its neighbours'.
func tableCells(out string) []string {
	var cells []string
	var bounds []int
	var pending []string

	endRow := func() {
		for i, c := range pending {
			if s := strings.Join(strings.Fields(c), " "); s != "" {
				cells = append(cells, s)
			}
			pending[i] = ""
		}
	}
	endTable := func() {
		endRow()
		pending, bounds = nil, nil
	}

	for _, raw := range strings.Split(stripANSI(out), "\n") {
		line := []rune(raw)
		if len(line) == 0 {
			endTable()
			continue
		}
		switch line[0] {
		case '┌':
			endTable()
			for i, r := range line {
				if r == '┌' || r == '┬' || r == '┐' {
					bounds = append(bounds, i)
				}
			}
			pending = make([]string, max(len(bounds)-1, 0))
		case '│':
			for i := 0; i+1 < len(bounds) && i < len(pending); i++ {
				lo, hi := bounds[i]+1, bounds[i+1]
				if hi > len(line) {
					hi = len(line)
				}
				if lo < hi {
					pending[i] += " " + string(line[lo:hi])
				}
			}
		case '├':
			endRow()
		case '└':
			endTable()
		default:
			endTable()
		}
	}
	endTable()
	return cells
}

// containsText looks for want anywhere in out, treating a wrapped table cell as
// the single string it reads as.
func containsText(out, want string) bool {
	if strings.Contains(stripANSI(out), want) {
		return true
	}
	for _, cell := range tableCells(out) {
		if strings.Contains(cell, want) {
			return true
		}
	}
	return false
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
	out := render(t, Options{Format: FormatTable, ShowPassed: true, Details: true})
	if !containsText(out, "Ensure two approvals") {
		t.Error("--show-passed should list passing controls")
	}
	if !strings.Contains(out, statusPass) {
		t.Errorf("no %s appears in the Status column\n---\n%s", statusPass, out)
	}

	// Not with a fix beside them. Telling someone how to change a setting that
	// is already right reads as an instruction to go and break it.
	for _, cell := range tableCells(out) {
		if strings.Contains(cell, "Pull requests require 2 approvals.") && strings.Contains(cell, "fix:") {
			t.Errorf("a passing control was given a fix: %q", cell)
		}
	}
}

func TestTableIsPlainWithoutColor(t *testing.T) {
	out := render(t, Options{Format: FormatTable, Color: false})
	if strings.Contains(out, "\033[") {
		t.Error("colour was disabled but ANSI escapes were emitted")
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

// Remediation is a paragraph naming a settings path, and a paragraph in a table
// cell is a column of three-word lines. It keeps its own section, as prose.
func TestRemediationLivesInItsOwnSection(t *testing.T) {
	out := render(t, Options{Format: FormatTable})

	remedy := "Repository settings -> Branch permissions -> Add restriction"
	tables, remediations, ok := strings.Cut(out, "Remediations (")
	if !ok {
		t.Fatalf("no remediation section\n---\n%s", out)
	}
	if containsText(tables, remedy) {
		t.Errorf("remediation is still inside a finding's table\n---\n%s", tables)
	}
	if !containsText(remediations, remedy) {
		t.Errorf("remediation missing from its own section\n---\n%s", remediations)
	}
	// Prose, not a table: no borders after the section heading.
	if strings.Contains(remediations, "┌") {
		t.Errorf("the remediation section was drawn as a table\n---\n%s", remediations)
	}
}

// A control that failed on one repository and could not be read on another is
// still one control, so its settings path is stated once.
func TestRemediationIsListedOncePerControl(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 4)
	rep.Findings[2].Status = engine.StatusManual
	rep.Score = engine.Compute(rep.Findings)

	out := renderReport(t, rep, Options{Format: FormatTable, MaxResources: DefaultMaxResources})
	if n := countText(out, "Repository settings -> Branch permissions"); n != 1 {
		t.Errorf("remediation printed %d times, want once\n---\n%s", n, out)
	}
}

func TestNoRemediationsDropsTheSection(t *testing.T) {
	out := render(t, Options{Format: FormatTable, NoRemediations: true})

	if strings.Contains(out, "Remediations (") {
		t.Errorf("--no-remediations left the section in\n---\n%s", out)
	}
	// The findings themselves are still there; only the fixes are gone.
	if !strings.Contains(out, "CIS-1.1.15") {
		t.Errorf("--no-remediations dropped the findings too\n---\n%s", out)
	}
}

// All four states are counted, on one line, in the state names the rest of the
// tool uses.
//
// PASS/FAIL/MANUAL/NA — the names in the JSON and in `list-checks`. The Status
// column inside a table says UNREAD where a MANUAL was this run's shortfall
// rather than the control's; the arithmetic up here does not, because the score
// counts what the control returned.
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

// countText counts occurrences of want, treating a wrapped table cell as the
// single string it reads as.
func countText(out, want string) int {
	n := strings.Count(stripANSI(out), want)
	for _, cell := range tableCells(out) {
		n += strings.Count(cell, want)
	}
	return n
}

// The cap exists for an instance with more repositories than anyone will read
// through, and when it bites it has to say so — a report that silently omits
// repositories with findings is worse than a long one.
func TestMaxResourcesCapsTheTablesAndSaysSo(t *testing.T) {
	out := renderReport(t, reportWithRepeatedFinding(t, 6), Options{Format: FormatTable, Details: true, MaxResources: 2})

	if n := strings.Count(out, "Total: "); n != 2 {
		t.Errorf("drew %d resource tables, want 2\n---\n%s", n, out)
	}
	if !containsText(out, "4 more resources with findings not shown") {
		t.Errorf("the cap was applied silently\n---\n%s", out)
	}
	// Every resource is still in the summary, so nothing disappears entirely.
	summary := out[strings.Index(out, "Report Summary"):]
	for i := range 6 {
		if !strings.Contains(summary, fmt.Sprintf("PRJ/repo-%02d", i)) {
			t.Errorf("PRJ/repo-%02d is missing from the summary\n---\n%s", i, summary)
		}
	}
}

// Zero is the default and means "draw them all", because each table is a
// resource's whole verdict rather than a list that could be trimmed.
func TestMaxResourcesZeroDrawsEveryTable(t *testing.T) {
	out := renderReport(t, reportWithRepeatedFinding(t, 6), Options{Format: FormatTable, Details: true, MaxResources: 0})
	if n := strings.Count(out, "Total: "); n != 6 {
		t.Errorf("drew %d resource tables, want 6\n---\n%s", n, out)
	}
	if strings.Contains(out, "not shown") {
		t.Errorf("nothing should have been withheld\n---\n%s", out)
	}
}

// A report with no warnings and no policy errors should not print the headings
// for them.
func TestQuietScanOmitsTheWarningSections(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 1)
	out := renderReport(t, rep, Options{Format: FormatTable})
	for _, unwanted := range []string{"Scan warnings", "Policy errors"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("%q was printed for a scan that had none\n---\n%s", unwanted, out)
		}
	}
}

// A policy that would not evaluate is a failure of the tool, and it has to be
// as visible as one.
func TestPolicyErrorsAreReported(t *testing.T) {
	rep := sampleReport()
	rep.Errors = []string{"CIS-1.1.3 on PRJ/app: policy evaluation failed: undefined function"}
	out := renderReport(t, rep, Options{Format: FormatTable})

	if !strings.Contains(out, "Policy errors") {
		t.Errorf("no policy error section\n---\n%s", out)
	}
	if !containsText(out, "policy evaluation failed") {
		t.Errorf("the policy error text is missing\n---\n%s", out)
	}
}

// A long base URL pushes the header past the width. It sheds a part at a time
// rather than wrapping, because the padded "·" separators do not survive being
// broken across a line.
func TestHeaderShedsPartsRatherThanOverflowing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		baseURL string
		want    []string
	}{
		{"fits on one line", "https://bb.example.com",
			[]string{"scm-bench 1.2.3  ·  https://bb.example.com  ·  2026-01-01 12:00:00 UTC"}},
		{"timestamp moves down", "https://bitbucket.a-fairly-long-hostname.example.com",
			[]string{"scm-bench 1.2.3  ·  https://bitbucket.a-fairly-long-hostname.example.com", "scanned 2026-01-01 12:00:00 UTC"}},
		{"url gets its own line", "https://bitbucket.a-very-long-hostname-for-one-company.example.com",
			[]string{"scm-bench 1.2.3", "https://bitbucket.a-very-long-hostname-for-one-company.example.com", "scanned 2026-01-01 12:00:00 UTC"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := sampleReport()
			rep.Metadata.BaseURL = tc.baseURL
			out := renderReport(t, rep, Options{Format: FormatTable})

			header := strings.SplitN(out, "\n\n", 2)[0]
			if got := strings.Split(header, "\n"); !slices.Equal(got, tc.want) {
				t.Errorf("header =\n%#v\nwant\n%#v", got, tc.want)
			}
			// The URL is one unbreakable token, so its own line may still be
			// long; nothing else may be.
			for _, l := range strings.Split(header, "\n") {
				if strings.Contains(l, tc.baseURL) {
					continue
				}
				if n := utf8.RuneCountInString(l); n > 80 {
					t.Errorf("header line is %d columns: %q", n, l)
				}
			}
		})
	}
}

// One misconfiguration across many repositories is one row saying so, not one
// table per repository saying the same thing.
func TestOverviewAggregatesFailuresByControl(t *testing.T) {
	out := renderReport(t, reportWithRepeatedFinding(t, 6), Options{Format: FormatTable})

	cells := tableCells(out)
	rows := 0
	for _, c := range cells {
		if strings.Contains(c, "CIS-1.1.15") {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("the control appears in %d table rows, want 1\n---\n%s", rows, out)
	}
	found := false
	for _, c := range cells {
		if c == "6/6" {
			found = true
		}
	}
	if !found {
		t.Errorf("no 6/6 resources cell\n---\n%s", out)
	}
}

// FAIL rows lead, ordered by severity and then benchmark number, so the top of
// the table is the top of the to-do list.
func TestOverviewOrdersRowsBySeverityThenID(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 1)
	rep.Findings = append(rep.Findings,
		engine.Finding{
			CheckID: "CIS-1.1.8", CISID: "1.1.8", Severity: "LOW", Status: engine.StatusFail,
			Title: "Ensure stale branches are removed", Resource: "PRJ/repo-00",
			ResourceType: engine.ResourceRepository, Details: "d", Automated: true,
		},
		engine.Finding{
			CheckID: "CIS-1.1.4", CISID: "1.1.4", Severity: "MEDIUM", Status: engine.StatusFail,
			Title: "Ensure approvals are dismissed", Resource: "PRJ/repo-00",
			ResourceType: engine.ResourceRepository, Details: "d", Automated: true,
		},
		engine.Finding{
			CheckID: "CIS-1.3.5", CISID: "1.3.5", Severity: "HIGH", Status: engine.StatusManual,
			Title: "Ensure MFA is enforced", Resource: engine.InstanceResourceName,
			ResourceType: engine.ResourceOrganization, Details: "ask the IdP",
		},
	)
	rep.Score = engine.Compute(rep.Findings)
	out := renderReport(t, rep, Options{Format: FormatTable})

	high := strings.Index(out, "CIS-1.1.15")  // HIGH FAIL
	medium := strings.Index(out, "CIS-1.1.4") // MEDIUM FAIL
	low := strings.Index(out, "CIS-1.1.8")    // LOW FAIL
	manual := strings.Index(out, "CIS-1.3.5") // HIGH MANUAL
	if high < 0 || medium < 0 || low < 0 || manual < 0 {
		t.Fatalf("a control is missing from the overview\n---\n%s", out)
	}
	if !(high < medium && medium < low) {
		t.Errorf("FAIL rows are not ordered by severity\n---\n%s", out)
	}
	if manual < low {
		t.Errorf("a MANUAL row appears before the FAIL rows\n---\n%s", out)
	}
}

// Thirteen controls the token could not read are one problem with one cause,
// so the overview says so once instead of thirteen times.
func TestOverviewCollapsesUnreadToOneSentence(t *testing.T) {
	ids := []string{"1.1.3", "1.1.15", "1.1.16"}
	rep := reportWithUnreadableResource(t, ids...)
	rep.Metadata.Warnings = []string{"branch permissions are not readable (401)"}
	out := renderReport(t, rep, Options{Format: FormatTable})

	if strings.Contains(out, statusUnread) {
		t.Errorf("unread findings should not appear as rows in the overview\n---\n%s", out)
	}
	if !containsText(out, "3 controls could not be read (Unread) on PRJ/locked; see Scan warnings below.") {
		t.Errorf("no unread summary sentence\n---\n%s", out)
	}
	// The control no API can answer is a different thing and keeps its row.
	if !containsText(out, "CIS-1.3.5") {
		t.Errorf("the manual control lost its row\n---\n%s", out)
	}
}

// Unread instances still widen the denominator: a control failing on two of
// four resources while two went unread is 2/4, not 2/2.
func TestOverviewCountsUnreadInTheDenominator(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 4)
	rep.Findings[2].Status = engine.StatusManual // unread: Automated stays true
	rep.Findings[3].Status = engine.StatusManual
	rep.Score = engine.Compute(rep.Findings)
	out := renderReport(t, rep, Options{Format: FormatTable})

	found := false
	for _, c := range tableCells(out) {
		if c == "2/4" {
			found = true
		}
	}
	if !found {
		t.Errorf("no 2/4 resources cell\n---\n%s", out)
	}
}

func TestOverviewShowPassedAddsPassRows(t *testing.T) {
	out := render(t, Options{Format: FormatTable, ShowPassed: true})
	if !containsText(out, "Ensure two approvals") {
		t.Errorf("--show-passed should add PASS rows to the overview\n---\n%s", out)
	}
	if !strings.Contains(out, statusPass) {
		t.Errorf("no %s in the Status column\n---\n%s", statusPass, out)
	}

	// Without it, passes stay out.
	out = render(t, Options{Format: FormatTable})
	if containsText(out, "Ensure two approvals") {
		t.Errorf("a PASS row appeared without --show-passed\n---\n%s", out)
	}
}

func TestOverviewSaysSoWhenNothingNeedsAttention(t *testing.T) {
	rep := &engine.Report{
		Metadata: scm.Metadata{Tool: "scm-bench", Platform: scm.PlatformBitbucketDC},
		Findings: []engine.Finding{{
			CheckID: "CIS-1.1.15", CISID: "1.1.15", Title: "t", Severity: "HIGH",
			Status: engine.StatusPass, Resource: "PRJ/app", ResourceType: engine.ResourceRepository,
			Details: "fine", Automated: true,
		}},
	}
	rep.Score = engine.Compute(rep.Findings)
	out := renderReport(t, rep, Options{Format: FormatTable})

	if !strings.Contains(out, "No failed or manual-review controls.") {
		t.Errorf("a clean overview should say it is clean\n---\n%s", out)
	}
}

func TestHintAppearsOnlyInOverview(t *testing.T) {
	if out := render(t, Options{Format: FormatTable}); !strings.Contains(out, "Details: rerun with --details") {
		t.Errorf("the overview should say how to get the details\n---\n%s", out)
	}
	if out := render(t, Options{Format: FormatTable, Details: true}); strings.Contains(out, "Details: rerun with --details") {
		t.Errorf("the details layout should not advertise itself\n---\n%s", out)
	}
}

func TestDetailsFilterByResource(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 3) // PRJ/repo-00 .. PRJ/repo-02
	out := renderReport(t, rep, Options{Format: FormatTable, Details: true, DetailFilters: []string{"REPO-01"}})

	sections := out[strings.Index(out, "Total: "):]
	if strings.Contains(sections, "PRJ/repo-00") || strings.Contains(sections, "PRJ/repo-02") {
		t.Errorf("resources outside the filter got sections\n---\n%s", out)
	}
	if n := strings.Count(out, "Total: "); n != 1 {
		t.Errorf("drew %d sections, want 1\n---\n%s", n, out)
	}
	// The summary keeps every resource: it is the navigation.
	summary := out[strings.Index(out, "Report Summary"):strings.Index(out, "Total: ")]
	if !strings.Contains(summary, "PRJ/repo-00") {
		t.Errorf("the report summary lost a resource to the filter\n---\n%s", out)
	}
}

func TestDetailsFilterByControl(t *testing.T) {
	rep := sampleReport()
	for _, value := range []string{"CIS-1.1.15", "cis-1.1.15", "1.1.15"} {
		out := renderReport(t, rep, Options{Format: FormatTable, Details: true, DetailFilters: []string{value}})
		if !containsText(out, "Anyone with write access can push directly to main.") {
			t.Errorf("--details=%s lost the control it names\n---\n%s", value, out)
		}
		if strings.Contains(out[strings.Index(out, "Total: "):], "CIS-1.3.5") {
			t.Errorf("--details=%s kept a control it does not name\n---\n%s", value, out)
		}
		// Remediations narrow with the filter.
		if strings.Contains(out, "Enforce MFA at the IdP") {
			t.Errorf("--details=%s kept a filtered control's remediation\n---\n%s", value, out)
		}
	}
}

func TestDetailsFilterKindsIntersect(t *testing.T) {
	rep := reportWithRepeatedFinding(t, 3)
	rep.Findings = append(rep.Findings, engine.Finding{
		CheckID: "CIS-1.1.8", CISID: "1.1.8", Severity: "LOW", Status: engine.StatusFail,
		Title: "Ensure stale branches are removed", Resource: "PRJ/repo-01",
		ResourceType: engine.ResourceRepository, Details: "stale branches exist",
		Remediation: "Delete the stale branches", Automated: true,
	})
	rep.Score = engine.Compute(rep.Findings)

	out := renderReport(t, rep, Options{Format: FormatTable, Details: true,
		DetailFilters: []string{"repo-01", "1.1.8"}})
	sections := out[strings.Index(out, "Total: "):]
	if !strings.Contains(sections, "CIS-1.1.8") {
		t.Errorf("the named control on the named resource is missing\n---\n%s", out)
	}
	if strings.Contains(sections, "CIS-1.1.15") {
		t.Errorf("a control outside the filter survived it\n---\n%s", out)
	}
	if n := strings.Count(out, "Total: "); n != 1 {
		t.Errorf("drew %d sections, want 1\n---\n%s", n, out)
	}
}

// A filter that matches nothing would render a report indistinguishable from a
// clean one, so it is an error — and an early one, before any output.
func TestDetailsFilterMatchingNothingErrors(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, sampleReport(), Options{Format: FormatTable, Details: true, DetailFilters: []string{"bogus"}})
	if err == nil {
		t.Fatal("a filter matching nothing should be an error")
	}
	if !strings.Contains(err.Error(), `"bogus"`) {
		t.Errorf("the error does not name the value: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("output was written before the filter was rejected:\n%s", buf.String())
	}
}
