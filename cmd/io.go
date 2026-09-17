package cmd

import (
	"io"
	"os"
)

// cobraStderr returns the stderr writer to use from RunE handlers.
// Centralised so tests can swap it out later.
func cobraStderr() io.Writer { return os.Stderr }
