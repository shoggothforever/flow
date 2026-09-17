// Package template implements a tiny placeholder renderer used by `flow cmd run`.
//
// Supported syntax:
//
//	{{key}}              -- value must be supplied (in vars or via Defaults)
//	{{key:default}}      -- if not supplied, the literal default after ":" is used
//
// Anything that doesn't match the {{...}} pattern is emitted verbatim. Unknown
// placeholders (no value, no default) are reported via the returned MissingError
// so the caller can produce an actionable user message.
package template

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MissingError lists the placeholder keys that were referenced without a value
// or default. Sorted, deduplicated.
type MissingError struct {
	Keys []string
}

func (m *MissingError) Error() string {
	return fmt.Sprintf("missing values for: %s", strings.Join(m.Keys, ", "))
}

// IsMissing reports whether err (or any wrapped error) is a MissingError.
func IsMissing(err error) (*MissingError, bool) {
	var m *MissingError
	if errors.As(err, &m) {
		return m, true
	}
	return nil, false
}

// Render substitutes placeholders in s using the supplied values. Defaults
// inside the template ({{key:default}}) are used when no value was provided.
func Render(s string, vars map[string]string) (string, error) {
	var b strings.Builder
	missing := map[string]struct{}{}

	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '{' && s[i+1] == '{' {
			end := strings.Index(s[i+2:], "}}")
			if end < 0 {
				return "", fmt.Errorf("template: unterminated placeholder starting at offset %d", i)
			}
			expr := s[i+2 : i+2+end]
			key, def, hasDef := splitKeyDefault(expr)
			key = strings.TrimSpace(key)
			if key == "" {
				return "", fmt.Errorf("template: empty placeholder at offset %d", i)
			}
			if v, ok := vars[key]; ok {
				b.WriteString(v)
			} else if hasDef {
				b.WriteString(def)
			} else {
				missing[key] = struct{}{}
				// Keep the placeholder visible in the output so the caller can show
				// a sensible "what would have run".
				b.WriteString("{{")
				b.WriteString(expr)
				b.WriteString("}}")
			}
			i += 2 + end + 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}

	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for k := range missing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return b.String(), &MissingError{Keys: keys}
	}
	return b.String(), nil
}

// Keys returns all placeholder keys referenced in s. Useful for `cmd show`.
func Keys(s string) []string {
	seen := map[string]struct{}{}
	var keys []string
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '{' && s[i+1] == '{' {
			end := strings.Index(s[i+2:], "}}")
			if end < 0 {
				break
			}
			k, _, _ := splitKeyDefault(s[i+2 : i+2+end])
			k = strings.TrimSpace(k)
			if k != "" {
				if _, ok := seen[k]; !ok {
					seen[k] = struct{}{}
					keys = append(keys, k)
				}
			}
			i += 2 + end + 2
			continue
		}
		i++
	}
	return keys
}

func splitKeyDefault(expr string) (key, def string, hasDef bool) {
	if i := strings.Index(expr, ":"); i >= 0 {
		return expr[:i], expr[i+1:], true
	}
	return expr, "", false
}
