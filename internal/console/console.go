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
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
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

// Pluralize renders a count with its noun, adding "s" for anything but one.
//
// It lives here rather than in either command because both need it and they
// must not disagree: the scan report went to the trouble of writing real
// grammar instead of "1 control(s)", and diff was writing "control(s)" three
// files away. A shared helper is how that stops drifting.
func Pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Blank separates blocks. It is deliberately a real empty line rather than a
// bare "[INFO]": the tag column exists to be scanned down, and a column of tags
// attached to nothing makes that harder, not easier.
func (w Writer) Blank() { fmt.Fprintln(w.W) }

// Width limits are deliberately narrow. 80 is what a pipe, a CI log and an
// unconfigured terminal all are; 100 is where a line stops being comfortable to
// read regardless of how wide the window is.
const (
	fallbackWidth = 80
	minWidth      = 60
	maxWidth      = 100
)

// Width reports how wide a rendered line may be.
//
// It reads COLUMNS rather than asking the kernel for the window size. The ioctl
// is not portable across the three operating systems this ships on, and
// golang.org/x/term is not worth adding to a supply chain security tool for one
// integer — the same trade already made for terminal detection in the CLI. The
// cost is that an unexported COLUMNS gets 80, which is the right answer for a
// pipe and a safe one for a terminal; a reader who wants their full window can
// export it.
func Width() int {
	w := fallbackWidth
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		w = n
	}
	if w < minWidth {
		return minWidth
	}
	if w > maxWidth {
		return maxWidth
	}
	return w
}

// Wrap breaks s into lines of at most width columns, returning at least one
// line. The caller supplies the width already reduced by whatever prefix and
// indent it intends to put in front, and indents the continuations itself, so
// the text keeps a straight left edge.
//
// It must be called before any colour is applied: an escape sequence is bytes
// the reader never sees, and counting it as width is what made the request
// trace's columns drift before it padded by hand.
//
// A word longer than width is left whole on its own line rather than split. The
// long words here are settings paths, config keys and URLs, and a reader who
// cannot select one of those in a double click has lost more than the ragged
// right margin cost them.
func Wrap(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	if width <= 0 {
		return []string{strings.Join(words, " ")}
	}

	var lines []string
	line := words[0]
	used := utf8.RuneCountInString(line)
	for _, word := range words[1:] {
		n := utf8.RuneCountInString(word)
		if used+1+n > width {
			lines = append(lines, line)
			line, used = word, n
			continue
		}
		line += " " + word
		used += 1 + n
	}
	return append(lines, line)
}

// TagWidth is what a rendered tag and its trailing space occupy, e.g. "[FAIL] ".
// Wrapping has to subtract it, and it is a constant here rather than a literal
// at each call site because the tag column is a contract.
const TagWidth = 7

// Wrapped writes s across as many tagged lines as it needs. The first carries
// prefix, every continuation carries prefixWidth spaces in its place, and the
// tag column is preserved on all of them — a continuation is still a line of
// the report, and `grep '^\[' ` must not have holes in it.
//
// prefix may be coloured; prefixWidth is its width on screen, which is why the
// caller passes it rather than having it measured.
func (w Writer) Wrapped(t Tag, width int, prefix string, prefixWidth int, s string) {
	lines := Wrap(s, width-TagWidth-prefixWidth)
	w.Line(t, "%s%s", prefix, lines[0])
	pad := strings.Repeat(" ", prefixWidth)
	for _, line := range lines[1:] {
		w.Line(Info, "%s%s", pad, line)
	}
}
