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

// writeTable renders the report in the shape kube-bench established — verdicts
// as a scannable list, remediations as their own section — with the summary
// moved to the front.
//
// The summary leads because it is the part every reader wants and the only part
// some of them read. A CI log is scrolled to its end, but a terminal is read
// from its top, and a score that arrived after four screens of findings was
// answering "how bad is this" long after the reader had started guessing. What
// follows the score, in order, is: what the scan could not see, what failed,
// what it could not judge, and then how to fix it.
//
// Keeping the full remediation out of the list is the part that has not
// changed. Each fix is a paragraph naming a settings path, and printing it
// under every control turned a twenty-repository scan into a wall of prose you
// had to read to find the next verdict. The list carries FixSummary, one line
// of it; the paragraphs are still all present, just at the end.
func writeTable(w io.Writer, rep *engine.Report, opts Options) error {
	p := painter{enabled: opts.Color}
	t := console.Writer{W: w, P: console.Painter{Enabled: opts.Color}}
	width := console.Width()

	writeHeader(t, rep, p, width)
	writeSummary(t, rep, p, width)
	writeWarnings(t, rep, p, width)
	writeFindings(t, rep, p, opts, width)
	if !opts.NoRemediations {
		writeRemediations(t, rep, p, width)
	}
	return nil
}

// writeHeader names what was scanned and when.
//
// It splits rather than wraps. The parts are separated by a padded "·" that
// Wrap would collapse to a single space, and breaking between two of them mid
// separator reads as damage; the timestamp is the piece that can move to its
// own line without losing anything.
func writeHeader(t console.Writer, rep *engine.Report, p painter, width int) {
	m := rep.Metadata
	stamp := m.GeneratedAt.Format("2006-01-02 15:04:05 MST")

	oneLine := fmt.Sprintf("scm-bench %s  ·  %s  ·  %s", m.ToolVersion, m.BaseURL, stamp)
	if console.TagWidth+len([]rune(oneLine)) <= width {
		t.Info("%s %s  ·  %s  ·  %s", p.paint(ansiBold, "scm-bench"), m.ToolVersion, m.BaseURL, stamp)
		return
	}
	t.Info("%s %s  ·  %s", p.paint(ansiBold, "scm-bench"), m.ToolVersion, m.BaseURL)
	t.Info("%s", p.paint(ansiDim, "scanned "+stamp))
}

func section(t console.Writer, p painter, title string) {
	t.Blank()
	t.Info("%s", p.paint(ansiBold, "== "+title+" =="))
}

// writeSummary opens with the score and the counts behind it.
//
// The counts are of one control against one resource — findings, in the term
// the rest of the tool uses — and every line below the score states its own
// arithmetic, so the numbers are checkable rather than something to trust.
//
// They are on one line rather than kube-bench's four. Four lines reading "15
// findings PASS / 13 findings FAIL / ..." spend a quarter of a screen on four
// integers, and the reader has to hold three of them in their head to see the
// shape. Side by side, the shape is the line.
func writeSummary(t console.Writer, rep *engine.Report, p painter, width int) {
	s := rep.Score

	scoreColor := ansiGreen
	switch {
	case s.Value < 50:
		scoreColor = ansiRed
	case s.Value < 80:
		scoreColor = ansiYellow
	}
	t.Blank()
	// The state names are PASS, FAIL, MANUAL and NA — the ones in the JSON and
	// in `list-checks` — not the four tags. The tag column answers "what should
	// I do with this line"; this line is arithmetic, and borrowing WARN for
	// MANUAL here would mean the summary and the report counted in different
	// vocabularies.
	score := fmt.Sprintf("SCORE %d/100", s.Value)
	counts := fmt.Sprintf("%d passed  %d failed  %d manual  %d n/a",
		s.Passed, s.Failed, s.Manual, s.NotApplicable)
	painted := fmt.Sprintf("%s  %s  %s  %s",
		p.paint(ansiGreen, fmt.Sprintf("%d passed", s.Passed)),
		p.paint(ansiRed, fmt.Sprintf("%d failed", s.Failed)),
		p.paint(ansiYellow, fmt.Sprintf("%d manual", s.Manual)),
		p.paint(ansiDim, fmt.Sprintf("%d n/a", s.NotApplicable)),
	)
	head := p.paint(ansiBold, "SCORE") + " " + p.paint(scoreColor+ansiBold, fmt.Sprintf("%d/100", s.Value))
	// Measured on the uncoloured text: the escapes are bytes the reader never
	// sees, and counting them would break the line onto two on a terminal wide
	// enough for it.
	if console.TagWidth+len(score)+3+len(counts) <= width {
		t.Info("%s   %s", head, painted)
	} else {
		// The counts go on one line of their own and are not wrapped again:
		// four counts of four digits each is 46 columns, which fits inside the
		// narrowest width Width will report even after the indent and the tag.
		t.Info("%s", head)
		t.Info("      %s", painted)
	}

	summaryLine(t, p, width, ansiDim, fmt.Sprintf(
		"weighted %d/%d (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)",
		s.EarnedWeight, s.TotalWeight,
	))

	// How much of the instance the score is actually based on.
	//
	// Excluding WARN from both sides of the fraction is right control by
	// control — an instance should not be marked down for a question its API
	// cannot answer — and misleading in aggregate, because it shrinks the
	// denominator. A token that could read a tenth of an instance scored 78
	// where a working one scored 53. The arithmetic was never wrong; the
	// summary simply had no line that said the sample was small. This is that
	// line, and it turns yellow when most of the instance went unseen.
	if scored := s.Passed + s.Failed; s.Manual > 0 {
		decidable := scored + s.Manual
		coverage := scored * 100 / decidable
		colour := ansiDim
		if coverage < 50 {
			colour = ansiYellow
		}
		summaryLine(t, p, width, colour, fmt.Sprintf(
			"scored %d of %d controls (%d%%); %d could not be evaluated",
			scored, decidable, coverage, s.Manual))
	}

	var parts []string
	for _, sev := range []string{checks.SeverityHigh, checks.SeverityMedium, checks.SeverityLow} {
		c := s.BySeverity[sev]
		if c.Failed > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", sev, c.Failed))
		}
	}
	if len(parts) > 0 {
		// Separated by a bullet rather than the double space it used to be:
		// wrapping normalises runs of whitespace, so a gap wide enough to read
		// as a separator cannot survive it. A character can.
		summaryLine(t, p, width, ansiRed, "failures by severity: "+strings.Join(parts, " · "))
	}
	if affected := mostAffected(rep.Findings); affected != "" {
		summaryLine(t, p, width, ansiDim, "most affected: "+affected)
	}
}

// summaryLine writes one indented, wrapped line of the summary block.
//
// Colour goes on after wrapping, one line at a time. An escape sequence is
// width the reader never sees, so measuring a coloured string breaks the line
// early on a terminal that had room for it — the same reason the request trace
// pads by hand.
func summaryLine(t console.Writer, p painter, width int, colour, text string) {
	const indent = 6
	for _, line := range console.Wrap(text, width-console.TagWidth-indent) {
		t.Info("%s%s", strings.Repeat(" ", indent), p.paint(colour, line))
	}
}

// mostAffected names the resources carrying the failures, worst first.
//
// The report groups by control, which is the right axis for deciding what is
// wrong but the wrong one for deciding who fixes it: twelve of the thirteen
// failures on the sample instance are the same repository, and reading that off
// the list means scanning thirteen separate blocks and keeping a tally. This is
// the tally, on one line.
//
// It stays quiet when there is nothing to concentrate on. A resource with a
// single failure is not a pattern, and naming one when the failures are spread
// evenly would invent a culprit.
func mostAffected(findings []engine.Finding) string {
	counts := map[string]int{}
	for _, f := range findings {
		if f.Status == engine.StatusFail {
			counts[f.Resource]++
		}
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	// Count descending, then by name, so the line does not reshuffle between
	// two runs that found the same thing.
	sort.Slice(names, func(a, b int) bool {
		if counts[names[a]] != counts[names[b]] {
			return counts[names[a]] > counts[names[b]]
		}
		return names[a] < names[b]
	})

	var parts []string
	for _, name := range names {
		if counts[name] < 2 || len(parts) == 2 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", name, console.Pluralize(counts[name], "failure")))
	}
	return strings.Join(parts, ", ")
}

// unevaluated reports whether a finding is a control the tool would normally
// decide but could not, as opposed to one no API can answer.
//
// Both are MANUAL, and treating them as one list is what made the sample
// instance report nineteen things needing a person when six of them did. The
// other thirteen were one unreadable repository, said thirteen ways. Automated
// is the metadata that already knows the difference: false means the control is
// documented as unanswerable and always reports MANUAL; true means this run in
// particular came up short.
func unevaluated(f engine.Finding) bool {
	return f.Status == engine.StatusManual && f.Automated
}

func writeFindings(t console.Writer, rep *engine.Report, p painter, opts Options, width int) {
	// The tag on each section's headings. MANUAL takes WARN on both of its
	// sections — kube-bench's word for a control a human still has to decide,
	// and the one console reserves for anything the scan could not read — and
	// NA takes INFO, because a control that does not apply asks nothing of
	// anyone. Splitting MANUAL in two did not need a fifth tag; the two
	// sections differ in what they ask for, not in how loudly.
	groups := []struct {
		header string
		tag    console.Tag
		match  func(engine.Finding) bool
		show   bool
		// fix is whether the one-line remediation belongs under this section's
		// verdicts. It does not under Passed or Not applicable: telling someone
		// how to change a setting that is already right, or that the control
		// does not apply to, reads as an instruction to go and break it.
		fix bool
	}{
		{"Failed", console.Fail, func(f engine.Finding) bool { return f.Status == engine.StatusFail }, true, true},
		{"Not evaluated", console.Warn, unevaluated, true, false},
		{"Needs manual review", console.Warn, func(f engine.Finding) bool {
			return f.Status == engine.StatusManual && !f.Automated
		}, true, true},
		{"Passed", console.Pass, func(f engine.Finding) bool { return f.Status == engine.StatusPass }, opts.ShowPassed, false},
		{"Not applicable", console.Info, func(f engine.Finding) bool { return f.Status == engine.StatusNA }, opts.ShowPassed, false},
	}

	for _, g := range groups {
		if !g.show {
			continue
		}
		matching := filter(rep.Findings, g.match)
		if len(matching) == 0 {
			continue
		}
		section(t, p, fmt.Sprintf("%s (%d)", g.header, len(matching)))

		if g.header == "Not evaluated" {
			writeUnevaluated(t, p, matching, width)
			continue
		}
		writeCheckBlocks(t, p, matching, g.tag, width, opts.MaxResources, g.fix)
	}
}

// writeCheckBlocks renders findings grouped by control: one tagged heading per
// control, then its distinct verdicts beneath it.
//
// The column widths are measured across the whole section before anything is
// written. Padding each line against its own content let the text start at a
// different column on every finding — "instance" and "PLAT/legacy-billing" put
// the sentence eleven columns apart — and a left edge that moves is one the eye
// has to find again on every line.
func writeCheckBlocks(t console.Writer, p painter, findings []engine.Finding, tag console.Tag, width, maxResources int, withFix bool) {
	blocks := groupByCheck(findings)

	idWidth := 0
	resourceWidth := 0
	for _, block := range blocks {
		if n := len(block[0].CheckID); n > idWidth {
			idWidth = n
		}
		for _, v := range groupByMessage(block) {
			if len(v.resources) != 1 {
				continue
			}
			if n := len([]rune(v.resources[0])); n > resourceWidth {
				resourceWidth = n
			}
		}
	}
	if resourceWidth > maxResourceColumn {
		resourceWidth = maxResourceColumn
	}

	for _, block := range blocks {
		first := block[0]
		// The control's own line carries the verdict tag; everything beneath it
		// is context for that verdict and is tagged INFO. One control therefore
		// contributes exactly one PASS/FAIL/WARN to the column, however many
		// repositories it covers.
		heading := fmt.Sprintf("%s  %s  ",
			p.paint(ansiCyan+ansiBold, pad(first.CheckID, idWidth)),
			severityLabel(p, first.Severity),
		)
		t.Wrapped(tag, width, heading, idWidth+2+severityWidth+2, first.Title)

		for _, v := range groupByMessage(block) {
			writeVerdict(t, p, v, width, maxResources, resourceWidth)
		}
		if withFix && first.FixSummary != "" {
			t.Wrapped(console.Info, width, p.paint(ansiDim, "      fix: "), 11, first.FixSummary)
		}
	}
}

// writeUnevaluated collapses the controls this run could not decide into one
// entry per resource.
//
// This section groups by resource rather than by control because its cause is a
// property of the resource: one repository the token could not read produced
// thirteen entries, each with its own heading and its own remediation, and
// every one of them was the same 403. Named once, with the controls it cost
// listed after it, the same information is four lines and points at the thing
// to actually fix.
func writeUnevaluated(t console.Writer, p painter, findings []engine.Finding, width int) {
	t.Wrapped(console.Info, width, "    ", 4,
		"No verdict was reached for these. The scan could not read what the control asks about — "+
			"widen the token's access, check the scan warnings above, and run again.")

	byResource := map[string][]engine.Finding{}
	var order []string
	for _, f := range findings {
		if _, seen := byResource[f.Resource]; !seen {
			order = append(order, f.Resource)
		}
		byResource[f.Resource] = append(byResource[f.Resource], f)
	}
	sort.Slice(order, func(a, b int) bool {
		if len(byResource[order[a]]) != len(byResource[order[b]]) {
			return len(byResource[order[a]]) > len(byResource[order[b]])
		}
		return order[a] < order[b]
	})

	resourceWidth := 0
	for _, name := range order {
		if n := len([]rune(name)); n > resourceWidth {
			resourceWidth = n
		}
	}
	if resourceWidth > maxResourceColumn {
		resourceWidth = maxResourceColumn
	}

	for _, name := range order {
		group := byResource[name]
		prefix := p.paint(ansiDim, pad(truncate(name, maxResourceColumn), resourceWidth)) + "  "
		t.Wrapped(console.Warn, width, prefix, resourceWidth+2,
			fmt.Sprintf("%s could not be evaluated", console.Pluralize(len(group), "control")))

		// Every ID, in benchmark order, never summarised as "and 8 more". The
		// point of this section is to say exactly what went unanswered, and a
		// truncated list would leave the reader unable to tell whether the
		// controls they care about are among them. They are short enough that
		// thirteen of them cost two lines.
		sort.SliceStable(group, func(a, b int) bool { return checks.LessCISID(group[a].CISID, group[b].CISID) })
		ids := make([]string, len(group))
		for i, f := range group {
			ids[i] = f.CheckID
		}
		t.Wrapped(console.Info, width, "    ", 4, strings.Join(ids, " "))
	}
}

// writeRemediations lists the full fix for every control that needs one, once
// each.
//
// A control that failed on one repository and needs a person on another appears
// once here, not twice: the settings path is a property of the control, not of
// the verdict.
//
// Controls that merely went unevaluated are left out. Their settings path is
// not known to be wrong — the scan never saw it — and printing how to change it
// would say the opposite. What those need is in the Not evaluated section, and
// it is access, not configuration.
func writeRemediations(t console.Writer, rep *engine.Report, p painter, width int) {
	seen := map[string]bool{}
	var ordered []engine.Finding
	for _, f := range rep.Findings {
		if f.Status != engine.StatusFail && f.Status != engine.StatusManual {
			continue
		}
		if unevaluated(f) || seen[f.CheckID] {
			continue
		}
		seen[f.CheckID] = true
		ordered = append(ordered, f)
	}
	if len(ordered) == 0 {
		return
	}

	idWidth := 0
	for _, f := range ordered {
		if n := len(f.CheckID); n > idWidth {
			idWidth = n
		}
	}

	section(t, p, fmt.Sprintf("Remediations (%d)", len(ordered)))
	for _, f := range ordered {
		prefix := p.paint(ansiCyan+ansiBold, pad(f.CheckID, idWidth)) + "  "
		t.Wrapped(console.Info, width, prefix, idWidth+2, f.Remediation)
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

func writeVerdict(t console.Writer, p painter, v verdict, width, maxResources, resourceWidth int) {
	// One resource is the common case on a small instance, and naming it on the
	// same line as its verdict is more readable than spreading it over two.
	if len(v.resources) == 1 {
		name := truncate(v.resources[0], maxResourceColumn)
		prefix := "    " + p.paint(ansiDim, pad(name, resourceWidth)) + "  "
		t.Wrapped(console.Info, width, prefix, 4+resourceWidth+2, v.details)
	} else {
		t.Wrapped(console.Info, width, "    ", 4, v.details)
	}
	for _, e := range v.evidence {
		t.Wrapped(console.Info, width, p.paint(ansiDim, "      · "), 8, e)
	}
	if len(v.resources) > 1 {
		t.Wrapped(console.Info, width, "      ", 6, p.paint(ansiDim, resourceList(v.resources, v.resourceType, maxResources)))
	}
}

// maxResourceColumn bounds the width the resource names may claim. A project
// key and a slug are usually short, but nothing stops either being long, and
// one outlier should not push every sentence in the section off the screen.
const maxResourceColumn = 24

// severityWidth is what severityLabel pads to. It is a constant here because
// the heading has to reserve the column before the label is rendered, and a
// coloured label cannot be measured with len.
const severityWidth = 6

func pad(s string, width int) string {
	if n := len([]rune(s)); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
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

// writeWarnings reports on the scan itself rather than on the instance.
//
// It comes before the findings because it is what decides how much of them to
// believe. A 403 that cost the scan a whole repository explains a run of
// unevaluated controls further down, and printing that explanation after them
// meant the reader met the symptom several screens before the cause.
func writeWarnings(t console.Writer, rep *engine.Report, p painter, width int) {
	// These get their own tags rather than being folded into the verdict
	// column. A reader grepping for '^\[WARN\]' is asking "what did this scan
	// fail to see?", which is a different question from "what is wrong with my
	// instance?".
	if len(rep.Metadata.Warnings) > 0 {
		section(t, p, "Scan warnings")
		for _, warning := range rep.Metadata.Warnings {
			// A hanging indent, so a warning that runs to three lines still
			// reads as one warning rather than three.
			t.Wrapped(console.Warn, width, "", 2, warning)
		}
	}
	if len(rep.Errors) > 0 {
		section(t, p, "Policy errors")
		for _, e := range rep.Errors {
			// A policy that would not evaluate is a failure of the tool, and
			// FAIL is the tag that says so without inventing a fifth word.
			t.Wrapped(console.Fail, width, "", 2, e)
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

func filter(findings []engine.Finding, keep func(engine.Finding) bool) []engine.Finding {
	var out []engine.Finding
	for _, f := range findings {
		if keep(f) {
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
