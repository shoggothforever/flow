package agentsession

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yithcai/flow/internal/config"
)

type discoveryClient struct {
	*fakeClient
	calls atomic.Int32
	list  func(context.Context, int) ([]config.AgentSession, error)
}

func (c *discoveryClient) Discover(ctx context.Context) ([]config.AgentSession, error) {
	n := int(c.calls.Add(1))
	if c.list != nil {
		return c.list(ctx, n)
	}
	return c.fakeClient.Discover(ctx)
}

func discover(t *testing.T, s *Service, refresh bool) []config.AgentSession {
	t.Helper()
	items, err := s.Discover(context.Background(), refresh)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestDiscoveryCacheFreshnessAndIsolation(t *testing.T) {
	s, f := setup(t)
	c := &discoveryClient{fakeClient: f}
	s.Client = c
	items := discover(t, s, false)
	items[0].Title = "caller mutation"
	if discover(t, s, false)[0].Title == "caller mutation" || c.calls.Load() != 1 {
		t.Fatal("cache was mutated or repeated discovery did not reuse it")
	}
	s.discovery.mu.Lock()
	s.discovery.savedAt = time.Now().Add(-discoveryTTL)
	s.discovery.mu.Unlock()
	discover(t, s, false)
	if c.calls.Load() != 2 {
		t.Fatal("expired cache was reused")
	}
	f.sessions = nil
	if items := discover(t, s, true); items == nil || len(items) != 0 {
		t.Fatal("explicit refresh did not return an empty JSON array")
	}
	discover(t, s, false)
	if c.calls.Load() != 3 {
		t.Fatal("empty inventories must also be cached")
	}
	f.discoverErr = errors.New("offline")
	if _, err := s.Discover(context.Background(), true); err == nil {
		t.Fatal("failed refresh returned stale data as current")
	}
	f.discoverErr = nil
	f.sessions = []config.AgentSession{fixture(dID, "")}
	if items := discover(t, s, false); len(items) != 1 || items[0].ID != dID || c.calls.Load() != 5 {
		t.Fatal("failed scan was cached instead of retried", items, c.calls.Load())
	}
}

func waitDiscovery(t *testing.T, s *Service, condition func(*discoveryFlight) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.discovery.mu.Lock()
		ok := condition(s.discovery.flight)
		s.discovery.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for discovery state")
}

func TestDiscoveryCoalescesAndCancelsOnlyAfterLastWaiter(t *testing.T) {
	s, f := setup(t)
	release, canceled := make(chan struct{}), make(chan struct{})
	c := &discoveryClient{fakeClient: f, list: func(ctx context.Context, n int) ([]config.AgentSession, error) {
		select {
		case <-release:
			return []config.AgentSession{fixture(aID, "")}, nil
		case <-ctx.Done():
			close(canceled)
			return nil, ctx.Err()
		}
	}}
	s.Client = c
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := s.Discover(ctx, false); first <- err }()
	waitDiscovery(t, s, func(f *discoveryFlight) bool { return f != nil && f.waiters == 1 })
	second := make(chan error, 1)
	go func() { _, err := s.Discover(context.Background(), false); second <- err }()
	waitDiscovery(t, s, func(f *discoveryFlight) bool { return f != nil && f.waiters == 2 })
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal("canceled reader did not exit", err)
	}
	select {
	case <-canceled:
		t.Fatal("one canceled reader killed the other reader's scan")
	default:
	}
	close(release)
	if err := <-second; err != nil || c.calls.Load() != 1 {
		t.Fatal("concurrent discovery was duplicated", err, c.calls.Load())
	}

	// If every reader leaves, terminate the subprocess scan rather than leaking it.
	s.invalidateDiscovery()
	release = make(chan struct{})
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { _, err := s.Discover(ctx, false); first <- err }()
	waitDiscovery(t, s, func(f *discoveryFlight) bool { return f != nil && f.waiters == 1 })
	cancel()
	<-first
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned scan was not canceled")
	}
}

func TestRefreshCannotBeOverwrittenByOlderDiscovery(t *testing.T) {
	s, f := setup(t)
	release := make(chan struct{})
	c := &discoveryClient{fakeClient: f, list: func(ctx context.Context, n int) ([]config.AgentSession, error) {
		if n == 1 {
			<-release
			return []config.AgentSession{fixture(aID, "")}, nil
		}
		return []config.AgentSession{fixture(bID, "")}, nil
	}}
	s.Client = c
	old := make(chan error, 1)
	go func() { _, err := s.Discover(context.Background(), false); old <- err }()
	waitDiscovery(t, s, func(f *discoveryFlight) bool { return f != nil && f.waiters == 1 })
	// Wait for the first client call, not just the cache flight allocation.
	for c.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if discover(t, s, true)[0].ID != bID {
		t.Fatal("explicit refresh joined an older scan")
	}
	close(release)
	if err := <-old; err != nil {
		t.Fatal(err)
	}
	if discover(t, s, false)[0].ID != bID || c.calls.Load() != 2 {
		t.Fatal("older scan overwrote the refreshed cache")
	}
}

func TestDiscoveryCacheDoesNotHideExternalForksOrPrefixConflicts(t *testing.T) {
	s, f := setup(t)
	c := &discoveryClient{fakeClient: f}
	s.Client = c
	r := addReq(t, s, "https://tapd.example/story/1")
	f.sessions = []config.AgentSession{fixture(aID, "")}
	link(t, s, r, aID)
	discover(t, s, false)
	f.sessions = append(f.sessions, fixture(bID, aID))
	if _, err := s.Link(context.Background(), r, "00000000"); err == nil {
		t.Fatal("prefix conflict hidden by the picker cache")
	}
	if _, err := s.Fork(context.Background(), "00000000", requestID); err == nil {
		t.Fatal("fork prefix conflict hidden by the picker cache")
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	items := discover(t, s, false)
	snapshot, _ := s.Snapshot()
	if len(items) != 2 || len(snapshot.Sessions) != 2 || c.calls.Load() != 4 {
		t.Fatal("sync must scan fresh and populate the picker cache", c.calls.Load())
	}
	if _, err := s.Fork(context.Background(), aID, requestID); err != nil {
		t.Fatal(err)
	}
	discover(t, s, false)
	if c.calls.Load() != 5 {
		t.Fatal("fork did not invalidate discovery")
	}
}

func TestUnknownForkAlsoInvalidatesDiscovery(t *testing.T) {
	s, f := setup(t)
	c := &discoveryClient{fakeClient: f}
	s.Client = c
	r := addReq(t, s, "https://tapd.example/story/1")
	link(t, s, r, aID)
	discover(t, s, false)
	f.onFork = func() { f.sessions = append(f.sessions, fixture(dID, aID)) }
	f.forkErr = &ForkFailure{Err: errors.New("response lost"), Uncertain: true}
	op, err := s.Fork(context.Background(), aID, requestID)
	if err == nil || op.State != "unknown" {
		t.Fatal("fixture did not produce an uncertain fork", op, err)
	}
	if len(discover(t, s, false)) != 4 || c.calls.Load() != 2 {
		t.Fatal("unknown fork left a stale inventory")
	}
}
