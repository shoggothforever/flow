// Package finder locates files under one or more roots, filtered by name
// glob/regex, file type, extension, mtime window and size, sorted and
// truncated. It is a small replacement for `find` that keeps the same
// semantics across platforms and is easy to feed from saved presets.
package finder

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind narrows which directory entries are kept.
type Kind string

const (
	KindAny     Kind = ""  // default: file + dir + symlink
	KindFile    Kind = "f"
	KindDir     Kind = "d"
	KindSymlink Kind = "l"
)

// SortKey controls how matches are ordered before truncation.
type SortKey string

const (
	SortMTime SortKey = "mtime"
	SortName  SortKey = "name"
	SortSize  SortKey = "size"
)

// Query is the structural input. All fields are optional except Roots.
//
// Glob patterns use Go's filepath.Match semantics.
// Time windows are inclusive on both ends; an unset After/Before means open.
// SizeMin/SizeMax of -1 mean unset.
type Query struct {
	Roots []string

	// Filename matchers (combined: must match Name AND Regex if both set).
	Name  string // glob, e.g. "*_test.go"
	Regex string // RE2, matched against the file's basename

	// File-type filters
	Kind Kind     // KindAny by default
	Exts []string // ".go", ".rs"; matched case-insensitively if all-lowercase

	// Time window on mtime
	After  time.Time
	Before time.Time

	// Size in bytes; -1 means unset
	SizeMin int64
	SizeMax int64

	// Excluded path globs, matched against the path **and** the basename.
	// Common defaults like ".git", "node_modules" are added unless Hidden=true.
	Exclude []string
	Hidden  bool

	// Output shaping
	Sort    SortKey
	Reverse bool
	Limit   int
}

// Hit is one filesystem entry returned by Find.
type Hit struct {
	Path  string    // absolute or root-relative depending on input root
	Name  string    // basename
	Size  int64     // bytes (0 for dirs/symlinks unless resolved)
	MTime time.Time // last modification time
	IsDir bool
	Kind  Kind // resolved Kind: f / d / l
}

// Default excludes applied when Hidden=false.
var defaultExcludes = []string{".git", "node_modules", ".cache", ".idea", ".vscode"}

// ParseDuration accepts simple suffixes: s/m/h/d/w (e.g. "30m", "24h", "7d", "2w").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// Try Go's native parser first (handles ns/us/ms/s/m/h).
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	// Then handle d / w which Go doesn't support.
	last := s[len(s)-1]
	if last != 'd' && last != 'D' && last != 'w' && last != 'W' {
		return 0, fmt.Errorf("unrecognised duration %q (use 30m, 24h, 7d, 2w...)", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	unit := 24 * time.Hour
	if last == 'w' || last == 'W' {
		unit = 7 * 24 * time.Hour
	}
	return time.Duration(n) * unit, nil
}

// ParseDate parses YYYY-MM-DD (UTC midnight). Returns zero on empty input.
func ParseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q (expected YYYY-MM-DD)", s)
	}
	return t, nil
}

// ParseSize accepts +N / -N / N with K/M/G suffixes. +N means at least N, -N at most N.
// Returns (min, max). Either bound may be -1 if unset by the input.
func ParseSize(s string) (min, max int64, err error) {
	min, max = -1, -1
	s = strings.TrimSpace(s)
	if s == "" {
		return min, max, nil
	}
	op := byte('=')
	if s[0] == '+' || s[0] == '-' {
		op = s[0]
		s = s[1:]
	}
	if s == "" {
		return -1, -1, fmt.Errorf("invalid size (empty)")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K', 'k':
		mult = 1 << 10
		s = s[:len(s)-1]
	case 'M', 'm':
		mult = 1 << 20
		s = s[:len(s)-1]
	case 'G', 'g':
		mult = 1 << 30
		s = s[:len(s)-1]
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 {
		return -1, -1, fmt.Errorf("invalid size value %q", s)
	}
	bytes := n * mult
	switch op {
	case '+':
		return bytes, -1, nil
	case '-':
		return -1, bytes, nil
	default:
		return bytes, bytes, nil
	}
}

// Find walks q.Roots and returns all hits matching q.
//
// Errors during walk on individual entries (permission denied etc.) are
// silently skipped: the caller wants results, not a hard fail. The first
// fatal error (no roots, bad regex) is returned.
func Find(q Query) ([]Hit, error) {
	if len(q.Roots) == 0 {
		return nil, fmt.Errorf("no roots provided")
	}
	if q.SizeMin == 0 && q.SizeMax == 0 {
		// preserve "unset" semantics: caller may have left zero-values intact
		q.SizeMin, q.SizeMax = -1, -1
	}

	var nameRe *regexp.Regexp
	if q.Regex != "" {
		re, err := regexp.Compile(q.Regex)
		if err != nil {
			return nil, fmt.Errorf("invalid --regex: %w", err)
		}
		nameRe = re
	}

	excludes := append([]string{}, q.Exclude...)
	if !q.Hidden {
		excludes = append(excludes, defaultExcludes...)
	}

	out := make([]Hit, 0, 64)
	for _, root := range q.Roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			base := d.Name()

			// Excludes: match against basename and path; skip dirs entirely on hit.
			if matchesAny(excludes, base) || matchesAny(excludes, path) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}

			// Hidden default: skip dotfiles unless --hidden.
			if !q.Hidden && len(base) > 1 && base[0] == '.' {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}

			// Don't include the root itself.
			if path == root {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			kind := classify(info)
			if !kindMatches(q.Kind, kind) {
				return nil
			}

			if q.Name != "" {
				ok, _ := filepath.Match(q.Name, base)
				if !ok {
					return nil
				}
			}
			if nameRe != nil && !nameRe.MatchString(base) {
				return nil
			}
			if !extMatches(q.Exts, base) {
				return nil
			}
			if !q.After.IsZero() && info.ModTime().Before(q.After) {
				return nil
			}
			if !q.Before.IsZero() && info.ModTime().After(q.Before) {
				return nil
			}
			if !sizeMatches(q.SizeMin, q.SizeMax, info.Size(), kind) {
				return nil
			}

			out = append(out, Hit{
				Path:  path,
				Name:  base,
				Size:  info.Size(),
				MTime: info.ModTime(),
				IsDir: kind == KindDir,
				Kind:  kind,
			})
			return nil
		})
	}

	sortHits(out, q.Sort, q.Reverse)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func classify(info os.FileInfo) Kind {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return KindSymlink
	case info.IsDir():
		return KindDir
	default:
		return KindFile
	}
}

func kindMatches(want, got Kind) bool {
	if want == KindAny {
		return true
	}
	return want == got
}

func extMatches(exts []string, base string) bool {
	if len(exts) == 0 {
		return true
	}
	want := strings.ToLower(filepath.Ext(base))
	for _, e := range exts {
		e = strings.ToLower(e)
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if e == want {
			return true
		}
	}
	return false
}

func sizeMatches(min, max, size int64, kind Kind) bool {
	if min < 0 && max < 0 {
		return true
	}
	if kind != KindFile {
		// size filters only meaningful for files; drop non-files.
		return false
	}
	if min >= 0 && size < min {
		return false
	}
	if max >= 0 && size > max {
		return false
	}
	return true
}

func matchesAny(patterns []string, s string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		// Plain substring/dir-name match for the common case.
		if strings.Contains(s, string(filepath.Separator)+p+string(filepath.Separator)) {
			return true
		}
		if filepath.Base(s) == p {
			return true
		}
		if ok, _ := filepath.Match(p, filepath.Base(s)); ok {
			return true
		}
	}
	return false
}

func sortHits(hits []Hit, key SortKey, reverse bool) {
	less := func(i, j int) bool {
		switch key {
		case SortName:
			return hits[i].Path < hits[j].Path
		case SortSize:
			return hits[i].Size < hits[j].Size
		default: // SortMTime, default = newest first
			return hits[i].MTime.After(hits[j].MTime)
		}
	}
	if reverse {
		orig := less
		less = func(i, j int) bool { return !orig(i, j) && !(hits[i] == hits[j]) }
	}
	sort.SliceStable(hits, less)
}
