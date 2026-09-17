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
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect/edit/validate the on-disk config",
	}

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print the resolved config path",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			fmt.Println(s.Path)
			return nil
		},
	}

	editCmd := &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $EDITOR (or $VISUAL, falling back to vi)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			// Make sure the file exists so the editor doesn't open a blank buffer.
			if _, statErr := os.Stat(s.Path); statErr != nil {
				if err := s.Mutate(func(c *config.Config) error { return nil }); err != nil {
					return err
				}
			}
			editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
			cmd := exec.Command(editor, s.Path)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return errs.System("editor failed", err)
			}
			// Re-validate after the user saved.
			c, err := s.Load()
			if err != nil {
				return errs.UserWrap(err, "config is no longer valid JSON",
					"undo recent changes or fix manually:  flow config path")
			}
			if err := c.Validate(); err != nil {
				return errs.UserWrap(err, "config validation failed",
					"fix the offending entry and run  flow config validate")
			}
			fmt.Println("config valid")
			return nil
		},
	}

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the config (structure + cross-references)",
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
			if err := c.Validate(); err != nil {
				return errs.UserWrap(err, "config is invalid",
					"edit it:  flow config edit")
			}
			warnings := crossRefCheck(c)
			if len(warnings) == 0 {
				fmt.Println("OK")
				return nil
			}
			fmt.Println("OK with warnings:")
			for _, w := range warnings {
				fmt.Println("  -", w)
			}
			return nil
		},
	}

	migrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Bring the config up to the current schema version",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				changed, err := config.Migrate(c)
				if err != nil {
					return err
				}
				if !changed {
					fmt.Println("config already at schema_version", c.SchemaVersion)
				} else {
					fmt.Println("migrated to schema_version", c.SchemaVersion)
				}
				return nil
			})
		},
	}

	configCmd.AddCommand(pathCmd, editCmd, validateCmd, migrateCmd, newConfigExportCmd(), newConfigImportCmd())
	rootCmd.AddCommand(configCmd)
}

// crossRefCheck flags references that point at unknown projects. We only emit
// warnings (not errors) so a partially-bootstrapped config remains usable.
func crossRefCheck(c *config.Config) []string {
	known := map[string]bool{}
	for _, p := range c.Projects {
		known[p.Alias] = true
	}
	var warns []string
	checkAlias := func(loc, raw string) {
		if !strings.HasPrefix(raw, "@") {
			return
		}
		alias := strings.TrimPrefix(raw, "@")
		if !known[alias] {
			warns = append(warns, fmt.Sprintf("%s: references unknown project @%s", loc, alias))
		}
	}
	for _, sc := range c.Scripts {
		checkAlias(fmt.Sprintf("script %s.cwd", sc.Alias), sc.Cwd)
	}
	for _, cm := range c.Cmds {
		checkAlias(fmt.Sprintf("cmd %s.cwd", cm.Alias), cm.Cwd)
	}
	for _, g := range c.Greps {
		for i, p := range g.Paths {
			checkAlias(fmt.Sprintf("grep %s.paths[%d]", g.Name, i), p)
		}
	}
	for _, f := range c.Finds {
		for i, p := range f.Paths {
			checkAlias(fmt.Sprintf("find %s.paths[%d]", f.Name, i), p)
		}
	}
	return warns
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
