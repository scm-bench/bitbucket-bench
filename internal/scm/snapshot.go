// Package scm defines the platform-neutral snapshot that fetchers produce and
// policies consume. Nothing in this package talks HTTP: a fetcher fills these
// structs in, they are marshalled to JSON, and every rule decision is made by
// Rego reading that JSON. Keeping the shape stable is what lets a snapshot be
// captured once and re-evaluated later, offline.
package scm

import "time"

// SchemaVersion is bumped whenever the snapshot shape changes in a way that
// existing policies would misread.
const SchemaVersion = "1"

// Platform identifiers used in Metadata.Platform and check metadata.
const (
	PlatformBitbucketDC = "bitbucket-dc"
)

// Snapshot is the complete, normalized view of an SCM instance.
type Snapshot struct {
	SchemaVersion string       `json:"schemaVersion"`
	Metadata      Metadata     `json:"metadata"`
	Organization  Organization `json:"organization"`
	Projects      []Project    `json:"projects,omitempty"`
}

// Metadata records how and when the snapshot was captured.
type Metadata struct {
	Tool        string    `json:"tool"`
	ToolVersion string    `json:"toolVersion"`
	Platform    string    `json:"platform"`
	BaseURL     string    `json:"baseUrl"`
	GeneratedAt time.Time `json:"generatedAt"`
	// Warnings collects instance-wide fetch problems (permission denied on an
	// admin endpoint, an API missing on an older Bitbucket version, ...).
	// Rules turn the corresponding gaps into MANUAL rather than FAIL.
	Warnings []string `json:"warnings,omitempty"`
}

// Organization is the instance-level view: who administers it and who can log in.
type Organization struct {
	// Admins holds principals with SYS_ADMIN or ADMIN global permission, as
	// granted — groups appear as groups.
	Admins []PrincipalPermission `json:"admins,omitempty"`
	// EffectiveAdmins is the same set with groups expanded to their members,
	// which is what an administrator count has to be based on.
	EffectiveAdmins EffectivePrincipals `json:"effectiveAdmins"`
	// Users is the full user directory, when readable.
	Users []User `json:"users,omitempty"`
	// Available marks which instance-level fetches succeeded.
	Available map[string]bool `json:"available"`
}

// User is a directory entry.
type User struct {
	Name         string `json:"name"`
	DisplayName  string `json:"displayName,omitempty"`
	EmailAddress string `json:"emailAddress,omitempty"`
	Active       bool   `json:"active"`
	// LastActivityEpoch is the last authentication time in Unix seconds.
	// 0 means the instance did not report one, which is common: Bitbucket only
	// exposes it when the access-tokens/last-authentication plugin data is
	// readable. Rules must treat 0 as unknown, never as "never logged in".
	LastActivityEpoch int64 `json:"lastActivityEpoch"`
	// InactiveDays is derived from LastActivityEpoch at capture time.
	// -1 means unknown.
	InactiveDays int `json:"inactiveDays"`
	// HasRepositoryAccess is true when the user holds any global, project or
	// repository grant that gives them access to code.
	HasRepositoryAccess bool `json:"hasRepositoryAccess"`
}

// Project is a Bitbucket project (a GitHub organization is the closest analogue).
type Project struct {
	Key          string       `json:"key"`
	Name         string       `json:"name"`
	Type         string       `json:"type,omitempty"`
	Public       bool         `json:"public"`
	Permissions  Permissions  `json:"permissions"`
	Repositories []Repository `json:"repositories,omitempty"`
}

// Repository is a single repository plus every setting the rules need.
type Repository struct {
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	ProjectKey string `json:"projectKey"`
	// FullName is "PROJECT/slug" and is what reports show.
	FullName string `json:"fullName"`
	Public   bool   `json:"public"`
	Archived bool   `json:"archived"`
	Forkable bool   `json:"forkable"`
	// DefaultBranch is the full ref ("refs/heads/main"); DefaultBranchDisplay
	// is the short form ("main"). Empty for an unborn/empty repository.
	DefaultBranch        string `json:"defaultBranch"`
	DefaultBranchDisplay string `json:"defaultBranchDisplay"`
	Empty                bool   `json:"empty"`

	PullRequestSettings PullRequestSettings `json:"pullRequestSettings"`
	BranchRestrictions  []BranchRestriction `json:"branchRestrictions,omitempty"`
	RequiredBuilds      []RequiredBuild     `json:"requiredBuilds,omitempty"`
	Hooks               []Hook              `json:"hooks,omitempty"`
	Branches            []Branch            `json:"branches,omitempty"`
	Files               Files               `json:"files"`
	Permissions         Permissions         `json:"permissions"`
	// Admins is the resolved set of people who can administer this repository,
	// unioning repository, project and global grants with groups expanded.
	Admins EffectivePrincipals `json:"admins"`

	// Available marks which per-repository fetches succeeded. A missing or
	// false entry means "unknown", and rules downgrade to MANUAL.
	Available map[string]bool `json:"available"`
	// Errors records why a fetch failed, for the report's manual guidance.
	Errors []string `json:"errors,omitempty"`
}

// PullRequestSettings mirrors the repository's pull request merge checks.
type PullRequestSettings struct {
	RequiredApprovers        int  `json:"requiredApprovers"`
	RequiredAllApprovers     bool `json:"requiredAllApprovers"`
	RequiredAllTasksComplete bool `json:"requiredAllTasksComplete"`
	RequiredSuccessfulBuilds int  `json:"requiredSuccessfulBuilds"`
	// UnapproveOnUpdate drops existing approvals when the source branch moves.
	UnapproveOnUpdate bool            `json:"unapproveOnUpdate"`
	MergeStrategies   []MergeStrategy `json:"mergeStrategies,omitempty"`
	DefaultStrategy   string          `json:"defaultStrategy,omitempty"`
}

// MergeStrategy is one selectable merge behaviour, e.g. "no-ff" or "squash".
type MergeStrategy struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
}

// Branch restriction types, as reported by the branch-permissions API.
const (
	RestrictionReadOnly        = "read-only"         // "Prevent all changes"
	RestrictionNoDeletes       = "no-deletes"        // "Prevent deletion"
	RestrictionFastForwardOnly = "fast-forward-only" // "Prevent rewriting history"
	RestrictionPullRequestOnly = "pull-request-only" // "Prevent changes without a pull request"
)

// BranchRestriction is one branch permission entry.
type BranchRestriction struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	MatcherID   string `json:"matcherId"`
	MatcherType string `json:"matcherType"`
	MatcherText string `json:"matcherText,omitempty"`
	// Scope is REPOSITORY or PROJECT: project-level restrictions are inherited
	// and count just as much as repository-level ones.
	Scope string `json:"scope,omitempty"`
	// MatchesDefaultBranch is resolved by the fetcher, which knows the branch
	// model and glob semantics. Policies read this boolean instead of trying
	// to re-implement matcher matching in Rego.
	MatchesDefaultBranch bool `json:"matchesDefaultBranch"`
	// Exempt principals can bypass the restriction. A restriction that exempts
	// somebody still counts as configured, but the report surfaces the holes.
	ExemptUsers      []string `json:"exemptUsers,omitempty"`
	ExemptGroups     []string `json:"exemptGroups,omitempty"`
	ExemptAccessKeys int      `json:"exemptAccessKeys,omitempty"`
}

// RequiredBuild is one required-builds merge condition.
type RequiredBuild struct {
	ID                   int      `json:"id"`
	BuildParentKeys      []string `json:"buildParentKeys,omitempty"`
	MatcherID            string   `json:"matcherId"`
	MatcherType          string   `json:"matcherType"`
	MatcherText          string   `json:"matcherText,omitempty"`
	ExemptMatcherID      string   `json:"exemptMatcherId,omitempty"`
	MatchesDefaultBranch bool     `json:"matchesDefaultBranch"`
}

// Hook is a repository hook (pre- or post-receive), enabled or not.
type Hook struct {
	Key        string `json:"key"`
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	Scope      string `json:"scope,omitempty"`
}

// Branch is a ref plus the age of its tip, used to find abandoned branches.
type Branch struct {
	ID           string `json:"id"`
	DisplayID    string `json:"displayId"`
	IsDefault    bool   `json:"isDefault"`
	LatestCommit string `json:"latestCommit,omitempty"`
	// LatestCommitEpoch is Unix seconds; 0 when the commit date was not fetched.
	LatestCommitEpoch int64 `json:"latestCommitEpoch"`
	// AgeDays is days since the tip commit at capture time; -1 when unknown.
	AgeDays int `json:"ageDays"`
}

// Files records the outcome of probing the default branch for known paths.
type Files struct {
	// SecurityPolicyPaths lists the security policy files that were found.
	SecurityPolicyPaths []string `json:"securityPolicyPaths,omitempty"`
	// Probed lists every path that was checked, so an empty result is
	// distinguishable from "we never looked".
	Probed []string `json:"probed,omitempty"`
}

// Permissions is a grant table for a project or repository.
type Permissions struct {
	Users  []PrincipalPermission `json:"users,omitempty"`
	Groups []PrincipalPermission `json:"groups,omitempty"`
	// DefaultPermission is the project-wide permission handed to every
	// authenticated user, or "" when none is granted.
	DefaultPermission string `json:"defaultPermission,omitempty"`
	// DefaultPermissionKnown distinguishes "no default permission is granted"
	// from "the probe could not run", which look identical in the field above.
	DefaultPermissionKnown bool `json:"defaultPermissionKnown"`
	// PublicAccess is true when anonymous users can read.
	PublicAccess bool `json:"publicAccess"`
}

// PrincipalPermission is one grant: a user or group and the permission held.
type PrincipalPermission struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Type        string `json:"type"` // "user" or "group"
	Permission  string `json:"permission"`
	Active      bool   `json:"active,omitempty"`
}

// EffectivePrincipals is a resolved principal set plus a completeness flag.
// Complete is false when a group could not be expanded, which means the set is
// a lower bound and rules should report MANUAL instead of a hard verdict.
type EffectivePrincipals struct {
	Users    []string `json:"users,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	Count    int      `json:"count"`
	Complete bool     `json:"complete"`
}
