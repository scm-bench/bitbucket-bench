package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadWithoutPathReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Thresholds.MinApprovers != 2 {
		t.Errorf("minApprovers = %d, want 2", cfg.Thresholds.MinApprovers)
	}
}

// A partial file must only change what it mentions; everything else keeps its
// default, so a user does not have to restate the whole benchmark to move one
// number.
func TestPartialConfigOverlaysDefaults(t *testing.T) {
	path := writeConfig(t, "thresholds:\n  minApprovers: 1\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Thresholds.MinApprovers != 1 {
		t.Errorf("minApprovers = %d, want 1", cfg.Thresholds.MinApprovers)
	}
	if cfg.Thresholds.StaleBranchDays != 90 {
		t.Errorf("staleBranchDays = %d, want the default 90", cfg.Thresholds.StaleBranchDays)
	}
	if len(cfg.SignatureHookKeys) == 0 {
		t.Error("signatureHookKeys should retain its defaults")
	}
	if cfg.MaxDefaultPermission != "REPO_READ" {
		t.Errorf("maxDefaultPermission = %q", cfg.MaxDefaultPermission)
	}
}

func TestListsAreReplacedNotMerged(t *testing.T) {
	path := writeConfig(t, "signatureHookKeys:\n  - my-vendor-hook\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SignatureHookKeys) != 1 || cfg.SignatureHookKeys[0] != "my-vendor-hook" {
		t.Errorf("signatureHookKeys = %v, want the user's list verbatim", cfg.SignatureHookKeys)
	}
}

func TestInvalidConfigIsRejected(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"negative approvers", "thresholds:\n  minApprovers: -1\n"},
		{"inverted admin range", "thresholds:\n  minOrgAdmins: 9\n  maxOrgAdmins: 2\n"},
		{"negative stale window", "thresholds:\n  staleBranchDays: -5\n"},
		{"unknown permission ceiling", "maxDefaultPermission: NOT_A_PERMISSION\n"},
		{"malformed yaml", "thresholds: [\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.body)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestMissingFileIsAnError(t *testing.T) {
	if _, err := Load("/nonexistent/config.yaml"); err == nil {
		t.Error("a config path that does not exist should be an error, not a silent default")
	}
}

// A misspelled key is the dangerous failure: it parses, changes nothing, and
// produces a report the user believes was evaluated at their threshold. Both
// levels of the document have to reject it.
func TestUnknownKeysAreRejected(t *testing.T) {
	for name, body := range map[string]string{
		"misspelled threshold": "thresholds:\n  minApprover: 5\n",
		"unknown top level":    "typoKey: true\n",
		"unknown nested":       "thresholds:\n  minApprovers: 2\n  maxApprovers: 9\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Errorf("Load(%q) succeeded; an unrecognised key must be an error", body)
			}
		})
	}
}

// Rejecting unknown keys must not turn "change nothing" into an error: a file
// that is empty, or only comments, is a valid way to say "keep every default".
func TestEmptyConfigKeepsDefaults(t *testing.T) {
	for name, body := range map[string]string{
		"empty":         "",
		"comments only": "# nothing to change here\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Thresholds.MinApprovers != Default().Thresholds.MinApprovers {
				t.Errorf("minApprovers = %d, want the default", cfg.Thresholds.MinApprovers)
			}
		})
	}
}

func TestSelectsHonoursIncludeAndExclude(t *testing.T) {
	cfg := Default()
	if !cfg.Selects("CIS-1.1.3") {
		t.Error("everything should be selected by default")
	}

	cfg.Exclude = []string{"cis-1.1.3"}
	if cfg.Selects("CIS-1.1.3") {
		t.Error("exclude should match case-insensitively")
	}

	cfg = Default()
	cfg.Include = []string{"CIS-1.1.15"}
	if cfg.Selects("CIS-1.1.3") {
		t.Error("a non-empty include list should exclude everything else")
	}
	if !cfg.Selects("CIS-1.1.15") {
		t.Error("an included check should be selected")
	}

	// Exclude wins over include, so a user can pin a list and drop one entry.
	cfg.Exclude = []string{"CIS-1.1.15"}
	if cfg.Selects("CIS-1.1.15") {
		t.Error("exclude should take precedence over include")
	}
}

// A blank entry is matched by every string, so it does not narrow a list — it
// disables the check the list drives. signatureHookKeys is the dangerous one:
// `contains(haystack, "")` is true for any hook, so one stray blank turns
// CIS-1.1.12 into a PASS that verified nothing.
func TestBlankListEntriesAreRejected(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"signature hook key", "signatureHookKeys:\n  - \"\"\n  - gpg\n", "signatureHookKeys[0]"},
		{"whitespace only", "signatureHookKeys:\n  - gpg\n  - \"   \"\n", "signatureHookKeys[1]"},
		{"merge strategy", "nonLinearMergeStrategies:\n  - \"\"\n", "nonLinearMergeStrategies[0]"},
		{"security policy path", "securityPolicyPaths:\n  - SECURITY.md\n  - \"\"\n", "securityPolicyPaths[1]"},
		{"exclude", "exclude:\n  - \"\"\n", "exclude[0]"},
		{"include", "include:\n  - \"\"\n", "include[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.yaml))
			if err == nil {
				t.Fatalf("Load accepted a blank entry in %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
		})
	}
}

// A negative threshold does not produce an odd number, it inverts the control.
// `minRepositoryAdmins: -1` made `count >= minimum` true for a repository with
// no administrators at all, so CIS-1.3.7 reported PASS — and said "0
// administrator(s) can manage this repository" while doing it. Every threshold
// is covered, so a field added later is not left out by omission.
func TestNegativeThresholdsAreRejected(t *testing.T) {
	for _, field := range []string{
		"minApprovers",
		"minRepositoryAdmins",
		"minOrgAdmins",
		"maxOrgAdmins",
		"staleBranchDays",
		"maxStaleBranches",
		"inactiveUserDays",
	} {
		t.Run(field, func(t *testing.T) {
			_, err := Load(writeConfig(t, "thresholds:\n  "+field+": -1\n"))
			if err == nil {
				t.Fatalf("Load accepted thresholds.%s = -1", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error %q does not name the field", err)
			}
		})
	}
}

// Every threshold in the struct must appear in the check above; a new one that
// nobody adds is exactly how the first four came to be missing.
func TestEveryThresholdIsValidated(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(Thresholds{}))
	for _, f := range fields {
		if f.Type.Kind() != reflect.Int {
			continue
		}
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" {
			t.Errorf("Thresholds.%s has no yaml tag", f.Name)
			continue
		}
		if _, err := Load(writeConfig(t, "thresholds:\n  "+name+": -1\n")); err == nil {
			t.Errorf("thresholds.%s accepts -1; add it to Config.Validate", name)
		}
	}
}
