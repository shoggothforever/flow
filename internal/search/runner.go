package search

import (
	"os"
	"os/exec"
)

// Run executes the given backend with the given args, streaming stdout/stderr
// to the parent process. Returns the child's exit code.
//
// We don't treat exit code 1 (no matches) as an error, leaving that decision
// to the caller (since both rg and grep use 1 for "no matches").
func Run(cwd, cmd string, args []string) (int, error) {
	c := exec.Command(cmd, args...)
	if cwd != "" {
		c.Dir = cwd
	}
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}
