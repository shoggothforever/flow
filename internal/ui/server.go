// Package ui hosts a minimal local web UI for browsing/editing the flow
// config and running scripts/cmds/greps/finds with streamed stdout.
//
// The server binds to 127.0.0.1 by default and serves a single static
// page plus a small JSON API. There is intentionally no auth: it is a
// local-only tool. We refuse to bind to a non-loopback address unless
// the caller explicitly opts in.
package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/yithcai/flow/internal/agentsession"
	"github.com/yithcai/flow/internal/config"
)

//go:embed static
var staticFS embed.FS

// Server wraps an http.Server and a config.Store.
type Server struct {
	Store         *config.Store
	Addr          string
	mux           *http.ServeMux
	AgentSessions *agentsession.Service
}

// New builds a Server that listens on addr and reads/writes via store.
func New(store *config.Store, addr string) (*Server, error) {
	if err := assertLoopback(addr); err != nil {
		return nil, err
	}
	s := &Server{Store: store, Addr: addr, mux: http.NewServeMux(), AgentSessions: agentsession.New(store)}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// Static
	sub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/", http.FileServer(http.FS(sub)))

	// Config snapshot (GET) + full overwrite (PUT, optional)
	s.mux.HandleFunc("/api/config", s.handleConfig)
	// Server-side metadata (config path, version) for the UI header.
	s.mux.HandleFunc("/api/meta", s.handleMeta)
	s.mux.HandleFunc("/api/tapd-requirements", s.handleTAPD)
	s.mux.HandleFunc("/api/tapd-requirements/", s.handleTAPD)
	s.mux.HandleFunc("/api/agent-sessions", s.handleAgentSessions)
	s.mux.HandleFunc("/api/agent-sessions/", s.handleAgentSessions)

	// Per-resource CRUD via POST (upsert) and DELETE.
	s.mux.HandleFunc("/api/projects", s.handleProjects)
	s.mux.HandleFunc("/api/projects/", s.handleProjectsItem)
	s.mux.HandleFunc("/api/scripts", s.handleScripts)
	s.mux.HandleFunc("/api/scripts/", s.handleScriptsItem)
	s.mux.HandleFunc("/api/cmds", s.handleCmds)
	s.mux.HandleFunc("/api/cmds/", s.handleCmdsItem)
	s.mux.HandleFunc("/api/greps", s.handleGreps)
	s.mux.HandleFunc("/api/greps/", s.handleGrepsItem)
	s.mux.HandleFunc("/api/finds", s.handleFinds)
	s.mux.HandleFunc("/api/finds/", s.handleFindsItem)
	s.mux.HandleFunc("/api/schedules", s.handleSchedules)
	s.mux.HandleFunc("/api/schedules/", s.handleSchedulesItem)
	// Read-only view of past scheduler runs (history.jsonl).
	s.mux.HandleFunc("/api/schedule-history", s.handleScheduleHistory)

	// Streamed run endpoints. POST body for cmd: {"vars": {...}}.
	s.mux.HandleFunc("/api/run/script/", s.handleRunScript)
	s.mux.HandleFunc("/api/run/cmd/", s.handleRunCmd)
	s.mux.HandleFunc("/api/run/grep/", s.handleRunGrep)
	s.mux.HandleFunc("/api/run/find/", s.handleRunFind)
	s.mux.HandleFunc("/api/run/schedule/", s.handleRunSchedule)
}

func assertLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	// Reject anything else outright -- the UI has no auth.
	return fmt.Errorf("refusing to bind to non-loopback host %q (UI has no auth); use 127.0.0.1:PORT", host)
}

// helpers -----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// itemName extracts the trailing path segment after a /api/<resource>/ prefix.
func itemName(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimPrefix(rest, "/")
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
