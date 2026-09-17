package cmd

import (
	"errors"
	"fmt"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/picker"
)

// pickFromConfig is the shared "if the user didn't pass <name>, ask
// interactively" helper used by all the run/show/rm-style commands.
//
// items: list of (label, detail) tuples derived from the config.
// kind:  human-readable resource name used in error/prompt strings.
// usageHint: command line example to suggest when not in a tty.
//
// Returns the chosen label, or a User error with a helpful hint when the
// picker is unusable without an explicit argument or the user presses Esc.
func pickFromConfig(args []string, items []picker.Item, kind, addHint string) (string, error) {
	return pickFromConfigWithUsage(args, items, kind, addHint, "flow "+kind+" run <name>")
}

// pickFromConfigWithUsage lets callers override the "how to invoke me from
// scripts" example shown when stdin is not a tty.
func pickFromConfigWithUsage(args []string, items []picker.Item, kind, addHint, usage string) (string, error) {
	if len(args) >= 1 && args[0] != "" {
		return args[0], nil
	}
	if len(items) == 0 {
		return "", errs.User(
			fmt.Sprintf("no %s registered yet", kind),
			addHint,
		)
	}
	idx, err := picker.Pick(fmt.Sprintf("Select a %s", kind), items)
	if err != nil {
		if errors.Is(err, picker.ErrCancelled) {
			return "", errs.User("cancelled", "")
		}
		if errors.Is(err, picker.ErrNoTTY) {
			return "", errs.User(
				fmt.Sprintf("no %s name given and stdin is not a tty", kind),
				"pass the name explicitly, e.g.  "+usage,
			)
		}
		return "", errs.UserWrap(err, "picker failed", "")
	}
	return items[idx].Label, nil
}

// item builders -----------------------------------------------------------

func projectItems(c *config.Config) []picker.Item {
	out := make([]picker.Item, 0, len(c.Projects))
	for _, p := range c.Projects {
		out = append(out, picker.Item{Label: p.Alias, Detail: p.Path})
	}
	return out
}

func scriptItems(c *config.Config) []picker.Item {
	out := make([]picker.Item, 0, len(c.Scripts))
	for _, s := range c.Scripts {
		detail := s.Command
		if s.Cwd != "" {
			detail = "[" + s.Cwd + "]  " + detail
		}
		out = append(out, picker.Item{Label: s.Alias, Detail: detail})
	}
	return out
}

func cmdItems(c *config.Config) []picker.Item {
	out := make([]picker.Item, 0, len(c.Cmds))
	for _, x := range c.Cmds {
		out = append(out, picker.Item{Label: x.Alias, Detail: x.Template})
	}
	return out
}

func grepItems(c *config.Config) []picker.Item {
	out := make([]picker.Item, 0, len(c.Greps))
	for _, g := range c.Greps {
		out = append(out, picker.Item{Label: g.Name, Detail: g.Pattern})
	}
	return out
}

func findItems(c *config.Config) []picker.Item {
	out := make([]picker.Item, 0, len(c.Finds))
	for _, f := range c.Finds {
		summary := ""
		if f.NamePattern != "" {
			summary = f.NamePattern
		} else if f.Regex != "" {
			summary = "re:" + f.Regex
		}
		if f.ModifiedWithin != "" {
			summary += "  <" + f.ModifiedWithin
		}
		out = append(out, picker.Item{Label: f.Name, Detail: summary})
	}
	return out
}
