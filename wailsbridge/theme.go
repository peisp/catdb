package wailsbridge

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Theme preference plumbing. The user picks light / dark / system; the value is
// persisted in app_settings["ui.theme"] and seeded here at startup (InitTheme)
// so windows are created with the right native appearance from the first frame,
// then applied live when the user switches (ApplyTheme).
//
// Applying it natively — not just swapping CSS tokens — is what keeps the
// titlebar, the macOS translucent backdrop, native scrollbars and the webview's
// own `prefers-color-scheme` in step with the app. See theme_darwin.go /
// theme_windows.go for the per-platform calls and their limits.

const (
	ThemeLight  = "light"
	ThemeDark   = "dark"
	ThemeSystem = "system"
)

var (
	themeMu      sync.RWMutex
	currentTheme = ThemeSystem
)

func normalizeTheme(pref string) string {
	switch pref {
	case ThemeLight, ThemeDark, ThemeSystem:
		return pref
	default:
		return ThemeSystem
	}
}

// CurrentTheme returns the active theme preference.
func CurrentTheme() string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return currentTheme
}

// InitTheme seeds the preference WITHOUT touching any native handle. Call once
// from main() before the first window is created — same ordering rule as
// InitMenuLocale — so MacAppearance/WindowsTheme below return the right value
// while the window options are being built.
func InitTheme(pref string) {
	themeMu.Lock()
	currentTheme = normalizeTheme(pref)
	themeMu.Unlock()
}

// ApplyTheme switches the preference and re-applies it natively to every open
// window. Safe to call from a Service goroutine: the platform implementation
// marshals the native calls onto the main thread.
func ApplyTheme(pref string) {
	themeMu.Lock()
	currentTheme = normalizeTheme(pref)
	themeMu.Unlock()
	applyNativeTheme(CurrentTheme())
}

// MacAppearance maps the preference onto the window-creation option
// (WebviewWindowOptions.Mac.Appearance). "" means "inherit from NSApp", which
// is what we want for the system preference.
func MacAppearance() application.MacAppearanceType {
	switch CurrentTheme() {
	case ThemeLight:
		return application.NSAppearanceNameAqua
	case ThemeDark:
		return application.NSAppearanceNameDarkAqua
	default:
		return application.DefaultAppearance
	}
}

// WindowsTheme maps the preference onto WebviewWindowOptions.Windows.Theme.
// SystemDefault additionally makes Wails subscribe to system theme changes.
func WindowsTheme() application.Theme {
	switch CurrentTheme() {
	case ThemeLight:
		return application.Light
	case ThemeDark:
		return application.Dark
	default:
		return application.SystemDefault
	}
}

// WindowBackground is the pre-paint window background colour. It must match the
// theme, otherwise a dark-mode launch flashes a white rectangle before the
// webview paints. Values mirror `surface-content` in frontend/src/styles/tokens.ts.
func WindowBackground() application.RGBA {
	if isDarkTheme() {
		return application.NewRGB(0x1d, 0x1d, 0x20)
	}
	return application.NewRGB(246, 246, 247)
}
