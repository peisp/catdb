//go:build darwin

package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Install runs the macOS-specific install dance:
//
//  1. Locate the currently-running .app bundle (walk up from os.Executable).
//  2. Write a self-contained bash script that:
//     a. waits for our process to die
//     b. hdiutil attach the freshly-downloaded DMG
//     c. rm -rf the old .app, cp -R the new one over
//     d. strip the quarantine xattr (unsigned build → Gatekeeper would
//     otherwise refuse to launch the freshly-copied bundle)
//     e. hdiutil detach
//     f. open the new bundle
//  3. Spawn the script detached and return — the caller MUST then call
//     app.Quit() so the script can finish its job.
//
// The script lives in os.TempDir() and is left there for post-mortem if it
// fails; not worth doing a chained cleanup.
func Install(ctx context.Context, dmgPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("updater: locate self: %w", err)
	}
	appBundle, err := findAppBundle(exe)
	if err != nil {
		return fmt.Errorf("updater: locate .app bundle: %w", err)
	}

	scriptPath := filepath.Join(os.TempDir(), "catdb-update-install.sh")
	script := buildSwapScript(os.Getpid(), dmgPath, appBundle)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("updater: write swap script: %w", err)
	}

	// Detach: nohup + setsid-equivalent so the script outlives this process.
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Setpgid: detach from our process group so we don't take it down on Quit.
	if err := startDetached(cmd); err != nil {
		return fmt.Errorf("updater: spawn installer: %w", err)
	}
	return nil
}

// findAppBundle returns the path to the .app bundle that contains exe.
// Example: /Applications/catdb.app/Contents/MacOS/catdb → /Applications/catdb.app
// If exe is NOT inside a .app (e.g. `go run` development), returns an error.
func findAppBundle(exe string) (string, error) {
	dir := filepath.Dir(exe)
	for i := 0; i < 6; i++ {
		if strings.HasSuffix(dir, ".app") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("not running from a .app bundle (exe=%s)", exe)
}

func buildSwapScript(pid int, dmgPath, appBundle string) string {
	// Heredoc-style template, but using fmt to splice values is enough and
	// keeps shell quoting predictable. All paths are passed via single-quoted
	// shell literals so spaces survive.
	const tpl = `#!/bin/bash
# catdb auto-update swap script — generated, do not edit by hand.
set -u

PID=%d
DMG=%s
APP=%s

# 1. Wait for the running catdb to actually exit (5s timeout safety net).
for i in $(seq 1 50); do
  kill -0 "$PID" 2>/dev/null || break
  sleep 0.1
done
sleep 0.3

# 2. Mount the DMG read-only without opening Finder.
MOUNT_OUTPUT=$(hdiutil attach -nobrowse -noverify -noautoopen "$DMG" 2>&1) || {
  echo "hdiutil attach failed: $MOUNT_OUTPUT" >&2
  exit 1
}
# Last line of attach output has the mount point.
MOUNT=$(echo "$MOUNT_OUTPUT" | grep -Eo '/Volumes/[^	]+' | tail -1)
if [ -z "$MOUNT" ]; then
  echo "could not parse mount point from: $MOUNT_OUTPUT" >&2
  exit 1
fi

# 3. Find the .app inside the mounted volume.
NEW_APP=$(find "$MOUNT" -maxdepth 2 -name '*.app' -type d | head -1)
if [ -z "$NEW_APP" ]; then
  echo "no .app inside $MOUNT" >&2
  hdiutil detach "$MOUNT" -quiet 2>/dev/null || true
  exit 1
fi

# 4. Replace the bundle. Use ditto for resource-fork-safe copy on HFS+/APFS.
rm -rf "$APP"
ditto "$NEW_APP" "$APP"

# 5. Unsigned build → strip quarantine so Gatekeeper allows launch.
xattr -dr com.apple.quarantine "$APP" 2>/dev/null || true

# 6. Unmount.
hdiutil detach "$MOUNT" -quiet 2>/dev/null || true

# 7. Launch the new app and exit.
open "$APP"
`
	return fmt.Sprintf(tpl, pid, shellSingleQuote(dmgPath), shellSingleQuote(appBundle))
}

// shellSingleQuote wraps s in single quotes, escaping embedded single quotes.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
