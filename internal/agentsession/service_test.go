package agentsession

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yithcai/flow/internal/config"
)

const aID = "00000000-0000-4000-8000-000000000001"
const bID = "00000000-0000-4000-8000-000000000002"
const cID = "00000000-0000-4000-8000-000000000003"
const dID = "00000000-0000-4000-8000-000000000004"
const requestID = "10000000-0000-4000-8000-000000000001"

type fakeClient struct {
	mu          sync.Mutex
	sessions    []config.AgentSession
	discoverErr error
	forkErr     error
	forks       int
	onFork      func()
}

func fixture(id, parent string) config.AgentSession {
	return config.AgentSession{ID: id, Agent: "codex", Title: "Session " + id, Cwd: "/tmp", ForkedFromID: parent, Available: true, CreatedAt: time.Now().Unix()}
}
func (f *fakeClient) Discover(context.Context) ([]config.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]config.AgentSession(nil), f.sessions...), f.discoverErr
}
func (f *fakeClient) Read(_ context.Context, id string) (config.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return config.AgentSession{}, errNotFound
}
func (f *fakeClient) Fork(_ context.Context, id string) (config.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forks++
	if f.onFork != nil {
		f.onFork()
	}
	return fixture(dID, id), f.forkErr
}
func (f *fakeClient) Status(context.Context) Status { return Status{Available: true, Version: "fake"} }
func setup(t *testing.T) (*Service, *fakeClient) {
	t.Helper()
	f := &fakeClient{sessions: []config.AgentSession{fixture(aID, ""), fixture(bID, aID), fixture(cID, bID)}}
	return &Service{Store: &config.Store{Path: filepath.Join(t.TempDir(), "config.json")}, Client: f}, f
}
func addReq(t *testing.T, s *Service, url string) string {
	t.Helper()
	r, e := s.SaveRequirement("", url, "Requirement", "")
	if e != nil {
		t.Fatal(e)
	}
	return r.ID
}
func link(t *testing.T, s *Service, r, id string) {
	t.Helper()
	if _, e := s.Link(context.Background(), r, id); e != nil {
		t.Fatal(e)
	}
}

func TestSharedTreesAndExclusionSurviveRefresh(t *testing.T) {
	s, f := setup(t)
	r1 := addReq(t, s, "https://tapd.example/story/1?tab=detail")
	r2 := addReq(t, s, "https://tapd.example/story/2")
	link(t, s, r1, aID)
	link(t, s, r2, aID)
	if e := s.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	c, _ := s.Store.Load()
	if len(Membership(c, *FindRequirement(c, r1))) != 3 || len(Membership(c, *FindRequirement(c, r2))) != 3 {
		t.Fatal("fork descendants missing")
	}
	// Even an explicitly linked descendant is removed with the entire branch.
	link(t, s, r1, cID)
	if e := s.Unlink(r1, bID); e != nil {
		t.Fatal(e)
	}
	f.sessions = append(f.sessions, fixture(dID, cID))
	if e := s.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	c, _ = s.Store.Load()
	m := Membership(c, *FindRequirement(c, r1))
	if len(m) != 1 || !m[aID] {
		t.Fatalf("excluded branch restored: %v", m)
	}
	if len(Membership(c, *FindRequirement(c, r2))) != 4 {
		t.Fatal("unlink changed another requirement")
	}
	link(t, s, r1, bID)
	c, _ = s.Store.Load()
	if len(Membership(c, *FindRequirement(c, r1))) != 4 {
		t.Fatal("explicit reassociation did not restore branch")
	}
	if e := s.DeleteRequirement(r1); e != nil {
		t.Fatal(e)
	}
	c, _ = s.Store.Load()
	if len(c.AgentSessions) != 4 {
		t.Fatal("deleting requirement deleted sessions")
	}
}
func TestImportAncestorsMissingIDsAndPrefixAmbiguity(t *testing.T) {
	s, f := setup(t)
	r := addReq(t, s, "https://tapd.example/story/1")
	link(t, s, r, bID)
	v, e := s.Snapshot()
	if e != nil {
		t.Fatal(e)
	}
	nodes := v.Trees[0].Nodes
	if len(nodes) != 2 {
		t.Fatal(nodes)
	}
	for _, n := range nodes {
		if n.ID == aID && !n.ContextOnly {
			t.Fatal("ancestor became associated")
		}
	}
	if _, e = s.Link(context.Background(), r, "00000000"); e == nil {
		t.Fatal("ambiguous prefix accepted")
	}
	if _, e = s.Link(context.Background(), r, "000"); e == nil {
		t.Fatal("short prefix accepted")
	}
	f.sessions = nil
	link(t, s, r, dID)
	v, _ = s.Snapshot()
	for _, x := range v.Sessions {
		if x.ID == dID && x.Available {
			t.Fatal("missing session marked available")
		}
	}
	// Prefixes may not be resolved from a partial cache when discovery failed.
	f.discoverErr = errors.New("offline")
	if _, e = s.Link(context.Background(), r, aID[:35]); e == nil {
		t.Fatal("resolved prefix against incomplete local index")
	}
}
func TestSyncFailurePreservesConfigAndLabel(t *testing.T) {
	s, f := setup(t)
	r := addReq(t, s, "https://tapd.example/story/1")
	link(t, s, r, aID)
	if e := s.Rename(aID, "My local title"); e != nil {
		t.Fatal(e)
	}
	if e := s.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(s.Store.Path)
	f.discoverErr = errors.New("page two failed")
	if e := s.Sync(context.Background()); e == nil {
		t.Fatal("failure hidden")
	}
	after, _ := os.ReadFile(s.Store.Path)
	if string(before) != string(after) {
		t.Fatal("partial discovery changed config")
	}
	c, _ := s.Store.Load()
	if c.AgentSessions[0].Label != "My local title" {
		t.Fatal("sync erased display name")
	}
}
func TestConcurrentLinksAndSnapshotRoundTrip(t *testing.T) {
	s, _ := setup(t)
	r := addReq(t, s, "https://tapd.example/story/1?q=kept#fragment")
	var wg sync.WaitGroup
	for _, id := range []string{aID, bID, cID, dID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, e := s.Link(context.Background(), r, id); e != nil {
				t.Error(e)
			}
		}(id)
	}
	wg.Wait()
	c, _ := s.Store.Load()
	if len(c.TAPDRequirements[0].SessionIDs) != 4 {
		t.Fatal("lost concurrent link")
	}
	if c.TAPDRequirements[0].URL != "https://tapd.example/story/1?q=kept" {
		t.Fatal("query parameters changed")
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.json")
	if _, e := config.ExportSnapshot(s.Store, snapshot); e != nil {
		t.Fatal(e)
	}
	dest := &config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	if _, e := config.ImportSnapshot(snapshot, dest); e != nil {
		t.Fatal(e)
	}
	d, _ := dest.Load()
	if len(d.AgentSessions) != 4 || len(d.TAPDRequirements[0].SessionIDs) != 4 {
		t.Fatal("snapshot lost associations")
	}
}
func TestCycleAndDuplicateValidation(t *testing.T) {
	c := &config.Config{AgentSessions: []config.AgentSession{fixture(aID, bID), fixture(bID, aID)}}
	if c.Validate() == nil {
		t.Fatal("cycle accepted")
	}
	c.AgentSessions = []config.AgentSession{fixture(aID, ""), fixture(aID, "")}
	if c.Validate() == nil {
		t.Fatal("duplicate ID accepted")
	}
	c.AgentSessions = []config.AgentSession{fixture(aID, dID)}
	if e := c.Validate(); e != nil {
		t.Fatal("missing ancestor should be allowed", e)
	}
}
func TestForkIdempotencyAndRecoveryAfterConfigFailure(t *testing.T) {
	s, f := setup(t)
	r := addReq(t, s, "https://tapd.example/story/1")
	link(t, s, r, aID)
	before, _ := os.ReadFile(s.Store.Path)
	f.onFork = func() { _ = os.WriteFile(s.Store.Path, []byte("broken config"), 0600) }
	op, e := s.Fork(context.Background(), aID, requestID)
	if e == nil || op.State != "created" || op.Session.ID != dID {
		t.Fatalf("lost created fork: %+v %v", op, e)
	}
	_ = os.WriteFile(s.Store.Path, before, 0600)
	f.onFork = nil
	op, e = s.Fork(context.Background(), aID, requestID)
	if e != nil || op.State != "completed" {
		t.Fatalf("recovery: %+v %v", op, e)
	}
	if _, e = s.Fork(context.Background(), aID, requestID); e != nil {
		t.Fatal(e)
	}
	if f.forks != 1 {
		t.Fatal("retry forked twice")
	}
	c, _ := s.Store.Load()
	if !Membership(c, *FindRequirement(c, r))[dID] {
		t.Fatal("fork was not associated")
	}
	if _, e = s.Fork(context.Background(), bID, requestID); e == nil {
		t.Fatal("request ID reused with different parent")
	}
}
func TestUnknownForkNeverAutomaticallyRetries(t *testing.T) {
	s, f := setup(t)
	r := addReq(t, s, "https://tapd.example/story/1")
	link(t, s, r, aID)
	f.forkErr = &ForkFailure{Err: errors.New("connection lost"), Uncertain: true}
	op, e := s.Fork(context.Background(), aID, requestID)
	if e == nil || op.State != "unknown" {
		t.Fatal(op, e)
	}
	_, _ = s.Fork(context.Background(), aID, requestID)
	if f.forks != 1 {
		t.Fatal("unknown outcome retried")
	}
	f.sessions = append(f.sessions, fixture(dID, aID))
	if _, e = s.ResolveOperation(context.Background(), requestID, cID); e == nil {
		t.Fatal("wrong parent accepted")
	}
	op, e = s.ResolveOperation(context.Background(), requestID, dID)
	if e != nil || op.State != "completed" {
		t.Fatal(op, e)
	}
	if f.forks != 1 {
		t.Fatal("manual recovery created a fork")
	}
}

func TestConcurrentForkRequestOnlyCreatesOnce(t *testing.T) {
	s, f := setup(t)
	r := addReq(t, s, "https://tapd.example/story/1")
	link(t, s, r, aID)
	started, release := make(chan struct{}), make(chan struct{})
	f.onFork = func() { close(started); <-release }
	done := make(chan error, 1)
	go func() { _, e := s.Fork(context.Background(), aID, requestID); done <- e }()
	<-started
	_, err := s.Fork(context.Background(), aID, requestID)
	close(release)
	if err == nil {
		t.Error("concurrent fork should report running")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if f.forks != 1 {
		t.Fatal("concurrent request forked more than once")
	}
}
