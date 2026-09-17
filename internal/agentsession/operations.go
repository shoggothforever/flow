package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yithcai/flow/internal/config"
	"golang.org/x/sys/unix"
)

type Operation struct {
	RequestID string               `json:"request_id"`
	ParentID  string               `json:"parent_id"`
	State     string               `json:"state"` // pending, created, completed, failed, unknown
	StartedAt int64                `json:"started_at"`
	Session   *config.AgentSession `json:"session,omitempty"`
	Error     string               `json:"error,omitempty"`
}
type OperationError struct{ Operation Operation }

func (e *OperationError) Error() string  { return e.Operation.Error }
func (s *Service) OperationsDir() string { return s.Store.Path + ".agent-operations" }

func writeOperation(path string, op Operation) error {
	data, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".operation-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (s *Service) Operations() ([]Operation, error) {
	entries, err := os.ReadDir(s.OperationsDir())
	if os.IsNotExist(err) {
		return []Operation{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Operation{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.OperationsDir(), e.Name()))
		if err != nil {
			return nil, err
		}
		var op Operation
		if err = json.Unmarshal(b, &op); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, nil
}

// ResolveOperation records the user's explicit identification of an uncertain fork.
// Merely discovering a plausible child must not resolve an ambiguous operation.
func (s *Service) ResolveOperation(ctx context.Context, requestID, childID string) (Operation, error) {
	if !config.ValidSessionID(requestID) || !config.ValidSessionID(childID) {
		return Operation{}, fmt.Errorf("full request and child IDs are required")
	}
	path := filepath.Join(s.OperationsDir(), requestID+".json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Operation{}, err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return Operation{}, fmt.Errorf("operation is still running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	data, err := os.ReadFile(path)
	if err != nil {
		return Operation{}, err
	}
	var op Operation
	if err = json.Unmarshal(data, &op); err != nil {
		return op, err
	}
	if op.State == "completed" && op.Session != nil && op.Session.ID == childID {
		return op, nil
	}
	if op.State != "pending" && op.State != "unknown" {
		return op, fmt.Errorf("only an uncertain operation can be matched manually")
	}
	child, err := s.Client.Read(ctx, childID)
	if err != nil {
		return op, err
	}
	if child.ID == op.ParentID || child.ForkedFromID != op.ParentID || child.CreatedAt < op.StartedAt-2 {
		return op, fmt.Errorf("child does not match this operation's parent and creation time")
	}
	op.Session = &child
	op.State = "created"
	op.Error = ""
	if err = writeOperation(path, op); err != nil {
		return op, err
	}
	if err = s.Store.Mutate(func(c *config.Config) error { upsert(c, child); return nil }); err != nil {
		op.Error = err.Error()
		return op, &OperationError{op}
	}
	op.State = "completed"
	if err = writeOperation(path, op); err != nil {
		return op, err
	}
	return op, nil
}

// Fork serializes each idempotency key across processes, without holding the config lock
// during RPC. Unknown outcomes never cause an automatic second fork.
func (s *Service) Fork(ctx context.Context, query, requestID string) (Operation, error) {
	if !config.ValidSessionID(requestID) {
		return Operation{}, fmt.Errorf("a UUID request_id is required")
	}
	c, err := s.Store.Load()
	if err != nil {
		return Operation{}, err
	}
	known := c.AgentSessions
	if !config.ValidSessionID(strings.ToLower(strings.TrimSpace(query))) {
		local, e := s.Client.Discover(ctx)
		if e != nil {
			return Operation{}, fmt.Errorf("cannot resolve fork prefix: %w; use the full ID", e)
		}
		known = append(append([]config.AgentSession(nil), known...), local...)
	}
	id, err := Resolve(query, known)
	if err != nil {
		return Operation{}, err
	}
	if err = os.MkdirAll(s.OperationsDir(), 0700); err != nil {
		return Operation{}, err
	}
	path := filepath.Join(s.OperationsDir(), requestID+".json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Operation{}, err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return Operation{}, fmt.Errorf("fork request is still running; refresh operations before retrying")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	var op Operation
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &op); err != nil {
			return op, err
		}
		if op.ParentID != id {
			return op, fmt.Errorf("request_id already belongs to another parent")
		}
		switch op.State {
		case "completed":
			return op, nil
		case "pending", "unknown":
			op.State = "unknown"
			op.Error = "Fork outcome is unknown. Refresh local sessions and associate the matching child; this request will not fork again."
			if err = writeOperation(path, op); err != nil {
				return op, err
			}
			return op, &OperationError{op}
		case "failed":
			return op, &OperationError{op}
		case "created": // Persist the recorded child, without issuing another RPC.
		default:
			return op, fmt.Errorf("invalid operation state")
		}
	} else if !os.IsNotExist(err) {
		return op, err
	} else {
		associated := false
		for _, r := range c.TAPDRequirements {
			if Membership(c, r)[id] {
				associated = true
				break
			}
		}
		if !associated {
			return op, fmt.Errorf("associate the parent with a TAPD requirement before forking")
		}
		parent, err := s.Client.Read(ctx, id)
		if err != nil {
			return op, err
		}
		if !parent.Available {
			return op, fmt.Errorf("parent session is unavailable locally")
		}
		op = Operation{RequestID: requestID, ParentID: id, State: "pending", StartedAt: time.Now().Unix()}
		if err = writeOperation(path, op); err != nil {
			return op, err
		}
		// Even a lost response may have created a child. Invalidate after the
		// attempt so a pre-fork scan cannot hide it from subsequent pickers.
		defer s.invalidateDiscovery()
		x, err := s.Client.Fork(ctx, id)
		if err != nil {
			op.State = "unknown"
			var fe *ForkFailure
			if errors.As(err, &fe) && !fe.Uncertain {
				op.State = "failed"
			}
			op.Error = err.Error()
			if e := writeOperation(path, op); e != nil {
				op.Error += "; operation record: " + e.Error()
			}
			return op, &OperationError{op}
		}
		op.Session = &x
		op.State = "created"
		if err = writeOperation(path, op); err != nil {
			op.Error = "Fork created " + x.ID + ", but recording the result failed: " + err.Error()
			return op, &OperationError{op}
		}
	}
	if op.Session == nil {
		return op, fmt.Errorf("fork result is missing from operation record")
	}
	if err = s.Store.Mutate(func(c *config.Config) error { upsert(c, *op.Session); return nil }); err != nil {
		op.Error = "Fork created " + op.Session.ID + ", but saving the association failed: " + err.Error()
		_ = writeOperation(path, op)
		return op, &OperationError{op}
	}
	op.State = "completed"
	op.Error = ""
	if err = writeOperation(path, op); err != nil {
		return op, err
	}
	return op, nil
}
