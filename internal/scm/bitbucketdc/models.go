package bitbucketdc

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// The shapes below mirror Bitbucket Data Center REST responses. Several merge
// check fields changed representation across 7.x/8.x/9.x (a bare number in one
// release, an object with a "count" in another), so the scalar wrappers accept
// every form rather than silently decoding to a zero value — a zero would read
// as "no approvals required" and produce a confident, wrong FAIL.

// flexInt decodes a number, a numeric string, or an object carrying a count.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		i, convErr := strconv.ParseFloat(n.String(), 64)
		if convErr != nil {
			return convErr
		}
		*f = flexInt(int(i))
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		i, convErr := strconv.Atoi(s)
		if convErr != nil {
			return nil // a non-numeric string carries no count
		}
		*f = flexInt(i)
		return nil
	}
	var obj struct {
		Count   *int  `json:"count"`
		Value   *int  `json:"value"`
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		switch {
		case obj.Count != nil:
			*f = flexInt(*obj.Count)
		case obj.Value != nil:
			*f = flexInt(*obj.Value)
		case obj.Enabled != nil && *obj.Enabled:
			// An enabled check with no count still means "at least one".
			*f = 1
		}
	}
	return nil
}

func (f flexInt) Int() int { return int(f) }

// flexBool decodes a bool, a "true"/"false" string, or an object with enabled.
type flexBool bool

func (f *flexBool) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*f = flexBool(b)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		parsed, convErr := strconv.ParseBool(s)
		if convErr != nil {
			return nil
		}
		*f = flexBool(parsed)
		return nil
	}
	var obj struct {
		Enabled *bool `json:"enabled"`
		Value   *bool `json:"value"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		switch {
		case obj.Enabled != nil:
			*f = flexBool(*obj.Enabled)
		case obj.Value != nil:
			*f = flexBool(*obj.Value)
		}
	}
	return nil
}

func (f flexBool) Bool() bool { return bool(f) }

// flexID decodes an identifier that may be a plain string or an object with an
// "id". Restriction types and matcher types both vary this way.
type flexID struct {
	ID   string
	Name string
}

func (f *flexID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.ID = s
		return nil
	}
	var obj struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		f.ID = obj.ID
		f.Name = obj.Name
	}
	return nil
}

// apiProject is an entry from /api/1.0/projects.
type apiProject struct {
	Key    string `json:"key"`
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Public bool   `json:"public"`
	Type   string `json:"type"`
}

// apiRepository is an entry from /api/1.0/projects/{key}/repos.
type apiRepository struct {
	Slug     string     `json:"slug"`
	ID       int        `json:"id"`
	Name     string     `json:"name"`
	Public   bool       `json:"public"`
	Archived bool       `json:"archived"`
	Forkable bool       `json:"forkable"`
	State    string     `json:"state"`
	ScmID    string     `json:"scmId"`
	Project  apiProject `json:"project"`
}

// apiPullRequestSettings is /settings/pull-requests.
type apiPullRequestSettings struct {
	RequiredApprovers        flexInt  `json:"requiredApprovers"`
	RequiredAllApprovers     flexBool `json:"requiredAllApprovers"`
	RequiredAllTasksComplete flexBool `json:"requiredAllTasksComplete"`
	RequiredSuccessfulBuilds flexInt  `json:"requiredSuccessfulBuilds"`
	UnapproveOnUpdate        flexBool `json:"unapproveOnUpdate"`
	MergeConfig              struct {
		DefaultStrategy flexID `json:"defaultStrategy"`
		Strategies      []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Enabled *bool  `json:"enabled"`
		} `json:"strategies"`
	} `json:"mergeConfig"`
}

// apiMatcher is a ref matcher shared by branch restrictions and required builds.
type apiMatcher struct {
	ID        string `json:"id"`
	DisplayID string `json:"displayId"`
	Active    bool   `json:"active"`
	Type      flexID `json:"type"`
}

// apiRestriction is an entry from the branch-permissions API.
type apiRestriction struct {
	ID      int        `json:"id"`
	Type    flexID     `json:"type"`
	Matcher apiMatcher `json:"matcher"`
	Scope   struct {
		Type       string `json:"type"`
		ResourceID int    `json:"resourceId"`
	} `json:"scope"`
	Users []struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"users"`
	Groups     []string `json:"groups"`
	AccessKeys []struct {
		ID int `json:"id"`
	} `json:"accessKeys"`
}

// apiRequiredBuild is an entry from the required-builds API.
type apiRequiredBuild struct {
	ID               int         `json:"id"`
	BuildParentKeys  []string    `json:"buildParentKeys"`
	RefMatcher       apiMatcher  `json:"refMatcher"`
	ExemptRefMatcher *apiMatcher `json:"exemptRefMatcher"`
}

// apiHook is an entry from /settings/hooks.
type apiHook struct {
	Details struct {
		Key         string `json:"key"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Version     string `json:"version"`
	} `json:"details"`
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
	Scope      struct {
		Type string `json:"type"`
	} `json:"scope"`
}

// apiBranch is an entry from /branches?details=true.
type apiBranch struct {
	ID           string `json:"id"`
	DisplayID    string `json:"displayId"`
	Type         string `json:"type"`
	LatestCommit string `json:"latestCommit"`
	IsDefault    bool   `json:"isDefault"`
	// Metadata keys are plugin-namespaced and differ by version, so the
	// timestamp is dug out by suffix rather than by an exact key.
	Metadata map[string]json.RawMessage `json:"metadata"`
}

// latestCommitEpoch returns the tip commit time in Unix seconds, or 0 when the
// instance did not include commit metadata with the branch listing.
func (b apiBranch) latestCommitEpoch() int64 {
	for key, raw := range b.Metadata {
		if !bytes.Contains([]byte(key), []byte("latest-commit-metadata")) {
			continue
		}
		var meta struct {
			CommitterTimestamp *int64 `json:"committerTimestamp"`
			AuthorTimestamp    *int64 `json:"authorTimestamp"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		// Bitbucket reports these in milliseconds.
		if meta.CommitterTimestamp != nil && *meta.CommitterTimestamp > 0 {
			return *meta.CommitterTimestamp / 1000
		}
		if meta.AuthorTimestamp != nil && *meta.AuthorTimestamp > 0 {
			return *meta.AuthorTimestamp / 1000
		}
	}
	return 0
}

// apiRef is the default-branch response.
type apiRef struct {
	ID        string `json:"id"`
	DisplayID string `json:"displayId"`
}

// apiUser is a user record from the admin or permissions APIs.
type apiUser struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
	Active       bool   `json:"active"`
	Type         string `json:"type"`
	// LastAuthenticationTimestamp is milliseconds since epoch and is only
	// present on instances that expose it to admins.
	LastAuthenticationTimestamp *int64 `json:"lastAuthenticationTimestamp"`
}

// apiUserPermission pairs a user with a granted permission.
type apiUserPermission struct {
	User       apiUser `json:"user"`
	Permission string  `json:"permission"`
}

// apiGroupPermission pairs a group with a granted permission.
type apiGroupPermission struct {
	Group struct {
		Name string `json:"name"`
	} `json:"group"`
	Permission string `json:"permission"`
}

// apiBrowseResponse is the /browse/{path} response. A file has "lines" (or is
// flagged binary); a directory has "children".
type apiBrowseResponse struct {
	Lines    []json.RawMessage `json:"lines"`
	Binary   *bool             `json:"binary"`
	Children *json.RawMessage  `json:"children"`
	Path     struct {
		ToString string `json:"toString"`
	} `json:"path"`
}

// isFile reports whether the browsed path resolved to a file rather than a
// directory.
func (r apiBrowseResponse) isFile() bool {
	if r.Children != nil {
		return false
	}
	return r.Lines != nil || (r.Binary != nil && *r.Binary)
}
