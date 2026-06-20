//go:build windows

package updater

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
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
var shellExecute = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

func Install(_ context.Context, exePath string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return fmt.Errorf("updater: encode verb: %w", err)
	}
	file, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return fmt.Errorf("updater: encode path: %w", err)
	}

	ret, _, _ := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		5, // SW_SHOW
	)
	if ret <= 32 {
		return fmt.Errorf("updater: spawn installer (elevation): return code %d", ret)
	}
	return nil
}
