package wailsbridge

import (
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// openChildWindows is the registry of named auxiliary windows we keep so that
// re-opening (e.g. the connection editor) focuses the existing window instead
// of spawning a duplicate.
var (
	childWinMu sync.Mutex
	childWins  = make(map[string]*application.WebviewWindow)
)

// OpenChildWindow opens (or focuses) a child WebviewWindow with the given
// logical name. If a window with that name is already open it is brought to
// front and re-pointed at url; otherwise a fresh window is created with the
// supplied geometry. Background colour and titlebar treatment intentionally
// match the main window so the auxiliary feels native, not "external tab"-ish.
//
// The child window deliberately does NOT install the close-guard (only the
// main window cares about dirty SQL tabs).
func OpenChildWindow(name, title, url string, width, height int) {
	a := App()
	if a == nil {
		return
	}

	childWinMu.Lock()
	existing := childWins[name]
	childWinMu.Unlock()
	if existing != nil {
		// Re-point and focus. SetURL drives the webview to the new hash route
		// so re-opens with different params work without spawning a new window.
		existing.SetURL(url)
		existing.Focus()
		return
	}

	if width <= 0 {
		width = 720
	}
	if height <= 0 {
		height = 560
	}

	w := a.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:     name,
		Title:    title,
		Width:    width,
		Height:   height,
		Frameless: runtime.GOOS == "windows",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 30,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(245, 245, 247),
		URL:              url,
	})

	childWinMu.Lock()
	childWins[name] = w
	childWinMu.Unlock()

	// Drop the registry entry when the window closes so the next OpenChildWindow
	// creates a fresh instance instead of trying to focus a destroyed handle.
	w.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		childWinMu.Lock()
		defer childWinMu.Unlock()
		if childWins[name] == w {
			delete(childWins, name)
		}
	})
}
