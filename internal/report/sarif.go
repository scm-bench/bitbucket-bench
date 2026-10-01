package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/scm-bench/bitbucket-bench/internal/checks"
	"github.com/scm-bench/bitbucket-bench/internal/engine"
)

// SARIF 2.1.0. Findings here are configuration facts about a repository rather
// than lines of source, so each result names the repository as a
// logicalLocation — and also carries a physicalLocation, because GitHub code
// scanning drops any result without one. The upload reports success and the
// Security tab stays empty, which a pipeline reads as nothing to fix. The
// artifact URI is a stable path naming the instance and the resource; it does
// not need to exist in the repository the SARIF is uploaded to (OpenSSF
// Scorecard does the same).

const (
	sarifVersion = "2.1.0"
	// The old master/Schemata/ path 404s — oasis-tcs renamed the default branch
	// and restructured the repository. A $schema that does not resolve is not
	// cosmetic: a validating consumer fetches it, and every report we emit
	// carries the URL.
	sarifSchema  = "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/main/sarif-2.1/schema/sarif-schema-2.1.0.json"
	sarifInfoURI = "https://github.com/scm-bench/bitbucket-bench"
	sarifTool    = "bitbucket-bench"

	// maxSARIFResults is GitHub's cap on what it keeps from one run: past
	// 5,000 it keeps the most severe 5,000, past 25,000 it rejects the file.
	// Capping here, most severe first, makes which ones survive our choice
	// and lets the run say how many it withheld.
	maxSARIFResults = 5000
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool              sarifToolInfo          `json:"tool"`
	AutomationDetails *sarifAutomationDetail `json:"automationDetails,omitempty"`
	Results           []sarifResult          `json:"results"`
	Invocations       []sarifInvocation      `json:"invocations,omitempty"`
	Properties        map[string]any         `json:"properties,omitempty"`
}

// sarifAutomationDetail identifies the run's category. Without one, two
// instances uploading to the same GitHub repository share a category, and each
// upload closes the other's alerts as fixed.
type sarifAutomationDetail struct {
	ID string `json:"id"`
}

type sarifToolInfo struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	ShortDescription sarifText `json:"shortDescription"`
	// No omitempty on these two: it does nothing on a struct, and both are
	// always populated anyway — fullDescription falls back to the title, and
	// remediation is required of every control.
	FullDescription      sarifText         `json:"fullDescription"`
	Help                 sarifText         `json:"help"`
	HelpURI              string            `json:"helpUri,omitempty"`
	DefaultConfiguration sarifRuleConfig   `json:"defaultConfiguration"`
	Properties           sarifRuleProperty `json:"properties"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifRuleConfig struct {
	Level string `json:"level"`
}

type sarifRuleProperty struct {
	Tags []string `json:"tags,omitempty"`
	// SecuritySeverity is the 0-10 numeric score consumers such as GitHub code
	// scanning use to bucket findings.
	SecuritySeverity string `json:"security-severity,omitempty"`
	Severity         string `json:"severity,omitempty"`
	CISID            string `json:"cisId,omitempty"`
	Automated        bool   `json:"automated"`
}

type sarifResult struct {
	RuleID              string             `json:"ruleId"`
	Level               string             `json:"level"`
	Message             sarifText          `json:"message"`
	Locations           []sarifLocation    `json:"locations,omitempty"`
	PartialFingerprints map[string]string  `json:"partialFingerprints,omitempty"`
	Suppressions        []sarifSuppression `json:"suppressions,omitempty"`
	Properties          map[string]any     `json:"properties,omitempty"`
}

// sarifSuppression is SARIF's own way of saying a result was reviewed and
// accepted, which is what an exception is; a consumer that understands it
// shows the result as dismissed rather than open.
type sarifSuppression struct {
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Justification string `json:"justification"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation  `json:"physicalLocation"`
	LogicalLocations []sarifLogicalLocation `json:"logicalLocations"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

// sarifRegion is required by GitHub alongside the artifact; a configuration
// finding has no line, so it is the first character of the artifact.
type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

type sarifLogicalLocation struct {
	Name               string `json:"name"`
	FullyQualifiedName string `json:"fullyQualifiedName"`
	Kind               string `json:"kind"`
}

type sarifInvocation struct {
	ExecutionSuccessful        bool                `json:"executionSuccessful"`
	ToolExecutionNotifications []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string    `json:"level"`
	Message sarifText `json:"message"`
}

func writeSARIF(w io.Writer, rep *engine.Report, opts Options) error {
	host := instanceHost(rep.Metadata.BaseURL)
	where := place{platform: platformOf(rep), host: host}
	rules := map[string]sarifRule{}
	// Not `var results []sarifResult`: a nil slice marshals to null, and
	// run.results is typed `array` in the SARIF schema. Strict validators and
	// GitHub's SARIF upload reject the file outright — and they would do it on
	// the one run where nothing failed, which is precisely the run an operator
	// least expects to be told their report is malformed.
	results := []sarifResult{}

	// A control no API can answer is one question for a person, however many
	// resources it spans: one result per control, not one per repository.
	// Per repository, a thousand-repository instance carried thousands of
	// identical "manual review" results toward GitHub's cap.
	byDesign := map[string][]engine.Finding{}

	for _, f := range rep.Findings {
		// NA findings are noise in a CI report: the control does not apply.
		// PASS is omitted for the same reason SARIF consumers expect only
		// actionable results.
		if f.Status != engine.StatusFail && f.Status != engine.StatusManual {
			continue
		}
		if f.Status == engine.StatusManual && !f.Automated {
			byDesign[f.CheckID] = append(byDesign[f.CheckID], f)
			continue
		}
		rule := buildRule(f)
		rules[rule.ID] = rule
		results = append(results, buildResult(f, where))
	}
	for _, group := range byDesign {
		rule := buildRule(group[0])
		rules[rule.ID] = rule
		results = append(results, buildAggregateResult(group, where))
	}

	// Most severe first, so a cap keeps what matters; then stable.
	sort.SliceStable(results, func(i, j int) bool {
		if a, b := resultRank(results[i]), resultRank(results[j]); a != b {
			return a > b
		}
		return results[i].RuleID+results[i].Locations[0].PhysicalLocation.ArtifactLocation.URI <
			results[j].RuleID+results[j].Locations[0].PhysicalLocation.ArtifactLocation.URI
	})
	withheld := 0
	if len(results) > maxSARIFResults {
		withheld = len(results) - maxSARIFResults
		results = results[:maxSARIFResults]
	}

	ruleList := make([]sarifRule, 0, len(rules))
	for _, r := range rules {
		ruleList = append(ruleList, r)
	}
	sort.Slice(ruleList, func(i, j int) bool { return ruleList[i].ID < ruleList[j].ID })

	run := sarifRun{
		Tool: sarifToolInfo{Driver: sarifDriver{
			Name:           sarifTool,
			Version:        opts.ToolVersion,
			InformationURI: sarifInfoURI,
			Rules:          ruleList,
		}},
		AutomationDetails: &sarifAutomationDetail{ID: sarifTool + "/" + host + "/"},
		Results:           results,
		Properties: map[string]any{
			"score":    rep.Score.Value,
			"passed":   rep.Score.Passed,
			"failed":   rep.Score.Failed,
			"manual":   rep.Score.Manual,
			"platform": rep.Metadata.Platform,
			"baseUrl":  rep.Metadata.BaseURL,
		},
	}

	// Scan warnings and policy errors travel as invocation notifications so a
	// partial scan is not mistaken for a clean one.
	var notifications []sarifNotification
	for _, warning := range rep.Metadata.Warnings {
		notifications = append(notifications, sarifNotification{Level: "warning", Message: sarifText{Text: warning}})
	}
	for _, e := range rep.Errors {
		notifications = append(notifications, sarifNotification{Level: "error", Message: sarifText{Text: e}})
	}
	if len(rep.Metadata.Unlisted) > 0 {
		notifications = append(notifications, sarifNotification{Level: "error", Message: sarifText{Text: fmt.Sprintf(
			"the repositories of %s could not be listed and are missing from this run",
			strings.Join(rep.Metadata.Unlisted, ", "))}})
	}
	if withheld > 0 {
		notifications = append(notifications, sarifNotification{Level: "warning", Message: sarifText{Text: fmt.Sprintf(
			"%d less severe results were withheld to stay within code scanning's %d-result limit; -o json carries every finding",
			withheld, maxSARIFResults)}})
	}
	run.Invocations = []sarifInvocation{{
		// A run that could not list every project did not succeed in what it
		// set out to do, whatever it found in the rest.
		ExecutionSuccessful:        len(rep.Errors) == 0 && len(rep.Metadata.Unlisted) == 0,
		ToolExecutionNotifications: notifications,
	}}

	log := sarifLog{Schema: sarifSchema, Version: sarifVersion, Runs: []sarifRun{run}}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(log)
}

// instanceHost is the instance's host, the stable part of every artifact URI
// and of the run's category.
func instanceHost(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "instance"
}

// place is where a run's results live: the platform and the instance.
type place struct {
	platform string
	host     string
}

func platformOf(rep *engine.Report) string {
	if rep.Metadata.Platform != "" {
		return rep.Metadata.Platform
	}
	return "bitbucket-dc"
}

// artifactURI names a resource as a relative path: platform, instance, then
// the resource's own path, each segment escaped.
func artifactURI(where place, f engine.Finding) string {
	segments := []string{url.PathEscape(where.platform), url.PathEscape(where.host)}
	for _, part := range strings.Split(f.Resource, "/") {
		segments = append(segments, url.PathEscape(part))
	}
	return strings.Join(segments, "/")
}

// resultRank orders results for the cap: real failures by severity, then
// everything that only needs a person.
func resultRank(r sarifResult) int {
	if strings.HasSuffix(r.RuleID, manualRuleSuffix) {
		return 0
	}
	switch r.Level {
	case "error":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

// manualRuleSuffix marks the rule a MANUAL result points at.
const manualRuleSuffix = "/manual"

func buildRule(f engine.Finding) sarifRule {
	helpURI := ""
	if len(f.References) > 0 {
		helpURI = f.References[0]
	}
	// SARIF distinguishes the two: shortDescription is the label a viewer puts
	// in a list, fullDescription is what it shows when the reader wants to know
	// what the rule is about. Repeating the title in both, as this did before
	// the control's description was carried on the finding, wasted the field.
	full := f.Description
	if strings.TrimSpace(full) == "" {
		full = f.Title
	}

	rule := sarifRule{
		ID:                   f.CheckID,
		Name:                 strings.ReplaceAll(f.CheckID, "-", ""),
		ShortDescription:     sarifText{Text: f.Title},
		FullDescription:      sarifText{Text: full},
		Help:                 sarifText{Text: f.Remediation},
		HelpURI:              helpURI,
		DefaultConfiguration: sarifRuleConfig{Level: sarifLevel(f.Severity)},
		Properties: sarifRuleProperty{
			Tags:             []string{"security", "supply-chain", "cis", "source-code"},
			SecuritySeverity: securitySeverity(f.Severity),
			Severity:         strings.ToUpper(f.Severity),
			CISID:            f.CISID,
			Automated:        f.Automated,
		},
	}
	// A MANUAL result points at a rule of its own, with no security-severity.
	// GitHub takes an alert's displayed severity from the rule, not the
	// result, so sharing the control's rule made "a person needs to check
	// this" display as a High alert the moment any repository failed the same
	// control — and a control that can only be MANUAL, like multi-factor
	// authentication, displayed as High on every run.
	if f.Status == engine.StatusManual {
		rule.ID += manualRuleSuffix
		rule.Name += "Manual"
		rule.ShortDescription = sarifText{Text: "Manual review: " + f.Title}
		rule.DefaultConfiguration = sarifRuleConfig{Level: "note"}
		rule.Properties.SecuritySeverity = ""
		rule.Properties.Tags = append(rule.Properties.Tags, "manual-review")
	}
	return rule
}

func buildResult(f engine.Finding, where place) sarifResult {
	level := sarifLevel(f.Severity)
	ruleID := f.CheckID
	if f.Status == engine.StatusManual {
		// A control nobody could evaluate is not an assertion that something
		// is broken, so it never escalates past a note.
		level = "note"
		ruleID += manualRuleSuffix
	}

	message := f.Details
	if f.Status == engine.StatusManual {
		message = "Manual review required: " + message
	}
	if fix := f.Remediation; fix != "" {
		message += "\n\nRemediation: " + fix
	}

	return sarifResult{
		RuleID:    ruleID,
		Level:     level,
		Message:   sarifText{Text: message},
		Locations: []sarifLocation{location(where, f)},
		// Fingerprinting on control plus resource lets a consumer track the
		// same finding across runs even as wording changes. GitHub reads only
		// primaryLocationLineHash; scmBenchFindingV1 is the family's own key.
		PartialFingerprints: map[string]string{
			"primaryLocationLineHash": fingerprint(where.host, f.CheckID, f.Resource) + ":1",
			"scmBenchFindingV1":       fingerprint(f.CheckID, f.Resource),
		},
		Suppressions: suppressionsFor(f),
		Properties: map[string]any{
			"status":       string(f.Status),
			"severity":     strings.ToUpper(f.Severity),
			"cisId":        f.CISID,
			"resourceType": f.ResourceType,
		},
	}
}

func suppressionsFor(f engine.Finding) []sarifSuppression {
	if f.Waiver == nil {
		return nil
	}
	return []sarifSuppression{{Kind: "external", Status: "accepted", Justification: acceptedNote(f.Waiver)}}
}

// buildAggregateResult is the one result for a control no API can answer,
// naming how many resources it covers and anchored at the instance.
func buildAggregateResult(group []engine.Finding, where place) sarifResult {
	f := group[0]
	anchor := f
	anchor.Resource = "instance"
	anchor.ResourceType = "organization"
	result := buildResult(anchor, where)
	if len(group) > 1 {
		result.Message.Text = fmt.Sprintf("Manual review required for %d %ss: %s", len(group), f.ResourceType, f.Details)
		if fix := f.Remediation; fix != "" {
			result.Message.Text += "\n\nRemediation: " + fix
		}
	}
	result.Properties["resources"] = len(group)
	return result
}

func location(where place, f engine.Finding) sarifLocation {
	return sarifLocation{
		PhysicalLocation: sarifPhysicalLocation{
			ArtifactLocation: sarifArtifactLocation{URI: artifactURI(where, f)},
			Region:           sarifRegion{StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 1},
		},
		LogicalLocations: []sarifLogicalLocation{{
			Name:               f.Resource,
			FullyQualifiedName: f.Resource,
			Kind:               f.ResourceType,
		}},
	}
}

func sarifLevel(severity string) string {
	switch strings.ToUpper(severity) {
	case checks.SeverityHigh:
		return "error"
	case checks.SeverityMedium:
		return "warning"
	default:
		return "note"
	}
}

func securitySeverity(severity string) string {
	switch strings.ToUpper(severity) {
	case checks.SeverityHigh:
		return "8.0"
	case checks.SeverityMedium:
		return "5.0"
	default:
		return "2.0"
	}
}

func fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
