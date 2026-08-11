package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/scm-bench/scm-bench/internal/console"
)

// configTemplate is the file init writes. Every key is present with its
// default and a comment, because the file is how a user discovers what can be
// configured — a template that only shows two keys teaches two keys. Values
// match config.Default(); TestInitTemplateMatchesTheDefaults holds them
// together.
const configTemplate = `# scm-bench configuration.
#
# scan finds this file on its own: scm-bench.yaml (or .scm-bench.yaml) in the
# working directory first, then config.yaml in the user config directory
# (SCM_BENCH_CONFIG_DIR, or the platform default). --config overrides the
# search. Every key is optional; an absent key keeps the default shown here.
# A single run can override any key without touching the file:
#   scm-bench scan --set scan.failOn=none

# Settings that describe the deployment rather than any one run.
scan:
  # Exit 1 when a failure at or above this severity exists:
  # high, medium, low, or none.
  failOn: high
  # Exit 1 when the score is below this; 0 disables.
  failUnder: 0
  # Exit 1 when more than this percent of controls need manual review;
  # -1 disables. Useful in CI so a token that lost read access fails the
  # run instead of producing a high score from a small sample.
  maxManual: -1
  # How many repositories to fetch in parallel.
  concurrency: 8
  # Per-request HTTP timeout.
  timeout: 30s
  # Abandon the scan after this long; 0s means no limit.
  maxDuration: 0s
  # Skip TLS certificate verification (for private CAs).
  insecure: false
  # Permit an http:// URL, sending credentials in the clear.
  allowPlaintext: false
  # What to show while scanning: full (every request), compact (one line),
  # or off (only the closing audit line).
  progress: compact

# Numeric knobs the policies read. Uncomment to change.
#thresholds:
#  minApprovers: 2        # approvals a pull request must collect (CIS-1.1.3)
#  minRepositoryAdmins: 2 # fewer is a bus-factor risk (CIS-1.3.7)
#  minOrgAdmins: 2        # instance administrator lower bound (CIS-1.3.3)
#  maxOrgAdmins: 5        # ... and upper bound; 0 means unbounded
#  staleBranchDays: 90    # days untouched before a branch counts as abandoned (CIS-1.1.8)
#  maxStaleBranches: 0    # how many abandoned branches a repository may carry
#  inactiveUserDays: 90   # days without authenticating before review (CIS-1.3.1)

# Which hook add-ons count as commit signature verification (CIS-1.1.12).
#signatureHookKeys: [signature, signed-commit, gpg, verify-commit, commit-signing]

# Merge strategies that break linear history (CIS-1.1.13).
#nonLinearMergeStrategies: [no-ff, rebase-no-ff]

# Paths probed on the default branch for a security policy (CIS-1.2.1).
#securityPolicyPaths: [SECURITY.md, .github/SECURITY.md, docs/SECURITY.md, SECURITY.rst, SECURITY.txt, SECURITY]

# The highest permission a project may hand every licensed user (CIS-1.3.8).
#maxDefaultPermission: REPO_READ

# For instances that intentionally publish code.
#allowPublicRepositories: false

# Drop archived repositories from the scan.
#skipArchivedRepositories: true

# Leave controls out of the run, or restrict the run to a list.
#exclude: [CIS-1.1.8]
#include: []
`

func newInitCommand() *cobra.Command {
	var path string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a commented scm-bench.yaml into the working directory",
		Long: `Init writes a configuration template with every key present, commented, and
set to its default, so the file doubles as the documentation of what can be
configured. scan discovers it in the working directory without --config.

An existing file is never overwritten: a config that changes how an audit
judges an instance is not something a scaffolding command should replace.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// O_EXCL does the refusing atomically: stat-then-write would race,
			// and racing towards overwriting someone's thresholds is the worst
			// direction to race in.
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				if os.IsExist(err) {
					return fmt.Errorf("%s already exists; edit it, or pass --path to write the template elsewhere", path)
				}
				return fmt.Errorf("create %s: %w", path, err)
			}
			if _, err := f.WriteString(configTemplate); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", path, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}

			stderr := cmd.ErrOrStderr()
			console.Writer{W: stderr, P: console.Painter{Enabled: isTerminal(stderr) && !hasNoColorEnv()}}.
				Line(console.Info, "wrote %s; scan will find it here without --config", path)
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "scm-bench.yaml", "where to write the template")
	return cmd
}
