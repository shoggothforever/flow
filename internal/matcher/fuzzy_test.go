package matcher

import (
	"reflect"
	"testing"
)

func TestResolveExact(t *testing.T) {
	got, ok, _ := Resolve("oev", []string{"oev", "oeve", "rev"})
	if !ok || got != "oev" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestResolvePrefix(t *testing.T) {
	got, ok, _ := Resolve("cu", []string{"oev", "cube", "cubic"})
	if ok {
		t.Fatalf("expected ambiguity but got %q", got)
	}
}

func TestResolveSingleSubstring(t *testing.T) {
	got, ok, _ := Resolve("ub", []string{"oev", "cube"})
	if !ok || got != "cube" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestResolveAmbiguousReturnsTied(t *testing.T) {
	// Two equal-prefix matches -- ambiguous.
	_, ok, tied := Resolve("a", []string{"abba", "abacus"})
	if ok {
		t.Fatalf("expected ambiguous, got tied=%v", tied)
	}
	if !reflect.DeepEqual(tied, []string{"abacus", "abba"}) {
		t.Fatalf("tied=%v", tied)
	}
}

func TestFindEmpty(t *testing.T) {
	got := Find("", []string{"a", "b"})
	if len(got) != 2 {
		t.Fatalf("want 2 matches got %d", len(got))
	}
}
