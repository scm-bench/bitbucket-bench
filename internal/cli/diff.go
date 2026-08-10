package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/scm-bench/scm-bench/internal/config"
	"github.com/scm-bench/scm-bench/internal/console"
	"github.com/scm-bench/scm-bench/internal/diff"
	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/report"
	"github.com/scm-bench/scm-bench/internal/scm"
)

type diffOptions struct {
	configPath         string
	format             string
	outputPath         string
	noColor            bool
	failOnRegression   bool
	allowOtherInstance bool
}

func newDiffCommand() *cobra.Command {
	opts := &diffOptions{}

	cmd := &cobra.Command{
		Use:   "diff <before.json> <after.json>",
		Short: "Compare two snapshots and report what changed",
		Long: `Diff evaluates two snapshots and reports which controls changed verdict.

Both snapshots are evaluated by this build with this configuration before being
compared, so the difference is the instance's, not the tool's. Comparing two
reports produced by different versions or different thresholds would mix the two
together.

Only PASS -> FAIL counts as a regression and drives the exit code. A failure on a
repository that did not exist before is listed separately: nothing got worse,
there is simply more of the instance. Anything involving MANUAL is listed as an
other change, because losing the ability to see a setting is not the same as the
setting getting worse.

Exit codes: 0 no regression, 1 at least one PASS -> FAIL, 2 the comparison failed.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, opts, args[0], args[1])
		},
	}

	f := cmd.Flags()
	f.StringVarP(&opts.configPath, "config", "c", "", "path to a YAML config file; applied to both snapshots")
	f.StringVarP(&opts.format, "output", "o", report.FormatTable, "output format: table, json")
	f.StringVar(&opts.outputPath, "output-file", "", "write the comparison to this file instead of stdout")
	f.BoolVar(&opts.noColor, "no-color", false, "disable ANSI colour")
	f.BoolVar(&opts.failOnRegression, "fail-on-regression", true, "exit 1 when a control fell from PASS to FAIL")
	f.BoolVar(&opts.allowOtherInstance, "allow-other-instance", false, "compare snapshots taken from different base URLs")

	return cmd
}

func runDiff(cmd *cobra.Command, opts *diffOptions, beforePath, afterPath string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}

	before, err := readSnapshot(beforePath)
	if err != nil {
		return err
	}
	after, err := readSnapshot(afterPath)
	if err != nil {
		return err
	}

	// Diffing two different instances produces a result that looks meaningful
	// and is not: every repository reads as departed and arrived at once.
	if !diff.SameInstance(before, after) && !opts.allowOtherInstance {
		return fmt.Errorf("these snapshots came from different instances (%s and %s); pass --allow-other-instance if that is deliberate",
			before.Metadata.BaseURL, after.Metadata.BaseURL)
	}
	if before.Metadata.Platform != after.Metadata.Platform {
		return fmt.Errorf("these snapshots came from different platforms (%s and %s)",
			before.Metadata.Platform, after.Metadata.Platform)
	}

	beforeReport, err := evaluateSnapshot(ctx, cfg, before)
	if err != nil {
		return fmt.Errorf("evaluate %s: %w", beforePath, err)
	}
	afterReport, err := evaluateSnapshot(ctx, cfg, after)
	if err != nil {
		return fmt.Errorf("evaluate %s: %w", afterPath, err)
	}

	result := diff.Compare(beforeReport, afterReport)

	out, closeOut, err := openOutput(cmd, opts.outputPath)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := diff.Write(&buf, result, diff.Options{
		Format: opts.format,
		Color:  useDiffColor(opts, out),
	}); err != nil {
		closeOut()
		return err
	}
	if _, err := io.Copy(out, &buf); err != nil {
		closeOut()
		return err
	}
	if err := closeOut(); err != nil {
		return err
	}

	if opts.failOnRegression && result.HasRegression() {
		return &exitCodeError{
			code: ExitFindings,
			// pluralize, not "control(s)": the scan report went to the trouble
			// of writing real grammar for exactly this line, and having the two
			// subcommands disagree about how to say the same thing is the drift
			// the shared console package exists to stop.
			msg: fmt.Sprintf("%s fell from PASS to FAIL", console.Pluralize(len(result.Regressed), "control")),
		}
	}
	return nil
}

// evaluateSnapshot runs the policy bundle over one snapshot. Both sides go
// through this, so neither can be evaluated under different rules.
func evaluateSnapshot(ctx context.Context, cfg config.Config, snapshot *scm.Snapshot) (*engine.Report, error) {
	eng, err := engine.New(ctx, cfg, snapshot.Metadata.Platform)
	if err != nil {
		return nil, err
	}
	return eng.Evaluate(ctx, snapshot)
}

func useDiffColor(opts *diffOptions, out io.Writer) bool {
	if opts.noColor || opts.format != report.FormatTable {
		return false
	}
	return isTerminal(out) && !hasNoColorEnv()
}
