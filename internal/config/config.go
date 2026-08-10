// Package config holds the tunable thresholds that policies read. Every value
// here is handed to Rego as `input.config`, so a rule never hard-codes a number
// that a user might reasonably disagree with.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the full evaluation configuration.
type Config struct {
	// Thresholds are the numeric knobs used by the policies.
	Thresholds Thresholds `yaml:"thresholds" json:"thresholds"`
	// SignatureHookKeys are lowercase substrings that identify a commit
	// signature verification hook. Bitbucket has no built-in one, so which
	// add-on counts is deployment-specific.
	SignatureHookKeys []string `yaml:"signatureHookKeys" json:"signatureHookKeys"`
	// NonLinearMergeStrategies are merge strategy IDs that can introduce a
	// merge commit and therefore break linear history.
	NonLinearMergeStrategies []string `yaml:"nonLinearMergeStrategies" json:"nonLinearMergeStrategies"`
	// SecurityPolicyPaths are the paths probed on the default branch for a
	// security policy, in priority order.
	SecurityPolicyPaths []string `yaml:"securityPolicyPaths" json:"securityPolicyPaths"`
	// MaxDefaultPermission is the highest permission a project may hand to
	// every authenticated user by default.
	MaxDefaultPermission string `yaml:"maxDefaultPermission" json:"maxDefaultPermission"`
	// AllowPublicRepositories relaxes the public-access rule, for instances
	// that intentionally publish code.
	AllowPublicRepositories bool `yaml:"allowPublicRepositories" json:"allowPublicRepositories"`
	// SkipArchivedRepositories drops archived repositories from the scan.
	SkipArchivedRepositories bool `yaml:"skipArchivedRepositories" json:"skipArchivedRepositories"`
	// PermissionRank lets Rego compare Bitbucket permission levels ordinally.
	PermissionRank map[string]int `yaml:"permissionRank" json:"permissionRank"`
	// Exclude lists check IDs (e.g. "CIS-1.1.8") to leave out of the run.
	Exclude []string `yaml:"exclude" json:"exclude"`
	// Include, when non-empty, restricts the run to these check IDs.
	Include []string `yaml:"include" json:"include"`
}

// Thresholds are the numeric policy knobs.
type Thresholds struct {
	// MinApprovers is the number of approvals a pull request must collect.
	MinApprovers int `yaml:"minApprovers" json:"minApprovers"`
	// MinRepositoryAdmins guards against a repository with a single owner.
	MinRepositoryAdmins int `yaml:"minRepositoryAdmins" json:"minRepositoryAdmins"`
	// MinOrgAdmins / MaxOrgAdmins bracket the instance administrator count:
	// too few is a bus-factor risk, too many is an oversized blast radius.
	// Zero means "no bound on this side": the only other reading of
	// maxOrgAdmins: 0 is "no administrators at all", which is not a policy
	// anybody wants and used to fail every instance that had any.
	MinOrgAdmins int `yaml:"minOrgAdmins" json:"minOrgAdmins"`
	MaxOrgAdmins int `yaml:"maxOrgAdmins" json:"maxOrgAdmins"`
	// StaleBranchDays is how long a branch may sit untouched before it counts
	// as abandoned.
	StaleBranchDays int `yaml:"staleBranchDays" json:"staleBranchDays"`
	// MaxStaleBranches is how many abandoned branches a repository may carry.
	MaxStaleBranches int `yaml:"maxStaleBranches" json:"maxStaleBranches"`
	// InactiveUserDays is how long a user may go without authenticating before
	// their access should be reviewed.
	InactiveUserDays int `yaml:"inactiveUserDays" json:"inactiveUserDays"`
}

// Default returns the configuration used when the user supplies none. The
// values follow the CIS Software Supply Chain Security Guide where it is
// specific, and common practice where it is not.
func Default() Config {
	return Config{
		Thresholds: Thresholds{
			MinApprovers:        2,
			MinRepositoryAdmins: 2,
			MinOrgAdmins:        2,
			MaxOrgAdmins:        5,
			StaleBranchDays:     90,
			MaxStaleBranches:    0,
			InactiveUserDays:    90,
		},
		SignatureHookKeys: []string{
			"signature",
			"signed-commit",
			"gpg",
			"verify-commit",
			"commit-signing",
		},
		// "ff" is deliberately absent: it falls back to a merge commit only
		// when the target has moved, which is the normal cost of an otherwise
		// linear workflow. "no-ff" and "rebase-no-ff" always create one.
		NonLinearMergeStrategies: []string{"no-ff", "rebase-no-ff"},
		SecurityPolicyPaths: []string{
			"SECURITY.md",
			".github/SECURITY.md",
			"docs/SECURITY.md",
			"SECURITY.rst",
			"SECURITY.txt",
			"SECURITY",
		},
		MaxDefaultPermission:     "REPO_READ",
		AllowPublicRepositories:  false,
		SkipArchivedRepositories: true,
		PermissionRank: map[string]int{
			"":               0,
			"LICENSED_USER":  1,
			"REPO_READ":      10,
			"PROJECT_VIEW":   10,
			"PROJECT_READ":   10,
			"REPO_WRITE":     20,
			"PROJECT_WRITE":  20,
			"REPO_ADMIN":     30,
			"PROJECT_ADMIN":  30,
			"PROJECT_CREATE": 35,
			"ADMIN":          40,
			"SYS_ADMIN":      50,
		},
	}
}

// Load reads a YAML config from path and overlays it on the defaults, so a
// user file only needs to mention what it changes.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	// Decoding onto the populated struct leaves absent keys at their default.
	// Sequences are the exception: YAML replaces them wholesale, which is what
	// a user who lists signature hook keys expects.
	//
	// KnownFields is on because the failure mode without it is silent and
	// wrong: `minApprover` for `minApprovers` parses cleanly, changes nothing,
	// and produces a report the user believes was evaluated at their threshold.
	// An audit tool that quietly ignores its own configuration is worse than
	// one that refuses to start.
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		// An empty file is not an error: it means "keep every default".
		if !errors.Is(err, io.EOF) {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate rejects thresholds that would make a policy meaningless.
func (c Config) Validate() error {
	// Every threshold is checked, not just the ones that looked risky.
	//
	// A negative threshold does not merely produce an odd number: it silently
	// inverts the control it feeds. `minRepositoryAdmins: -1` makes
	// `count >= minimum` true for a repository with zero administrators, so
	// CIS-1.3.7 reports PASS and says "0 administrator(s) can manage this
	// repository" while doing it. `maxStaleBranches: -3` fails every
	// repository instead. Either way a single typo reconfigures a control into
	// something that is not a control any more, and nothing in the report says
	// so — which is why this is a startup error rather than a warning.
	t := c.Thresholds
	for _, f := range []struct {
		name  string
		value int
	}{
		{"minApprovers", t.MinApprovers},
		{"minRepositoryAdmins", t.MinRepositoryAdmins},
		{"minOrgAdmins", t.MinOrgAdmins},
		{"maxOrgAdmins", t.MaxOrgAdmins},
		{"staleBranchDays", t.StaleBranchDays},
		{"maxStaleBranches", t.MaxStaleBranches},
		{"inactiveUserDays", t.InactiveUserDays},
	} {
		if f.value < 0 {
			return fmt.Errorf("thresholds.%s must be >= 0, got %d", f.name, f.value)
		}
	}
	if t.MinOrgAdmins > 0 && t.MaxOrgAdmins > 0 && t.MinOrgAdmins > t.MaxOrgAdmins {
		return fmt.Errorf("thresholds.minOrgAdmins (%d) must not exceed maxOrgAdmins (%d)", t.MinOrgAdmins, t.MaxOrgAdmins)
	}
	if c.MaxDefaultPermission != "" {
		if _, ok := c.PermissionRank[c.MaxDefaultPermission]; !ok {
			return fmt.Errorf("maxDefaultPermission %q has no entry in permissionRank", c.MaxDefaultPermission)
		}
	}

	// A blank entry in any of these lists is matched by everything.
	//
	// signatureHookKeys is the one that bites: the rule asks whether a hook's
	// key or name contains any configured substring, and every string contains
	// "". One stray `- ""` in a YAML file turns CIS-1.1.12 into a control that
	// reports "signature verification is enforced by <whatever hook exists>" —
	// a confident PASS for a setting nobody verified, which is the single
	// failure mode this project is built to avoid. The others are less
	// dramatic but wrong in the same way, so they are checked together.
	for _, list := range []struct {
		field string
		items []string
	}{
		{"signatureHookKeys", c.SignatureHookKeys},
		{"nonLinearMergeStrategies", c.NonLinearMergeStrategies},
		{"securityPolicyPaths", c.SecurityPolicyPaths},
		{"exclude", c.Exclude},
		{"include", c.Include},
	} {
		for i, item := range list.items {
			if strings.TrimSpace(item) == "" {
				return fmt.Errorf("%s[%d] is empty; remove the entry rather than leaving it blank", list.field, i)
			}
		}
	}
	return nil
}

// Selects reports whether a check ID should run under this configuration.
func (c Config) Selects(id string) bool {
	for _, ex := range c.Exclude {
		if strings.EqualFold(strings.TrimSpace(ex), id) {
			return false
		}
	}
	if len(c.Include) == 0 {
		return true
	}
	for _, in := range c.Include {
		if strings.EqualFold(strings.TrimSpace(in), id) {
			return true
		}
	}
	return false
}
