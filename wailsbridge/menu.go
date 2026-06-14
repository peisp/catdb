package wailsbridge

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// BuildApplicationMenu wires the native top-level menu (macOS menu bar; on
// Windows/Linux it lands inside the window).
//
// Each non-role item Emits a `menu:<command>` event. The front-end listens
// on these via api/menuEvents and routes them to the active tab (run query,
// new tab, save, etc.). Doing it this way keeps the menu definition in Go
// (single source of truth, native accelerator handling) while the actions
// stay in the front-end (closer to the editor state).
func BuildApplicationMenu(app *application.App) *application.Menu {
	m := app.NewMenu()

	if runtime.GOOS == "darwin" {
		// macOS conventional first menu: app name + about / hide / quit roles.
		appMenu := m.AddSubmenu("catdb")
		appMenu.AddRole(application.About)
		appMenu.AddSeparator()
		appMenu.AddRole(application.Hide)
		appMenu.AddRole(application.HideOthers)
		appMenu.AddRole(application.UnHide)
		appMenu.AddSeparator()
		appMenu.AddRole(application.Quit)
	}

	// File
	fileMenu := m.AddSubmenu("File")
	emitItem(fileMenu, "New Tab", "menu:new-tab", "CmdOrCtrl+T")
	emitItem(fileMenu, "Close Tab", "menu:close-tab", "CmdOrCtrl+W")
	fileMenu.AddSeparator()
	emitItem(fileMenu, "Save SQL…", "menu:save-sql", "CmdOrCtrl+S")
	emitItem(fileMenu, "Open SQL…", "menu:open-sql", "CmdOrCtrl+O")
	fileMenu.AddSeparator()
	emitItem(fileMenu, "Export Result…", "menu:export-result", "")
	emitItem(fileMenu, "Import…", "menu:import", "")
	if runtime.GOOS != "darwin" {
		fileMenu.AddSeparator()
		fileMenu.AddRole(application.Quit)
	}

	// Edit
	editMenu := m.AddSubmenu("Edit")
	editMenu.AddRole(application.Undo)
	editMenu.AddRole(application.Redo)
	editMenu.AddSeparator()
	editMenu.AddRole(application.Cut)
	editMenu.AddRole(application.Copy)
	editMenu.AddRole(application.Paste)
	editMenu.AddRole(application.SelectAll)
	editMenu.AddSeparator()
	emitItem(editMenu, "Find…", "menu:find", "CmdOrCtrl+F")

	// View
	viewMenu := m.AddSubmenu("View")
	emitItem(viewMenu, "Toggle Sidebar", "menu:toggle-sidebar", "CmdOrCtrl+\\")
	viewMenu.AddSeparator()
	viewMenu.AddRole(application.Reload)
	viewMenu.AddRole(application.ToggleFullscreen)

	// Query
	queryMenu := m.AddSubmenu("Query")
	emitItem(queryMenu, "Run", "menu:run-query", "CmdOrCtrl+Enter")
	emitItem(queryMenu, "Run Selection", "menu:run-selection", "CmdOrCtrl+Shift+Enter")
	emitItem(queryMenu, "EXPLAIN", "menu:explain", "CmdOrCtrl+E")
	emitItem(queryMenu, "Cancel", "menu:cancel-query", "CmdOrCtrl+.")

	// Window (macOS conventional)
	winMenu := m.AddSubmenu("Window")
	winMenu.AddRole(application.Minimise)
	winMenu.AddRole(application.Zoom)
	winMenu.AddRole(application.BringAllToFront)

	// Help
	helpMenu := m.AddSubmenu("Help")
	emitItem(helpMenu, "Documentation", "menu:open-docs", "")

	return m
}

// emitItem creates a regular menu item that Emits `event` on click and binds
// the accelerator (if any). Wails' SetAccelerator already handles the
// platform-specific modifier rendering (Cmd on macOS, Ctrl elsewhere).
func emitItem(parent *application.Menu, label, event, accelerator string) *application.MenuItem {
	it := parent.Add(label)
	it.OnClick(func(_ *application.Context) {
		Emit(event, nil)
	})
	if accelerator != "" {
		it.SetAccelerator(accelerator)
	}
	return it
}
