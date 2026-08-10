package checks

import (
	"path"
	"strings"
	"testing"
)

func TestBundleLoads(t *testing.T) {
	bundle, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bundle.Checks) < 20 {
		t.Errorf("bundle has %d checks, want at least 20", len(bundle.Checks))
	}
	if len(bundle.Modules) < len(bundle.Checks) {
		t.Errorf("bundle has %d modules for %d checks", len(bundle.Modules), len(bundle.Checks))
	}
}

// The Rego package declared in metadata has to match the module sitting next to
// it, otherwise the engine silently evaluates the wrong rule or none at all.
func TestEveryCheckHasAMatchingModule(t *testing.T) {
	bundle, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	packagesByDir := map[string]string{}
	for _, m := range bundle.Modules {
		for _, line := range strings.Split(m.Source, "\n") {
			line = strings.TrimSpace(line)
			if pkg, ok := strings.CutPrefix(line, "package "); ok {
				packagesByDir[path.Dir(m.Path)] = strings.TrimSpace(pkg)
				break
			}
		}
	}

	for _, c := range bundle.Checks {
		declared, ok := packagesByDir[c.Dir]
		if !ok {
			t.Errorf("%s: no Rego module in %s", c.ID, c.Dir)
			continue
		}
		if declared != c.Package {
			t.Errorf("%s: metadata declares package %q but the module declares %q", c.ID, c.Package, declared)
		}
	}
}

// Remediation is the part of a finding that gets acted on. A vague one is worse
// than useless, so every control must say where to go: a settings path, the
// file to add, or an explicit statement that nothing applies.
func TestRemediationSaysWhereToAct(t *testing.T) {
	bundle, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	namesALocation := func(remediation string) bool {
		lower := strings.ToLower(remediation)
		switch {
		case strings.Contains(remediation, "->"): // a settings path
			return true
		case strings.Contains(remediation, ".md"): // a file to add
			return true
		case strings.Contains(lower, "no action applies"), strings.Contains(remediation, "无需处理"):
			// The control is NA; saying so plainly is the correct remediation.
			return true
		}
		return false
	}

	for _, c := range bundle.Checks {
		if len(c.Remediation) < 40 {
			t.Errorf("%s: remediation is too terse to act on: %q", c.ID, c.Remediation)
		}
		if !namesALocation(c.Remediation) {
			t.Errorf("%s: remediation does not say where to act: %q", c.ID, c.Remediation)
		}
		if !namesALocation(c.RemediationZh) {
			t.Errorf("%s: Chinese remediation does not say where to act: %q", c.ID, c.RemediationZh)
		}
		if c.Description == "" {
			t.Errorf("%s: description is empty", c.ID)
		}
		if len(c.References) == 0 {
			t.Errorf("%s: no references", c.ID)
		}
		if c.TitleZh == "" {
			t.Errorf("%s: missing Chinese title", c.ID)
		}
		if c.RemediationZh == "" {
			t.Errorf("%s: missing Chinese remediation", c.ID)
		}
	}
}

func TestChecksAreSortedByBenchmarkNumber(t *testing.T) {
	bundle, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 1.1.9 must come before 1.1.11: the ordering is numeric, not lexical.
	var seen1119, seen11111 int = -1, -1
	for i, c := range bundle.Checks {
		switch c.CISID {
		case "1.1.9":
			seen1119 = i
		case "1.1.11":
			seen11111 = i
		}
	}
	if seen1119 < 0 || seen11111 < 0 {
		t.Fatal("expected both 1.1.9 and 1.1.11 in the bundle")
	}
	if seen1119 > seen11111 {
		t.Error("1.1.9 should sort before 1.1.11")
	}
}

func TestWeightsFollowSeverity(t *testing.T) {
	if Weight(SeverityHigh) <= Weight(SeverityMedium) || Weight(SeverityMedium) <= Weight(SeverityLow) {
		t.Error("weights must be strictly decreasing from HIGH to LOW")
	}
	if Weight("nonsense") != Weight(SeverityLow) {
		t.Error("an unknown severity should weigh the least, not the most")
	}
}

func TestAppliesTo(t *testing.T) {
	c := Check{Metadata: Metadata{Platforms: []string{"bitbucket-dc"}}}
	if !c.AppliesTo("BITBUCKET-DC") {
		t.Error("platform matching should be case-insensitive")
	}
	if c.AppliesTo("github") {
		t.Error("a bitbucket-only check must not apply to github")
	}
}

func TestLessCISID(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.1.9", "1.1.11", true},
		{"1.1.11", "1.1.9", false},
		{"1.1.3", "1.2.1", true},
		{"1.2.1", "1.1.17", false},
		{"1.1", "1.1.1", true},
	}
	for _, tc := range tests {
		if got := LessCISID(tc.a, tc.b); got != tc.want {
			t.Errorf("LessCISID(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
