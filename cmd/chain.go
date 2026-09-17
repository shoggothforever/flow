package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/picker"
)

func init() {
	var chainKeepGoing, chainDryRun bool
	var chainSteps []string
	chainCmd := &cobra.Command{
		Use:     "chain [step ...]",
		Aliases: []string{"seq"},
		Short:   "Run an ad-hoc sequence of flow aliases/commands",
		Long: `Run multiple flow actions in order without saving a permanent script.

Each step is executed by re-invoking flow itself, so lazy dispatch works:
  flow chain restart_server build_cook
  flow chain 'kill-port 8080' restart_server
  flow chain -s 'kill-port 8080' -s 'tail-cxb --grep doors'

With no steps, chain opens an interactive picker. Pick items one by one, then
select "Run selected steps" to execute the sequence.

Simple aliases can be passed as plain positional steps. If a step needs its
own arguments or flags, quote the whole step or use --step/-s. By default the
chain stops at the first failed step; pass --keep-going to continue.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			steps := append([]string{}, chainSteps...)
			steps = append(steps, args...)
			if len(steps) == 0 {
				picked, err := pickChainSteps()
				if err != nil {
					return err
				}
				steps = picked
			}
			return runChainSteps(steps, chainDryRun, chainKeepGoing)
		},
	}
	chainCmd.Flags().BoolVar(&chainKeepGoing, "keep-going", false, "continue after a failed step")
	chainCmd.Flags().BoolVar(&chainDryRun, "dry-run", false, "print the resolved child flow commands without executing")
	chainCmd.Flags().StringArrayVarP(&chainSteps, "step", "s", nil, "one full flow command step; repeatable (quote if it has args/flags)")
	rootCmd.AddCommand(chainCmd)
}

func pickChainSteps() ([]string, error) {
	s, err := loadStore()
	if err != nil {
		return nil, err
	}
	cfg, err := s.Load()
	if err != nil {
		return nil, err
	}
	entries := collectRunEntries(cfg)
	selectable := make([]runEntry, 0, len(entries))
	for _, e := range entries {
		// `jump` changes the child process' cwd only, so it is not useful in a chain.
		if e.Kind == "jump" {
			continue
		}
		selectable = append(selectable, e)
	}
	if len(selectable) == 0 {
		return nil, errs.User(
			"nothing chainable yet",
			"register a script/cmd/grep/find first, e.g.  flow script add ...",
		)
	}

	var steps []string
	for {
		items := []picker.Item{}
		if len(steps) > 0 {
			items = append(items, picker.Item{
				Label:  fmt.Sprintf("[run] %d selected step(s)", len(steps)),
				Detail: strings.Join(steps, "  →  "),
				Match:  "run done finish execute selected",
			})
		}
		items = append(items, picker.Item{Label: "[cancel]", Detail: "abort chain", Match: "cancel abort quit"})
		for _, e := range selectable {
			items = append(items, picker.Item{
				Label:  fmt.Sprintf("%-7s %s", "["+e.Kind+"]", e.Name),
				Detail: e.Detail,
				Match:  e.Kind + " " + e.Name + " " + e.Detail,
			})
		}

		idx, err := picker.Pick(fmt.Sprintf("Build chain (%d selected)", len(steps)), items)
		if err != nil {
			if errors.Is(err, picker.ErrCancelled) {
				return nil, errs.User("cancelled", "")
			}
			if errors.Is(err, picker.ErrNoTTY) {
				return nil, errs.User(
					"flow chain is interactive when no steps are provided and requires a tty",
					`pass steps explicitly, e.g.  flow chain restart_server "tail-cxb --grep doors"`,
				)
			}
			return nil, errs.UserWrap(err, "picker failed", "")
		}

		if len(steps) > 0 && idx == 0 {
			return steps, nil
		}
		if (len(steps) > 0 && idx == 1) || (len(steps) == 0 && idx == 0) {
			return nil, errs.User("cancelled", "")
		}
		entryIdx := idx - 1
		if len(steps) > 0 {
			entryIdx = idx - 2
		}
		if entryIdx < 0 || entryIdx >= len(selectable) {
			return nil, errs.System("invalid picker selection", nil)
		}
		steps = append(steps, chainCommandFor(selectable[entryIdx]))
	}
}

func chainCommandFor(e runEntry) string {
	switch e.Kind {
	case "script":
		return "script run " + e.Name
	case "cmd":
		return "cmd run " + e.Name
	case "grep":
		return "grep run " + e.Name
	case "find":
		return "find run " + e.Name
	default:
		return e.Name
	}
}

func runChainSteps(steps []string, dryRun, keepGoing bool) error {
	if len(steps) == 0 {
		return errs.User(
			"chain requires at least one step",
			`example:  flow chain restart_server build_cook   or   flow chain -s "kill-port 8080" -s restart_server`,
		)
	}
	self, err := os.Executable()
	if err != nil {
		return errs.System("could not locate flow binary", err)
	}

	failed := 0
	for i, raw := range steps {
		parts := strings.Fields(strings.TrimSpace(raw))
		if len(parts) == 0 {
			continue
		}
		childArgs := append([]string{}, parts...)
		if flagConfigPath != "" {
			childArgs = append([]string{"--config", flagConfigPath}, childArgs...)
		}
		fmt.Printf("▶ [%d/%d] flow %s\n", i+1, len(steps), strings.Join(childArgs, " "))
		if dryRun {
			continue
		}
		cmd := exec.Command(self, childArgs...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			code := -1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			failed++
			if !keepGoing {
				return errs.System(fmt.Sprintf("chain stopped at step %d/%d (%s), exit code %d",
					i+1, len(steps), raw, code), err)
			}
			fmt.Fprintf(os.Stderr, "step failed but continuing: %s (exit code %d)\n", raw, code)
		}
	}
	if failed > 0 {
		return errs.System(fmt.Sprintf("chain completed with %d failed step(s)", failed), nil)
	}
	return nil
}
