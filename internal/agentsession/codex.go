package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yithcai/flow/internal/config"
)

var errSubagent = errors.New("Codex internal subagents cannot be associated as user sessions")
var errNotFound = errors.New("Codex session not found")

func IsSubagent(err error) bool { return errors.Is(err, errSubagent) }
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }

// ForkFailure distinguishes a rejected request from a lost response after dispatch.
type ForkFailure struct {
	Err       error
	Uncertain bool
}

func (e *ForkFailure) Error() string { return e.Err.Error() }
func (e *ForkFailure) Unwrap() error { return e.Err }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("Codex RPC %d: %s", e.Code, e.Message) }

type Codex struct {
	Binary      string   // Optional override for tests; production resolves PATH.
	Env         []string // nil inherits the Flow process environment.
	mu          sync.Mutex
	unsupported string
}

func (c *Codex) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "codex"
}
func (c *Codex) Status(ctx context.Context) Status {
	c.mu.Lock()
	problem := c.unsupported
	c.mu.Unlock()
	if problem != "" {
		return Status{Error: problem}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.binary(), "--version")
	cmd.Env = c.Env
	out, err := cmd.Output()
	if err != nil {
		return Status{Error: fmt.Sprintf("Codex unavailable: %v", err)}
	}
	return Status{Available: true, Version: strings.TrimSpace(string(out))}
}

type rpcProcess struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	dec    *json.Decoder
	seq    int
	cancel context.CancelFunc
}

func (c *Codex) open(ctx context.Context) (*rpcProcess, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	cmd := exec.CommandContext(ctx, c.binary(), "app-server", "--listen", "stdio://")
	cmd.Env = c.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// App-server may own helper processes. Reap the whole isolated group on exit.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, fmt.Errorf("start Codex: %w", err)
	}
	p := &rpcProcess{cmd: cmd, in: in, dec: json.NewDecoder(out), cancel: cancel}
	var initResult json.RawMessage
	if err = p.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "flow", "title": "Flow", "version": "1.0.0"}}, &initResult); err != nil {
		p.close()
		return nil, err
	}
	if err = json.NewEncoder(in).Encode(map[string]any{"method": "initialized"}); err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}
func (p *rpcProcess) close() { p.in.Close(); p.cancel(); _ = p.cmd.Wait() }
func (p *rpcProcess) call(method string, params any, out any) error {
	p.seq++
	id := p.seq
	if err := json.NewEncoder(p.in).Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := p.dec.Decode(&msg); err != nil {
			return fmt.Errorf("Codex response interrupted: %w", err)
		}
		if msg.Method != "" {
			if len(msg.ID) > 0 { // No tool, approval or auth callback is executed by this metadata client.
				if err := json.NewEncoder(p.in).Encode(map[string]any{"id": msg.ID, "error": rpcError{Code: -32601, Message: "Flow only supports session metadata and fork operations"}}); err != nil {
					return err
				}
			}
			continue
		}
		var got int
		if json.Unmarshal(msg.ID, &got) != nil || got != id {
			continue
		}
		if msg.Error != nil {
			return msg.Error
		}
		return json.Unmarshal(msg.Result, out)
	}
}

type codexThread struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Cwd            string          `json:"cwd"`
	ForkedFromID   string          `json:"forkedFromId"`
	ParentThreadID string          `json:"parentThreadId"`
	Source         json.RawMessage `json:"source"`
	ThreadSource   string          `json:"threadSource"`
	CreatedAt      int64           `json:"createdAt"`
	UpdatedAt      int64           `json:"updatedAt"`
}

func (t codexThread) session() (config.AgentSession, error) {
	var source string
	_ = json.Unmarshal(t.Source, &source)
	if t.ParentThreadID != "" || strings.Contains(strings.ToLower(string(t.Source)), "subagent") || strings.Contains(strings.ToLower(t.ThreadSource), "subagent") {
		return config.AgentSession{}, errSubagent
	}
	if !config.ValidSessionID(t.ID) {
		return config.AgentSession{}, fmt.Errorf("Codex returned an invalid thread ID")
	}
	return config.AgentSession{ID: t.ID, Agent: "codex", Title: t.Name, Cwd: t.Cwd, Source: source, ForkedFromID: t.ForkedFromID, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Available: true}, nil
}
func (c *Codex) Discover(ctx context.Context) ([]config.AgentSession, error) {
	p, err := c.open(ctx)
	if err != nil {
		return nil, err
	}
	defer p.close()
	out := []config.AgentSession{}
	seen := map[string]bool{}
	for _, archived := range []bool{false, true} {
		cursor := ""
		cursors := map[string]bool{}
		for {
			params := map[string]any{"limit": 100, "archived": archived, "modelProviders": []string{}, "sourceKinds": []string{"cli", "vscode", "exec", "appServer", "unknown"}}
			if cursor != "" {
				params["cursor"] = cursor
			}
			var result struct {
				Data       []codexThread `json:"data"`
				NextCursor string        `json:"nextCursor"`
			}
			if err = p.call("thread/list", params, &result); err != nil {
				return nil, err
			}
			for _, t := range result.Data {
				x, e := t.session()
				if IsSubagent(e) {
					continue
				}
				if e != nil {
					return nil, e
				}
				if !seen[x.ID] {
					x.Archived = archived
					out = append(out, x)
					seen[x.ID] = true
				}
			}
			cursor = result.NextCursor
			if cursor == "" {
				break
			}
			if cursors[cursor] {
				return nil, fmt.Errorf("Codex returned a repeated pagination cursor")
			}
			cursors[cursor] = true
		}
	}
	return out, nil
}
func (c *Codex) Read(ctx context.Context, id string) (config.AgentSession, error) {
	if !config.ValidSessionID(id) {
		return config.AgentSession{}, fmt.Errorf("invalid session ID")
	}
	p, err := c.open(ctx)
	if err != nil {
		return config.AgentSession{}, err
	}
	defer p.close()
	var result struct {
		Thread codexThread `json:"thread"`
	}
	if err = p.call("thread/read", map[string]any{"threadId": id, "includeTurns": false}, &result); err != nil {
		var re *rpcError
		if errors.As(err, &re) && (strings.Contains(strings.ToLower(re.Message), "not found") || strings.Contains(strings.ToLower(re.Message), "no rollout")) {
			return config.AgentSession{}, fmt.Errorf("%w: %s", errNotFound, re.Message)
		}
		return config.AgentSession{}, err
	}
	return result.Thread.session()
}
func (c *Codex) Fork(ctx context.Context, id string) (config.AgentSession, error) {
	if !config.ValidSessionID(id) {
		return config.AgentSession{}, &ForkFailure{Err: fmt.Errorf("invalid session ID")}
	}
	p, err := c.open(ctx)
	if err != nil {
		return config.AgentSession{}, &ForkFailure{Err: err}
	}
	defer p.close()
	var result struct {
		Thread codexThread `json:"thread"`
	}
	if err = p.call("thread/fork", map[string]any{"threadId": id, "ephemeral": false, "excludeTurns": true}, &result); err != nil {
		var re *rpcError
		definite := errors.As(err, &re)
		if definite && re.Code == -32601 {
			c.mu.Lock()
			c.unsupported = "This Codex version does not support thread/fork; upgrade Codex"
			c.mu.Unlock()
		}
		return config.AgentSession{}, &ForkFailure{Err: err, Uncertain: !definite}
	}
	x, err := result.Thread.session()
	if err != nil {
		return x, &ForkFailure{Err: err, Uncertain: true}
	}
	if x.ID == id || (x.ForkedFromID != "" && x.ForkedFromID != id) {
		return x, &ForkFailure{Err: fmt.Errorf("Codex returned inconsistent fork ancestry"), Uncertain: true}
	}
	x.ForkedFromID = id
	return x, nil
}
