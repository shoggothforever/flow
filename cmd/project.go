package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
)

func init() {
	projectCmd := &cobra.Command{
		Use:   "project",
		Short: "Manage project bookmarks",
	}

	// add ----------------------------------------------------------------
	var addTags []string
	addCmd := &cobra.Command{
		Use:   "add [<alias>] [<path>]",
		Short: "Register a project bookmark (defaults: alias=basename of path, path=cwd)",
		Long: `Examples:
  flow project add                       # alias = basename($PWD), path = $PWD
  flow project add cube                  # alias = cube,            path = $PWD
  flow project add cube /dsm/oev/Cube    # alias = cube,            path = /dsm/oev/Cube
  flow project add cube -                # alias = cube,            path = $PWD (explicit)
`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return errs.System("could not read current directory", err)
			}

			var alias, rawPath string
			switch len(args) {
			case 0:
				rawPath = cwd
				alias = filepath.Base(cwd)
			case 1:
				rawPath = cwd
				alias = args[0]
			case 2:
				alias = args[0]
				rawPath = args[1]
				if rawPath == "-" || rawPath == "." {
					rawPath = cwd
				}
			}

			if alias == "" || alias == "/" || alias == "." {
				return errs.User(
					fmt.Sprintf("could not derive a sensible alias from %q", cwd),
					"pass one explicitly:  flow project add <alias>",
				)
			}

			absPath, err := filepath.Abs(rawPath)
			if err != nil {
				return errs.UserWrap(err, "invalid path", "use an absolute path or one your shell can resolve")
			}
			if st, err := os.Stat(absPath); err != nil || !st.IsDir() {
				return errs.User(
					fmt.Sprintf("%s is not a directory", absPath),
					"pass an existing directory or omit <path> to use the current one",
				)
			}

			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				if c.FindProject(alias) != nil {
					return errs.User(
						fmt.Sprintf("project %q already exists", alias),
						"remove it first:  flow project rm "+alias+
							"   (or pick a different alias)",
					)
				}
				c.Projects = append(c.Projects, config.Project{
					Alias: alias,
					Path:  absPath,
					Tags:  addTags,
				})
				fmt.Printf("Added project %s -> %s\n", alias, absPath)
				return nil
			})
		},
	}
	addCmd.Flags().StringSliceVar(&addTags, "tag", nil, "tag(s); repeatable")

	// list ---------------------------------------------------------------
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all projects",
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
			if len(c.Projects) == 0 {
				fmt.Println("(no projects yet -- add one with: flow project add <alias> <path>)")
				return nil
			}
			rows := make([][]string, 0, len(c.Projects))
			for _, p := range c.Projects {
				rows = append(rows, []string{p.Alias, p.Path, strings.Join(p.Tags, ",")})
			}
			printTable([]string{"ALIAS", "PATH", "TAGS"}, rows)
			return nil
		},
	}

	// rm -----------------------------------------------------------------
	rmCmd := &cobra.Command{
		Use:   "rm <alias>",
		Short: "Remove a project bookmark",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			alias := args[0]
			s, err := loadStore()
			if err != nil {
				return err
			}
			return s.Mutate(func(c *config.Config) error {
				for i, p := range c.Projects {
					if p.Alias == alias {
						c.Projects = append(c.Projects[:i], c.Projects[i+1:]...)
						fmt.Printf("Removed project %s\n", alias)
						return nil
					}
				}
				return errs.User(
					fmt.Sprintf("no project named %q", alias),
					"see available projects:  flow project list",
				)
			})
		},
	}

	// show ---------------------------------------------------------------
	showCmd := &cobra.Command{
		Use:   "show [<alias>]",
		Short: "Show details of a project (omit alias to pick interactively)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			alias, err := pickFromConfigWithUsage(args, projectItems(c), "project",
				"register one first:  flow project add <alias> <path>",
				"flow project show <alias>")
			if err != nil {
				return err
			}
			p, err := resolveProjectAlias(c, alias)
			if err != nil {
				return err
			}
			fmt.Printf("alias: %s\n", p.Alias)
			fmt.Printf("path:  %s\n", p.Path)
			if len(p.Tags) > 0 {
				fmt.Printf("tags:  %s\n", strings.Join(p.Tags, ","))
			}
			return nil
		},
	}

	projectCmd.AddCommand(addCmd, listCmd, rmCmd, showCmd)
	rootCmd.AddCommand(projectCmd)
}
