package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/picker"
)

// runEntry is one row in the unified picker shown by `flow run`.
// kind is one of: "script" / "cmd" / "grep" / "find" / "jump".
type runEntry struct {
	Kind, Name, Detail string
}

func init() {
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Pick something to do (scripts, cmds, greps, finds, jumps)",
		Long: `Show a single fuzzy-filtered picker covering every runnable
resource in your config, plus 'jump' targets. Select one and flow
dispatches to the right command for you.

Use this when you don't remember which kind of thing you want to run --
just type a few characters and arrow down.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			entries := collectRunEntries(c)
			if len(entries) == 0 {
				return errs.User(
					"nothing to run yet",
					"register a project/script/cmd first, e.g.  flow project add ... && flow script add ...",
				)
			}
			items := make([]picker.Item, len(entries))
			for i, e := range entries {
				items[i] = picker.Item{
					Label:  fmt.Sprintf("%-7s %s", "["+e.Kind+"]", e.Name),
					Detail: e.Detail,
					Match:  e.Kind + " " + e.Name + " " + e.Detail,
				}
			}
			idx, err := picker.Pick("Pick something to run", items)
			if err != nil {
				if errors.Is(err, picker.ErrCancelled) {
					return errs.User("cancelled", "")
				}
				if errors.Is(err, picker.ErrNoTTY) {
					return errs.User(
						"flow run is interactive and requires a tty",
						"use the kind-specific command, e.g.  flow script run <alias>",
					)
				}
				return errs.UserWrap(err, "picker failed", "")
			}
			return dispatchRun(entries[idx])
		},
	}
	rootCmd.AddCommand(runCmd)
}

func collectRunEntries(c *config.Config) []runEntry {
	var out []runEntry
	for _, s := range c.Scripts {
		detail := s.Command
		if s.Cwd != "" {
			detail = "[" + s.Cwd + "] " + detail
		}
		out = append(out, runEntry{"script", s.Alias, detail})
	}
	for _, x := range c.Cmds {
		out = append(out, runEntry{"cmd", x.Alias, x.Template})
	}
	for _, g := range c.Greps {
		out = append(out, runEntry{"grep", g.Name, g.Pattern})
	}
	for _, f := range c.Finds {
		summary := strings.TrimSpace(f.NamePattern + " " + f.Regex + " " + f.ModifiedWithin)
		out = append(out, runEntry{"find", f.Name, summary})
	}
	for _, p := range c.Projects {
		out = append(out, runEntry{"jump", p.Alias, p.Path})
	}
	return out
}

// dispatchRun re-executes flow with the right subcommand. Re-execing keeps
// the picker code path independent of each subcommand's RunE: any future
// flag/feature added to e.g. `script run` is automatically picked up.
func dispatchRun(e runEntry) error {
	self, err := os.Executable()
	if err != nil {
		return errs.System("could not locate flow binary", err)
	}
	var args []string
	switch e.Kind {
	case "script":
		args = []string{"script", "run", e.Name}
	case "cmd":
		args = []string{"cmd", "run", e.Name}
	case "grep":
		args = []string{"grep", "run", e.Name}
	case "find":
		args = []string{"find", "run", e.Name}
	case "jump":
		args = []string{"jump", e.Name}
	default:
		return errs.System("unknown run kind: "+e.Kind, nil)
	}
	if flagConfigPath != "" {
		args = append([]string{"--config", flagConfigPath}, args...)
	}
	cmd := exec.Command(self, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return errs.System("dispatch failed", err)
	}
	return nil
}
