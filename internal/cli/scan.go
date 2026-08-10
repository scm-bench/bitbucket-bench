package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/scm-bench/scm-bench/internal/config"
	"github.com/scm-bench/scm-bench/internal/console"
	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/report"
	"github.com/scm-bench/scm-bench/internal/scm"
	"github.com/scm-bench/scm-bench/internal/scm/bitbucketdc"
)

// Progress modes. Compact is the default: one self-overwriting line while the
// scan runs, and the closing audit line when it finishes. Full — every request
// on its own line — is what --verbose turns on.
const (
	ProgressFull    = "full"
	ProgressCompact = "compact"
	ProgressOff     = "off"
)

// exitCodeError carries a specific process exit code out of RunE.
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string { return e.msg }

// ExitCode extracts the process exit code from an error returned by the root
// command, defaulting to ExitError.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded *exitCodeError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ExitError
}

type scanOptions struct {
	baseURL  string
	token    string
	username string
	password string

	projects     []string
	repositories []string

	configPath     string
	format         string
	outputPath     string
	lang           string
	showPassed     bool
	maxResources   int
	failOn         string
	failUnder      int
	maxManual      int
	noColor        bool
	verbose        bool
	insecure       bool
	allowPlaintext bool
	concurrency    int
	timeout        time.Duration
	maxDuration    time.Duration
	progress       string
	noRemediations bool

	snapshotIn  string
	snapshotOut string

	// logMu serializes progress output: the fetcher scans repositories
	// concurrently and calls the log callback from each goroutine.
	logMu sync.Mutex
}

func newScanCommand() *cobra.Command {
	opts := &scanOptions{}

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan a Bitbucket Data Center instance",
		Long: `Scan captures a read-only snapshot of a Bitbucket Data Center instance and
evaluates it against the benchmark.

The token needs read access to the repositories in scope. Administrator read
access additionally enables the instance-level controls (administrator counts,
dormant accounts); without it those report MANUAL instead of failing.

Credentials may be supplied by flag or environment:
  BITBUCKET_URL, BITBUCKET_TOKEN, BITBUCKET_USERNAME, BITBUCKET_PASSWORD

Exit codes: 0 clean, 1 a threshold was breached, 2 the scan failed.

Three thresholds drive exit 1, and they answer different questions:
  --fail-on      are there failures this severe?
  --fail-under   is the score acceptable?
  --max-manual   did the scan see enough to have an opinion at all?

The last one matters because controls that could not be evaluated are excluded
from the score rather than counted against it: a token that can read very
little produces a high score from a small sample, and nothing else would say
so.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScan(cmd, opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.baseURL, "url", os.Getenv("BITBUCKET_URL"), "Bitbucket base URL, e.g. https://bitbucket.example.com [BITBUCKET_URL]")
	f.StringVar(&opts.token, "token", os.Getenv("BITBUCKET_TOKEN"), "HTTP access token; read-only is sufficient [BITBUCKET_TOKEN]")
	f.StringVar(&opts.username, "username", os.Getenv("BITBUCKET_USERNAME"), "username for basic auth, if not using a token [BITBUCKET_USERNAME]")
	f.StringVar(&opts.password, "password", os.Getenv("BITBUCKET_PASSWORD"), "password for basic auth [BITBUCKET_PASSWORD]")

	f.StringSliceVarP(&opts.projects, "project", "p", nil, "project key to scan; repeatable, defaults to all")
	f.StringSliceVarP(&opts.repositories, "repository", "r", nil, "repository to scan as PROJECT/slug; repeatable")

	f.StringVarP(&opts.configPath, "config", "c", "", "path to a YAML config file overriding the default thresholds")
	f.StringVarP(&opts.format, "output", "o", report.FormatTable, "output format: "+strings.Join(report.Formats(), ", "))
	f.StringVar(&opts.outputPath, "output-file", "", "write the report to this file instead of stdout")
	f.StringVar(&opts.lang, "lang", report.LangEnglish, "report language: en, zh")
	f.BoolVar(&opts.showPassed, "show-passed", false, "include passing and not-applicable controls in the table output")
	f.IntVar(&opts.maxResources, "max-resources", report.DefaultMaxResources, "table output: resource names to list per finding before summarising; 0 lists all")
	f.StringVar(&opts.failOn, "fail-on", "high", "exit 1 when a failure at or above this severity exists: high, medium, low, none")
	f.IntVar(&opts.failUnder, "fail-under", 0, "exit 1 when the score is below this; 0 disables")
	// Defaults to off, because how much of an instance a token can read is a
	// property of the deployment, and a guess here would fail scans that are
	// working as well as they can. -1 rather than 0 is the off switch, since 0
	// is the strictest setting a user could reasonably want.
	f.IntVar(&opts.maxManual, "max-manual", -1, "exit 1 when more than this percent of controls need manual review; -1 disables")
	f.BoolVar(&opts.noColor, "no-color", false, "disable ANSI colour")
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "log fetch progress to stderr")
	f.BoolVar(&opts.insecure, "insecure", false, "skip TLS certificate verification (for private CAs)")
	f.BoolVar(&opts.allowPlaintext, "allow-plaintext", false, "permit an http:// URL, sending credentials in the clear")
	f.IntVar(&opts.concurrency, "concurrency", 8, "how many repositories to fetch in parallel")
	f.DurationVar(&opts.timeout, "timeout", 30*time.Second, "per-request HTTP timeout")
	f.DurationVar(&opts.maxDuration, "max-duration", 0, "abandon the scan after this long; 0 means no limit")
	f.StringVar(&opts.progress, "progress", ProgressCompact, "what to show while scanning: full (every request), compact (one line), off (only the closing audit line)")
	f.BoolVar(&opts.noRemediations, "no-remediations", false, "omit the remediation section from the table report")

	f.StringVar(&opts.snapshotIn, "snapshot-in", "", "evaluate this snapshot file instead of contacting the instance")
	f.StringVar(&opts.snapshotOut, "snapshot-out", "", "write the captured snapshot to this file")

	return cmd
}

func runScan(cmd *cobra.Command, opts *scanOptions) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if err := validateScanOptions(opts); err != nil {
		return err
	}

	// A whole-scan deadline is opt-in. How long is too long depends entirely on
	// how big the instance is, and a default guess would turn a legitimately
	// long scan of a large instance into a failure.
	if opts.maxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.maxDuration)
		defer cancel()
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}

	// The tracer exists even when nothing is shown: its closing line is an
	// account of what the token was used for, and that is worth having in a
	// CI log too.
	stderr := cmd.ErrOrStderr()
	shown := strings.ToLower(opts.progress)
	// --verbose is what asks for the request log. The default is one
	// self-overwriting line, because a list of every GET is a description of
	// what the tool did, and the person running a benchmark wants to know what
	// it found — the requests are only interesting when something is wrong,
	// which is exactly when --verbose gets typed.
	if opts.verbose && !cmd.Flags().Changed("progress") {
		shown = ProgressFull
	}
	if !isTerminal(stderr) && shown == ProgressFull {
		// Every request on its own line needs a terminal to be readable; in a
		// log it is thousands of lines nobody asked for.
		shown = ProgressOff
	}
	// --verbose adds the fetcher's own narration on top; it does not replace
	// the request log, because "more detail" should never mean less.
	trace := newTracer(stderr, useProgressColor(opts, stderr), shown == ProgressFull)

	progress := newProgressWriter(stderr, shown == ProgressCompact && !opts.verbose)

	snapshot, err := obtainSnapshot(ctx, cmd, opts, cfg, trace, progress)
	progress.clear()
	if line, tag := trace.summary(); line != "" {
		console.Writer{W: stderr, P: console.Painter{Enabled: useProgressColor(opts, stderr)}}.Line(tag, "%s", line)
	}
	if err != nil {
		return describeScanFailure(ctx, opts, err)
	}

	if opts.snapshotOut != "" {
		if err := writeSnapshot(opts.snapshotOut, snapshot); err != nil {
			return err
		}
		logf(cmd, opts, "snapshot written to %s", opts.snapshotOut)
	}

	eng, err := engine.New(ctx, cfg, snapshot.Metadata.Platform)
	if err != nil {
		return err
	}
	rep, err := eng.Evaluate(ctx, snapshot)
	if err != nil {
		return err
	}

	out, closeOut, err := openOutput(cmd, opts.outputPath)
	if err != nil {
		return err
	}
	// The report is rendered into memory first so a write failure cannot leave
	// a half-written file that looks like a complete report.
	var buf bytes.Buffer
	if err := report.Write(&buf, rep, report.Options{
		Format:         opts.format,
		Lang:           opts.lang,
		Color:          useColor(opts, out),
		ShowPassed:     opts.showPassed,
		MaxResources:   opts.maxResources,
		NoRemediations: opts.noRemediations,
		ToolVersion:    Version,
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

	return exitStatus(rep, opts)
}

func validateScanOptions(opts *scanOptions) error {
	switch strings.ToLower(opts.lang) {
	case report.LangEnglish, report.LangChinese:
	default:
		return fmt.Errorf("unknown --lang %q; want en or zh", opts.lang)
	}
	switch strings.ToLower(opts.failOn) {
	case "high", "medium", "low", "none":
	default:
		return fmt.Errorf("unknown --fail-on %q; want high, medium, low or none", opts.failOn)
	}
	switch strings.ToLower(opts.progress) {
	case ProgressFull, ProgressCompact, ProgressOff:
	default:
		return fmt.Errorf("unknown --progress %q; want full, compact or off", opts.progress)
	}
	if opts.concurrency < 1 {
		return fmt.Errorf("--concurrency must be at least 1")
	}
	if opts.failUnder < 0 || opts.failUnder > 100 {
		return fmt.Errorf("--fail-under must be between 0 and 100, got %d", opts.failUnder)
	}
	if opts.maxManual < -1 || opts.maxManual > 100 {
		return fmt.Errorf("--max-manual must be between 0 and 100, or -1 to disable, got %d", opts.maxManual)
	}
	// Checked here as well as in the fetcher so it costs nothing to find out.
	// The fetcher only reaches its own check after the preflight and the
	// instance-level fetches, so a typo in a flag was answered by a network
	// round trip instead of immediately.
	for _, r := range opts.repositories {
		key, slug, ok := strings.Cut(strings.TrimSpace(r), "/")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(slug) == "" {
			return fmt.Errorf("--repository %q must be PROJECT/slug, e.g. PLATFORM/payments-api", r)
		}
	}
	if opts.snapshotIn == "" && strings.TrimSpace(opts.baseURL) == "" {
		return fmt.Errorf("--url is required (or set BITBUCKET_URL, or pass --snapshot-in to evaluate a saved snapshot)")
	}

	// --project and --repository narrow what is fetched from an instance.
	// A snapshot has already been fetched, so they had nothing to act on and
	// were ignored — but ignored in silence, which is the problem: the report
	// that came back covered every repository in the file, carried the score
	// for all of them, and looked exactly like the narrowed scan that had been
	// asked for. Saying so is the difference between a wrong answer and a
	// question.
	if opts.snapshotIn != "" && (len(opts.projects) > 0 || len(opts.repositories) > 0) {
		return fmt.Errorf("--project/--repository narrow what is fetched from an instance, so they cannot be combined with --snapshot-in\n" +
			"the snapshot already holds a fixed set of repositories; re-capture with --project/--repository to narrow it")
	}
	return nil
}

// obtainSnapshot either reads a saved snapshot or captures a fresh one.
func obtainSnapshot(ctx context.Context, cmd *cobra.Command, opts *scanOptions, cfg config.Config, trace *tracer, progress *progressWriter) (*scm.Snapshot, error) {
	if opts.snapshotIn != "" {
		return readSnapshot(opts.snapshotIn)
	}

	client, err := bitbucketdc.NewClient(bitbucketdc.Options{
		BaseURL:        opts.baseURL,
		Token:          opts.token,
		Username:       opts.username,
		Password:       opts.password,
		Timeout:        opts.timeout,
		Insecure:       opts.insecure,
		AllowPlaintext: opts.allowPlaintext,
		OnRequest:      trace.record,
		Logf: func(format string, args ...any) {
			logf(cmd, opts, format, args...)
		},
		// Warnings are what the scan could not see, so they get the WARN tag
		// rather than being narration with a "warning:" prefix. They still go
		// only to the verbose channel — they are all repeated in the report's
		// own WARN block, and printing them twice by default is noise.
		Warnf: func(format string, args ...any) {
			emit(cmd, opts, console.Warn, format, args...)
		},
	})
	if err != nil {
		return nil, err
	}

	fetcher := bitbucketdc.NewFetcher(client, cfg)
	return fetcher.Fetch(ctx, bitbucketdc.FetchOptions{
		Projects:         opts.projects,
		Repositories:     opts.repositories,
		Concurrency:      opts.concurrency,
		ToolVersion:      Version,
		Progress:         progress.callback(),
		OnRepositoryDone: trace.repositoryDone,
	})
}

// describeScanFailure names the deadline as the cause when one was set and hit.
// The bare error is whatever request happened to be in flight, which reads as a
// network problem rather than the limit the operator chose.
func describeScanFailure(ctx context.Context, opts *scanOptions, err error) error {
	if opts.maxDuration > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("scan abandoned after --max-duration %s: %w\n"+
			"raise or drop --max-duration, or narrow the scan with --project/--repository", opts.maxDuration, err)
	}
	return err
}

func readSnapshot(path string) (*scm.Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var snapshot scm.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	if snapshot.SchemaVersion != scm.SchemaVersion {
		return nil, fmt.Errorf("snapshot %s has schema version %q, but this build reads version %q",
			path, snapshot.SchemaVersion, scm.SchemaVersion)
	}
	if snapshot.Metadata.Platform == "" {
		return nil, fmt.Errorf("snapshot %s does not record which platform it came from", path)
	}
	return &snapshot, nil
}

func writeSnapshot(path string, snapshot *scm.Snapshot) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	// A snapshot describes an instance's security posture, so it is written
	// readable by its owner only.
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write snapshot %s: %w", path, err)
	}
	return nil
}

// openOutput returns the report destination and a close function that is safe
// to call for stdout.
func openOutput(cmd *cobra.Command, path string) (io.Writer, func() error, error) {
	if path == "" {
		return cmd.OutOrStdout(), func() error { return nil }, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	// 0600, for the same reason the snapshot is: a report names every
	// repository that can be force-pushed, every account that should have been
	// deactivated, and every project handing write access to all comers. That
	// is the same map of an instance's weak points, just rendered — so it gets
	// the same permissions rather than whatever the umask happens to allow.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", path, err)
	}
	return file, file.Close, nil
}

// useColor enables ANSI only for an actual terminal, honouring NO_COLOR.
// useProgressColor decides colour for the trace. It goes to stderr, so it
// cannot reuse the report's decision, which is about stdout.
func useProgressColor(opts *scanOptions, out io.Writer) bool {
	return !opts.noColor && !hasNoColorEnv() && isTerminal(out)
}

func useColor(opts *scanOptions, out io.Writer) bool {
	if opts.noColor || hasNoColorEnv() || opts.format != report.FormatTable {
		return false
	}
	return isTerminal(out)
}

// hasNoColorEnv honours the NO_COLOR convention, which every command respects.
func hasNoColorEnv() bool { return os.Getenv("NO_COLOR") != "" }

// exitStatus turns a report into the process's exit code.
//
// The three conditions answer three different questions, and a scan can pass
// the first while failing the others:
//
//   - --fail-on: are there failures this bad?
//   - --fail-under: is the score acceptable?
//   - --max-manual: did the scan actually see enough to have an opinion?
//
// The last one exists because MANUAL is excluded from both sides of the score,
// which is right in itself and perverse in aggregate: the fewer settings a
// token can read, the smaller the denominator, and the higher the score. A
// credential that could read a tenth of the instance scored 78 where a working
// one scored 53, and exited 0 while doing it. Nothing in the report was untrue;
// there was simply no way to say "this scan did not see enough to be believed".
func exitStatus(rep *engine.Report, opts *scanOptions) error {
	// A policy that could not run is a broken tool, not a finding about the
	// instance. It already degrades to MANUAL so the rest of the report
	// survives — but MANUAL leaves the score's denominator, so a bundle that
	// failed to evaluate raises the score and exits 0. That is the one outcome
	// a scan must never produce.
	if len(rep.Errors) > 0 {
		return &exitCodeError{
			code: ExitError,
			msg: fmt.Sprintf("%s could not be evaluated; the report is incomplete and its score is not comparable\n%s",
				console.Pluralize(len(rep.Errors), "control"), strings.Join(rep.Errors, "\n")),
		}
	}

	if opts.maxManual >= 0 {
		decidable := rep.Score.Passed + rep.Score.Failed + rep.Score.Manual
		if decidable > 0 {
			percent := rep.Score.Manual * 100 / decidable
			if percent > opts.maxManual {
				return &exitCodeError{
					code: ExitFindings,
					msg: fmt.Sprintf("%d%% of controls need manual review (--max-manual %d%%); the scan could not see enough to judge this instance\n"+
						"grant the token more read access, or raise --max-manual if this is expected",
						percent, opts.maxManual),
				}
			}
		}
	}

	if opts.failUnder > 0 && rep.Score.Value < opts.failUnder {
		return &exitCodeError{
			code: ExitFindings,
			msg:  fmt.Sprintf("score %d is below --fail-under %d", rep.Score.Value, opts.failUnder),
		}
	}

	if !strings.EqualFold(opts.failOn, "none") && rep.HasFailureAtOrAbove(opts.failOn) {
		return &exitCodeError{
			code: ExitFindings,
			msg:  failureSummary(rep, opts.failOn),
		}
	}
	return nil
}

// failureSummary describes the failures without inflating them.
//
// Score.Failed counts findings, which is one control per resource. Reporting
// that as "N control(s) failed" was not merely loose: the whole bundle holds
// twenty controls, so a twenty-repository scan announced "130 control(s)
// failed" — a number that cannot be true of twenty controls. The table output
// groups by control precisely so one misconfiguration across fifty
// repositories reads as one problem, and this line undid that at the very end.
//
// Both numbers are given because both matter: how many things are wrong, and
// how far each has spread.
func failureSummary(rep *engine.Report, failOn string) string {
	controls := map[string]bool{}
	for _, f := range rep.Findings {
		if f.Status == engine.StatusFail {
			controls[f.CheckID] = true
		}
	}

	msg := fmt.Sprintf("%s failed", console.Pluralize(len(controls), "control"))
	if rep.Score.Failed > len(controls) {
		msg += fmt.Sprintf(" across %s", console.Pluralize(rep.Score.Failed, "finding"))
	}
	return msg + fmt.Sprintf(", including at least one at or above %s severity", strings.ToUpper(failOn))
}

func logf(cmd *cobra.Command, opts *scanOptions, format string, args ...any) {
	emit(cmd, opts, console.Info, format, args...)
}

// emit writes one tagged line of narration to stderr. The fetcher calls this
// from the repository goroutines, so the lock covers the whole line: a tag and
// its text arriving from two goroutines interleaved would be worse than no tag
// at all.
func emit(cmd *cobra.Command, opts *scanOptions, tag console.Tag, format string, args ...any) {
	if !opts.verbose {
		return
	}
	stderr := cmd.ErrOrStderr()
	w := console.Writer{
		W: stderr,
		P: console.Painter{Enabled: useProgressColor(opts, stderr)},
	}
	opts.logMu.Lock()
	defer opts.logMu.Unlock()
	w.Line(tag, format, args...)
}

// StderrWriter returns a tagged writer for the process's final line, using the
// same colour rules as everything else: a terminal, and NO_COLOR unset.
func StderrWriter() console.Writer {
	return console.Writer{
		W: os.Stderr,
		P: console.Painter{Enabled: isTerminal(os.Stderr) && !hasNoColorEnv()},
	}
}
