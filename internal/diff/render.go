package diff

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/scm-bench/scm-bench/internal/checks"
	"github.com/scm-bench/scm-bench/internal/console"
	"github.com/scm-bench/scm-bench/internal/report"
)

// Options controls how a comparison is rendered.
type Options struct {
	Format string
	Color  bool
}

// The codes come from internal/console, which is the one place they are
// defined. Three private copies of the same six constants is how two renderers
// end up disagreeing about what "dim" is without anything failing.
const (
	ansiReset  = console.Reset
	ansiBold   = console.Bold
	ansiDim    = console.Dim
	ansiRed    = console.Red
	ansiGreen  = console.Green
	ansiYellow = console.Yellow
)

// Write renders the comparison.
func Write(w io.Writer, r *Result, opts Options) error {
	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "", report.FormatTable:
		return writeTable(w, r, opts)
	case report.FormatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(r)
	default:
		return fmt.Errorf("unknown output format %q for diff; want table or json", opts.Format)
	}
}

func paint(enabled bool, code, s string) string {
	return console.Painter{Enabled: enabled}.Paint(code, s)
}

// writeTable renders the comparison in the same shape as the scan report.
//
// Every line carries the tag column, and it carries it for the same reasons
// the scan report does: the tags are a fixed-width first column so verdicts
// line up down the page, and `scm-bench diff ... | grep '^\[FAIL\]'` answers
// "what got worse". Those reasons do not stop applying because this is a
// different subcommand, and this renderer used to skip the column entirely —
// so a diff looked like a different program's output, right up to the closing
// line, which came from main and did carry a tag.
func writeTable(w io.Writer, r *Result, opts Options) error {
	c := opts.Color
	t := console.Writer{W: w, P: console.Painter{Enabled: c}}

	t.Info("%s  %s", paint(c, ansiBold, "scm-bench diff"), paint(c, ansiDim, sideLabel(r)))
	writeScoreLine(t, r, c)

	sections := []struct {
		changes []Change
		header  string
		colour  string
		// tag is how the section reads in column one. A regression and a new
		// failure are failures; a fix is a pass; a change that involves MANUAL
		// is something a person has to look at; a departed resource is neither
		// good nor bad news.
		tag console.Tag
		// arrow is false where a from-status would be noise: a new resource has
		// no previous verdict to move from.
		arrow bool
	}{
		{r.Regressed, "REGRESSED", ansiRed, console.Fail, true},
		{r.NewFailures, "NEW FAILURES", ansiYellow, console.Fail, false},
		{r.Fixed, "FIXED", ansiGreen, console.Pass, true},
		{r.Changed, "OTHER CHANGES", ansiYellow, console.Warn, true},
		{r.Departed, "GONE", ansiDim, console.Info, false},
	}

	var any bool
	for _, s := range sections {
		if len(s.changes) == 0 {
			continue
		}
		any = true
		t.Blank()
		t.Info("%s %s", paint(c, ansiBold+s.colour, s.header), paint(c, ansiDim, fmt.Sprintf("(%d)", len(s.changes))))
		t.Info("%s", paint(c, ansiDim, strings.Repeat("─", 76)))
		for _, ch := range s.changes {
			writeChange(t, ch, opts, s.tag, s.arrow)
		}
	}

	if !any {
		t.Blank()
		t.Line(console.Pass, "%s", paint(c, ansiGreen, "No control changed verdict."))
	}

	// Remediation is printed once, for the regressions only: those are the ones
	// somebody is expected to act on right now.
	if len(r.Regressed) > 0 {
		t.Blank()
		t.Info("%s", paint(c, ansiBold, "HOW TO FIX THE REGRESSIONS"))
		for _, id := range distinctChecks(r.Regressed) {
			for _, ch := range r.Regressed {
				if ch.CheckID != id {
					continue
				}
				t.Info("  %s  %s", paint(c, ansiBold, ch.CheckID), ch.Remediation)
				break
			}
		}
	}

	t.Blank()
	return nil
}

func writeScoreLine(t console.Writer, r *Result, c bool) {
	before, after := r.Before.Score.Value, r.After.Score.Value
	delta := after - before

	arrow := paint(c, ansiDim, "→")
	sign := ""
	colour := ansiDim
	switch {
	case delta > 0:
		sign, colour = "+", ansiGreen
	case delta < 0:
		colour = ansiRed
	}

	t.Info("%s  %d %s %d   %s",
		paint(c, ansiBold, "SCORE"),
		before, arrow, after,
		paint(c, colour, fmt.Sprintf("(%s%d)", sign, delta)),
	)
	// The weighted arithmetic moves for two different reasons — a verdict
	// changed, or the number of applicable controls did — and the score alone
	// cannot tell them apart.
	t.Info("%s", paint(c, ansiDim, fmt.Sprintf(
		"       weighted %d/%d → %d/%d",
		r.Before.Score.EarnedWeight, r.Before.Score.TotalWeight,
		r.After.Score.EarnedWeight, r.After.Score.TotalWeight,
	)))
}

func writeChange(t console.Writer, ch Change, opts Options, tag console.Tag, arrow bool) {
	c := opts.Color

	transition := string(ch.To)
	if arrow && ch.From != "" {
		transition = fmt.Sprintf("%s → %s", ch.From, ch.To)
	}
	if ch.To == "" {
		transition = "gone"
	}

	// A departed resource has no control ID: it is one entry for the resource,
	// not one per control, so the column is dropped rather than padded with a
	// blank that reads like a missing value.
	head := ch.Resource
	if ch.CheckID != "" {
		head = fmt.Sprintf("%s  %s", paint(c, ansiBold, ch.CheckID), ch.Resource)
	}
	t.Line(tag, "  %s  %s  %s",
		severityLabel(c, ch.Severity),
		head,
		paint(c, ansiDim, transition),
	)
	if ch.Details != "" {
		// Detail lines are context, not a second verdict, so they carry INFO
		// exactly as they do in the scan report. Departed entries carry it too:
		// "it was failing 12 of 14 controls" is the part of a deletion worth
		// knowing, and it was being suppressed.
		t.Info("      %s", paint(c, ansiDim, ch.Details))
	}
}

func severityLabel(c bool, severity string) string {
	switch strings.ToUpper(severity) {
	case checks.SeverityHigh:
		return paint(c, ansiRed, "HIGH  ")
	case checks.SeverityMedium:
		return paint(c, ansiYellow, "MEDIUM")
	default:
		return paint(c, ansiDim, "LOW   ")
	}
}

func sideLabel(r *Result) string {
	if r.Before.GeneratedAt == "" || r.After.GeneratedAt == "" {
		return r.After.BaseURL
	}
	return fmt.Sprintf("%s  ·  %s → %s", r.After.BaseURL, r.Before.GeneratedAt, r.After.GeneratedAt)
}

func distinctChecks(changes []Change) []string {
	seen := map[string]bool{}
	var out []string
	for _, ch := range changes {
		if seen[ch.CheckID] {
			continue
		}
		seen[ch.CheckID] = true
		out = append(out, ch.CheckID)
	}
	return out
}
