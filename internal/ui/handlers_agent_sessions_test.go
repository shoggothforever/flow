package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/yithcai/flow/internal/agentsession"
	"github.com/yithcai/flow/internal/config"
)

type apiCodex struct{ forks, discoveries int }

const apiParent = "01a10000-1111-4000-8000-000000000001"
const apiChild = "01a20000-2222-4000-8000-000000000002"

func (*apiCodex) Status(context.Context) agentsession.Status {
	return agentsession.Status{Available: true, Version: "test"}
}
func (c *apiCodex) Discover(context.Context) ([]config.AgentSession, error) {
	c.discoveries++
	return []config.AgentSession{{ID: apiParent, Agent: "codex", Available: true}}, nil
}

func TestAgentSessionHTTPDiscoveryCacheAndRefresh(t *testing.T) {
	s, _ := New(&config.Store{Path: filepath.Join(t.TempDir(), "config.json")}, "127.0.0.1:0")
	client := &apiCodex{}
	s.AgentSessions.Client = client
	for _, test := range []struct {
		method, path string
		calls        int
	}{
		{"GET", "/api/agent-sessions/discover", 1},
		{"GET", "/api/agent-sessions/discover", 1},
		{"GET", "/api/agent-sessions/discover?refresh=1", 2},
		{"POST", "/api/agent-sessions/sync", 3},
		{"GET", "/api/agent-sessions/discover", 3},
	} {
		r := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || client.discoveries != test.calls {
			t.Fatalf("%s %s: status=%d scans=%d want=%d: %s", test.method, test.path, w.Code, client.discoveries, test.calls, w.Body.String())
		}
	}
}
func (*apiCodex) Read(_ context.Context, id string) (config.AgentSession, error) {
	if id != apiParent {
		return config.AgentSession{}, fmt.Errorf("not found")
	}
	return config.AgentSession{ID: id, Agent: "codex", Available: true}, nil
}
func (c *apiCodex) Fork(_ context.Context, id string) (config.AgentSession, error) {
	c.forks++
	return config.AgentSession{ID: apiChild, Agent: "codex", Available: true, ForkedFromID: id}, nil
}
func TestAgentSessionHTTPWorkflow(t *testing.T) {
	s, err := New(&config.Store{Path: filepath.Join(t.TempDir(), "config.json")}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client := &apiCodex{}
	s.AgentSessions.Client = client
	call := func(method, path, body string, want int) map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var result map[string]json.RawMessage
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	r := call("POST", "/api/tapd-requirements", `{"url":"https://tapd.example/story/1?foo=bar","title":"需求"}`, 200)
	var id string
	_ = json.Unmarshal(r["id"], &id)
	call("POST", "/api/tapd-requirements/"+id+"/sessions", `{"session_id":"`+apiParent+`"}`, 200)
	request := `{"request_id":"10000000-0000-4000-8000-000000000001"}`
	call("POST", "/api/agent-sessions/"+apiParent+"/fork", request, 200)
	call("POST", "/api/agent-sessions/"+apiParent+"/fork", request, 200)
	if client.forks != 1 {
		t.Fatal("duplicate HTTP request forked twice")
	}
	v := call("GET", "/api/agent-sessions", "", 200)
	var trees []agentsession.Tree
	_ = json.Unmarshal(v["trees"], &trees)
	if len(trees) != 1 || len(trees[0].Nodes) != 2 {
		t.Fatal("fork tree missing")
	}
	call("PATCH", "/api/agent-sessions/"+apiChild, `{"label":"分支"}`, 200)
	call("DELETE", "/api/tapd-requirements/"+id+"/sessions/"+apiChild, "", 200)
	call("GET", "/api/agent-sessions/operations", "", 200)
	call("DELETE", "/api/tapd-requirements/"+id, "", 200)
	call("POST", "/api/agent-sessions/"+apiParent+"/fork", `{"request_id":"../bad"}`, 400)
	call("POST", "/api/tapd-requirements", `{"url":"javascript:alert(1)"}`, 400)
	call("POST", "/api/tapd-requirements", `{"url":"https://tapd.example","session_ids":[]}`, 400)
	call("GET", "/api/agent-sessions/"+apiParent+"/fork", "", 405)
	for _, asset := range []string{"/assets/theme.css", "/assets/agent-sessions.css", "/assets/agent-sessions.js"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", asset, nil))
		if w.Code != 200 {
			t.Fatal("missing embedded asset", asset)
		}
	}
}
func TestAgentSessionWritesRejectCrossOrigin(t *testing.T) {
	s, _ := New(&config.Store{Path: filepath.Join(t.TempDir(), "config.json")}, "127.0.0.1:0")
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		path := "http://127.0.0.1:7777/api/tapd-requirements"
		if method == http.MethodDelete {
			path += "/123"
		}
		r := httptest.NewRequest(method, path, bytes.NewBufferString(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://unrelated.example")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross origin deletion accepted")
		}
	}
}
