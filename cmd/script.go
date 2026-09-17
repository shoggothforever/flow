package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
)

func init() {
	scriptCmd := &cobra.Command{
		Use:   "script",
		Short: "Manage and run named scripts",
	}

	// add ----------------------------------------------------------------
	var addCommand, addCwd string
	var addArgs, addEnv []string
	addCmd := &cobra.Command{
		Use:   "add <alias>",
		Short: "Register a named script",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			alias := args[0]
			if strings.TrimSpace(addCommand) == "" {
				return errs.User("--command is required",
					`example:  flow script add build --command "make all" --cwd @cube`)
			}
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				if c.FindScript(alias) != nil {
					return errs.User(
						fmt.Sprintf("script %q already exists", alias),
						"remove it first:  flow script rm "+alias,
					)
				}
				c.Scripts = append(c.Scripts, config.Script{
					Alias:   alias,
					Cwd:     addCwd,
					Command: addCommand,
					Args:    addArgs,
					Env:     addEnv,
				})
				fmt.Printf("Added script %s\n", alias)
				return nil
			})
		},
	}
	addCmd.Flags().StringVar(&addCommand, "command", "", "shell command to run (will be invoked via /bin/sh -c)")
	addCmd.Flags().StringVar(&addCwd, "cwd", "", "cwd: a path or @project-alias")
	addCmd.Flags().StringSliceVar(&addArgs, "arg", nil, "extra arg appended verbatim; repeatable")
	addCmd.Flags().StringSliceVar(&addEnv, "env", nil, "KEY=VAL; repeatable")

	// list ---------------------------------------------------------------
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all scripts",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			if len(c.Scripts) == 0 {
				fmt.Println("(no scripts yet -- add one with: flow script add ...)")
				return nil
			}
			rows := make([][]string, 0, len(c.Scripts))
			for _, x := range c.Scripts {
				rows = append(rows, []string{x.Alias, x.Cwd, x.Command})
			}
			printTable([]string{"ALIAS", "CWD", "COMMAND"}, rows)
			return nil
		},
	}

	// rm -----------------------------------------------------------------
	rmCmd := &cobra.Command{
		Use:   "rm <alias>",
		Short: "Remove a script",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				for i, x := range c.Scripts {
					if x.Alias == args[0] {
						c.Scripts = append(c.Scripts[:i], c.Scripts[i+1:]...)
						fmt.Printf("Removed script %s\n", args[0])
						return nil
					}
				}
				return errs.User(
					fmt.Sprintf("no script named %q", args[0]),
					"see available scripts:  flow script list",
				)
			})
		},
	}

	// run ----------------------------------------------------------------
	var runDryRun bool
	var runGrep []string
	runCmd := &cobra.Command{
		Use:   "run [<alias>] [-- extra-args...]",
		Short: "Run a registered script (omit <alias> to pick interactively)",
		Long: `Pipe through saved grep presets with --grep:

  flow script run my-tail --grep phase
  flow script run my-tail --grep phase,doors-open
  flow script run build --grep error,warn

Multiple presets are OR-combined. Use 'flow grep list' to see saved presets.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			cfg, err := s.Load()
			if err != nil {
				return err
			}
			alias, err := pickFromConfig(args, scriptItems(cfg), "script",
				`register one:  flow script add <alias> --command "..."`)
			if err != nil {
				return err
			}
			var extra []string
			if len(args) > 1 {
				extra = args[1:]
			}
			sc, err := resolveScript(cfg, alias)
			if err != nil {
				return err
			}
			cwd, err := expandCwd(cfg, sc.Cwd)
			if err != nil {
				return err
			}
			full := sc.Command
			if len(sc.Args) > 0 {
				full += " " + strings.Join(sc.Args, " ")
			}
			if len(extra) > 0 {
				full += " " + strings.Join(extra, " ")
			}
			// Apply --grep filter, if any.
			if len(runGrep) > 0 {
				pat, err := buildCombinedPattern(cfg, runGrep)
				if err != nil {
					return err
				}
				// `grep -E` exits 1 when there's no match -- don't let
				// that poison the script's exit code. We surface real
				// failures of the underlying script via PIPESTATUS.
				//
				// `--color=always` is important: grep auto-disables
				// colour when its stdout is a pipe, but here the pipe
				// just feeds *our* stdout, which is a tty. Forcing
				// colour keeps matches highlighted in the terminal.
				escaped := strings.ReplaceAll(pat, `'`, `'\''`)
				full = "{ " + full + "; } | grep --line-buffered --color=always -E '" + escaped +
					"'; rc=${PIPESTATUS[0]}; exit $rc"
			}
			if runDryRun {
				fmt.Printf("would run: /bin/bash -c %q\n", full)
				if cwd != "" {
					fmt.Printf("       in: %s\n", cwd)
				}
				return nil
			}
			// Use bash so PIPESTATUS works when --grep is set; for plain
			// scripts (no pipe) /bin/bash is still a fine drop-in for sh.
			cmd := exec.Command("/bin/bash", "-c", full)
			if cwd != "" {
				cmd.Dir = cwd
			}
			cmd.Env = append(os.Environ(), sc.Env...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					return errs.System(fmt.Sprintf("script %s exited with code %d", sc.Alias, ee.ExitCode()), err)
				}
				return errs.System("script failed to start", err)
			}
			return nil
		},
	}
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "print the resolved command without executing")
	runCmd.Flags().StringSliceVar(&runGrep, "grep", nil, "pipe through one or more saved grep presets (repeatable, comma-separated OK)")

	scriptCmd.AddCommand(addCmd, listCmd, rmCmd, runCmd)
	rootCmd.AddCommand(scriptCmd)
}
