// Package diff compares two evaluations of the same instance, so a scan can
// answer "did this get worse" rather than only "how bad is it".
//
// Both sides are evaluated by the same engine with the same configuration
// before being compared. Comparing two already-rendered reports would be
// easier, but the difference would then include whatever changed about the
// tool or its thresholds between the two runs, which is exactly what a
// regression check must not be confused by.
package diff

import (
	"sort"

	"github.com/scm-bench/scm-bench/internal/checks"
	"github.com/scm-bench/scm-bench/internal/engine"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// Change is one control on one resource whose verdict moved.
type Change struct {
	CheckID      string        `json:"checkId"`
	CISID        string        `json:"cisId"`
	Title        string        `json:"title"`
	TitleZh      string        `json:"titleZh,omitempty"`
	Severity     string        `json:"severity"`
	Resource     string        `json:"resource"`
	ResourceType string        `json:"resourceType"`
	From         engine.Status `json:"from,omitempty"`
	To           engine.Status `json:"to"`
	// Details is the current verdict's explanation — what is true now, which is
	// what someone acting on the change needs.
	Details     string `json:"details"`
	Remediation string `json:"remediation,omitempty"`
	// RemediationZh mirrors the finding so the diff can be rendered in Chinese
	// without reaching back into the bundle.
	RemediationZh string `json:"remediationZh,omitempty"`
}

// Result is the full comparison.
type Result struct {
	Before Side `json:"before"`
	After  Side `json:"after"`

	// Regressed is PASS to FAIL: a control that was satisfied and no longer is.
	// This is the only category that means the instance got worse in a way
	// nobody chose, and the only one that drives the exit code.
	Regressed []Change `json:"regressed,omitempty"`
	// Fixed is FAIL to PASS.
	Fixed []Change `json:"fixed,omitempty"`
	// NewFailures are failures on resources that did not exist before — a
	// repository added since the baseline. Worth seeing, but not a regression:
	// nothing got worse, there is simply more of the instance.
	NewFailures []Change `json:"newFailures,omitempty"`
	// Changed is every other movement, including anything involving MANUAL.
	// A control going PASS to MANUAL means the tool stopped being able to see,
	// not that the setting changed, and calling that a regression would blame
	// the instance for a lost permission or a removed add-on.
	Changed []Change `json:"changed,omitempty"`
	// Departed are resources present before and absent now.
	Departed []Change `json:"departed,omitempty"`
}

// Side summarises one of the two evaluations.
type Side struct {
	BaseURL     string       `json:"baseUrl,omitempty"`
	GeneratedAt string       `json:"generatedAt,omitempty"`
	Score       engine.Score `json:"score"`
}

// HasRegression reports whether any control fell from PASS to FAIL. It backs
// the exit code.
func (r *Result) HasRegression() bool { return len(r.Regressed) > 0 }

// Compare diffs two reports of the same instance.
func Compare(before, after *engine.Report) *Result {
	result := &Result{
		Before: side(before),
		After:  side(after),
	}

	old := index(before.Findings)
	current := index(after.Findings)

	for key, now := range current {
		then, existed := old[key]
		change := changeOf(now)

		switch {
		case !existed:
			// A resource nobody has seen before. Only its failures are worth
			// reporting; a new repository that passes is not news.
			if now.Status == engine.StatusFail {
				result.NewFailures = append(result.NewFailures, change)
			}
		case then.Status == now.Status:
			// No movement.
		case then.Status == engine.StatusPass && now.Status == engine.StatusFail:
			change.From = then.Status
			result.Regressed = append(result.Regressed, change)
		case then.Status == engine.StatusFail && now.Status == engine.StatusPass:
			change.From = then.Status
			result.Fixed = append(result.Fixed, change)
		default:
			change.From = then.Status
			result.Changed = append(result.Changed, change)
		}
	}

	for key, then := range old {
		if _, still := current[key]; !still {
			change := changeOf(then)
			change.From = then.Status
			change.To = ""
			result.Departed = append(result.Departed, change)
		}
	}

	for _, set := range [][]Change{
		result.Regressed, result.Fixed, result.NewFailures, result.Changed, result.Departed,
	} {
		sortChanges(set)
	}
	return result
}

func side(rep *engine.Report) Side {
	s := Side{Score: rep.Score, BaseURL: rep.Metadata.BaseURL}
	if !rep.Metadata.GeneratedAt.IsZero() {
		s.GeneratedAt = rep.Metadata.GeneratedAt.UTC().Format("2006-01-02 15:04:05 MST")
	}
	return s
}

// findingKey identifies the same question asked of the same thing. Both parts
// are needed: a control alone spans every repository, and a resource alone
// spans every control.
type findingKey struct {
	checkID  string
	resource string
}

func index(findings []engine.Finding) map[findingKey]engine.Finding {
	out := make(map[findingKey]engine.Finding, len(findings))
	for _, f := range findings {
		out[findingKey{checkID: f.CheckID, resource: f.Resource}] = f
	}
	return out
}

func changeOf(f engine.Finding) Change {
	return Change{
		CheckID:       f.CheckID,
		CISID:         f.CISID,
		Title:         f.Title,
		TitleZh:       f.TitleZh,
		Severity:      f.Severity,
		Resource:      f.Resource,
		ResourceType:  f.ResourceType,
		To:            f.Status,
		Details:       f.Details,
		Remediation:   f.Remediation,
		RemediationZh: f.RemediationZh,
	}
}

// sortChanges orders a category the way it is read: worst severity first, then
// by benchmark number, then by resource.
func sortChanges(changes []Change) {
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if wa, wb := checks.Weight(a.Severity), checks.Weight(b.Severity); wa != wb {
			return wa > wb
		}
		if a.CISID != b.CISID {
			return checks.LessCISID(a.CISID, b.CISID)
		}
		return a.Resource < b.Resource
	})
}

// SameInstance reports whether two snapshots describe the same instance.
// Diffing two different instances produces a result that looks meaningful and
// is not, so the caller warns rather than letting it pass unremarked.
func SameInstance(before, after *scm.Snapshot) bool {
	return before.Metadata.BaseURL == after.Metadata.BaseURL
}
