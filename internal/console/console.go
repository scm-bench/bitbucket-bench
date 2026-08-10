// Package console renders the tagged, coloured lines the terminal output is
// made of.
//
// It exists because two separate things write to the terminal — the scan trace
// on stderr and the report on stdout — and they have to look like one program.
// The tag vocabulary and the colours were defined twice before, once in each,
// which is exactly the arrangement that drifts: nothing fails when a new tag is
// added on one side only, the output just quietly stops lining up.
package console

import (
	"fmt"
	"io"
)

// ANSI codes, applied only when the destination is a terminal.
const (
	Reset  = "\033[0m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Cyan   = "\033[36m"
)

// Painter applies colour, or does not.
type Painter struct{ Enabled bool }

func (p Painter) Paint(code, s string) string {
	if !p.Enabled || s == "" {
		return s
	}
	return code + s + Reset
}

// A Tag is the bracketed word in column one, the idiom kube-bench and
// docker-bench-security both use.
//
// Its value is not decoration. Column one is a fixed width, so verdicts line up
// down the page and the eye finds the failures without reading any of the text;
// and because the tag is anchored at the start of the line,
// `scm-bench scan 2>&1 | grep '^\[FAIL\]'` works. Colour reinforces the tag but
// is never the only signal — the same text survives NO_COLOR, a pipe, and a
// redirect to a file.
//
// All tags are four characters wide so nothing needs re-aligning when a verdict
// changes.
type Tag struct {
	Text  string
	Color string
}

// Four tags, the same four kube-bench uses. An earlier version had seven —
// PASS, FAIL, NOTE, N/A, INFO, WARN and ERR — which is more vocabulary than a
// reader can hold while skimming, and the extra three were distinctions the
// reader could not act on differently anyway.
var (
	Pass = Tag{"PASS", Green}
	Fail = Tag{"FAIL", Red}
	// Warn is everything that needs a person but is not a failure: a control
	// the tool could not decide (MANUAL), and an endpoint the scan could not
	// read. kube-bench uses WARN for exactly the first of those.
	//
	// This is where MANUAL goes, and it must not go to Info. "Nobody has
	// checked this" is the one thing this tool refuses to let disappear —
	// folding it into the same tag as section headers would make an
	// unevaluated control look like decoration.
	Warn = Tag{"WARN", Yellow}
	// Info is structure, context, evidence, remediation, the request trace,
	// and controls that do not apply. Nothing here is asking anything of the
	// reader.
	Info = Tag{"INFO", Cyan}
)

// Render returns the tag as it appears in column one.
func (p Painter) Render(t Tag) string { return p.Paint(t.Color, "["+t.Text+"]") }

// Writer writes lines that all begin with a tag column.
type Writer struct {
	W io.Writer
	P Painter
}

func (w Writer) Line(t Tag, format string, args ...any) {
	fmt.Fprintf(w.W, "%s %s\n", w.P.Render(t), fmt.Sprintf(format, args...))
}

func (w Writer) Info(format string, args ...any) { w.Line(Info, format, args...) }

// Blank separates blocks. It is deliberately a real empty line rather than a
// bare "[INFO]": the tag column exists to be scanned down, and a column of tags
// attached to nothing makes that harder, not easier.
func (w Writer) Blank() { fmt.Fprintln(w.W) }
