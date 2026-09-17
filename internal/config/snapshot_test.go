package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportSnapshotWritesValidatedCopy(t *testing.T) {
	source := newTempStore(t)
	want := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Projects:      []Project{{Alias: "flow", Path: "/work/flow"}},
	}
	if err := source.Save(want); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "flow.config.json")
	path, err := ExportSnapshot(source, destination)
	if err != nil {
		t.Fatalf("ExportSnapshot: %v", err)
	}
	if path != destination {
		t.Fatalf("destination: want %s, got %s", destination, path)
	}
	got, err := (&Store{Path: destination}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].Alias != "flow" {
		t.Fatalf("snapshot mismatch: %+v", got)
	}
}

func TestImportSnapshotAtomicallyReplacesActiveConfig(t *testing.T) {
	destination := newTempStore(t)
	if err := destination.Save(&Config{
		SchemaVersion: CurrentSchemaVersion,
		Projects:      []Project{{Alias: "old", Path: "/old"}},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "flow.config.json")
	if err := (&Store{Path: snapshot}).Save(&Config{
		SchemaVersion: CurrentSchemaVersion,
		Scripts:       []Script{{Alias: "build", Command: "make"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportSnapshot(snapshot, destination); err != nil {
		t.Fatalf("ImportSnapshot: %v", err)
	}
	got, err := destination.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 0 || len(got.Scripts) != 1 || got.Scripts[0].Alias != "build" {
		t.Fatalf("active config was not replaced: %+v", got)
	}
}

func TestImportSnapshotRejectsInvalidInputWithoutChangingActiveConfig(t *testing.T) {
	destination := newTempStore(t)
	if err := destination.Save(&Config{
		SchemaVersion: CurrentSchemaVersion,
		Projects:      []Project{{Alias: "keep", Path: "/keep"}},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(snapshot, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportSnapshot(snapshot, destination); err == nil {
		t.Fatal("expected invalid snapshot to be rejected")
	}
	got, err := destination.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].Alias != "keep" {
		t.Fatalf("active config changed after failed import: %+v", got)
	}
}
