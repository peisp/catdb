//go:build !darwin && !windows

package wailsbridge

// Linux is best-effort only (CLAUDE.md #10 — Windows + macOS are the supported
// platforms). The CSS-token path in the front-end still honours the preference.
func applyNativeTheme(_ string) {}

func isDarkTheme() bool { return CurrentTheme() == ThemeDark }
