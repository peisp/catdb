//go:build windows

package wailsbridge

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// applyNativeTheme re-themes every open window's frame. w32.SetTheme
// (pkg/w32/theme.go:234) is exactly what Wails itself calls from
// windowsWebviewWindow.updateTheme (webview_window_windows.go:1482); Wails only
// invokes it at creation time or on a system theme change, so we call it
// directly to make a user-driven switch live.
//
// LIMIT: this drives DWM (dark caption/border) and the native menu theme only.
// WebView2's own colour scheme (ICoreWebView2Profile.PreferredColorScheme) is
// not exposed anywhere in v3.0.0-beta.4, so the webview's
// `prefers-color-scheme` keeps reporting the OS setting on Windows. The
// front-end therefore still derives its mode from the stored preference rather
// than trusting matchMedia — see frontend/src/stores/theme.ts.
func applyNativeTheme(pref string) {
	a := App()
	if a == nil {
		return
	}
	dark := pref == ThemeDark || (pref == ThemeSystem && w32.IsCurrentlyDarkMode())
	application.InvokeAsync(func() {
		for _, w := range a.Window.GetAll() {
			if h := w.NativeWindow(); h != nil {
				w32.SetTheme(uintptr(h), dark)
			}
		}
	})
}

func isDarkTheme() bool {
	switch CurrentTheme() {
	case ThemeDark:
		return true
	case ThemeLight:
		return false
	default:
		return w32.IsCurrentlyDarkMode()
	}
}
