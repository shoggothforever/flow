package agentsession

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yithcai/flow/internal/config"
)

// Launch the test executable as a real stdio RPC peer. This tests framing,
// initialization, pagination, callback rejection, and process cleanup together.
func TestCodexRPCProcess(t *testing.T) {
	if os.Getenv("FLOW_CODEX_TEST_PROCESS") != "1" {
		return
	}
	if os.Args[len(os.Args)-1] == "--version" {
		fmt.Println("codex-cli test")
		os.Exit(0)
	}
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	initialized := false
	for {
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			os.Exit(0)
		}
		if req.Method == "initialized" {
			initialized = true
			continue
		}
		var result any
		thread := func(id, parent string) map[string]any {
			return map[string]any{"id": id, "sessionId": aID, "name": "metadata only", "forkedFromId": parent, "cwd": "/tmp", "source": "appServer"}
		}
		if req.Method != "initialize" && !initialized {
			_ = enc.Encode(map[string]any{"id": req.ID, "error": rpcError{Code: -1, Message: "not initialized"}})
			continue
		}
		switch req.Method {
		case "initialize":
			result = map[string]any{"userAgent": "test"}
		case "thread/list":
			kinds, _ := req.Params["sourceKinds"].([]any)
			if len(kinds) < 4 {
				os.Exit(10)
			}
			if req.Params["archived"] == true {
				result = map[string]any{"data": []any{thread(cID, bID)}, "nextCursor": nil}
			} else if req.Params["cursor"] == "next" {
				if os.Getenv("FLOW_CODEX_TEST_FAIL_PAGE") == "1" {
					_ = enc.Encode(map[string]any{"id": req.ID, "error": rpcError{Code: -1, Message: "page failed"}})
					continue
				}
				sub := thread(dID, aID)
				sub["parentThreadId"] = aID
				result = map[string]any{"data": []any{thread(bID, aID), sub}, "nextCursor": nil}
			} else {
				result = map[string]any{"data": []any{thread(aID, "")}, "nextCursor": "next"}
			}
		case "thread/read":
			if req.Params["includeTurns"] != false {
				os.Exit(11)
			}
			if req.Params["threadId"] == dID {
				_ = enc.Encode(map[string]any{"id": req.ID, "error": rpcError{Code: -32600, Message: "thread not found"}})
				continue
			}
			result = map[string]any{"thread": thread(bID, aID)}
		case "thread/fork":
			if os.Getenv("FLOW_CODEX_TEST_UNSUPPORTED") == "1" {
				_ = enc.Encode(map[string]any{"id": req.ID, "error": rpcError{Code: -32601, Message: "unknown method"}})
				continue
			}
			if req.Params["ephemeral"] != false || req.Params["excludeTurns"] != true || req.Params["approvalPolicy"] != nil || req.Params["sandbox"] != nil {
				os.Exit(12)
			}
			result = map[string]any{"thread": thread(dID, req.Params["threadId"].(string))}
		default:
			os.Exit(13) // In particular, turn/start must never be sent.
		}
		_ = enc.Encode(map[string]any{"method": "thread/status/changed", "params": map[string]string{"threadId": aID}})
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
	}
}
func testCodex(t *testing.T) *Codex {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' -test.run=TestCodexRPCProcess -- \"$@\"\n"
	if e = os.WriteFile(path, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	return &Codex{Binary: path, Env: append(os.Environ(), "FLOW_CODEX_TEST_PROCESS=1")}
}
func TestCodexPaginationAndCorrectThreadID(t *testing.T) {
	c := testCodex(t)
	ctx := context.Background()
	if !c.Status(ctx).Available {
		t.Fatal("status unavailable")
	}
	items, e := c.Discover(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(items) != 3 || items[1].ID != bID || items[1].ForkedFromID != aID || !items[2].Archived {
		t.Fatalf("bad listing: %+v", items)
	}
	x, e := c.Read(ctx, bID)
	if e != nil || x.ID != bID {
		t.Fatalf("read incorrectly used session tree root: %+v %v", x, e)
	}
	if _, e = c.Read(ctx, dID); !IsNotFound(e) {
		t.Fatal("missing record not identified", e)
	}
	x, e = c.Fork(ctx, bID)
	if e != nil || x.ID != dID || x.ForkedFromID != bID {
		t.Fatal(x, e)
	}
}
func TestCodexFailedPageAndUnsupportedFork(t *testing.T) {
	c := testCodex(t)
	c.Env = append(c.Env, "FLOW_CODEX_TEST_FAIL_PAGE=1")
	if v, e := c.Discover(context.Background()); e == nil || v != nil {
		t.Fatal("partial listing returned as complete")
	}
	c.Env = append(c.Env, "FLOW_CODEX_TEST_UNSUPPORTED=1")
	if _, e := c.Fork(context.Background(), aID); e == nil {
		t.Fatal("unsupported fork accepted")
	}
	if c.Status(context.Background()).Available {
		t.Fatal("unsupported fork was not disabled")
	}
}

// Opt-in integration uses a temporary CODEX_HOME and a synthetic conversation.
// It performs metadata/fork/resume calls only, never a model turn or tool call.
func TestInstalledCodexForkIntegration(t *testing.T) {
	if os.Getenv("FLOW_CODEX_INTEGRATION") != "1" {
		t.Skip("set FLOW_CODEX_INTEGRATION=1 to exercise the installed Codex")
	}
	home := t.TempDir()
	cwd := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "09", "14")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "rollout-2026-09-14T10-00-00-"+aID+".jsonl")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	enc := json.NewEncoder(f)
	rows := []map[string]any{
		{"timestamp": "2026-09-14T10:00:00Z", "type": "session_meta", "payload": map[string]any{"id": aID, "session_id": aID, "timestamp": "2026-09-14T10:00:00Z", "cwd": cwd, "originator": "codex_cli_rs", "cli_version": "0.154.0", "source": "cli", "model_provider": "openai", "base_instructions": map[string]string{"text": "Synthetic Flow integration fixture. No model turns are requested."}}},
		{"timestamp": "2026-09-14T10:00:01Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "Synthetic metadata-only integration test."}}}},
		{"timestamp": "2026-09-14T10:00:01Z", "type": "event_msg", "payload": map[string]string{"type": "user_message", "message": "Synthetic metadata-only integration test."}},
		{"timestamp": "2026-09-14T10:00:02Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "Fixture response."}}}},
	}
	for _, row := range rows {
		if e = enc.Encode(row); e != nil {
			t.Fatal(e)
		}
	}
	f.Close()
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "CODEX_HOME=") {
			env = append(env, v)
		}
	}
	env = append(env, "CODEX_HOME="+home)
	c := &Codex{Env: env}
	ctx := context.Background()
	items, e := c.Discover(ctx)
	if e != nil {
		t.Fatal("discover", e)
	}
	if len(items) != 1 || items[0].ID != aID {
		t.Fatalf("unexpected listing: %+v", items)
	}
	child, e := c.Fork(ctx, aID)
	if e != nil {
		t.Fatal("fork", e)
	}
	if child.ID == aID || child.ForkedFromID != aID || !config.ValidSessionID(child.ID) {
		t.Fatalf("bad fork: %+v", child)
	}
	if child.Cwd != cwd {
		t.Fatalf("fork did not inherit source cwd: want %s, got %s", cwd, child.Cwd)
	}
	read, e := c.Read(ctx, child.ID)
	if e != nil || read.ForkedFromID != aID {
		t.Fatal("new process cannot read persisted fork", read, e)
	}
	// Resume without turn/start proves that this is a restorable thread ID.
	p, e := c.open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	var result struct {
		Thread codexThread `json:"thread"`
	}
	if e = p.call("thread/resume", map[string]any{"threadId": child.ID}, &result); e != nil {
		t.Fatal("resume", e)
	}
	if result.Thread.ID != child.ID {
		t.Fatal("resume returned wrong thread")
	}
	// Verify the persisted history is still just the fixture, without new user turns.
	if e = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		dec := json.NewDecoder(f)
		for {
			var row map[string]any
			if e = dec.Decode(&row); e == io.EOF {
				break
			} else if e != nil {
				return e
			}
			if row["type"] == "event_msg" {
				payload, _ := row["payload"].(map[string]any)
				if payload["type"] == "task_started" {
					t.Error("unexpected model turn")
				}
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	t.Logf("installed Codex: persisted fork %s -> %s, readable and resumable in new processes", aID, child.ID)
}
