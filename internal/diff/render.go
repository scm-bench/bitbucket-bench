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
	Lang   string
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
		r = localize(r, opts.Lang)
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

func writeTable(w io.Writer, r *Result, opts Options) error {
	c := opts.Color

	fmt.Fprintf(w, "%s  %s\n", paint(c, ansiBold, "scm-bench diff"), paint(c, ansiDim, sideLabel(r)))
	writeScoreLine(w, r, c)

	sections := []struct {
		changes []Change
		header  string
		colour  string
		// arrow is false where a from-status would be noise: a new resource has
		// no previous verdict to move from.
		arrow bool
	}{
		{r.Regressed, "REGRESSED", ansiRed, true},
		{r.NewFailures, "NEW FAILURES", ansiYellow, false},
		{r.Fixed, "FIXED", ansiGreen, true},
		{r.Changed, "OTHER CHANGES", ansiYellow, true},
		{r.Departed, "GONE", ansiDim, false},
	}

	var any bool
	for _, s := range sections {
		if len(s.changes) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(w, "\n%s %s\n", paint(c, ansiBold+s.colour, s.header), paint(c, ansiDim, fmt.Sprintf("(%d)", len(s.changes))))
		fmt.Fprintln(w, paint(c, ansiDim, strings.Repeat("─", 76)))
		for _, ch := range s.changes {
			writeChange(w, ch, opts, s.arrow)
		}
	}

	if !any {
		fmt.Fprintf(w, "\n%s\n", paint(c, ansiGreen, "No control changed verdict."))
	}

	// Remediation is printed once, for the regressions only: those are the ones
	// somebody is expected to act on right now.
	if len(r.Regressed) > 0 {
		fmt.Fprintf(w, "\n%s\n", paint(c, ansiBold, "HOW TO FIX THE REGRESSIONS"))
		for _, id := range distinctChecks(r.Regressed) {
			for _, ch := range r.Regressed {
				if ch.CheckID != id {
					continue
				}
				fmt.Fprintf(w, "  %s  %s\n", paint(c, ansiBold, ch.CheckID), remediation(ch, opts.Lang))
				break
			}
		}
	}

	fmt.Fprintln(w)
	return nil
}

func writeScoreLine(w io.Writer, r *Result, c bool) {
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

	fmt.Fprintf(w, "%s  %d %s %d   %s\n",
		paint(c, ansiBold, "SCORE"),
		before, arrow, after,
		paint(c, colour, fmt.Sprintf("(%s%d)", sign, delta)),
	)
	// The weighted arithmetic moves for two different reasons — a verdict
	// changed, or the number of applicable controls did — and the score alone
	// cannot tell them apart.
	fmt.Fprintf(w, "%s\n", paint(c, ansiDim, fmt.Sprintf(
		"       weighted %d/%d → %d/%d",
		r.Before.Score.EarnedWeight, r.Before.Score.TotalWeight,
		r.After.Score.EarnedWeight, r.After.Score.TotalWeight,
	)))
}

func writeChange(w io.Writer, ch Change, opts Options, arrow bool) {
	c := opts.Color

	transition := string(ch.To)
	if arrow && ch.From != "" {
		transition = fmt.Sprintf("%s → %s", ch.From, ch.To)
	}
	if ch.To == "" {
		transition = fmt.Sprintf("%s → gone", ch.From)
	}

	fmt.Fprintf(w, "  %s  %s  %s  %s\n",
		paint(c, ansiBold, ch.CheckID),
		severityLabel(c, ch.Severity),
		ch.Resource,
		paint(c, ansiDim, transition),
	)
	if ch.Details != "" && ch.To != "" {
		fmt.Fprintf(w, "      %s\n", paint(c, ansiDim, ch.Details))
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

// localize resolves each change's text to one language and clears the
// per-language variants, so a consumer reads one field rather than choosing
// between them. Same reasoning as the scan report, and the two must agree:
// nobody should have to learn a different rule per subcommand.
//
// Copied rather than edited, since the result belongs to the caller.
func localize(r *Result, lang string) *Result {
	out := *r
	for _, set := range []*[]Change{
		&out.Regressed, &out.Fixed, &out.NewFailures, &out.Changed, &out.Departed,
	} {
		localized := make([]Change, len(*set))
		for i, ch := range *set {
			ch.Title = title(ch, lang)
			ch.Remediation = remediation(ch, lang)
			ch.TitleZh = ""
			ch.RemediationZh = ""
			localized[i] = ch
		}
		*set = localized
	}
	return &out
}

// title picks the localised title, falling back to English so a control with no
// translation never renders blank.
func title(ch Change, lang string) string {
	if lang == report.LangChinese && ch.TitleZh != "" {
		return ch.TitleZh
	}
	return ch.Title
}

func remediation(ch Change, lang string) string {
	if lang == report.LangChinese && ch.RemediationZh != "" {
		return ch.RemediationZh
	}
	return ch.Remediation
}
