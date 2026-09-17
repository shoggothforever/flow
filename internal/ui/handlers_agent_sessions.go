package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yithcai/flow/internal/agentsession"
)

func agentBody(w http.ResponseWriter, r *http.Request, v any) bool {
	// JSON requests and same-origin writes are required for the new local-only APIs.
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			writeErr(w, 403, "cross-origin write rejected")
			return false
		}
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeErr(w, 415, "application/json required")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeErr(w, 400, err.Error())
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeErr(w, 400, "one JSON object required")
		return false
	}
	return true
}
func agentResult(w http.ResponseWriter, v any, err error) {
	if err != nil {
		var oe *agentsession.OperationError
		if errors.As(err, &oe) {
			writeJSON(w, 409, map[string]any{"error": err.Error(), "operation": oe.Operation})
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	writeJSON(w, 200, v)
}
func pathParts(path, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

func (s *Server) handleTAPD(w http.ResponseWriter, r *http.Request) {
	a := s.AgentSessions
	p := pathParts(r.URL.Path, "/api/tapd-requirements")
	if r.Method == http.MethodDelete {
		if o := r.Header.Get("Origin"); o != "" {
			u, e := url.Parse(o)
			if e != nil || u.Host != r.Host {
				writeErr(w, 403, "cross-origin write rejected")
				return
			}
		}
	}
	switch {
	case len(p) == 0 && r.Method == http.MethodGet:
		v, e := a.Snapshot()
		agentResult(w, v, e)
	case (len(p) == 0 && r.Method == http.MethodPost) || (len(p) == 1 && r.Method == http.MethodPatch):
		var body struct {
			URL   string `json:"url"`
			Title string `json:"title"`
			Notes string `json:"notes"`
		}
		if !agentBody(w, r, &body) {
			return
		}
		id := ""
		if len(p) == 1 {
			id = p[0]
		}
		v, e := a.SaveRequirement(id, body.URL, body.Title, body.Notes)
		agentResult(w, v, e)
	case len(p) == 1 && r.Method == http.MethodDelete:
		agentResult(w, nil, a.DeleteRequirement(p[0]))
	case len(p) == 2 && p[1] == "sessions" && r.Method == http.MethodPost:
		var body struct {
			SessionID string `json:"session_id"`
		}
		if !agentBody(w, r, &body) {
			return
		}
		id, e := a.Link(r.Context(), p[0], body.SessionID)
		agentResult(w, map[string]string{"id": id}, e)
	case len(p) == 3 && p[1] == "sessions" && r.Method == http.MethodDelete:
		agentResult(w, nil, a.Unlink(p[0], p[2]))
	default:
		writeErr(w, 405, "unsupported requirement operation")
	}
}
func (s *Server) handleAgentSessions(w http.ResponseWriter, r *http.Request) {
	a := s.AgentSessions
	p := pathParts(r.URL.Path, "/api/agent-sessions")
	if len(p) == 3 && p[0] == "operations" && p[2] == "resolve" && r.Method == http.MethodPost {
		var body struct {
			SessionID string `json:"session_id"`
		}
		if !agentBody(w, r, &body) {
			return
		}
		v, e := a.ResolveOperation(r.Context(), p[1], body.SessionID)
		agentResult(w, v, e)
		return
	}
	if len(p) == 0 && r.Method == http.MethodGet {
		v, e := a.Snapshot()
		agentResult(w, v, e)
		return
	}
	if len(p) == 1 && r.Method == http.MethodGet {
		switch p[0] {
		case "status":
			writeJSON(w, 200, a.Client.Status(r.Context()))
			return
		case "discover":
			v, e := a.Discover(r.Context(), r.URL.Query().Get("refresh") == "1")
			agentResult(w, map[string]any{"sessions": v}, e)
			return
		case "operations":
			v, e := a.Operations()
			agentResult(w, map[string]any{"operations": v}, e)
			return
		default:
			v, e := a.Snapshot()
			if e != nil {
				agentResult(w, nil, e)
				return
			}
			id, e := agentsession.Resolve(p[0], v.Sessions)
			if e != nil {
				agentResult(w, nil, e)
				return
			}
			for _, x := range v.Sessions {
				if x.ID == id {
					writeJSON(w, 200, x)
					return
				}
			}
			writeErr(w, 404, "session not found")
			return
		}
	}
	if len(p) == 1 && p[0] == "sync" && r.Method == http.MethodPost {
		var body struct{}
		if !agentBody(w, r, &body) {
			return
		}
		if e := a.Sync(r.Context()); e != nil {
			agentResult(w, nil, e)
			return
		}
		v, e := a.Snapshot()
		agentResult(w, v, e)
		return
	}
	if len(p) == 1 && r.Method == http.MethodPatch {
		var body struct {
			Label string `json:"label"`
		}
		if !agentBody(w, r, &body) {
			return
		}
		agentResult(w, nil, a.Rename(p[0], body.Label))
		return
	}
	if len(p) == 2 && p[1] == "fork" && r.Method == http.MethodPost {
		var body struct {
			RequestID string `json:"request_id"`
		}
		if !agentBody(w, r, &body) {
			return
		}
		v, e := a.Fork(r.Context(), p[0], body.RequestID)
		agentResult(w, v, e)
		return
	}
	writeErr(w, 405, "unsupported session operation")
}
