package agentsession

import (
	"context"
	"sync"
	"time"

	"github.com/yithcai/flow/internal/config"
)

const discoveryTTL = 30 * time.Second

// Only browsing uses this cache. Prefix resolution deliberately calls the client
// directly: even a recent listing can miss a newly created, conflicting ID.
type discoveryCache struct {
	mu      sync.Mutex
	items   []config.AgentSession
	savedAt time.Time
	flight  *discoveryFlight
}

type discoveryFlight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	finished bool
	items    []config.AgentSession
	err      error
}

func cloneSessions(items []config.AgentSession) []config.AgentSession {
	return append([]config.AgentSession{}, items...)
}

// Discover reuses a complete inventory for 30 seconds and coalesces concurrent
// readers. An explicit refresh starts a new scan; older scans cannot overwrite it.
// Neither the cache nor refreshing it writes the Flow configuration.
func (s *Service) Discover(ctx context.Context, refresh bool) ([]config.AgentSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := &s.discovery
	c.mu.Lock()
	if refresh {
		c.savedAt = time.Time{}
		c.flight = nil
	}
	if !c.savedAt.IsZero() && time.Since(c.savedAt) < discoveryTTL {
		items := cloneSessions(c.items)
		c.mu.Unlock()
		return items, nil
	}
	f := c.flight
	if f == nil {
		// One disconnected browser must not cancel another reader's scan. The
		// scan is canceled below when its last waiter leaves, or at the deadline.
		scanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		f = &discoveryFlight{done: make(chan struct{}), cancel: cancel}
		c.flight = f
		client := s.Client
		go func() {
			defer cancel()
			items, err := client.Discover(scanCtx)
			c.mu.Lock()
			defer c.mu.Unlock()
			f.items, f.err, f.finished = cloneSessions(items), err, true
			if c.flight == f {
				if err == nil && scanCtx.Err() == nil {
					c.items, c.savedAt = cloneSessions(items), time.Now()
				}
				c.flight = nil
			}
			close(f.done)
		}()
	}
	f.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		f.waiters--
		if f.waiters == 0 && !f.finished {
			f.cancel()
			if c.flight == f {
				c.flight = nil
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		return cloneSessions(f.items), nil
	}
}

func (s *Service) invalidateDiscovery() {
	c := &s.discovery
	c.mu.Lock()
	defer c.mu.Unlock()
	c.savedAt = time.Time{}
	c.flight = nil
}
