package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/template"
)

func init() {
	cmdRoot := &cobra.Command{
		Use:   "cmd",
		Short: "Manage parametrised command aliases ({{key}} placeholders)",
	}

	// add ----------------------------------------------------------------
	var addTemplate, addCwd string
	var addDefaults, addPositional []string
	addCmd := &cobra.Command{
		Use:   "add <alias>",
		Short: "Register a parametrised command",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			alias := args[0]
			if strings.TrimSpace(addTemplate) == "" {
				return errs.User("--template is required",
					`example:  flow cmd add deploy --template "kubectl apply -f deploy/{{env:dev}}.yaml"`)
			}
			defaults, err := parseKVPairs(addDefaults)
			if err != nil {
				return errs.UserWrap(err, "invalid --default value", `expected key=value pairs`)
			}
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				if c.FindCmd(alias) != nil {
					return errs.User(
						fmt.Sprintf("cmd %q already exists", alias),
						"remove it first:  flow cmd rm "+alias,
					)
				}
				c.Cmds = append(c.Cmds, config.CmdAlias{
					Alias:      alias,
					Template:   addTemplate,
					Defaults:   defaults,
					Positional: addPositional,
					Cwd:        addCwd,
				})
				fmt.Printf("Added cmd %s\n", alias)
				return nil
			})
		},
	}
	addCmd.Flags().StringVar(&addTemplate, "template", "", "command template; supports {{key}} and {{key:default}}")
	addCmd.Flags().StringVar(&addCwd, "cwd", "", "cwd: a path or @project-alias")
	addCmd.Flags().StringSliceVar(&addDefaults, "default", nil, "key=value default; repeatable")
	addCmd.Flags().StringSliceVar(&addPositional, "positional", nil,
		"map positional argv to placeholders, in order; repeatable. "+
			"e.g. --positional port  lets you write  flow kill-port 8080  instead of  port=8080")

	// list ---------------------------------------------------------------
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all cmd aliases",
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
			if len(c.Cmds) == 0 {
				fmt.Println("(no cmds yet -- add one with: flow cmd add ...)")
				return nil
			}
			rows := make([][]string, 0, len(c.Cmds))
			for _, x := range c.Cmds {
				rows = append(rows, []string{x.Alias, x.Cwd, x.Template})
			}
			printTable([]string{"ALIAS", "CWD", "TEMPLATE"}, rows)
			return nil
		},
	}

	// rm -----------------------------------------------------------------
	rmCmd := &cobra.Command{
		Use:   "rm <alias>",
		Short: "Remove a cmd alias",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				for i, x := range c.Cmds {
					if x.Alias == args[0] {
						c.Cmds = append(c.Cmds[:i], c.Cmds[i+1:]...)
						fmt.Printf("Removed cmd %s\n", args[0])
						return nil
					}
				}
				return errs.User(
					fmt.Sprintf("no cmd named %q", args[0]),
					"see available cmds:  flow cmd list",
				)
			})
		},
	}

	// run ----------------------------------------------------------------
	var runDryRun bool
	runCmd := &cobra.Command{
		Use:   "run [<alias>] [positional...] [key=value ...]",
		Short: "Render a cmd alias and run it (omit <alias> to pick interactively)",
		Long: `Three ways to pass values, in increasing precedence:

  1. defaults baked into the cmd (--default at add time)
  2. positional arguments mapped via the cmd's --positional list
  3. explicit key=value pairs

If any required placeholder is still empty, you'll be prompted on the tty
(one line per key, default in [brackets], blank line accepts the default).`,
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

			// Split args into bare positional tokens and key=value pairs.
			// The first bare token is the alias (unless we have to pick).
			var bare, kvArgs []string
			for _, a := range args {
				if strings.Contains(a, "=") {
					kvArgs = append(kvArgs, a)
				} else {
					bare = append(bare, a)
				}
			}

			var alias string
			if len(bare) > 0 {
				alias = bare[0]
				bare = bare[1:]
			} else {
				alias, err = pickFromConfig(nil, cmdItems(cfg), "cmd",
					`register one:  flow cmd add <alias> --template "..."`)
				if err != nil {
					return err
				}
			}

			cm, err := resolveCmd(cfg, alias)
			if err != nil {
				return err
			}

			// Build the merged value map: defaults < positional < explicit kv.
			merged := map[string]string{}
			for k, v := range cm.Defaults {
				merged[k] = v
			}
			for i, val := range bare {
				if i >= len(cm.Positional) {
					return errs.User(
						fmt.Sprintf("cmd %q only accepts %d positional argument(s); got extra %q",
							cm.Alias, len(cm.Positional), val),
						"either pass it as key=value, or extend the cmd:  flow cmd add ... --positional KEY",
					)
				}
				merged[cm.Positional[i]] = val
			}
			explicit, err := parseKVPairs(kvArgs)
			if err != nil {
				return errs.UserWrap(err, "invalid argument",
					`pass arguments as key=value pairs, e.g.  env=prod region=us`)
			}
			for k, v := range explicit {
				merged[k] = v
			}

			// First render: catches missing keys. If any are missing AND we
			// have a tty, prompt for them and re-render once.
			rendered, rErr := template.Render(cm.Template, merged)
			if m, ok := template.IsMissing(rErr); ok {
				prompted, perr := promptValues(
					fmt.Sprintf("cmd %q needs values:", cm.Alias),
					m.Keys, cm.Defaults)
				if perr != nil {
					// promptValues already returns a User error with hint.
					return perr
				}
				for k, v := range prompted {
					merged[k] = v
				}
				rendered, rErr = template.Render(cm.Template, merged)
			}
			if rErr != nil {
				if m, ok := template.IsMissing(rErr); ok {
					return errs.User(
						fmt.Sprintf("missing values for: %s", strings.Join(m.Keys, ", ")),
						"pass them as key=value, e.g.  flow cmd run "+alias+" "+m.Keys[0]+"=...",
					)
				}
				return errs.UserWrap(rErr, "could not render command template", "check the {{...}} placeholders in the saved template")
			}
			cwd, err := expandCwd(cfg, cm.Cwd)
			if err != nil {
				return err
			}
			if runDryRun {
				fmt.Printf("would run: /bin/sh -c %q\n", rendered)
				if cwd != "" {
					fmt.Printf("       in: %s\n", cwd)
				}
				return nil
			}
			cmd := exec.Command("/bin/sh", "-c", rendered)
			if cwd != "" {
				cmd.Dir = cwd
			}
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					return errs.System(fmt.Sprintf("cmd %s exited with code %d", alias, ee.ExitCode()), err)
				}
				return errs.System("cmd failed to start", err)
			}
			return nil
		},
	}
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "print the resolved command without executing")

	cmdRoot.AddCommand(addCmd, listCmd, rmCmd, runCmd)
	rootCmd.AddCommand(cmdRoot)
}

// parseKVPairs accepts ["key=value", ...] and returns a map. Empty input is OK.
func parseKVPairs(in []string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for _, s := range in {
		i := strings.Index(s, "=")
		if i <= 0 {
			return nil, fmt.Errorf("not a key=value pair: %q", s)
		}
		out[s[:i]] = s[i+1:]
	}
	return out, nil
}
