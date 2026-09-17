package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/yithcai/flow/internal/errs"
)

func init() {
	fix := &cobra.Command{
		Use:   "shell-fix",
		Short: "Restore the terminal to a sane state (use after a crashed TUI program)",
		Long: `Equivalent to running "stty sane" -- forces canonical mode + echo
back on so the shell shows what you type. Use this if a flow picker (or
any other TUI tool) was killed before it could restore the terminal.

You can also recover by running:  stty sane
or by typing blindly:  reset<Enter>`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				return errs.System("could not open /dev/tty", err)
			}
			defer tty.Close()
			t, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
			if err != nil {
				return errs.System("ioctl(TCGETS) failed", err)
			}
			t.Lflag |= unix.ICANON | unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ECHOCTL | unix.ISIG
			t.Iflag |= unix.IXON | unix.ICRNL | unix.BRKINT
			t.Oflag |= unix.OPOST | unix.ONLCR
			t.Cc[unix.VMIN] = 1
			t.Cc[unix.VTIME] = 0
			if err := unix.IoctlSetTermios(int(tty.Fd()), unix.TCSETS, t); err != nil {
				return errs.System("ioctl(TCSETS) failed", err)
			}
			fmt.Println("terminal restored")
			return nil
		},
	}
	rootCmd.AddCommand(fix)
}
