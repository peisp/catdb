//go:build !darwin && !windows

package updater

import (
	"context"
	"fmt"
	"runtime"
)

// Install is a no-op on platforms outside MVP scope (Linux: not in CI
// pipeline yet — manual install only).
func Install(_ context.Context, _ string) error {
	return fmt.Errorf("updater: in-app install not supported on %s", runtime.GOOS)
}
