//go:build windows

package updater

import (
	"context"
	"fmt"
	"os/exec"
)

// Install on Windows spawns the freshly-downloaded NSIS installer detached
// and returns. The caller MUST then call app.Quit() so the installer can
// replace the in-use exe (NSIS will offer a brief "uninstalling previous
// version" step automatically).
//
// We launch the installer interactively rather than silent (/S):
//   - the user sees what's happening and can cancel
//   - UAC needs to surface anyway (NSIS writes to Program Files)
//   - NSIS includes a "Run catdb now" checkbox at the end, so relaunch works
//     without any extra glue here
func Install(_ context.Context, exePath string) error {
	cmd := exec.Command(exePath)
	if err := startDetached(cmd); err != nil {
		return fmt.Errorf("updater: spawn installer: %w", err)
	}
	return nil
}
