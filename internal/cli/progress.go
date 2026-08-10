package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// progressWriter renders a single self-overwriting status line on stderr.
//
// It is deliberately only used when stderr is a terminal. A redirected stream
// has no cursor to move, so carriage returns would accumulate as thousands of
// lines in a CI log — the opposite of the point. In that case there is no
// writer at all and the fetcher's callback is nil, so nothing is formatted
// either.
type progressWriter struct {
	out io.Writer

	mu   sync.Mutex
	last int
}

// newProgressWriter returns a writer for stderr, or nil when progress should
// not be shown: not a terminal, explicitly disabled, or superseded by the
// per-repository logging that --verbose already prints.
func newProgressWriter(out io.Writer, enabled bool) *progressWriter {
	if !enabled || !isTerminal(out) {
		return nil
	}
	return &progressWriter{out: out}
}

// update replaces the current status line. Safe for concurrent use: the
// fetcher calls this from every repository goroutine.
func (p *progressWriter) update(text string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	// Pad to the previous width so a shorter line cannot leave the tail of a
	// longer one behind it.
	padding := ""
	if trailing := p.last - len(text); trailing > 0 {
		padding = strings.Repeat(" ", trailing)
	}
	fmt.Fprintf(p.out, "\r%s%s", text, padding)
	p.last = len(text)
}

// clear erases the status line, so the report does not begin on a line that
// still holds a half-finished count.
func (p *progressWriter) clear() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.last > 0 {
		fmt.Fprintf(p.out, "\r%s\r", strings.Repeat(" ", p.last))
		p.last = 0
	}
}

// callback adapts the writer to what the fetcher expects, returning nil when
// there is no writer so the fetcher can skip formatting entirely.
func (p *progressWriter) callback() func(string) {
	if p == nil {
		return nil
	}
	return p.update
}

// isTerminal reports whether w is a character device, which is the same test
// used to decide on colour.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
