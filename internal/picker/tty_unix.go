package picker

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openTTY returns a handle to /dev/tty so the picker keeps working when the
// surrounding command's stdin/stdout were redirected.
func openTTY() (*os.File, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/tty: %w", err)
	}
	return f, nil
}

// setRaw puts the terminal into cbreak mode and returns the previous
// termios so callers can restore it.
func setRaw(f *os.File) (*unix.Termios, error) {
	prev, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		return nil, err
	}
	t := *prev
	// Disable canonical mode and echo, but keep signals alive so users can
	// still get a clean exit via the picker's own Ctrl-C handling above.
	t.Lflag &^= unix.ICANON | unix.ECHO
	t.Iflag &^= unix.IXON | unix.ICRNL
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, &t); err != nil {
		return nil, err
	}
	return prev, nil
}
