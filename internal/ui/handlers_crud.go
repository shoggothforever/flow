package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/scheduler"
)

// handleConfig returns the full config (GET).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c, err := s.Store.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handleMeta returns server-side metadata that the UI displays in the header
// (mainly the absolute config path so the user can spot wrong-config issues).
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config_path":  s.Store.Path,
		"history_path": scheduler.HistoryPathFor(s.Store.Path),
	})
}

// handleScheduleHistory serves the recent run history of all schedules.
//
//	GET /api/schedule-history?limit=200            -> all schedules, last 200
//	GET /api/schedule-history?name=foo&limit=50    -> only schedule "foo"
//
// The response is the raw JSONL parsed into a JSON array, oldest-first.
// The UI typically reverses to show newest-first.
func (s *Server) handleScheduleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	name := r.URL.Query().Get("name")

	// We over-read when filtering by name (the last `limit` rows of the
	// whole file may not contain `limit` rows for that one schedule), then
	// re-trim post-filter. 0 = no upper bound.
	readLimit := limit
	if name != "" {
		readLimit = 0
	}
	entries, err := scheduler.ReadHistory(scheduler.HistoryPathFor(s.Store.Path), readLimit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if name != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Name == name {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
		if len(entries) > limit {
			entries = entries[len(entries)-limit:]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
	})
}

// upsertResource is a generic helper for POST handlers.
func upsertResource[T any](
	s *Server, w http.ResponseWriter, r *http.Request,
	get func(c *config.Config) []T,
	set func(c *config.Config, items []T),
	keyOf func(T) string,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var item T
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	key := keyOf(item)
	if key == "" {
		writeErr(w, http.StatusBadRequest, "name/alias is required")
		return
	}
	err := s.Store.Mutate(func(c *config.Config) error {
		items := get(c)
		replaced := false
		for i := range items {
			if keyOf(items[i]) == key {
				items[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			items = append(items, item)
		}
		set(c, items)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": key})
}

// deleteResource is a generic helper for DELETE handlers.
func deleteResource[T any](
	s *Server, w http.ResponseWriter, r *http.Request, name string,
	get func(c *config.Config) []T,
	set func(c *config.Config, items []T),
	keyOf func(T) string,
) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name segment required")
		return
	}
	err := s.Store.Mutate(func(c *config.Config) error {
		items := get(c)
		for i := range items {
			if keyOf(items[i]) == name {
				items = append(items[:i], items[i+1:]...)
				set(c, items)
				return nil
			}
		}
		return fmt.Errorf("no item named %q", name)
	})
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// projects ---------------------------------------------------------------
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	upsertResource(s, w, r,
		func(c *config.Config) []config.Project { return c.Projects },
		func(c *config.Config, v []config.Project) { c.Projects = v },
		func(p config.Project) string { return p.Alias },
	)
}
func (s *Server) handleProjectsItem(w http.ResponseWriter, r *http.Request) {
	deleteResource(s, w, r, itemName(r.URL.Path, "/api/projects/"),
		func(c *config.Config) []config.Project { return c.Projects },
		func(c *config.Config, v []config.Project) { c.Projects = v },
		func(p config.Project) string { return p.Alias },
	)
}

// scripts ---------------------------------------------------------------
func (s *Server) handleScripts(w http.ResponseWriter, r *http.Request) {
	upsertResource(s, w, r,
		func(c *config.Config) []config.Script { return c.Scripts },
		func(c *config.Config, v []config.Script) { c.Scripts = v },
		func(p config.Script) string { return p.Alias },
	)
}
func (s *Server) handleScriptsItem(w http.ResponseWriter, r *http.Request) {
	deleteResource(s, w, r, itemName(r.URL.Path, "/api/scripts/"),
		func(c *config.Config) []config.Script { return c.Scripts },
		func(c *config.Config, v []config.Script) { c.Scripts = v },
		func(p config.Script) string { return p.Alias },
	)
}

// cmds ------------------------------------------------------------------
func (s *Server) handleCmds(w http.ResponseWriter, r *http.Request) {
	upsertResource(s, w, r,
		func(c *config.Config) []config.CmdAlias { return c.Cmds },
		func(c *config.Config, v []config.CmdAlias) { c.Cmds = v },
		func(p config.CmdAlias) string { return p.Alias },
	)
}
func (s *Server) handleCmdsItem(w http.ResponseWriter, r *http.Request) {
	deleteResource(s, w, r, itemName(r.URL.Path, "/api/cmds/"),
		func(c *config.Config) []config.CmdAlias { return c.Cmds },
		func(c *config.Config, v []config.CmdAlias) { c.Cmds = v },
		func(p config.CmdAlias) string { return p.Alias },
	)
}

// greps -----------------------------------------------------------------
func (s *Server) handleGreps(w http.ResponseWriter, r *http.Request) {
	upsertResource(s, w, r,
		func(c *config.Config) []config.GrepPref { return c.Greps },
		func(c *config.Config, v []config.GrepPref) { c.Greps = v },
		func(p config.GrepPref) string { return p.Name },
	)
}
func (s *Server) handleGrepsItem(w http.ResponseWriter, r *http.Request) {
	deleteResource(s, w, r, itemName(r.URL.Path, "/api/greps/"),
		func(c *config.Config) []config.GrepPref { return c.Greps },
		func(c *config.Config, v []config.GrepPref) { c.Greps = v },
		func(p config.GrepPref) string { return p.Name },
	)
}

// finds -----------------------------------------------------------------
func (s *Server) handleFinds(w http.ResponseWriter, r *http.Request) {
	upsertResource(s, w, r,
		func(c *config.Config) []config.FindPref { return c.Finds },
		func(c *config.Config, v []config.FindPref) { c.Finds = v },
		func(p config.FindPref) string { return p.Name },
	)
}
func (s *Server) handleFindsItem(w http.ResponseWriter, r *http.Request) {
	deleteResource(s, w, r, itemName(r.URL.Path, "/api/finds/"),
		func(c *config.Config) []config.FindPref { return c.Finds },
		func(c *config.Config, v []config.FindPref) { c.Finds = v },
		func(p config.FindPref) string { return p.Name },
	)
}

// schedules ----------------------------------------------------------------
func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	upsertResource(s, w, r,
		func(c *config.Config) []config.Schedule { return c.Schedules },
		func(c *config.Config, v []config.Schedule) { c.Schedules = v },
		func(p config.Schedule) string { return p.Name },
	)
}
func (s *Server) handleSchedulesItem(w http.ResponseWriter, r *http.Request) {
	deleteResource(s, w, r, itemName(r.URL.Path, "/api/schedules/"),
		func(c *config.Config) []config.Schedule { return c.Schedules },
		func(c *config.Config, v []config.Schedule) { c.Schedules = v },
		func(p config.Schedule) string { return p.Name },
	)
}
