package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/errs"
)

func init() {
	var listOnFail bool
	jumpCmd := &cobra.Command{
		Use:     "jump <alias>",
		Aliases: []string{"j"},
		Short:   "Print __FLOW_CD__:<path> so the shell wrapper can cd into a project",
		Long: `jump prints a single line of the form

    __FLOW_CD__:/absolute/path

which the bash/zsh wrapper installed by 'flow shell init' picks up and turns
into a real 'cd'. When invoked without the wrapper the directive prints
literally so you can see what would have happened.`,
		Args: cobra.MaximumNArgs(1),
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
				"flow jump <alias>")
			if err != nil {
				return err
			}
			p, err := resolveProjectAlias(c, alias)
			if err != nil {
				if listOnFail {
					names := make([]string, 0, len(c.Projects))
					for _, x := range c.Projects {
						names = append(names, x.Alias)
					}
					fmt.Fprintln(cobraStderr(), "available aliases:", strings.Join(names, ", "))
				}
				return err
			}
			path, err := expandPath(p.Path)
			if err != nil {
				return errs.UserWrap(err, "could not expand project path",
					"check that the path stored under  flow project show "+p.Alias+"  exists")
			}
			fmt.Printf("__FLOW_CD__:%s\n", path)
			return nil
		},
	}
	jumpCmd.Flags().BoolVar(&listOnFail, "list-on-fail", true, "print the available aliases on stderr when no match is found")
	rootCmd.AddCommand(jumpCmd)
}
