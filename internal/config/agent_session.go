package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Session IDs are persisted in full. Short prefixes are only an input/display aid.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ValidSessionID(id string) bool { return uuidPattern.MatchString(id) }

type TAPDRequirement struct {
	ID                string   `json:"id"`
	URL               string   `json:"url"`
	Title             string   `json:"title,omitempty"`
	Notes             string   `json:"notes,omitempty"`
	SessionIDs        []string `json:"session_ids,omitempty"`
	ExcludedBranchIDs []string `json:"excluded_branch_ids,omitempty"`
}

type AgentSession struct {
	ID           string `json:"id"`
	Agent        string `json:"agent"`
	Label        string `json:"label,omitempty"` // Local override; never changes Codex's title.
	Title        string `json:"title,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Source       string `json:"source,omitempty"`
	ForkedFromID string `json:"forked_from_id,omitempty"`
	CreatedAt    int64  `json:"created_at,omitempty"`
	UpdatedAt    int64  `json:"updated_at,omitempty"`
	Available    bool   `json:"available"` // Last successful discovery, not live execution state.
	Archived     bool   `json:"archived,omitempty"`
}

func NormalizeTAPDURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("TAPD URL must be an http(s) link without credentials")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	return u.String(), nil
}

func (c *Config) validateAgentSessions() error {
	sessions := map[string]AgentSession{}
	for _, s := range c.AgentSessions {
		if !ValidSessionID(s.ID) || s.Agent != "codex" {
			return fmt.Errorf("invalid Codex session %q", s.ID)
		}
		if _, ok := sessions[s.ID]; ok {
			return fmt.Errorf("duplicate session %s", s.ID)
		}
		if s.ForkedFromID != "" && !ValidSessionID(s.ForkedFromID) {
			return fmt.Errorf("invalid parent ID for %s", s.ID)
		}
		sessions[s.ID] = s
	}
	// A colour walk detects cycles without recursion or quadratic ancestor scans.
	colours := map[string]int{}
	for id := range sessions {
		var path []string
		for id != "" && colours[id] == 0 {
			colours[id] = 1
			path = append(path, id)
			id = sessions[id].ForkedFromID
		}
		if id != "" && colours[id] == 1 {
			return fmt.Errorf("session ancestry contains a cycle at %s", id)
		}
		for _, x := range path {
			colours[x] = 2
		}
	}
	ids, urls := map[string]bool{}, map[string]bool{}
	for _, r := range c.TAPDRequirements {
		if !ValidSessionID(r.ID) || ids[r.ID] {
			return fmt.Errorf("invalid or duplicate requirement ID %q", r.ID)
		}
		ids[r.ID] = true
		u, err := NormalizeTAPDURL(r.URL)
		if err != nil {
			return err
		}
		if urls[u] {
			return fmt.Errorf("TAPD link is already registered: %s", u)
		}
		urls[u] = true
		for _, list := range [][]string{r.SessionIDs, r.ExcludedBranchIDs} {
			seen := map[string]bool{}
			for _, id := range list {
				if !ValidSessionID(id) || seen[id] {
					return fmt.Errorf("invalid or duplicate requirement session ID %q", id)
				}
				seen[id] = true
			}
		}
	}
	return nil
}
