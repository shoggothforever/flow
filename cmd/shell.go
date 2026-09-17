package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/shellintegr"
)

func init() {
	shellCmd := &cobra.Command{
		Use:   "shell",
		Short: "Shell integration helpers",
	}
	initCmd := &cobra.Command{
		Use:       "init <bash|zsh>",
		Short:     "Print the shell wrapper enabling 'flow jump' to actually cd",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh"},
		RunE: func(_ *cobra.Command, args []string) error {
			snippet, err := shellintegr.Snippet(args[0])
			if err != nil {
				return errs.User(err.Error(), "supported values: bash, zsh")
			}
			fmt.Print(snippet)
			return nil
		},
	}
	shellCmd.AddCommand(initCmd)
	rootCmd.AddCommand(shellCmd)
}
