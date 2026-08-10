package console

import (
	"bytes"
	"strings"
	"testing"
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
