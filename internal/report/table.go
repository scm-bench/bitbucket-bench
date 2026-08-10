package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/scm-bench/scm-bench/internal/checks"
	"github.com/scm-bench/scm-bench/internal/console"
	"github.com/scm-bench/scm-bench/internal/engine"
)

// ANSI codes and the tag vocabulary come from internal/console so the report
// and the scan trace cannot drift apart. The local names are kept because they
// read better at the call sites here, where nearly every line paints something.
const (
	ansiReset  = console.Reset
	ansiBold   = console.Bold
	ansiDim    = console.Dim
	ansiRed    = console.Red
	ansiGreen  = console.Green
	ansiYellow = console.Yellow
	ansiCyan   = console.Cyan
)

type painter struct{ enabled bool }

func (p painter) paint(code, s string) string {
	if !p.enabled || s == "" {
		return s
	}
	return code + s + ansiReset
}

// statusTag maps a verdict onto one of the four tags. MANUAL becomes WARN —
// kube-bench's word for a control a human still has to decide — and NA becomes
// INFO, because a control that does not apply asks nothing of anyone.
func statusTag(s engine.Status) console.Tag {
	switch s {
	case engine.StatusPass:
		return console.Pass
	case engine.StatusFail:
		return console.Fail
	case engine.StatusManual:
		return console.Warn
	default:
		return console.Info
	}
}

// writeTable renders the report in the shape kube-bench established: the
// verdicts first as a scannable list, then the remediations as their own
// section, then a summary.
//
// Keeping remediation out of the list is the part that matters. Each fix here
// is a paragraph naming a settings path, and printing it under every control
// turned a twenty-repository scan into a wall of prose you had to read to find
// the next verdict. They are still all present, just after the list rather
// than woven through it.
func writeTable(w io.Writer, rep *engine.Report, opts Options) error {
	p := painter{enabled: opts.Color}
	t := console.Writer{W: w, P: console.Painter{Enabled: opts.Color}}

	t.Info("%s %s  ·  %s  ·  %s",
		p.paint(ansiBold, "scm-bench"),
		rep.Metadata.ToolVersion,
		rep.Metadata.BaseURL,
		rep.Metadata.GeneratedAt.Format("2006-01-02 15:04:05 MST"),
	)

	writeFindings(t, rep, p, opts)
	if !opts.NoRemediations {
		writeRemediations(t, rep, p, opts)
	}
	writeWarnings(t, rep, p)
	writeSummary(t, rep, p)
	return nil
}

func section(t console.Writer, p painter, title string) {
	t.Blank()
	t.Info("%s", p.paint(ansiBold, "== "+title+" =="))
}

// writeSummary closes with the counts, in kube-bench's shape, plus the score.
//
// The counts are of controls-by-resource, which is what "19 checks WARN" means
// everywhere else in this file; the score line below states its own arithmetic
// so the number is checkable rather than something to trust.
func writeSummary(t console.Writer, rep *engine.Report, p painter) {
	s := rep.Score
	section(t, p, "Summary")

	// kube-bench writes "1 checks PASS"; the grammar is fixed here because
	// copying a wart is not the same as following a convention.
	t.Info("%s", p.paint(ansiGreen, checkCount(s.Passed, "PASS")))
	t.Info("%s", p.paint(ansiRed, checkCount(s.Failed, "FAIL")))
	t.Info("%s", p.paint(ansiYellow, checkCount(s.Manual, "WARN")))
	t.Info("%s", p.paint(ansiDim, checkCount(s.NotApplicable, "INFO")))

	scoreColor := ansiGreen
	switch {
	case s.Value < 50:
		scoreColor = ansiRed
	case s.Value < 80:
		scoreColor = ansiYellow
	}
	t.Blank()
	t.Info("%s %s", p.paint(ansiBold, "SCORE"), p.paint(scoreColor+ansiBold, fmt.Sprintf("%d/100", s.Value)))
	t.Info("%s", p.paint(ansiDim, fmt.Sprintf(
		"      weighted %d/%d (HIGH=3, MEDIUM=2, LOW=1; WARN and INFO excluded)",
		s.EarnedWeight, s.TotalWeight,
	)))

	var parts []string
	for _, sev := range []string{checks.SeverityHigh, checks.SeverityMedium, checks.SeverityLow} {
		c := s.BySeverity[sev]
		if c.Failed > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", sev, c.Failed))
		}
	}
	if len(parts) > 0 {
		t.Info("      %s %s", p.paint(ansiRed, "failures by severity:"), strings.Join(parts, "  "))
	}
	t.Blank()
}

func checkCount(n int, state string) string {
	noun := "checks"
	if n == 1 {
		noun = "check"
	}
	return fmt.Sprintf("%d %s %s", n, noun, state)
}

func writeFindings(t console.Writer, rep *engine.Report, p painter, opts Options) {
	groups := []struct {
		status engine.Status
		header string
		show   bool
	}{
		{engine.StatusFail, "Failed", true},
		{engine.StatusManual, "Needs manual review", true},
		{engine.StatusPass, "Passed", opts.ShowPassed},
		{engine.StatusNA, "Not applicable", opts.ShowPassed},
	}

	for _, g := range groups {
		if !g.show {
			continue
		}
		matching := filterByStatus(rep.Findings, g.status)
		if len(matching) == 0 {
			continue
		}
		section(t, p, fmt.Sprintf("%s (%d)", g.header, len(matching)))

		for _, group := range groupByCheck(matching) {
			first := group[0]
			// The control's own line carries the verdict tag; everything
			// beneath it is context for that verdict and is tagged INFO. One
			// control therefore contributes exactly one PASS/FAIL/WARN to the
			// column, however many repositories it covers.
			t.Line(statusTag(g.status), "%s  %s  %s",
				p.paint(ansiCyan+ansiBold, first.CheckID),
				severityLabel(p, first.Severity),
				p.paint(ansiBold, title(first, opts.Lang)),
			)

			for _, verdict := range groupByMessage(group) {
				writeVerdict(t, p, verdict, opts.MaxResources)
			}
		}
	}
}

// writeRemediations lists the fix for every control that needs one, once each.
//
// A control that both failed on one repository and could not be read on
// another appears once here, not twice: the settings path is a property of the
// control, not of the verdict.
func writeRemediations(t console.Writer, rep *engine.Report, p painter, opts Options) {
	seen := map[string]bool{}
	var ordered []engine.Finding
	for _, f := range rep.Findings {
		if f.Status != engine.StatusFail && f.Status != engine.StatusManual {
			continue
		}
		if seen[f.CheckID] {
			continue
		}
		seen[f.CheckID] = true
		ordered = append(ordered, f)
	}
	if len(ordered) == 0 {
		return
	}

	section(t, p, fmt.Sprintf("Remediations (%d)", len(ordered)))
	for _, f := range ordered {
		t.Info("%s  %s", p.paint(ansiCyan+ansiBold, f.CheckID), remediation(f, opts.Lang))
	}
}

// verdict is one distinct outcome within a control, together with every
// resource that produced it.
type verdict struct {
	details      string
	evidence     []string
	resourceType string
	resources    []string
}

// groupByMessage collapses the findings of one control by the text they
// produced.
//
// This is what makes the claim "one misconfiguration across fifty repositories
// reads as one problem" actually true. Grouping by control alone did not do it:
// the same sentence was still printed once per repository, so a control that
// failed everywhere produced fifty identical lines with only the name changing.
// Distinct outcomes stay distinct — "no restriction covers master" and "branch
// permissions could not be read" are different problems and are not merged.
func groupByMessage(findings []engine.Finding) []verdict {
	index := map[string]int{}
	var verdicts []verdict

	for _, f := range findings {
		key := f.Details + "\x00" + strings.Join(f.Evidence, "\x00")
		i, seen := index[key]
		if !seen {
			index[key] = len(verdicts)
			verdicts = append(verdicts, verdict{
				details:      f.Details,
				evidence:     f.Evidence,
				resourceType: f.ResourceType,
				resources:    []string{f.Resource},
			})
			continue
		}
		verdicts[i].resources = append(verdicts[i].resources, f.Resource)
	}

	for i := range verdicts {
		sort.Strings(verdicts[i].resources)
	}
	return verdicts
}

func writeVerdict(t console.Writer, p painter, v verdict, maxResources int) {
	// One resource is the common case on a small instance, and naming it on the
	// same line as its verdict is more readable than spreading it over two.
	if len(v.resources) == 1 {
		t.Info("    %s  %s", p.paint(ansiDim, v.resources[0]), v.details)
	} else {
		t.Info("    %s", v.details)
	}
	for _, e := range v.evidence {
		t.Info("      %s", p.paint(ansiDim, "· "+e))
	}
	if len(v.resources) > 1 {
		t.Info("      %s", p.paint(ansiDim, resourceList(v.resources, v.resourceType, maxResources)))
	}
}

// resourceList renders the affected resources, summarising the tail. The count
// comes first because it is the part that decides what to do: three
// repositories is an oversight, three hundred is a policy that was never
// applied, and that difference should not depend on counting names.
func resourceList(resources []string, kind string, max int) string {
	total := len(resources)
	label := fmt.Sprintf("%d %s:", total, plural(kind))
	if max <= 0 || total <= max {
		return fmt.Sprintf("%s %s", label, strings.Join(resources, ", "))
	}
	return fmt.Sprintf("%s %s and %d more", label, strings.Join(resources[:max], ", "), total-max)
}

// plural names the resource kind for a count that is always at least two — a
// single resource is rendered inline and never reaches here.
func plural(kind string) string {
	switch kind {
	case engine.ResourceRepository:
		return "repositories"
	case engine.ResourceOrganization:
		return "instances"
	case "":
		return "resources"
	}
	return kind + "s"
}

func writeWarnings(t console.Writer, rep *engine.Report, p painter) {
	// These are the tool reporting on itself, not on the instance, so they get
	// their own tags rather than being folded into the verdict column. A reader
	// grepping for '^\[WARN\]' is asking "what did this scan fail to see?",
	// which is a different question from "what is wrong with my instance?".
	if len(rep.Metadata.Warnings) > 0 {
		section(t, p, "Scan warnings")
		for _, warning := range rep.Metadata.Warnings {
			t.Line(console.Warn, "%s", warning)
		}
	}
	if len(rep.Errors) > 0 {
		section(t, p, "Policy errors")
		for _, e := range rep.Errors {
			// A policy that would not evaluate is a failure of the tool, and
			// FAIL is the tag that says so without inventing a fifth word.
			t.Line(console.Fail, "%s", e)
		}
	}
}

func severityLabel(p painter, severity string) string {
	switch strings.ToUpper(severity) {
	case checks.SeverityHigh:
		return p.paint(ansiRed, "HIGH  ")
	case checks.SeverityMedium:
		return p.paint(ansiYellow, "MEDIUM")
	default:
		return p.paint(ansiDim, "LOW   ")
	}
}

func filterByStatus(findings []engine.Finding, status engine.Status) []engine.Finding {
	var out []engine.Finding
	for _, f := range findings {
		if f.Status == status {
			out = append(out, f)
		}
	}
	return out
}

// groupByCheck buckets findings by control, preserving the report's existing
// severity ordering for the groups themselves.
func groupByCheck(findings []engine.Finding) [][]engine.Finding {
	index := map[string]int{}
	var groups [][]engine.Finding
	for _, f := range findings {
		i, ok := index[f.CheckID]
		if !ok {
			index[f.CheckID] = len(groups)
			groups = append(groups, []engine.Finding{f})
			continue
		}
		groups[i] = append(groups[i], f)
	}
	for _, g := range groups {
		sort.SliceStable(g, func(a, b int) bool { return g[a].Resource < g[b].Resource })
	}
	return groups
}
