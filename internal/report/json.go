package report

import (
	"encoding/json"
	"io"

	"github.com/scm-bench/scm-bench/internal/engine"
)

// writeJSON emits the report with its text resolved to the requested language.
//
// It used to emit engine.Report verbatim, which meant --lang did nothing here
// while table and sarif both honoured it, and every consumer received both
// languages and had to work out which field to read. Resolving once, here,
// leaves consumers with stable field names carrying the language they asked
// for.
func writeJSON(w io.Writer, rep *engine.Report, opts Options) error {
	rep = localize(rep, opts.Lang)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Findings carry remediation text with paths like "Settings -> Hooks";
	// escaping those into > would make the output unreadable.
	enc.SetEscapeHTML(false)
	return enc.Encode(rep)
}
