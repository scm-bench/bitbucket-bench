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

// ANSI codes come from internal/console so the report and the scan trace cannot
// drift apart. The local names are kept because they read better at the call
// sites here, where nearly every line paints something.
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

// writeTable renders the report as trivy does: a summary of every resource
// first, then one section per resource, each a table of what that resource got
// wrong.
//
// Grouping by resource rather than by control is a trade with a name. A control
// that fails identically across fifty repositories appears fifty times, once in
// each repository's table, where grouping by control would have read it as the
// single misconfiguration it is. What is bought is the question a reader
// actually arrives with — "what is wrong with *my* repository" — answered
// without reading anything about anybody else's.
//
// The full remediation stays out of the tables and keeps its own section at the
// end. Each fix is a paragraph naming a settings path, and a paragraph does not
// belong in a cell; the one-line form rides in the Finding column instead.
func writeTable(w io.Writer, rep *engine.Report, opts Options) error {
	p := painter{enabled: opts.Color}
	width := console.Width()

	writeHeader(w, rep, p, width)
	writeNotice(w, p, width, opts.Notice)
	writeSummary(w, rep, p, width)
	writeReportSummary(w, rep, p, width, opts)
	writeWarnings(w, rep, p, width)
	writeResourceSections(w, rep, p, width, opts)
	if !opts.NoRemediations {
		writeRemediations(w, rep, p, width)
	}
	return nil
}

func line(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format+"\n", args...)
}

func blank(w io.Writer) { fmt.Fprintln(w) }

// prose writes wrapped text with a hanging indent, for the parts of the report
// that are paragraphs rather than table cells.
func prose(w io.Writer, width int, prefix string, prefixWidth int, text string) {
	lines := console.Wrap(text, width-prefixWidth)
	pad := strings.Repeat(" ", prefixWidth)
	for i, l := range lines {
		if i == 0 {
			line(w, "%s%s", prefix, l)
			continue
		}
		line(w, "%s%s", pad, l)
	}
}

// writeHeader names what was scanned and when.
//
// It splits rather than wraps. The parts are separated by a padded "·" that
// wrapping would collapse to a single space, and breaking between two of them
// mid separator reads as damage, so instead the line sheds a part at a time:
// first the timestamp, then the URL. A base URL longer than the terminal is
// still longer than the terminal — it is one unbreakable token, and cutting it
// would produce something nobody can paste back.
func writeHeader(w io.Writer, rep *engine.Report, p painter, width int) {
	m := rep.Metadata
	stamp := m.GeneratedAt.Format("2006-01-02 15:04:05 MST")
	name := p.paint(ansiBold, "scm-bench")

	if fits(width, "scm-bench %s  ·  %s  ·  %s", m.ToolVersion, m.BaseURL, stamp) {
		line(w, "%s %s  ·  %s  ·  %s", name, m.ToolVersion, m.BaseURL, stamp)
		return
	}
	if fits(width, "scm-bench %s  ·  %s", m.ToolVersion, m.BaseURL) {
		line(w, "%s %s  ·  %s", name, m.ToolVersion, m.BaseURL)
		line(w, "%s", p.paint(ansiDim, "scanned "+stamp))
		return
	}
	line(w, "%s %s", name, m.ToolVersion)
	line(w, "%s", m.BaseURL)
	line(w, "%s", p.paint(ansiDim, "scanned "+stamp))
}

// writeNotice renders Options.Notice directly under the header, bold and
// yellow: the one thing a reader must take in before believing anything the
// tables below say. Yellow because the tag vocabulary already uses it for
// "needs a person's judgement", which is exactly what a caveat is.
func writeNotice(w io.Writer, p painter, width int, text string) {
	if text == "" {
		return
	}
	blank(w)
	for _, l := range console.Wrap(text, width) {
		line(w, "%s", p.paint(ansiBold+ansiYellow, l))
	}
}

// fits reports whether the uncoloured form of a line stays inside the width.
func fits(width int, format string, args ...any) bool {
	return len([]rune(fmt.Sprintf(format, args...))) <= width
}

// writeSummary opens with the score and the counts behind it.
//
// Every line below the score states its own arithmetic, so the numbers are
// checkable rather than something to trust. The state names are PASS, FAIL,
// MANUAL and NA — the ones in the JSON and in `list-checks`.
func writeSummary(w io.Writer, rep *engine.Report, p painter, width int) {
	s := rep.Score

	scoreColor := ansiGreen
	switch {
	case s.Value < 50:
		scoreColor = ansiRed
	case s.Value < 80:
		scoreColor = ansiYellow
	}
	blank(w)

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
	if len(score)+3+len(counts) <= width {
		line(w, "%s   %s", head, painted)
	} else {
		// Four counts of four digits each is 46 columns, which fits inside the
		// narrowest width Width reports, so this never needs wrapping again.
		line(w, "%s", head)
		line(w, "      %s", painted)
	}

	summaryLine(w, p, width, ansiDim, fmt.Sprintf(
		"weighted %d/%d (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)",
		s.EarnedWeight, s.TotalWeight,
	))

	// How much of the instance the score is actually based on.
	//
	// Excluding MANUAL from both sides of the fraction is right control by
	// control — an instance should not be marked down for a question its API
	// cannot answer — and misleading in aggregate, because it shrinks the
	// denominator. A token that could read a tenth of an instance scored 78
	// where a working one scored 53. This is the line that says the sample was
	// small, and it turns yellow when most of the instance went unseen.
	if scored := s.Passed + s.Failed; s.Manual > 0 {
		decidable := scored + s.Manual
		coverage := scored * 100 / decidable
		colour := ansiDim
		if coverage < 50 {
			colour = ansiYellow
		}
		summaryLine(w, p, width, colour, fmt.Sprintf(
			"scored %d of %d controls (%d%%); %d could not be evaluated",
			scored, decidable, coverage, s.Manual))
	}
}

// summaryLine writes one indented, wrapped line of the summary block.
//
// Colour goes on after wrapping, one line at a time. An escape sequence is
// width the reader never sees, so measuring a coloured string breaks the line
// early on a terminal that had room for it.
func summaryLine(w io.Writer, p painter, width int, colour, text string) {
	const indent = 6
	for _, l := range console.Wrap(text, width-indent) {
		line(w, "%s%s", strings.Repeat(" ", indent), p.paint(colour, l))
	}
}

// unread reports whether a finding is a control the tool would normally decide
// but could not, as opposed to one no API can answer.
//
// Both are MANUAL, and treating them as one state is what made the sample
// instance report nineteen controls needing a person when six of them did. The
// other thirteen were one unreadable repository. Automated is the metadata that
// already knows the difference: false means the control is documented as
// unanswerable and always reports MANUAL; true means this run in particular
// came up short.
//
// This is a rendering distinction only. The JSON and the SARIF both still say
// MANUAL, because that is what the control returned.
func unread(f engine.Finding) bool {
	return f.Status == engine.StatusManual && f.Automated
}

// Status labels as they appear in the Status column.
const (
	statusFail   = "FAIL"
	statusUnread = "UNREAD"
	statusManual = "MANUAL"
	statusPass   = "PASS"
	statusNA     = "N/A"
)

func statusLabel(f engine.Finding) string {
	switch {
	case f.Status == engine.StatusFail:
		return statusFail
	case unread(f):
		return statusUnread
	case f.Status == engine.StatusManual:
		return statusManual
	case f.Status == engine.StatusPass:
		return statusPass
	default:
		return statusNA
	}
}

func statusColour(p painter) func(string) string {
	return func(s string) string {
		switch s {
		case statusFail:
			return p.paint(ansiRed, s)
		case statusUnread, statusManual:
			return p.paint(ansiYellow, s)
		case statusPass:
			return p.paint(ansiGreen, s)
		default:
			return p.paint(ansiDim, s)
		}
	}
}

func severityColour(p painter) func(string) string {
	return func(s string) string {
		switch strings.ToUpper(s) {
		case checks.SeverityHigh:
			return p.paint(ansiRed, s)
		case checks.SeverityMedium:
			return p.paint(ansiYellow, s)
		default:
			return p.paint(ansiDim, s)
		}
	}
}

// resourceTally is one row of the report summary: a resource and how its
// controls came out.
type resourceTally struct {
	name   string
	kind   string
	failed int
	unread int
	manual int
	passed int
	na     int
}

func tallyResources(findings []engine.Finding) []resourceTally {
	index := map[string]int{}
	var out []resourceTally
	for _, f := range findings {
		i, ok := index[f.Resource]
		if !ok {
			i = len(out)
			index[f.Resource] = i
			out = append(out, resourceTally{name: f.Resource, kind: f.ResourceType})
		}
		switch statusLabel(f) {
		case statusFail:
			out[i].failed++
		case statusUnread:
			out[i].unread++
		case statusManual:
			out[i].manual++
		case statusPass:
			out[i].passed++
		default:
			out[i].na++
		}
	}

	// Worst first, and stable between two runs that found the same thing.
	sort.Slice(out, func(a, b int) bool {
		x, y := out[a], out[b]
		switch {
		case x.failed != y.failed:
			return x.failed > y.failed
		case x.unread != y.unread:
			return x.unread > y.unread
		case x.manual != y.manual:
			return x.manual > y.manual
		}
		return x.name < y.name
	})
	return out
}

// count renders a tally cell. Zero is a dash rather than a nought, which is
// trivy's idiom and the right one: the eye skips a dash and stops on a digit,
// so a table of mostly-clean resources shows only the numbers that matter.
func count(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprint(n)
}

// writeReportSummary is the table that answers "where do I start".
//
// The report proper groups by resource, which tells a reader everything about
// one repository and nothing about how it compares. This is the comparison:
// every resource, worst first, so the instance's shape is visible before any of
// the detail is.
func writeReportSummary(w io.Writer, rep *engine.Report, p painter, width int, opts Options) {
	tallies := tallyResources(rep.Findings)
	if len(tallies) == 0 {
		return
	}

	blank(w)
	line(w, "%s", p.paint(ansiBold, "Report Summary"))
	blank(w)

	cols := []console.Column{
		{Header: "Resource", Align: console.AlignLeft},
		{Header: "Type", Align: console.AlignLeft, Colour: func(s string) string { return p.paint(ansiDim, s) }},
		{Header: "Failed", Align: console.AlignRight, Colour: func(s string) string {
			if s == "-" {
				return p.paint(ansiDim, s)
			}
			return p.paint(ansiRed, s)
		}},
		{Header: "Unread", Align: console.AlignRight, Colour: func(s string) string {
			if s == "-" {
				return p.paint(ansiDim, s)
			}
			return p.paint(ansiYellow, s)
		}},
		{Header: "Manual", Align: console.AlignRight, Colour: func(s string) string {
			if s == "-" {
				return p.paint(ansiDim, s)
			}
			return p.paint(ansiYellow, s)
		}},
		{Header: "Passed", Align: console.AlignRight, Colour: func(s string) string {
			if s == "-" {
				return p.paint(ansiDim, s)
			}
			return p.paint(ansiGreen, s)
		}},
	}
	rows := make([][]string, 0, len(tallies))
	for _, t := range tallies {
		row := []string{t.name, t.kind, count(t.failed), count(t.unread), count(t.manual), count(t.passed)}
		if opts.ShowPassed {
			row = append(row, count(t.na))
		}
		rows = append(rows, row)
	}
	if opts.ShowPassed {
		cols = append(cols, console.Column{Header: "N/A", Align: console.AlignRight,
			Colour: func(s string) string { return p.paint(ansiDim, s) }})
	}

	console.RenderTable(w, width, cols, rows)

	// A legend, because a dash and the word UNREAD are both things the table
	// invents. Everything else in it is a word the rest of the tool already
	// uses.
	line(w, "%s", p.paint(ansiDim, "Legend:"))
	for _, entry := range []string{
		"'-': none in this state",
		"'Unread': the scan could not read what the control asks about",
	} {
		for i, l := range console.Wrap(entry, width-2) {
			prefix := "- "
			if i > 0 {
				prefix = "  "
			}
			line(w, "%s", p.paint(ansiDim, prefix+l))
		}
	}
}

// writeWarnings reports on the scan itself rather than on the instance.
//
// It comes before the findings because it is what decides how much of them to
// believe. A 403 that cost the scan a whole repository explains a column of
// UNREAD further down, and printing that explanation after them meant the
// reader met the symptom several screens before the cause.
func writeWarnings(w io.Writer, rep *engine.Report, p painter, width int) {
	if len(rep.Metadata.Warnings) > 0 {
		blank(w)
		line(w, "%s", p.paint(ansiBold+ansiYellow, "Scan warnings"))
		blank(w)
		for _, warning := range rep.Metadata.Warnings {
			prose(w, width, "  ", 2, warning)
		}
	}
	if len(rep.Errors) > 0 {
		blank(w)
		line(w, "%s", p.paint(ansiBold+ansiRed, "Policy errors"))
		blank(w)
		for _, e := range rep.Errors {
			prose(w, width, "  ", 2, e)
		}
	}
}

// writeResourceSections is the body of the report: one heading, one total and
// one table per resource, in trivy's shape.
func writeResourceSections(w io.Writer, rep *engine.Report, p painter, width int, opts Options) {
	tallies := tallyResources(rep.Findings)

	byResource := map[string][]engine.Finding{}
	for _, f := range rep.Findings {
		byResource[f.Resource] = append(byResource[f.Resource], f)
	}

	shown := 0
	skipped := 0
	for _, t := range tallies {
		findings := selectForSection(byResource[t.name], opts.ShowPassed)
		if len(findings) == 0 {
			continue
		}
		if opts.MaxResources > 0 && shown >= opts.MaxResources {
			skipped++
			continue
		}
		shown++
		writeResourceSection(w, p, width, t, findings)
	}

	if skipped > 0 {
		blank(w)
		prose(w, width, "", 0, fmt.Sprintf(
			"%s with findings not shown (--max-resources %d); use -o json for all of them.",
			console.Pluralize(skipped, "more resource"), opts.MaxResources))
	}
}

// selectForSection picks the findings a resource's table lists. A report is a
// list of things to do, so passes and not-applicables stay out of it unless
// they were asked for.
func selectForSection(findings []engine.Finding, showPassed bool) []engine.Finding {
	var out []engine.Finding
	for _, f := range findings {
		switch f.Status {
		case engine.StatusFail, engine.StatusManual:
			out = append(out, f)
		default:
			if showPassed {
				out = append(out, f)
			}
		}
	}
	return out
}

func writeResourceSection(w io.Writer, p painter, width int, t resourceTally, findings []engine.Finding) {
	blank(w)
	line(w, "%s %s", p.paint(ansiBold, t.name), p.paint(ansiDim, "("+t.kind+")"))
	blank(w)

	// Severities ascending, as trivy writes them: the reader scans right to the
	// number that decides whether this resource is tonight's problem.
	var parts []string
	for _, sev := range []string{checks.SeverityLow, checks.SeverityMedium, checks.SeverityHigh} {
		n := 0
		for _, f := range findings {
			if strings.EqualFold(f.Severity, sev) {
				n++
			}
		}
		parts = append(parts, fmt.Sprintf("%s: %d", sev, n))
	}
	line(w, "Total: %d (%s)", len(findings), strings.Join(parts, ", "))
	blank(w)

	cols := []console.Column{
		{Header: "Control", Align: console.AlignLeft,
			Colour: func(s string) string { return p.paint(ansiCyan, s) }},
		{Header: "Severity", Align: console.AlignLeft, Colour: severityColour(p)},
		{Header: "Status", Align: console.AlignLeft, Colour: statusColour(p)},
		{Header: "Title", Align: console.AlignLeft, Flex: true},
		{Header: "Finding", Align: console.AlignLeft, Flex: true},
	}

	rows := make([][]string, 0, len(findings))
	for _, f := range findings {
		rows = append(rows, []string{
			f.CheckID,
			strings.ToUpper(f.Severity),
			statusLabel(f),
			f.Title,
			findingCell(f),
		})
	}
	console.RenderTable(w, width, cols, rows)
}

// findingCell is what this resource did, what the scan saw when it looked, and
// the one move that changes it — three kinds of thing, on their own lines
// inside one cell, the way trivy puts a CVE's title and its URL together.
//
// A pass gets no fix. Telling someone how to change a setting that is already
// right reads as an instruction to go and break it, and a control the scan
// never read is not known to be wrong at all — what that one needs is access,
// which is what the scan warnings are for.
func findingCell(f engine.Finding) string {
	parts := []string{f.Details}
	for _, e := range f.Evidence {
		parts = append(parts, "· "+e)
	}
	if f.FixSummary != "" && (f.Status == engine.StatusFail || (f.Status == engine.StatusManual && !f.Automated)) {
		parts = append(parts, "fix: "+f.FixSummary)
	}
	return strings.Join(parts, "\n")
}

// writeRemediations lists the full fix for every control that needs one, once
// each.
//
// It stays prose. These are paragraphs naming settings paths, project-level
// variants and config keys, and a paragraph in a table cell is a column of
// three-word lines. trivy leaves its legend as prose for the same reason.
//
// A control that failed on one repository and needs a person on another appears
// once here, not twice: the settings path is a property of the control, not of
// the verdict. Controls that merely went unread are left out — their settings
// are not known to be wrong, and printing how to change them would say
// otherwise.
func writeRemediations(w io.Writer, rep *engine.Report, p painter, width int) {
	seen := map[string]bool{}
	var ordered []engine.Finding
	for _, f := range rep.Findings {
		if f.Status != engine.StatusFail && f.Status != engine.StatusManual {
			continue
		}
		if unread(f) || seen[f.CheckID] {
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

	blank(w)
	line(w, "%s", p.paint(ansiBold, fmt.Sprintf("Remediations (%d)", len(ordered))))
	blank(w)
	for _, f := range ordered {
		id := f.CheckID + strings.Repeat(" ", idWidth-len(f.CheckID))
		prose(w, width, "  "+p.paint(ansiCyan+ansiBold, id)+"  ", idWidth+4, f.Remediation)
	}
}
