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

	demo bool
	// saveInstance remembers the interactively entered URL and token once the
	// scan proves they work.
	saveInstance bool

	configPath     string
	set            []string
	format         string
	outputPath     string
	showPassed     bool
	details        []string
	maxResources   int
	noColor        bool
	verbose        bool
	noRemediations bool

	// The deployment-stable settings, filled from the config file's scan
	// section rather than flags: they describe the instance, not the run.
	scan config.Scan

	snapshotIn  string
	snapshotOut string
	// last renders the previous scan's cached snapshot instead of fetching.
	last bool

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

No instance yet? --demo evaluates a sample bundled into the binary, so you can
see what a report looks like before configuring anything. Run bare on a
terminal, scan offers the same choice interactively — and can save the URL and
token you enter (0600, under your user config directory, or SCM_BENCH_CONFIG_DIR)
so later scans need nothing. Delete the file to forget it.

The table report is an overview aggregated by control: one row per failed
control, however many resources it failed on. --details expands it to one
section per resource; --details=<resource|control>[,...] narrows those
sections to what is named.

Each network scan also leaves its snapshot behind (0600, under the user
config directory), so the next question does not cost another scan:
` + "`scan --last --details`" + ` expands the previous scan's findings without
contacting the instance, and any report option works the same way. The
snapshot is the same map of weak points the report is — scan.cache: false
in the config keeps it off disk, and deleting the cache directory forgets
what has been kept.

Exit codes: 0 clean, 1 a threshold was breached, 2 the scan failed.

The settings that describe the deployment rather than any one run — exit
thresholds, transport, concurrency, progress — live in the config file's scan
section rather than in flags. Run ` + "`scm-bench init`" + ` to write a commented
scm-bench.yaml; scan finds it in the working directory (or the user config
directory) without --config being typed. For a one-off, --set overrides any
config key without a file: --set scan.failOn=none.

Three of those settings drive exit 1, and they answer different questions:
  scan.failOn      are there failures this severe?
  scan.failUnder   is the score acceptable?
  scan.maxManual   did the scan see enough to have an opinion at all?

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

	f.BoolVar(&opts.demo, "demo", false, "evaluate the bundled example instead of an instance, to see what a report looks like")

	f.StringVarP(&opts.configPath, "config", "c", "", "path to a YAML config file; found automatically as ./scm-bench.yaml or in the user config directory")
	f.StringArrayVar(&opts.set, "set", nil, "override one config key for this run, e.g. --set scan.failOn=none; repeatable")
	f.StringVarP(&opts.format, "output", "o", report.FormatTable, "output format: "+strings.Join(report.Formats(), ", "))
	f.StringVar(&opts.outputPath, "output-file", "", "write the report to this file instead of stdout")
	f.BoolVar(&opts.showPassed, "show-passed", false, "include passing and not-applicable controls in the table output")
	f.StringSliceVar(&opts.details, "details", nil, "per-resource findings instead of the overview; --details=<resource|control>[,...] narrows it (the '=' is required when passing values)")
	// Bare --details, no value, means every resource and every control. pflag
	// needs a sentinel to allow the bare form; "all" is stripped by the parser.
	f.Lookup("details").NoOptDefVal = "all"
	f.IntVar(&opts.maxResources, "max-resources", report.DefaultMaxResources, "with --details: how many resources get a table of their own; 0 means every one")
	f.BoolVar(&opts.noColor, "no-color", false, "disable ANSI colour")
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "log fetch progress to stderr")
	f.BoolVar(&opts.noRemediations, "no-remediations", false, "omit the remediation section from the table report")

	f.StringVar(&opts.snapshotIn, "snapshot-in", "", "evaluate this snapshot file instead of contacting the instance")
	f.StringVar(&opts.snapshotOut, "snapshot-out", "", "write the captured snapshot to this file")
	f.BoolVar(&opts.last, "last", false, "render the previous scan's cached snapshot instead of contacting the instance")

	// The nine flags that used to live here describe the deployment, not the
	// run, and moved to the config file's scan section. Someone typing one
	// from muscle memory or an old pipeline gets told where it went instead
	// of cobra's bare "unknown flag".
	cmd.SetFlagErrorFunc(movedFlagError)

	return cmd
}

// movedFlags maps the retired scan flags to their config keys.
var movedFlags = map[string]string{
	"fail-on":         "scan.failOn",
	"fail-under":      "scan.failUnder",
	"max-manual":      "scan.maxManual",
	"concurrency":     "scan.concurrency",
	"timeout":         "scan.timeout",
	"max-duration":    "scan.maxDuration",
	"insecure":        "scan.insecure",
	"allow-plaintext": "scan.allowPlaintext",
	"progress":        "scan.progress",
}

// movedFlagError upgrades "unknown flag" for a retired flag into directions:
// the config key it became, and the command that writes a config to put it
// in. Anything else passes through untouched.
func movedFlagError(cmd *cobra.Command, err error) error {
	msg := err.Error()
	for flag, key := range movedFlags {
		if strings.Contains(msg, "--"+flag) {
			return fmt.Errorf("--%s moved to the config file as %s\n"+
				"run `scm-bench init` to keep it in a file, or override once with --set %s=<value>", flag, key, key)
		}
	}
	return err
}

func runScan(cmd *cobra.Command, opts *scanOptions) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	resolveCredentials(cmd, opts)

	if opts.demo {
		// A typed flag that the demo would silently ignore is refused, for the
		// same reason --project is refused against --snapshot-in: the report
		// would look exactly like the scan that was asked for and not be it.
		// Flags merely filled in from the environment do not count — an
		// exported BITBUCKET_URL must not make the demo argue.
		for _, name := range []string{"url", "token", "username", "password", "project", "repository", "snapshot-in", "last"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--demo evaluates the bundled example, so --%s has nothing to act on; drop one of them", name)
			}
		}
		opts.baseURL, opts.token, opts.username, opts.password = "", "", "", ""
	}

	// --last replays the previous scan from its cached snapshot, so every
	// flag that shapes a fresh capture has nothing to act on and is refused
	// rather than ignored, for the demo's reason. Only typed flags count:
	// an exported BITBUCKET_URL must not make --last argue. From here on it
	// is exactly --snapshot-in pointed at the cache, and inherits its rules —
	// no credentials needed, --project/--repository refused, exit thresholds
	// applied.
	if opts.last {
		for _, name := range []string{"url", "token", "username", "password", "project", "repository", "snapshot-in"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--last renders the previous scan's cached snapshot, so --%s has nothing to act on; drop one of them", name)
			}
		}
		path, err := config.LatestSnapshotCache()
		if err != nil {
			return err
		}
		if path == "" {
			return fmt.Errorf("no cached snapshot to render: --last replays the previous scan, and none has been cached yet\n" +
				"scan the instance first; its snapshot is kept automatically unless the config sets scan.cache: false")
		}
		opts.snapshotIn = path
		opts.baseURL, opts.token, opts.username, opts.password = "", "", "", ""
	}

	// The config comes first, because nearly everything after reads it: the
	// scan section carries what used to be nine flags. An explicit --config
	// wins; otherwise the file is discovered — the project's scm-bench.yaml
	// in the working directory, then the user's config.yaml — and named on
	// stderr, because a scan whose thresholds quietly came from a file is a
	// scan whose exit code makes no sense.
	configPath := opts.configPath
	if configPath == "" {
		discovered, err := config.Discover()
		if err != nil {
			return err
		}
		if discovered != "" {
			configPath = discovered
			stderr := cmd.ErrOrStderr()
			console.Writer{W: stderr, P: console.Painter{Enabled: useProgressColor(opts, stderr)}}.
				Line(console.Info, "using config %s", discovered)
		}
	}
	cfg, err := config.LoadWithOverrides(configPath, opts.set)
	if err != nil {
		return err
	}
	opts.scan = cfg.Scan

	// Before asking anybody anything: an instance saved by an earlier run's
	// menu answers the question silently. It only fills what is absent —
	// a typed flag or an exported variable always wins, and its token is not
	// used over any credential arriving another way. The stderr line is what
	// keeps this debuggable: a scan that silently picks up a credential from
	// disk is a scan whose authentication failures make no sense.
	if !opts.demo && opts.snapshotIn == "" && strings.TrimSpace(opts.baseURL) == "" {
		inst, path, err := config.LoadInstance()
		if err != nil {
			return err
		}
		if inst.URL != "" {
			opts.baseURL = inst.URL
			if strings.TrimSpace(opts.token) == "" && strings.TrimSpace(opts.username) == "" {
				opts.token = inst.Token
			}
			stderr := cmd.ErrOrStderr()
			console.Writer{W: stderr, P: console.Painter{Enabled: useProgressColor(opts, stderr)}}.
				Line(console.Info, "using saved instance %s (%s)", inst.URL, path)
		}
	}

	// Nothing configured, but a person present: offer the menu instead of the
	// error. Both ends must be terminals — a redirected stderr means the
	// question would go somewhere nobody is reading, and a redirected stdin
	// means nobody typed this invocation interactively. The stdin file check
	// keeps `scan < /dev/null` out via readLine's immediate EOF.
	if !opts.demo && opts.snapshotIn == "" && strings.TrimSpace(opts.baseURL) == "" {
		stderr := cmd.ErrOrStderr()
		if in, ok := cmd.InOrStdin().(*os.File); ok && isTerminal(in) && isTerminal(stderr) {
			res, err := promptFirstRun(in, stderr, useProgressColor(opts, stderr))
			if err != nil {
				return err
			}
			if res.demo {
				opts.demo = true
			} else {
				opts.baseURL = res.url
				opts.token = res.token
				opts.saveInstance = res.save
				// The prompt collected a token, so basic-auth values inherited
				// from the environment must not be left to conflict with it.
				if res.token != "" {
					opts.username, opts.password = "", ""
				}
			}
		}
	}

	// Both are table-layout knobs, checked here because they need to know
	// which flags were actually typed. --details on a machine format is
	// refused rather than ignored, for the demo's reason: the output would
	// look exactly like what was asked for and not be it. --max-resources
	// caps the per-resource tables, which the default overview never draws,
	// so alone it is a request the report cannot honour.
	if len(opts.details) > 0 && !strings.EqualFold(opts.format, report.FormatTable) {
		return fmt.Errorf("--details shapes the table output; -o %s already carries every finding", opts.format)
	}
	if cmd.Flags().Changed("max-resources") && len(opts.details) == 0 {
		return fmt.Errorf("--max-resources caps the per-resource tables, which the default overview does not print; combine it with --details")
	}

	if err := validateScanOptions(opts); err != nil {
		return err
	}

	// A whole-scan deadline is opt-in. How long is too long depends entirely on
	// how big the instance is, and a default guess would turn a legitimately
	// long scan of a large instance into a failure.
	if opts.scan.MaxDuration.Get() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.scan.MaxDuration.Get())
		defer cancel()
	}

	// The tracer exists even when nothing is shown: its closing line is an
	// account of what the token was used for, and that is worth having in a
	// CI log too.
	stderr := cmd.ErrOrStderr()
	shown := strings.ToLower(opts.scan.Progress)
	// --verbose is what asks for the request log. The default is one
	// self-overwriting line, because a list of every GET is a description of
	// what the tool did, and the person running a benchmark wants to know what
	// it found — the requests are only interesting when something is wrong,
	// which is exactly when --verbose gets typed. It wins over the config's
	// progress setting: the file is ambient, the flag was typed just now.
	if opts.verbose {
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

	// Said on stderr as well as in the table's banner, because with stdout
	// redirected the banner travels with the file and this line stays on the
	// terminal — each reaches a reader the other cannot.
	if opts.demo {
		console.Writer{W: stderr, P: console.Painter{Enabled: useProgressColor(opts, stderr)}}.
			Line(console.Info, "evaluating the bundled example; pass --url to scan your own instance")
	}

	progress := newProgressWriter(stderr, shown == ProgressCompact && !opts.verbose)

	snapshot, err := obtainSnapshot(ctx, cmd, opts, cfg, trace, progress)
	progress.clear()
	if line, tag := trace.summary(); line != "" {
		console.Writer{W: stderr, P: console.Painter{Enabled: useProgressColor(opts, stderr)}}.Line(tag, "%s", line)
	}
	if err != nil {
		return describeScanFailure(ctx, opts, err)
	}

	// A replayed snapshot must say how old it is, prominently and every time:
	// the report below looks exactly like a fresh scan, and its one real
	// difference from one is the capture time. Past a day it becomes a
	// warning — old enough that "current state" is now a guess.
	if opts.last {
		w := console.Writer{W: stderr, P: console.Painter{Enabled: useProgressColor(opts, stderr)}}
		age := time.Since(snapshot.Metadata.GeneratedAt)
		if age > staleSnapshotAge {
			w.Line(console.Warn, "rendering the snapshot of %s captured %s ago — scan again for current state", snapshot.Metadata.BaseURL, humanAge(age))
		} else {
			w.Line(console.Info, "rendering the snapshot of %s captured %s ago", snapshot.Metadata.BaseURL, humanAge(age))
		}
	}

	// Only now, with the fetch behind it, is the interactively entered
	// instance worth remembering: a credential saved before it worked would
	// replay its typo on every following run. A failure to write is a warning
	// rather than an error — the scan in hand succeeded, and refusing to
	// report it over a bookkeeping problem would cost more than it protects.
	if opts.saveInstance {
		w := console.Writer{W: stderr, P: console.Painter{Enabled: useProgressColor(opts, stderr)}}
		if path, err := config.SaveInstance(config.Instance{URL: opts.baseURL, Token: opts.token}); err != nil {
			w.Line(console.Warn, "could not save the instance: %v", err)
		} else {
			w.Line(console.Info, "saved to %s; delete the file to forget it", path)
		}
	}

	if opts.snapshotOut != "" {
		if err := writeSnapshot(opts.snapshotOut, snapshot); err != nil {
			return err
		}
		logf(cmd, opts, "snapshot written to %s", opts.snapshotOut)
	}

	// A network scan's snapshot is kept for --last — 0600, like every other
	// copy of an instance's posture this tool writes; scan.cache: false in
	// the config keeps it off disk. Replays and the demo are excluded: one
	// would only rewrite what it just read, the other would let --last pass
	// off the bundled example as somebody's instance. The write is named on
	// stderr because data appearing on disk unannounced is how a cache
	// becomes a leak; a failure is a warning for saveInstance's reason — the
	// scan in hand succeeded.
	if !opts.demo && opts.snapshotIn == "" && opts.scan.Cache {
		if path, err := config.SnapshotCachePath(opts.baseURL); err != nil {
			emit(cmd, opts, console.Warn, "could not cache the snapshot: %v", err)
		} else if err := writeSnapshot(path, snapshot); err != nil {
			emit(cmd, opts, console.Warn, "could not cache the snapshot: %v", err)
		} else {
			logf(cmd, opts, "snapshot cached for --last (%s)", path)
		}
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
	reportOpts := report.Options{
		Format:         opts.format,
		Color:          useColor(opts, out),
		Width:          console.WidthFor(out),
		ShowPassed:     opts.showPassed,
		Details:        len(opts.details) > 0,
		DetailFilters:  opts.details,
		MaxResources:   opts.maxResources,
		NoRemediations: opts.noRemediations,
		ToolVersion:    Version,
	}
	if opts.demo {
		reportOpts.Notice = demoNotice
	}
	if err := report.Write(&buf, rep, reportOpts); err != nil {
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

// resolveCredentials settles which credential wins when more than one is
// present.
//
// The flags default to the environment, so an exported BITBUCKET_TOKEN filled
// --token in before the command line was read — and the client prefers a token
// over basic auth. Someone with a stale token in their shell profile who typed
// --username and --password was therefore authenticated with the token they
// had not mentioned, and told "the instance rejected the credentials", which
// sent them to check the password they had just typed. What was typed wins
// over what was merely lying around.
func resolveCredentials(cmd *cobra.Command, opts *scanOptions) {
	flags := cmd.Flags()
	typedToken := flags.Changed("token")
	typedBasic := flags.Changed("username") || flags.Changed("password")

	if typedBasic && !typedToken {
		opts.token = ""
		return
	}
	if typedToken && !typedBasic {
		opts.username, opts.password = "", ""
	}
}

func validateScanOptions(opts *scanOptions) error {
	// The scan section's own checks (failOn, progress, concurrency, …) run in
	// config.Validate, where the settings now live.
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
	// Both typed out explicitly is a question, not something to resolve by
	// precedence: only one of them will be used and the user cannot tell which.
	if strings.TrimSpace(opts.token) != "" && strings.TrimSpace(opts.username) != "" &&
		opts.snapshotIn == "" {
		return fmt.Errorf("--token and --username were both given; use one or the other")
	}
	if !opts.demo && opts.snapshotIn == "" && strings.TrimSpace(opts.baseURL) == "" {
		return errNoInstance()
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
	if opts.demo {
		return demoSnapshot()
	}
	if opts.snapshotIn != "" {
		return readSnapshot(opts.snapshotIn)
	}

	// Only the network path gets the spinner: a snapshot or the demo is done
	// before a rotor could finish a turn, and starting it here rather than in
	// runScan keeps that knowledge in one place.
	progress.start()

	client, err := bitbucketdc.NewClient(bitbucketdc.Options{
		BaseURL:        opts.baseURL,
		Token:          opts.token,
		Username:       opts.username,
		Password:       opts.password,
		Timeout:        opts.scan.Timeout.Get(),
		Concurrency:    opts.scan.Concurrency,
		Insecure:       opts.scan.Insecure,
		AllowPlaintext: opts.scan.AllowPlaintext,
		OnRequest: func(e bitbucketdc.RequestEvent) {
			trace.record(e)
			progress.tick()
		},
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
		Concurrency:      opts.scan.Concurrency,
		ToolVersion:      Version,
		Progress:         progress.callback(),
		OnRepositoryDone: trace.repositoryDone,
	})
}

// describeScanFailure names the deadline as the cause when one was set and hit.
// The bare error is whatever request happened to be in flight, which reads as a
// network problem rather than the limit the operator chose.
func describeScanFailure(ctx context.Context, opts *scanOptions, err error) error {
	if opts.scan.MaxDuration.Get() > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("scan abandoned after scan.maxDuration %s: %w\n"+
			"raise or drop scan.maxDuration in the config, or narrow the scan with --project/--repository", opts.scan.MaxDuration.Get(), err)
	}
	return err
}

func readSnapshot(path string) (*scm.Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	return parseSnapshot(raw, "snapshot "+path)
}

// parseSnapshot decodes and sanity-checks snapshot bytes, wherever they came
// from — a file on disk, or the sample compiled into the binary. source names
// the origin in errors.
func parseSnapshot(raw []byte, source string) (*scm.Snapshot, error) {
	var snapshot scm.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	if snapshot.SchemaVersion != scm.SchemaVersion {
		return nil, fmt.Errorf("%s has schema version %q, but this build reads version %q",
			source, snapshot.SchemaVersion, scm.SchemaVersion)
	}
	if snapshot.Metadata.Platform == "" {
		return nil, fmt.Errorf("%s does not record which platform it came from", source)
	}
	return &snapshot, nil
}

// staleSnapshotAge is when a replayed snapshot's age line turns into a
// warning: past a day, treating it as current state is a guess.
const staleSnapshotAge = 24 * time.Hour

// humanAge renders a snapshot's age at the precision the decision needs:
// seconds are noise, and past two days so are hours.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
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
//   - scan.failOn: are there failures this bad?
//   - scan.failUnder: is the score acceptable?
//   - scan.maxManual: did the scan actually see enough to have an opinion?
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

	if opts.scan.MaxManual >= 0 {
		decidable := rep.Score.Passed + rep.Score.Failed + rep.Score.Manual
		if decidable > 0 {
			percent := rep.Score.Manual * 100 / decidable
			if percent > opts.scan.MaxManual {
				return &exitCodeError{
					code: ExitFindings,
					msg: fmt.Sprintf("%d%% of controls need manual review (scan.maxManual %d%%); the scan could not see enough to judge this instance\n"+
						"grant the token more read access, or raise scan.maxManual if this is expected",
						percent, opts.scan.MaxManual),
				}
			}
		}
	}

	if opts.scan.FailUnder > 0 && rep.Score.Value < opts.scan.FailUnder {
		return &exitCodeError{
			code: ExitFindings,
			msg:  fmt.Sprintf("score %d is below scan.failUnder %d", rep.Score.Value, opts.scan.FailUnder),
		}
	}

	if !strings.EqualFold(opts.scan.FailOn, "none") && rep.HasFailureAtOrAbove(opts.scan.FailOn) {
		return &exitCodeError{
			code: ExitFindings,
			msg:  failureSummary(rep, opts.scan.FailOn),
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
