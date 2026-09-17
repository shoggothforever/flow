// Package shellintegr exposes the small shell wrapper functions needed for
// `flow jump` to actually change the parent shell's directory.
package shellintegr

import (
	_ "embed"
	"fmt"
)

//go:embed bash.sh
var bashSnippet string

//go:embed zsh.sh
var zshSnippet string

// Snippet returns the wrapper script for the given shell.
// shell must be one of: "bash", "zsh".
func Snippet(shell string) (string, error) {
	switch shell {
	case "bash":
		return bashSnippet, nil
	case "zsh":
		return zshSnippet, nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh)", shell)
	}
}
