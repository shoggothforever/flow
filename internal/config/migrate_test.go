package config

import (
	"os"
	"strings"
	"testing"
)

func TestMigrateV1ToV2DropsLegacyLayoutsOnSave(t *testing.T) {
	s := newTempStore(t)
	legacy := `{
  "schema_version": 1,
  "projects": [{"alias": "flow", "path": "/src/flow"}],
  "layouts": [{"name": "dev", "session": "dev", "windows": []}],
  "scripts": [{"alias": "test", "cwd": "@flow", "command": "go test ./..."}]
}`
	if err := os.WriteFile(s.Path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Migrate(c)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || c.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("migration result: changed=%v schema_version=%d", changed, c.SchemaVersion)
	}
	if len(c.Projects) != 1 || len(c.Scripts) != 1 {
		t.Fatalf("supported resources were not preserved: %+v", c)
	}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"layouts"`) {
		t.Fatalf("legacy layouts field survived migration: %s", got)
	}
}

func TestMigrateCurrentVersionIsNoop(t *testing.T) {
	c := &Config{SchemaVersion: CurrentSchemaVersion}
	changed, err := Migrate(c)
	if err != nil || changed {
		t.Fatalf("Migrate: changed=%v err=%v", changed, err)
	}
}

func TestMigrateRejectsFutureVersion(t *testing.T) {
	c := &Config{SchemaVersion: CurrentSchemaVersion + 1}
	if _, err := Migrate(c); err == nil {
		t.Fatal("expected future schema version to be rejected")
	}
}
