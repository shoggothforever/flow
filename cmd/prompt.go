package cmd

// Tiny tty prompt used by `flow cmd run` (and friends) to ask for missing
// template placeholders interactively. Stays in cooked mode — no raw-mode
// gymnastics — so the user gets the usual line-editing keys for free.
//
// We open /dev/tty directly so prompts work even when stdin/stdout are
// piped (mirrors what the picker does).

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/yithcai/flow/internal/errs"
)

// promptValues asks for each key in `keys` (in order). For keys that have
// an entry in `defaults`, the default is shown in `[...]` and accepted on
// an empty line. Returns the (possibly empty) collected values.
//
// Returns errs.User("cancelled", "") when the user closes stdin (Ctrl-D)
// or the controlling terminal is unavailable.
func promptValues(prompt string, keys []string, defaults map[string]string) (map[string]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errs.User(
			"can't prompt for missing values: /dev/tty unavailable",
			"pass them as key=value, e.g.  "+keys[0]+"=...",
		)
	}
	defer tty.Close()

	if prompt != "" {
		fmt.Fprintln(tty, prompt)
	}
	out := make(map[string]string, len(keys))
	r := bufio.NewReader(tty)
	for _, k := range keys {
		def, hasDef := defaults[k]
		label := k
		if hasDef {
			label = fmt.Sprintf("%s [%s]", k, def)
		}
		fmt.Fprintf(tty, "  %s: ", label)
		line, err := r.ReadString('\n')
		if err != nil {
			// Ctrl-D / closed pipe -> treat as cancellation.
			fmt.Fprintln(tty)
			return nil, errs.User("cancelled", "")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" && hasDef {
			out[k] = def
			continue
		}
		out[k] = line
	}
	return out, nil
}
