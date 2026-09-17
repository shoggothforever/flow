// Package main is the entrypoint for the flow CLI.
package main

import (
	"os"

	"github.com/yithcai/flow/cmd"
)

func main() {
	// Lazy dispatch: rewrite `flow <alias> ...` -> `flow <kind> run <alias> ...`
	// and `flow` (no args) -> `flow pick`. See cmd/dispatch.go.
	cmd.RewriteArgs()
	os.Exit(cmd.Execute())
}
