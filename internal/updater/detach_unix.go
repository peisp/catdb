//go:build darwin || linux

package updater

import (
	"os/exec"
	"syscall"
)

// startDetached fires off the command in its own process group so it survives
// the parent (the running catdb) being SIGTERM'd at the next step.
func startDetached(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return cmd.Start()
}
