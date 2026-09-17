package finder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"30m", 30 * time.Minute, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"2w", 14 * 24 * time.Hour, false},
		{"", 0, true},
		{"foo", 0, true},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseDuration(%q) err=%v want_err=%v", c.in, err, c.err)
			continue
		}
		if !c.err && got != c.want {
			t.Errorf("ParseDuration(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in            string
		min, max      int64
		err           bool
	}{
		{"", -1, -1, false},
		{"+1K", 1024, -1, false},
		{"-1M", -1, 1024 * 1024, false},
		{"512", 512, 512, false},
		{"+1G", 1 << 30, -1, false},
		{"abc", -1, -1, true},
	}
	for _, c := range cases {
		min, max, err := ParseSize(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseSize(%q) err=%v", c.in, err)
			continue
		}
		if min != c.min || max != c.max {
			t.Errorf("ParseSize(%q) = (%d,%d) want (%d,%d)", c.in, min, max, c.min, c.max)
		}
	}
}

// TestFindFiltering builds a small temp tree and verifies that name/ext/mtime/kind
// filters and Limit/sort cooperate.
func TestFindFiltering(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, content string, mtime time.Time) {
		full := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
		if err := os.Chtimes(full, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", full, err)
		}
	}

	now := time.Now()
	mustWrite("a.go", "package a", now.Add(-1*time.Hour))
	mustWrite("b_test.go", "package a", now.Add(-30*time.Minute))
	mustWrite("c.rs", "fn main(){}", now.Add(-72*time.Hour))
	mustWrite("nested/d.go", "package n", now.Add(-2*time.Hour))
	mustWrite("nested/.hidden", "secret", now)
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite("node_modules/skip.go", "x", now)

	// 1. type=f + ext=.go => 3 hits
	hits, err := Find(Query{Roots: []string{root}, Kind: KindFile, Exts: []string{".go"}, SizeMin: -1, SizeMax: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("want 3 .go hits, got %d (%+v)", len(hits), hits)
	}

	// 2. modified within 90m => a.go (1h) and b_test.go (30m) only
	hits, _ = Find(Query{
		Roots:   []string{root},
		Exts:    []string{".go"},
		After:   now.Add(-90 * time.Minute),
		SizeMin: -1, SizeMax: -1,
	})
	if len(hits) != 2 {
		t.Fatalf("mtime window: want 2, got %d (%+v)", len(hits), hits)
	}

	// 3. name=*_test.go
	hits, _ = Find(Query{Roots: []string{root}, Name: "*_test.go", SizeMin: -1, SizeMax: -1})
	if len(hits) != 1 || hits[0].Name != "b_test.go" {
		t.Fatalf("glob: %+v", hits)
	}

	// 4. limit=1 + sort=mtime (newest first) => b_test.go
	hits, _ = Find(Query{Roots: []string{root}, Exts: []string{".go"}, Limit: 1, SizeMin: -1, SizeMax: -1})
	if len(hits) != 1 || hits[0].Name != "b_test.go" {
		t.Fatalf("limit/sort: %+v", hits)
	}

	// 5. excludes default catches node_modules; .hidden excluded by Hidden=false
	hits, _ = Find(Query{Roots: []string{root}, SizeMin: -1, SizeMax: -1})
	for _, h := range hits {
		if h.Name == ".hidden" || filepath.Base(filepath.Dir(h.Path)) == "node_modules" {
			t.Fatalf("default exclude leaked: %s", h.Path)
		}
	}
}

func TestFindRegex(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"schema_v1.go", "schema_v2.go", "other.go"} {
		_ = os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644)
	}
	hits, err := Find(Query{Roots: []string{root}, Regex: `^schema_v\d+\.go$`, SizeMin: -1, SizeMax: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("regex: got %d hits (%+v)", len(hits), hits)
	}
}
