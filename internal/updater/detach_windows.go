//go:build windows

package updater

import (
	"os/exec"
	"syscall"
)

// startDetached launches cmd as a detached process tree so it survives the
// parent (catdb) exiting. DETACHED_PROCESS = 0x00000008.
func startDetached(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = 0x00000008 | 0x00000200 // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	return cmd.Start()
}
