//go:build windows

package updater

import (
	"context"
	"fmt"
	"syscall"
)

// Install on Windows spawns the freshly-downloaded NSIS installer with
// elevation (UAC) via ShellExecute "runas" and returns.
// The caller MUST then call app.Quit() so the installer can replace the
// in-use exe (NSIS will offer a brief "uninstalling previous version" step
// automatically).
//
// We use the "runas" verb instead of "open" because NSIS writes to
// Program Files and requires administrator privileges. ShellExecute
// with "runas" triggers the UAC elevation prompt; if the user
// approves, the installer runs elevated. The process is automatically
// detached (ShellExecute creates a new process tree), so it survives
// catdb exiting.
func Install(_ context.Context, exePath string) error {
	ret, err := syscall.ShellExecute(
		0,                                 // hwnd: no parent window
		syscall.StringToUTF16Ptr("runas"), // verb: request elevation
		syscall.StringToUTF16Ptr(exePath), // file: installer path
		nil,                               // args
		nil,                               // dir: inherit CWD
		syscall.SW_SHOW,                   // showCmd: show window normally
	)
	if err != nil {
		return fmt.Errorf("updater: spawn installer (elevation): %w", err)
	}
	// ShellExecute returns a value > 32 on success.
	if ret <= 32 {
		return fmt.Errorf("updater: spawn installer (elevation): return code %d", ret)
	}
	return nil
}
