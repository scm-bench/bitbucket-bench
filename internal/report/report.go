// Package report renders an evaluation as a human table, machine JSON, or
// SARIF for CI ingestion.
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/scm-bench/scm-bench/internal/engine"
)

// Output formats.
const (
	FormatTable = "table"
	FormatJSON  = "json"
	FormatSARIF = "sarif"
)

// Languages the report can be rendered in.
const (
	LangEnglish = "en"
	LangChinese = "zh"
)

// Options controls rendering.
type Options struct {
	// Format is one of the Format* constants.
	Format string
	// Lang selects between the English and Chinese metadata fields.
	Lang string
	// Color enables ANSI colour in the table output.
	Color bool
	// ShowPassed includes passing controls in the table's detail section.
	// The summary always counts them.
	ShowPassed bool
	// MaxResources caps how many resource names the table prints per distinct
	// verdict before summarising the rest as "and N more". Zero means print
	// every one. It affects only the table: json and sarif always carry the
	// full set, because something is consuming those rather than reading them.
	MaxResources int
	// NoRemediations drops the remediation section. The fixes are the longest
	// part of the output by far, so a reader who only wants to know what is
	// wrong — a CI log, a second look after fixing — can turn them off.
	NoRemediations bool
	// ToolVersion is stamped into SARIF.
	ToolVersion string
}

// DefaultMaxResources is how many resource names a table verdict lists before
// summarising. Enough to recognise a pattern — one repository is an incident,
// a handful is a policy that was never applied — without the list becoming the
// report.
const DefaultMaxResources = 5

// Formats lists the supported output formats, for flag help and validation.
func Formats() []string { return []string{FormatTable, FormatJSON, FormatSARIF} }

// Write renders the report in the requested format.
func Write(w io.Writer, rep *engine.Report, opts Options) error {
	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "", FormatTable:
		return writeTable(w, rep, opts)
	case FormatJSON:
		return writeJSON(w, rep, opts)
	case FormatSARIF:
		return writeSARIF(w, rep, opts)
	default:
		return fmt.Errorf("unknown output format %q; want one of %s", opts.Format, strings.Join(Formats(), ", "))
	}
}

// title picks the localised title, falling back to English when a translation
// is absent so a report is never blank.
func title(f engine.Finding, lang string) string {
	if lang == LangChinese && f.TitleZh != "" {
		return f.TitleZh
	}
	return f.Title
}

// remediation picks the localised remediation, with the same fallback.
func remediation(f engine.Finding, lang string) string {
	if lang == LangChinese && f.RemediationZh != "" {
		return f.RemediationZh
	}
	return f.Remediation
}

// localize returns a copy of the report with each finding's text resolved to
// one language, and the per-language variants cleared so they drop out of the
// encoding entirely.
//
// The report is copied rather than edited: it belongs to the caller, and a
// renderer that mutated it would change what a later renderer saw.
func localize(rep *engine.Report, lang string) *engine.Report {
	out := *rep
	out.Findings = make([]engine.Finding, len(rep.Findings))

	for i, f := range rep.Findings {
		f.Title = title(f, lang)
		f.Remediation = remediation(f, lang)
		// Cleared, not left alongside: both are `omitempty`, so emptying them
		// is what removes them from the output.
		f.TitleZh = ""
		f.RemediationZh = ""
		out.Findings[i] = f
	}
	return &out
}
