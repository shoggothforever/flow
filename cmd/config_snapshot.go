package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/config"
	"github.com/yithcai/flow/internal/errs"
)

const defaultConfigSnapshot = "flow.config.json"

func newConfigExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export [path]",
		Short: "Export the active config as a versionable JSON snapshot",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := loadStore()
			if err != nil {
				return err
			}
			destination := defaultConfigSnapshot
			if len(args) == 1 {
				destination = args[0]
			}
			path, err := config.ExportSnapshot(store, destination)
			if err != nil {
				return errs.UserWrap(err, "could not export config snapshot", "fix the active config with flow config edit, then retry")
			}
			fmt.Println("exported", store.Path, "->", path)
			return nil
		},
	}
}

func newConfigImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import [path]",
		Short: "Validate and import a versioned JSON snapshot",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := loadStore()
			if err != nil {
				return err
			}
			source := defaultConfigSnapshot
			if len(args) == 1 {
				source = args[0]
			}
			path, err := config.ImportSnapshot(source, store)
			if err != nil {
				return errs.UserWrap(err, "could not import config snapshot", "check the snapshot path and run flow config validate on the source file")
			}
			fmt.Println("imported", path, "->", store.Path)
			for _, warning := range crossRefCheckMustLoad(store) {
				fmt.Println("warning:", warning)
			}
			return nil
		},
	}
}

func crossRefCheckMustLoad(store *config.Store) []string {
	cfg, err := store.Load()
	if err != nil {
		return nil
	}
	return crossRefCheck(cfg)
}
