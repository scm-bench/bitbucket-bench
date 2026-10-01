package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/scm-bench/bitbucket-bench/internal/config"
)

var exceptionNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func failing(check, resource, severity string) Finding {
	return Finding{CheckID: check, Resource: resource, Severity: severity, Status: StatusFail}
}

func TestExceptionsAcceptMatchingFindingsOnly(t *testing.T) {
	findings := []Finding{
		failing("CIS-1.1.13", "PLAT/legacy-billing", "LOW"),
		failing("CIS-1.1.13", "PLAT/payments", "LOW"),
		failing("CIS-1.1.15", "PLAT/legacy-billing", "HIGH"),
		{CheckID: "CIS-1.1.13", Resource: "PLAT/legacy-ui", Severity: "LOW", Status: StatusPass},
	}
	warnings := applyExceptions(findings, []config.Exception{{
		Control: "cis-1.1.13", Resources: []string{"plat/legacy-*"},
		Reason: "release tooling", Owner: "platform", Expires: "2027-03-31",
	}}, exceptionNow)

	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if w := findings[0].Waiver; w == nil || w.Reason != "release tooling" || w.Owner != "platform" || w.Expires != "2027-03-31" {
		t.Errorf("legacy-billing CIS-1.1.13 waiver = %+v, want the exception", w)
	}
	if findings[1].Waiver != nil {
		t.Error("PLAT/payments does not match plat/legacy-* and must not be accepted")
	}
	if findings[2].Waiver != nil {
		t.Error("another control on the same repository must not be accepted")
	}
	if findings[3].Waiver != nil {
		t.Error("a PASS has nothing to accept")
	}
}

// A lapsed exception does nothing, and says so: its findings fail the run
// again from the day after its expiry.
func TestLapsedExceptionsAreNotAppliedAndAreReported(t *testing.T) {
	findings := []Finding{failing("CIS-1.1.13", "PLAT/legacy", "LOW")}
	warnings := applyExceptions(findings, []config.Exception{{
		Control: "CIS-1.1.13", Resources: []string{"PLAT/legacy"}, Reason: "r", Expires: "2026-09-30",
	}}, exceptionNow)
	if findings[0].Waiver != nil {
		t.Error("a lapsed exception was applied")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "lapsed on 2026-09-30") {
		t.Errorf("warnings = %v, want the lapse reported", warnings)
	}

	// The expiry day itself still counts.
	findings = []Finding{failing("CIS-1.1.13", "PLAT/legacy", "LOW")}
	applyExceptions(findings, []config.Exception{{
		Control: "CIS-1.1.13", Resources: []string{"PLAT/legacy"}, Reason: "r", Expires: "2026-10-01",
	}}, exceptionNow)
	if findings[0].Waiver == nil {
		t.Error("an exception expiring today must still apply today")
	}
}

// An exception that accepts nothing is a finding fixed, renamed or out of
// scope — or a typo. Either way the list is rotting, and it is said.
func TestExceptionsMatchingNothingAreReported(t *testing.T) {
	warnings := applyExceptions([]Finding{failing("CIS-1.1.13", "PLAT/a", "LOW")}, []config.Exception{{
		Control: "CIS-1.1.15", Resources: []string{"PLAT/a"}, Reason: "r", Expires: "2027-01-01",
	}}, exceptionNow)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "accepts nothing") {
		t.Errorf("warnings = %v, want the stale exception reported", warnings)
	}
}

// The score describes the instance, and accepting a finding does not change
// the instance; what changes is whether the run fails on it.
func TestAcceptedFailuresStayInTheScoreButDoNotFailTheRun(t *testing.T) {
	findings := []Finding{failing("CIS-1.1.15", "PLAT/a", "HIGH"), {CheckID: "CIS-1.1.3", Resource: "PLAT/a", Severity: "HIGH", Status: StatusPass}}
	before := Compute(findings)
	applyExceptions(findings, []config.Exception{{
		Control: "CIS-1.1.15", Resources: []string{"PLAT/*"}, Reason: "r", Expires: "2027-01-01",
	}}, exceptionNow)
	rep := &Report{Findings: findings, Score: Compute(findings)}

	if rep.Score.Value != before.Value || rep.Score.Failed != before.Failed || rep.Score.EarnedWeight != before.EarnedWeight {
		t.Errorf("score changed from %+v to %+v", before, rep.Score)
	}
	if rep.HasFailureAtOrAbove("high") {
		t.Error("an accepted HIGH failure still fails the run")
	}
	findings[0].Waiver = nil
	if !(&Report{Findings: findings}).HasFailureAtOrAbove("high") {
		t.Error("an unaccepted HIGH failure must fail the run")
	}
}
