package picker

import (
	"reflect"
	"testing"
)

func TestFuzzyScoreSubstring(t *testing.T) {
	if _, ok := fuzzyScore("hello world", "world"); !ok {
		t.Fatal("substring should match")
	}
	if _, ok := fuzzyScore("hello world", "wor"); !ok {
		t.Fatal("partial substring should match")
	}
	if _, ok := fuzzyScore("hello world", "xyz"); ok {
		t.Fatal("non-existent should not match")
	}
}

func TestFuzzyScoreSubsequence(t *testing.T) {
	// "hlo" appears as a subsequence of "hello"
	if _, ok := fuzzyScore("hello world", "hlo"); !ok {
		t.Fatal("subsequence should match")
	}
	// substring beats subsequence
	subS, _ := fuzzyScore("hello world", "ello")
	subQ, _ := fuzzyScore("hello world", "elo")
	if subS >= subQ {
		t.Fatalf("substring score %d should be lower than subsequence %d", subS, subQ)
	}
}

func TestFilterOrder(t *testing.T) {
	items := []Item{
		{Label: "alpha"},
		{Label: "beta"},
		{Label: "alphabet"},
		{Label: "gamma"},
	}
	got := filter(items, "alp")
	want := []int{0, 2} // both contain "alp"; "alpha" is shorter / earlier match
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filter order: got %v want %v", got, want)
	}

	// Empty query => identity
	got = filter(items, "")
	if !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("empty query order: %v", got)
	}
}

func TestFilterRespectsMatchOverride(t *testing.T) {
	items := []Item{
		{Label: "x", Match: "build the world"},
		{Label: "y", Match: "deploy"},
	}
	got := filter(items, "world")
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("expected 1 hit on Match override, got %v", got)
	}
}
