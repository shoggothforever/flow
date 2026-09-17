package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{Path: filepath.Join(dir, "config.json")}
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	s := newTempStore(t)
	c, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema_version: want %d, got %d", CurrentSchemaVersion, c.SchemaVersion)
	}
	if len(c.Projects) != 0 {
		t.Fatalf("expected empty projects, got %d", len(c.Projects))
	}
}

func TestSaveLoadRoundTripAtomic(t *testing.T) {
	s := newTempStore(t)
	c := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Projects: []Project{
			{Alias: "oev", Path: "/dsm/oev"},
			{Alias: "cube", Path: "/dsm/oev/CubeSandbox", Tags: []string{"go", "rust"}},
		},
	}
	if err := s.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Sibling .tmp must not be left behind.
	entries, err := os.ReadDir(filepath.Dir(s.Path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != 2 || got.Projects[1].Alias != "cube" {
		t.Fatalf("round trip mismatch: %+v", got.Projects)
	}
}

func TestValidateRejectsDuplicateAliases(t *testing.T) {
	c := &Config{
		Projects: []Project{
			{Alias: "x", Path: "/a"},
			{Alias: "x", Path: "/b"},
		},
	}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected duplicate alias error")
	}
}

func TestMutateAppendsAndPersists(t *testing.T) {
	s := newTempStore(t)
	if err := s.Mutate(func(c *Config) error {
		c.Projects = append(c.Projects, Project{Alias: "a", Path: "/x"})
		return nil
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if err := s.Mutate(func(c *Config) error {
		c.Projects = append(c.Projects, Project{Alias: "b", Path: "/y"})
		return nil
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(got.Projects))
	}
}

func TestMutateConcurrentSerialisesViaFlock(t *testing.T) {
	s := newTempStore(t)
	var wg sync.WaitGroup
	const N = 10
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			err := s.Mutate(func(c *Config) error {
				c.Projects = append(c.Projects, Project{
					Alias: aliasFor(i),
					Path:  "/p",
				})
				return nil
			})
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != N {
		t.Fatalf("expected %d projects after concurrent mutate, got %d", N, len(got.Projects))
	}
}

func aliasFor(i int) string {
	return "p" + string(rune('a'+i))
}

func TestResolvePathHonoursOverride(t *testing.T) {
	t.Setenv(EnvVar, "/tmp/from-env.json")
	got, err := ResolvePath("/tmp/explicit.json")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if got != "/tmp/explicit.json" {
		t.Fatalf("override ignored: %s", got)
	}
}

func TestResolvePathHonoursEnv(t *testing.T) {
	t.Setenv(EnvVar, "/tmp/from-env.json")
	got, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if got != "/tmp/from-env.json" {
		t.Fatalf("env ignored: %s", got)
	}
}
