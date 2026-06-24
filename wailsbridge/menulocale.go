package wailsbridge

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"catdb/internal/i18n"
)

// Native menus (app menu + context menus) are built in the current UI locale.
// The locale is read from app_settings at startup (InitMenuLocale) and updated
// live when the user picks a language (SetMenuLocale).

var (
	localeMu      sync.RWMutex
	currentLocale = i18n.Default
)

// tr translates a native-menu key in the current locale.
func tr(key string) string {
	localeMu.RLock()
	loc := currentLocale
	localeMu.RUnlock()
	return i18n.T(loc, key)
}

// Tr is the exported translator for native UI strings built outside this
// package (e.g. SystemService child-window titles). Same catalog as the menus.
func Tr(key string) string { return tr(key) }

// InitMenuLocale sets the locale used by the native-menu builders WITHOUT
// rebuilding anything. Call once from main() before the initial
// BuildApplicationMenu / RegisterContextMenus so they render in the right
// language from the start.
func InitMenuLocale(locale string) {
	localeMu.Lock()
	currentLocale = i18n.Normalize(locale)
	localeMu.Unlock()
}

// SetMenuLocale switches the native-menu locale and rebuilds the menus in
// place. Safe to call from a Service goroutine:
//   - context menus are a locked map write (ContextMenuManager.Add), picked up
//     on the next right-click;
//   - the application menu's native apply must run on the main thread, so it
//     goes through application.InvokeAsync.
func SetMenuLocale(locale string) {
	localeMu.Lock()
	currentLocale = i18n.Normalize(locale)
	localeMu.Unlock()

	a := App()
	if a == nil {
		return
	}
	RegisterContextMenus(a)
	application.InvokeAsync(func() {
		a.Menu.SetApplicationMenu(BuildApplicationMenu(a))
	})
}
