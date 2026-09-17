package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/errs"
)

const systemdUnitName = "flow-scheduler.service"

// systemdUnitTemplate is rendered with the absolute flow binary path and
// the active config path so the daemon picks up the right files.
const systemdUnitTemplate = `[Unit]
Description=flow scheduler (recurring tasks for the flow CLI)
After=network.target

[Service]
Type=simple
ExecStart=%s --config %s scheduler run
Restart=on-failure
RestartSec=5
# Only run when the user is logged in; remove if you want it on at boot:
#   sudo loginctl enable-linger $USER

[Install]
WantedBy=default.target
`

func systemdAvailable() (string, bool) {
	for _, name := range []string{"systemctl"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

func userUnitPath() (string, error) {
	dir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, systemdUnitName), nil
}

func systemdInstall(_ *cobra.Command, _ []string) error {
	systemctl, ok := systemdAvailable()
	if !ok {
		return errs.User(
			"systemctl not found on this host",
			"run the scheduler manually instead:  nohup flow scheduler run > ~/.config/flow/scheduler.log 2>&1 &",
		)
	}
	self, err := os.Executable()
	if err != nil {
		return errs.System("could not locate flow binary", err)
	}
	store, err := loadStore()
	if err != nil {
		return err
	}
	unitPath, err := userUnitPath()
	if err != nil {
		return errs.System("could not create unit dir", err)
	}
	body := fmt.Sprintf(systemdUnitTemplate, self, store.Path)
	if err := os.WriteFile(unitPath, []byte(body), 0o644); err != nil {
		return errs.System("could not write unit file", err)
	}
	fmt.Println("wrote", unitPath)

	// daemon-reload + enable + start
	for _, sub := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", systemdUnitName},
		{"--user", "restart", systemdUnitName},
	} {
		out, err := runCapture(systemctl, sub...)
		if err != nil {
			return errs.User(
				fmt.Sprintf("systemctl %s failed: %s", strings.Join(sub, " "), strings.TrimSpace(out)),
				`if you're on a non-systemd shell, you can still run:  flow scheduler run &`,
			)
		}
	}
	fmt.Println("scheduler installed and started.")
	fmt.Println("verify with:  flow scheduler status")
	fmt.Println("logs:        journalctl --user -u " + systemdUnitName + " -f")
	fmt.Println()
	fmt.Println("(Optional) to keep it running across logout, run:  sudo loginctl enable-linger $USER")
	return nil
}

func systemdUninstall(_ *cobra.Command, _ []string) error {
	systemctl, ok := systemdAvailable()
	if !ok {
		return errs.User("systemctl not found", "")
	}
	for _, sub := range [][]string{
		{"--user", "stop", systemdUnitName},
		{"--user", "disable", systemdUnitName},
	} {
		_, _ = runCapture(systemctl, sub...) // best-effort
	}
	unitPath, _ := userUnitPath()
	if unitPath != "" {
		if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
			return errs.System("could not remove unit file", err)
		}
	}
	_, _ = runCapture(systemctl, "--user", "daemon-reload")
	fmt.Println("scheduler uninstalled.")
	return nil
}

func systemdStatus(_ *cobra.Command, _ []string) error {
	systemctl, ok := systemdAvailable()
	if !ok {
		fmt.Println("systemctl not available; check `pgrep -af 'flow scheduler run'` instead.")
		return nil
	}
	out, _ := runCapture(systemctl, "--user", "status", systemdUnitName, "--no-pager")
	fmt.Println(out)
	return nil
}

func runCapture(name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}
