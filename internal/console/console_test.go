package console

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// The tag column is a contract: `scm-bench scan 2>&1 | grep '^\[FAIL\]'` is the
// obvious thing to reach for, and it only works if every tag is the same width
// and the brackets are literal.
func TestEveryTagIsFourCharactersWide(t *testing.T) {
	for _, tag := range []Tag{Pass, Fail, Warn, Info} {
		if len(tag.Text) != 4 {
			t.Errorf("tag %q is %d characters, want 4 so the column never shifts", tag.Text, len(tag.Text))
		}
		if got := (Painter{}).Render(tag); got != "["+tag.Text+"]" {
			t.Errorf("Render(%q) = %q", tag.Text, got)
		}
	}
}

// Four is the whole vocabulary, matching kube-bench. A fifth should be a
// decision someone makes on purpose, not something that accumulates.
func TestTheVocabularyIsExactlyFour(t *testing.T) {
	seen := map[string]bool{}
	for _, tag := range []Tag{Pass, Fail, Warn, Info} {
		if seen[tag.Text] {
			t.Errorf("duplicate tag %q", tag.Text)
		}
		seen[tag.Text] = true
	}
	if len(seen) != 4 {
		t.Errorf("vocabulary has %d tags, want 4", len(seen))
	}
}

// Colour is a reinforcement, never the signal. Everything the reader needs has
// to survive a pipe, a redirect, and NO_COLOR.
func TestDisabledPainterEmitsNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	w := Writer{W: &buf, P: Painter{Enabled: false}}
	w.Line(Fail, "CIS-1.1.15 %s", w.P.Paint(Red, "HIGH"))
	w.Blank()

	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Errorf("escapes leaked with colour disabled: %q", out)
	}
	if !strings.HasPrefix(out, "[FAIL] CIS-1.1.15 HIGH\n") {
		t.Errorf("unexpected line: %q", out)
	}
	if !strings.HasSuffix(out, "\n\n") {
		t.Errorf("Blank should write a real empty line, got %q", out)
	}
}

func TestEnabledPainterWrapsAndResets(t *testing.T) {
	p := Painter{Enabled: true}
	if got := p.Render(Pass); got != Green+"[PASS]"+Reset {
		t.Errorf("Render = %q", got)
	}
	// An empty string is left alone: wrapping nothing in escapes produces a
	// stray reset that shows up as a blank in some terminals.
	if got := p.Paint(Red, ""); got != "" {
		t.Errorf("Paint of an empty string = %q, want it untouched", got)
	}
}

func TestWrapKeepsEveryLineWithinTheWidth(t *testing.T) {
	const text = "Repository settings -> Branch permissions -> Add restriction: " +
		"select the default branch and enable Prevent rewriting history."
	for _, width := range []int{20, 40, 62, 200} {
		lines := Wrap(text, width)
		for _, line := range lines {
			if n := utf8.RuneCountInString(line); n > width {
				t.Errorf("width %d: line of %d runes: %q", width, n, line)
			}
		}
		if got := strings.Join(lines, " "); got != text {
			t.Errorf("width %d: rejoined text changed:\n got %q\nwant %q", width, got, text)
		}
	}
}

// Settings paths, config keys and URLs are the long words here, and a reader
// who cannot double click one of them has lost more than the ragged margin cost.
func TestWrapLeavesAWordTooLongToFitWhole(t *testing.T) {
	lines := Wrap("set thresholds.inactiveUserDays in config", 10)
	for _, line := range lines {
		if strings.Contains(line, "thresholds.inactiveUserDays") {
			return
		}
	}
	t.Errorf("the long word was split across %q", lines)
}

// Callers index lines[0] unconditionally, so Wrap owes them a line even for
// text that is empty or all spaces.
func TestWrapAlwaysReturnsALine(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		if got := Wrap(in, 40); len(got) != 1 || got[0] != "" {
			t.Errorf("Wrap(%q) = %#v, want one empty line", in, got)
		}
	}
	if got := Wrap("two words", 0); len(got) != 1 || got[0] != "two words" {
		t.Errorf("Wrap with a non-positive width = %#v, want the text unbroken", got)
	}
}

func TestWidthIsClampedAndDefaultsToEighty(t *testing.T) {
	for _, tc := range []struct {
		columns string
		want    int
	}{
		{"", fallbackWidth},
		{"not a number", fallbackWidth},
		{"0", fallbackWidth},
		{"-10", fallbackWidth},
		{"20", minWidth},
		{"90", 90},
		{"400", maxWidth},
	} {
		t.Setenv("COLUMNS", tc.columns)
		if got := Width(); got != tc.want {
			t.Errorf("COLUMNS=%q: Width() = %d, want %d", tc.columns, got, tc.want)
		}
	}
}

// A continuation is still a line of the report. `grep '^\['` must not have
// holes in it, and the text has to keep a straight left edge under the prefix.
func TestWrappedTagsAndIndentsContinuations(t *testing.T) {
	var buf bytes.Buffer
	w := Writer{W: &buf, P: Painter{Enabled: false}}
	w.Wrapped(Fail, 40, "CIS-1.1.3  ", 11, "one two three four five six seven eight")

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the text to wrap, got %q", buf.String())
	}
	if !strings.HasPrefix(lines[0], "[FAIL] CIS-1.1.3  ") {
		t.Errorf("first line = %q", lines[0])
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "[FAIL] ") && !strings.HasPrefix(line, "[INFO] ") {
			t.Errorf("line lost its tag: %q", line)
		}
		if n := utf8.RuneCountInString(line); n > 40 {
			t.Errorf("line of %d runes exceeds the width: %q", n, line)
		}
	}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "[INFO] "+strings.Repeat(" ", 11)) {
			t.Errorf("continuation is not indented under the prefix: %q", line)
		}
	}
}
