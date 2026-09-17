package search

import (
	"reflect"
	"testing"
)

func TestBuildArgsRipgrepLiteral(t *testing.T) {
	gotCmd, gotArgs, err := BuildArgs(BackendRipgrep, Spec{
		Pattern:    "TODO(name)",
		Paths:      []string{"."},
		Include:    []string{"*.go"},
		Exclude:    []string{"vendor/*"},
		IgnoreCase: true,
		Word:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotCmd != "rg" {
		t.Fatalf("cmd=%s", gotCmd)
	}
	want := []string{"-F", "-w", "-i", "-g", "*.go", "-g", "!vendor/*", "--", "TODO(name)", "."}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("\n got: %v\nwant: %v", gotArgs, want)
	}
}

func TestBuildArgsGrepRegex(t *testing.T) {
	gotCmd, gotArgs, err := BuildArgs(BackendGrep, Spec{
		Pattern: "foo|bar",
		Regex:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotCmd != "grep" {
		t.Fatalf("cmd=%s", gotCmd)
	}
	want := []string{"-r", "-E", "--", "foo|bar", "."}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("\n got: %v\nwant: %v", gotArgs, want)
	}
}

func TestBuildArgsEmptyPattern(t *testing.T) {
	if _, _, err := BuildArgs(BackendRipgrep, Spec{}); err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestBuildArgsNoBackend(t *testing.T) {
	if _, _, err := BuildArgs(BackendNone, Spec{Pattern: "x"}); err == nil {
		t.Fatal("expected error for no backend")
	}
}
