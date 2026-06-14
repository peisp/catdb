// Package wailsbridge is the SOLE place in the Go codebase allowed to import
// github.com/wailsapp/wails/v3/pkg/application.
//
// Everything else (Services, core, plugins) talks to Wails through this
// package. The point: Wails v3 is still alpha — when a release renames or
// restructures the API, only THIS file changes. See CLAUDE.md #1.
//
// The bridge is a thin pass-through, not a re-engineering of the Wails API.
// Add a wrapper only when something outside this package needs it.
package wailsbridge

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	appMu sync.RWMutex
	app   *application.App
)

// SetApp is called from main() once the application.App is constructed so
// that other packages can route emits / window calls through this bridge.
func SetApp(a *application.App) {
	appMu.Lock()
	defer appMu.Unlock()
	app = a
}

// App returns the currently registered application.App (or nil if SetApp
// has not been called yet). Callers outside main() must tolerate nil during
// startup — prefer Emit() and the typed helpers below to direct App() access.
func App() *application.App {
	appMu.RLock()
	defer appMu.RUnlock()
	return app
}

// Emit fans an event out to every WebView. Safe to call before SetApp — the
// event is simply dropped in that case (e.g. during Service init).
func Emit(name string, data any) {
	a := App()
	if a == nil {
		return
	}
	a.Event.Emit(name, data)
}
