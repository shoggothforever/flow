// Package cmd contains the cobra command tree for the flow CLI.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/logx"
)

// Build-time injected metadata.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Global flags.
var (
	flagVerbose    bool
	flagConfigPath string
)

// rootCmd is the top-level command. Sub-commands are attached in their own files via init().
var rootCmd = &cobra.Command{
	Use:   "flow",
	Short: "flow — a personal developer workflow accelerator",
	Long: `flow integrates project bookmarks, scripts, command aliases, search presets,
schedules, and a local web UI into a single CLI.

Configuration is stored as a single JSON file (default: ~/.config/flow/config.json,
override with FLOW_CONFIG).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		logx.Init(flagVerbose)
		return nil
	},
}

// Execute runs the root command and returns the exit code.
// Exit codes follow the convention defined in internal/errs.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, errs.Format(err))
		return errs.ExitCode(err)
	}
	return 0
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose logging to stderr")
	rootCmd.PersistentFlags().StringVar(&flagConfigPath, "config", "", "path to config.json (overrides FLOW_CONFIG and default)")

	rootCmd.Version = fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
}
