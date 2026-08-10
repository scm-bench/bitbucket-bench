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

// Options controls rendering.
type Options struct {
	// Format is one of the Format* constants.
	Format string
	// Color enables ANSI colour in the table output.
	Color bool
	// ShowPassed includes passing controls in the table's detail section.
	// The summary always counts them.
	ShowPassed bool
	// MaxResources caps how many resources get a table of their own. Zero, the
	// default, gives every one of them a table.
	//
	// It used to mean something else — how many names one verdict listed before
	// summarising the rest — which was the right knob while the report grouped
	// by control. Grouping by resource made each resource's table the report's
	// main product, so this is now the cap on how many of those to draw. It
	// affects only the table: json and sarif always carry the full set, because
	// something is consuming those rather than reading them.
	MaxResources int
	// NoRemediations drops the remediation section. The fixes are the longest
	// part of the output by far, so a reader who only wants to know what is
	// wrong — a CI log, a second look after fixing — can turn them off.
	NoRemediations bool
	// ToolVersion is stamped into SARIF.
	ToolVersion string
}

// DefaultMaxResources is how many resources get a table. Zero means all of
// them, and that is the default because each table is now a resource's whole
// verdict rather than a list that could be trimmed: capping it by default would
// mean the report silently omitted repositories that had findings.
const DefaultMaxResources = 0

// Formats lists the supported output formats, for flag help and validation.
func Formats() []string { return []string{FormatTable, FormatJSON, FormatSARIF} }

// Write renders the report in the requested format.
func Write(w io.Writer, rep *engine.Report, opts Options) error {
	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "", FormatTable:
		return writeTable(w, rep, opts)
	case FormatJSON:
		return writeJSON(w, rep)
	case FormatSARIF:
		return writeSARIF(w, rep, opts)
	default:
		return fmt.Errorf("unknown output format %q; want one of %s", opts.Format, strings.Join(Formats(), ", "))
	}
}
