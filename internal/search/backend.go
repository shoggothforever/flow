// Package search builds command-line invocations for ripgrep (preferred) or
// grep (fallback) from a saved GrepPref. The actual execution streams output
// directly to the caller's stdout/stderr.
package search

import (
	"fmt"
	"os/exec"
)

// Backend identifies which executable is used.
type Backend int

const (
	BackendNone Backend = iota
	BackendRipgrep
	BackendGrep
)

func (b Backend) String() string {
	switch b {
	case BackendRipgrep:
		return "rg"
	case BackendGrep:
		return "grep"
	}
	return "none"
}

// Detect returns the best available backend on PATH.
func Detect() Backend {
	if _, err := exec.LookPath("rg"); err == nil {
		return BackendRipgrep
	}
	if _, err := exec.LookPath("grep"); err == nil {
		return BackendGrep
	}
	return BackendNone
}

// Spec is the (already expanded) input to argument construction.
type Spec struct {
	Pattern    string
	Paths      []string
	Include    []string
	Exclude    []string
	Regex      bool
	Word       bool
	IgnoreCase bool
}

// BuildArgs returns a (cmd, args) pair ready for exec.Command.
func BuildArgs(b Backend, s Spec) (string, []string, error) {
	if s.Pattern == "" {
		return "", nil, fmt.Errorf("pattern is empty")
	}
	switch b {
	case BackendRipgrep:
		var args []string
		if !s.Regex {
			args = append(args, "-F")
		}
		if s.Word {
			args = append(args, "-w")
		}
		if s.IgnoreCase {
			args = append(args, "-i")
		}
		for _, g := range s.Include {
			args = append(args, "-g", g)
		}
		for _, g := range s.Exclude {
			args = append(args, "-g", "!"+g)
		}
		args = append(args, "--", s.Pattern)
		args = append(args, s.Paths...)
		return "rg", args, nil

	case BackendGrep:
		args := []string{"-r"}
		if !s.Regex {
			args = append(args, "-F")
		} else {
			args = append(args, "-E")
		}
		if s.Word {
			args = append(args, "-w")
		}
		if s.IgnoreCase {
			args = append(args, "-i")
		}
		for _, g := range s.Include {
			args = append(args, "--include", g)
		}
		for _, g := range s.Exclude {
			args = append(args, "--exclude", g)
		}
		args = append(args, "--", s.Pattern)
		if len(s.Paths) == 0 {
			args = append(args, ".")
		} else {
			args = append(args, s.Paths...)
		}
		return "grep", args, nil

	default:
		return "", nil, fmt.Errorf("no search backend available (install ripgrep or grep)")
	}
}
