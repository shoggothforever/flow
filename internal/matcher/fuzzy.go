// Package matcher provides simple substring/prefix matching used to resolve
// short user-typed names to canonical aliases. fzf integration is reserved for
// a later milestone (interactive selection from multiple matches).
package matcher

import (
	"sort"
	"strings"
)

// Match represents one matched candidate with a relative score (lower is better).
type Match struct {
	Name  string
	Score int
}

// Find returns matches for query among candidates.
//
// Scoring (lower wins):
//
//	0: exact match
//	1: prefix match
//	2: substring match
//	3: case-insensitive substring match
//
// Empty query returns all candidates (score 0).
func Find(query string, candidates []string) []Match {
	if query == "" {
		out := make([]Match, len(candidates))
		for i, c := range candidates {
			out[i] = Match{Name: c, Score: 0}
		}
		return out
	}
	q := strings.ToLower(query)
	var out []Match
	for _, c := range candidates {
		lc := strings.ToLower(c)
		switch {
		case c == query:
			out = append(out, Match{c, 0})
		case strings.HasPrefix(c, query):
			out = append(out, Match{c, 1})
		case strings.HasPrefix(lc, q):
			out = append(out, Match{c, 1})
		case strings.Contains(c, query):
			out = append(out, Match{c, 2})
		case strings.Contains(lc, q):
			out = append(out, Match{c, 3})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Resolve returns the single best match for query, or:
//
//	("", false, nil) when there are no matches
//	("", false, ambiguous) when multiple matches share the best score
//	(name, true, nil) on success (exact match always wins outright)
func Resolve(query string, candidates []string) (string, bool, []string) {
	matches := Find(query, candidates)
	if len(matches) == 0 {
		return "", false, nil
	}
	if matches[0].Score == 0 {
		return matches[0].Name, true, nil
	}
	if len(matches) == 1 {
		return matches[0].Name, true, nil
	}
	// Multiple results -- ambiguous unless the best score is strictly better than the second.
	if matches[0].Score < matches[1].Score {
		return matches[0].Name, true, nil
	}
	tied := []string{matches[0].Name}
	for _, m := range matches[1:] {
		if m.Score == matches[0].Score {
			tied = append(tied, m.Name)
		}
	}
	return "", false, tied
}
