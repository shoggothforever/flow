// Package agentsession owns TAPD membership and Codex fork ancestry for both CLI and UI.
package agentsession

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"strings"

	"github.com/yithcai/flow/internal/config"
)

type Client interface {
	Discover(context.Context) ([]config.AgentSession, error)
	Read(context.Context, string) (config.AgentSession, error)
	Fork(context.Context, string) (config.AgentSession, error)
	Status(context.Context) Status
}

type Status struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}
type Service struct {
	Store     *config.Store
	Client    Client
	discovery discoveryCache
}

func New(store *config.Store) *Service { return &Service{Store: store, Client: &Codex{}} }

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func Resolve(query string, sessions []config.AgentSession) (string, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if config.ValidSessionID(q) {
		return q, nil
	}
	if len(q) < 8 {
		return "", fmt.Errorf("session prefix must contain at least 8 characters")
	}
	hits := map[string]bool{}
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, q) {
			hits[s.ID] = true
		}
	}
	if len(hits) == 1 {
		for id := range hits {
			return id, nil
		}
	}
	if len(hits) > 1 {
		return "", fmt.Errorf("session prefix %q is ambiguous; paste a longer prefix or the full ID", q)
	}
	return "", fmt.Errorf("session prefix %q was not found; paste the full ID or refresh local sessions", q)
}

func FindRequirement(c *config.Config, id string) *config.TAPDRequirement {
	for i := range c.TAPDRequirements {
		if c.TAPDRequirements[i].ID == id {
			return &c.TAPDRequirements[i]
		}
	}
	return nil
}
func sessionMap(c *config.Config) map[string]config.AgentSession {
	m := map[string]config.AgentSession{}
	for _, x := range c.AgentSessions {
		m[x.ID] = x
	}
	return m
}
func children(c *config.Config) map[string][]string {
	m := map[string][]string{}
	for _, x := range c.AgentSessions {
		if x.ForkedFromID != "" {
			m[x.ForkedFromID] = append(m[x.ForkedFromID], x.ID)
		}
	}
	return m
}
func descendants(seeds []string, kids map[string][]string, excluded []string) map[string]bool {
	blocked := map[string]bool{}
	for _, id := range excluded {
		blocked[id] = true
	}
	seen := map[string]bool{}
	queue := append([]string(nil), seeds...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] || blocked[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, kids[id]...)
	}
	return seen
}
func Membership(c *config.Config, r config.TAPDRequirement) map[string]bool {
	return descendants(r.SessionIDs, children(c), r.ExcludedBranchIDs)
}

type Node struct {
	config.AgentSession
	ContextOnly bool `json:"context_only"`
}
type Tree struct {
	Requirement config.TAPDRequirement `json:"requirement"`
	Nodes       []Node                 `json:"nodes"`
}
type Snapshot struct {
	Requirements []config.TAPDRequirement `json:"requirements"`
	Sessions     []config.AgentSession    `json:"sessions"`
	Trees        []Tree                   `json:"trees"`
}

func (s *Service) Snapshot() (*Snapshot, error) {
	c, err := s.Store.Load()
	if err != nil {
		return nil, err
	}
	if err = c.Validate(); err != nil {
		return nil, err
	}
	out := &Snapshot{Requirements: c.TAPDRequirements, Sessions: c.AgentSessions, Trees: []Tree{}}
	if out.Requirements == nil {
		out.Requirements = []config.TAPDRequirement{}
	}
	if out.Sessions == nil {
		out.Sessions = []config.AgentSession{}
	}
	all := sessionMap(c)
	for _, r := range c.TAPDRequirements {
		member := Membership(c, r)
		shown := map[string]bool{}
		for id := range member {
			for id != "" && !shown[id] {
				shown[id] = true
				id = all[id].ForkedFromID
			}
		}
		tree := Tree{Requirement: r, Nodes: []Node{}}
		for id := range shown {
			x, ok := all[id]
			if !ok {
				x = config.AgentSession{ID: id, Agent: "codex"}
			}
			tree.Nodes = append(tree.Nodes, Node{AgentSession: x, ContextOnly: !member[id]})
		}
		sort.Slice(tree.Nodes, func(i, j int) bool {
			a, b := tree.Nodes[i], tree.Nodes[j]
			if a.CreatedAt == b.CreatedAt {
				return a.ID < b.ID
			}
			return a.CreatedAt < b.CreatedAt
		})
		out.Trees = append(out.Trees, tree)
	}
	return out, nil
}

func (s *Service) SaveRequirement(id, rawURL, title, notes string) (config.TAPDRequirement, error) {
	u, err := config.NormalizeTAPDURL(rawURL)
	if err != nil {
		return config.TAPDRequirement{}, err
	}
	create := id == ""
	if create {
		id = NewID()
	}
	var out config.TAPDRequirement
	err = s.Store.Mutate(func(c *config.Config) error {
		r := FindRequirement(c, id)
		if r == nil {
			if !create {
				return fmt.Errorf("requirement not found")
			}
			c.TAPDRequirements = append(c.TAPDRequirements, config.TAPDRequirement{ID: id})
			r = &c.TAPDRequirements[len(c.TAPDRequirements)-1]
		}
		r.URL = u
		r.Title = strings.TrimSpace(title)
		r.Notes = strings.TrimSpace(notes)
		out = *r
		return nil
	})
	return out, err
}
func (s *Service) DeleteRequirement(id string) error {
	return s.Store.Mutate(func(c *config.Config) error {
		for i, r := range c.TAPDRequirements {
			if r.ID == id {
				c.TAPDRequirements = append(c.TAPDRequirements[:i], c.TAPDRequirements[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("requirement not found")
	})
}
func upsert(c *config.Config, x config.AgentSession) {
	for i, old := range c.AgentSessions {
		if old.ID == x.ID {
			x.Label = old.Label
			// A temporarily missing parent field must never erase known ancestry.
			if x.ForkedFromID == "" {
				x.ForkedFromID = old.ForkedFromID
			}
			c.AgentSessions[i] = x
			return
		}
	}
	c.AgentSessions = append(c.AgentSessions, x)
}
func removeIDs(ids []string, remove map[string]bool) []string {
	out := []string{}
	for _, id := range ids {
		if !remove[id] {
			out = append(out, id)
		}
	}
	return out
}
func addID(ids []string, id string) []string {
	for _, x := range ids {
		if id == x {
			return ids
		}
	}
	return append(ids, id)
}

func (s *Service) Link(ctx context.Context, requirementID, query string) (string, error) {
	c, err := s.Store.Load()
	if err != nil {
		return "", err
	}
	if FindRequirement(c, requirementID) == nil {
		return "", fmt.Errorf("requirement not found")
	}
	known := append([]config.AgentSession(nil), c.AgentSessions...)
	q := strings.ToLower(strings.TrimSpace(query))
	// Prefix uniqueness is checked against the complete local listing, not just a UI page.
	if !config.ValidSessionID(q) {
		local, e := s.Client.Discover(ctx)
		if e != nil {
			return "", fmt.Errorf("cannot resolve a prefix: %w; use a full ID", e)
		}
		known = append(known, local...)
	}
	id, err := Resolve(q, known)
	if err != nil {
		return "", err
	}
	all := sessionMap(c)
	seen := map[string]bool{}
	var imported []config.AgentSession
	for cur := id; cur != "" && !seen[cur]; {
		seen[cur] = true
		x, e := s.Client.Read(ctx, cur)
		if e != nil {
			if IsSubagent(e) {
				return "", e
			}
			var ok bool
			x, ok = all[cur]
			if !ok {
				x = config.AgentSession{ID: cur, Agent: "codex"}
			}
			// Preserve last known state on transport failure; only known absence means unavailable.
			if IsNotFound(e) {
				x.Available = false
			}
		}
		imported = append(imported, x)
		cur = x.ForkedFromID
	}
	err = s.Store.Mutate(func(c *config.Config) error {
		r := FindRequirement(c, requirementID)
		if r == nil {
			return fmt.Errorf("requirement not found")
		}
		r.SessionIDs = addID(r.SessionIDs, id)
		r.ExcludedBranchIDs = removeIDs(r.ExcludedBranchIDs, map[string]bool{id: true})
		for _, x := range imported {
			upsert(c, x)
		}
		return nil
	})
	return id, err
}

func (s *Service) Unlink(requirementID, query string) error {
	return s.Store.Mutate(func(c *config.Config) error {
		r := FindRequirement(c, requirementID)
		if r == nil {
			return fmt.Errorf("requirement not found")
		}
		id, err := Resolve(query, c.AgentSessions)
		if err != nil {
			return err
		}
		if !Membership(c, *r)[id] {
			return fmt.Errorf("session is not associated with this requirement")
		}
		branch := descendants([]string{id}, children(c), nil)
		r.SessionIDs = removeIDs(r.SessionIDs, branch)
		r.ExcludedBranchIDs = addID(r.ExcludedBranchIDs, id)
		return nil
	})
}

func (s *Service) Rename(query, label string) error {
	return s.Store.Mutate(func(c *config.Config) error {
		id, err := Resolve(query, c.AgentSessions)
		if err != nil {
			return err
		}
		for i := range c.AgentSessions {
			if c.AgentSessions[i].ID == id {
				c.AgentSessions[i].Label = strings.TrimSpace(label)
				return nil
			}
		}
		return fmt.Errorf("session not found")
	})
}

func (s *Service) Sync(ctx context.Context) error {
	local, err := s.Discover(ctx, true)
	if err != nil {
		return err
	}
	// No config lock is held while the external process scans Codex's store.
	return s.Store.Mutate(func(c *config.Config) error {
		all := sessionMap(c)
		present := map[string]bool{}
		for _, x := range local {
			present[x.ID] = true
			if old, ok := all[x.ID]; ok {
				x.Label = old.Label
				if x.ForkedFromID == "" {
					x.ForkedFromID = old.ForkedFromID
				}
			}
			all[x.ID] = x
		}
		tmp := &config.Config{}
		for _, x := range all {
			tmp.AgentSessions = append(tmp.AgentSessions, x)
		}
		// Keep only already tracked sessions, their ancestors, and ordinary fork descendants.
		var seeds []string
		for _, x := range c.AgentSessions {
			seeds = append(seeds, x.ID)
		}
		for _, r := range c.TAPDRequirements {
			seeds = append(seeds, r.SessionIDs...)
		}
		needed := descendants(seeds, children(tmp), nil)
		var initial []string
		for id := range needed {
			initial = append(initial, id)
		}
		for _, id := range initial {
			for parent := all[id].ForkedFromID; parent != "" && !needed[parent]; parent = all[parent].ForkedFromID {
				needed[parent] = true
			}
		}
		for id := range needed {
			x, ok := all[id]
			if !ok {
				x = config.AgentSession{ID: id, Agent: "codex"}
			}
			x.Available = present[id]
			upsert(c, x)
		}
		return nil
	})
}
