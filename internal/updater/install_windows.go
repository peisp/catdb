//go:build windows

package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Install on Windows performs a silent in-place upgrade:
//
//  1. Write a batch script that (a) waits for this process to exit, (b) runs
//     the freshly-downloaded NSIS installer with /S — fully silent, no
//     directory/finish pages — pinned to the current install dir via /D=,
//     and (c) relaunches the app de-elevated through explorer.exe.
//  2. Spawn the script with elevation (UAC "runas" on cmd.exe, hidden
//     console) and return — the caller MUST then call app.Quit() so the
//     installer can replace the in-use exe.
//
// We elevate because NSIS writes to Program Files. ShellExecute creates a
// detached process tree, so the script survives catdb exiting. The script is
// left in os.TempDir() for post-mortem if it fails.
var shellExecute = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

func Install(_ context.Context, installerPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("updater: locate self: %w", err)
	}
	installDir := filepath.Dir(exe)

	scriptPath := filepath.Join(os.TempDir(), "catdb-update-install.bat")
	script := buildSilentInstallScript(os.Getpid(), installerPath, installDir, exe)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("updater: write install script: %w", err)
	}

	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return fmt.Errorf("updater: encode verb: %w", err)
	}
	file, err := windows.UTF16PtrFromString("cmd.exe")
	if err != nil {
		return fmt.Errorf("updater: encode file: %w", err)
	}
	params, err := windows.UTF16PtrFromString(fmt.Sprintf(`/C "%s"`, scriptPath))
	if err != nil {
		return fmt.Errorf("updater: encode params: %w", err)
	}

	ret, _, _ := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		0,
		0, // SW_HIDE — the script runs in a hidden console
	)
	if ret <= 32 {
		return fmt.Errorf("updater: spawn installer (elevation): return code %d", ret)
	}
	return nil
}

func buildSilentInstallScript(pid int, installerPath, installDir, exePath string) string {
	// CRLF line endings — cmd.exe is picky about bare-LF batch files.
	// %% escapes a literal % for fmt; batch vars therefore appear as %%VAR%%.
	// /D= must be the last installer argument and unquoted, even with spaces.
	const tpl = "@echo off\r\n" +
		"rem catdb auto-update script - generated, do not edit by hand.\r\n" +
		"\r\n" +
		"rem 1. Wait for the running catdb to exit (30s timeout safety net).\r\n" +
		"set TRIES=0\r\n" +
		":wait\r\n" +
		"tasklist /FI \"PID eq %d\" /FO CSV /NH | find \"\"\"%d\"\"\" >nul\r\n" +
		"if errorlevel 1 goto install\r\n" +
		"set /a TRIES+=1\r\n" +
		"if %%TRIES%% GEQ 30 goto install\r\n" +
		"ping -n 2 127.0.0.1 >nul\r\n" +
		"goto wait\r\n" +
		"\r\n" +
		":install\r\n" +
		"rem 2. Silent NSIS install into the existing install dir.\r\n" +
		"\"%s\" /S /D=%s\r\n" +
		"\r\n" +
		"rem 3. Relaunch de-elevated - explorer.exe starts the target as the\r\n" +
		"rem    normal desktop user, not the elevated installer user.\r\n" +
		"start \"\" explorer.exe \"%s\"\r\n"
	return fmt.Sprintf(tpl, pid, pid, installerPath, installDir, exePath)
}
