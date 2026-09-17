package scheduler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeRawLines appends raw newline-terminated lines, bypassing rotation.
func writeRawLines(t *testing.T, path string, lines []string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	for _, ln := range lines {
		if _, err := f.WriteString(ln + "\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

func TestTailLinesAcrossChunkBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	// ~30 bytes/line * 5000 = ~150KB, well past the 64KB backward-read chunk.
	const total = 5000
	lines := make([]string, total)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%08d-padding", i)
	}
	writeRawLines(t, path, lines)

	got, err := tailLines(path, 100)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("want 100 lines, got %d", len(got))
	}
	// Must be the LAST 100, in original (oldest-first) order.
	for i, b := range got {
		want := lines[total-100+i]
		if string(b) != want {
			t.Fatalf("line %d: got %q want %q", i, b, want)
		}
	}
}

func TestTailLinesNonPositiveReturnsAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	writeRawLines(t, path, []string{"a", "b", "c"})
	got, err := tailLines(path, 0)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
}

func TestTailLinesMissingFile(t *testing.T) {
	_, err := tailLines(filepath.Join(t.TempDir(), "nope.jsonl"), 10)
	if !os.IsNotExist(err) {
		t.Fatalf("want IsNotExist, got %v", err)
	}
}

func TestReadHistoryReturnsLastN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	for i := 0; i < 500; i++ {
		if err := appendHistory(path, HistoryEntry{Name: fmt.Sprintf("s%d", i), Kind: "cmd"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	got, err := ReadHistory(path, 50)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("want 50, got %d", len(got))
	}
	// Oldest-first; the last entry must be the most recently appended.
	if got[0].Name != "s450" || got[49].Name != "s499" {
		t.Fatalf("window mismatch: first=%s last=%s", got[0].Name, got[49].Name)
	}
}

func TestReadHistoryMissingFileIsEmpty(t *testing.T) {
	got, err := ReadHistory(filepath.Join(t.TempDir(), "nope.jsonl"), 10)
	if err != nil {
		t.Fatalf("want nil err, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestRotateHistoryKeepsLastNAndShrinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	for i := 0; i < 1000; i++ {
		if err := appendHistory(path, HistoryEntry{Name: fmt.Sprintf("s%d", i)}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	before, _ := os.Stat(path)

	if err := rotateHistory(path, 100); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	after, _ := os.Stat(path)
	if after.Size() >= before.Size() {
		t.Fatalf("file did not shrink: before=%d after=%d", before.Size(), after.Size())
	}

	got, err := ReadHistory(path, 0) // read all -> should be exactly the kept window
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("want 100 after rotate, got %d", len(got))
	}
	if got[0].Name != "s900" || got[99].Name != "s999" {
		t.Fatalf("kept-window mismatch: first=%s last=%s", got[0].Name, got[99].Name)
	}
}
