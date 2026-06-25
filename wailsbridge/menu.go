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
	fileMenu := m.AddSubmenu(tr("menu.file"))
	emitItem(fileMenu, tr("menu.newTab"), "menu:new-tab", "CmdOrCtrl+T")
	emitItem(fileMenu, tr("menu.closeTab"), "menu:close-tab", "CmdOrCtrl+W")
	fileMenu.AddSeparator()
	emitItem(fileMenu, tr("menu.saveSql"), "menu:save-sql", "CmdOrCtrl+S")
	emitItem(fileMenu, tr("menu.openSql"), "menu:open-sql", "CmdOrCtrl+O")
	fileMenu.AddSeparator()
	emitItem(fileMenu, tr("menu.exportResult"), "menu:export-result", "")
	emitItem(fileMenu, tr("menu.import"), "menu:import", "")
	if runtime.GOOS != "darwin" {
		fileMenu.AddSeparator()
		fileMenu.AddRole(application.Quit)
	}

	// Edit — role items get OS-localised labels automatically.
	editMenu := m.AddSubmenu(tr("menu.edit"))
	// The standard clipboard/undo roles are added on macOS only. On Windows &
	// Linux each of these roles registers an accelerator (Ctrl+V, Ctrl+Z…)
	// *and* an OnClick that re-invokes the WebView's native clipboard action,
	// so the shortcut fires twice — Ctrl+V pastes doubled content (and Ctrl+Z
	// would undo two steps). The WebView already handles Ctrl+Z/Y/X/C/V/A
	// natively inside inputs and the editor, so we simply omit the roles there.
	if runtime.GOOS == "darwin" {
		editMenu.AddRole(application.Undo)
		editMenu.AddRole(application.Redo)
		editMenu.AddSeparator()
		editMenu.AddRole(application.Cut)
		editMenu.AddRole(application.Copy)
		editMenu.AddRole(application.Paste)
		editMenu.AddRole(application.SelectAll)
		editMenu.AddSeparator()
	}
	emitItem(editMenu, tr("menu.find"), "menu:find", "CmdOrCtrl+F")

	// View
	viewMenu := m.AddSubmenu(tr("menu.view"))
	emitItem(viewMenu, tr("menu.toggleSidebar"), "menu:toggle-sidebar", "CmdOrCtrl+\\")
	viewMenu.AddSeparator()
	devToolsItem := viewMenu.Add(tr("menu.toggleDevtools"))
	devToolsItem.SetAccelerator("CmdOrCtrl+Shift+I")
	devToolsItem.OnClick(func(_ *application.Context) {
		win := app.Window.Current()
		if win != nil {
			win.OpenDevTools()
		}
	})
	viewMenu.AddSeparator()
	// Language — the submenu title is localised, but the item labels are
	// endonyms (each shown in its own language) so they stay fixed. Each item
	// Emits `menu:set-locale:<code>`; the front-end (api/settings) switches
	// vue-i18n and persists the choice (which also rebuilds these native menus).
	langMenu := viewMenu.AddSubmenu(tr("menu.language"))
	emitItem(langMenu, "English", "menu:set-locale:en-US", "")
	emitItem(langMenu, "中文（简体）", "menu:set-locale:zh-CN", "")
	viewMenu.AddSeparator()
	viewMenu.AddRole(application.Reload)
	viewMenu.AddRole(application.ToggleFullscreen)

	// Query
	queryMenu := m.AddSubmenu(tr("menu.query"))
	emitItem(queryMenu, tr("menu.run"), "menu:run-query", "")
	emitItem(queryMenu, tr("menu.runSelection"), "menu:run-selection", "CmdOrCtrl+Shift+Enter")
	emitItem(queryMenu, tr("menu.explain"), "menu:explain", "CmdOrCtrl+E")
	emitItem(queryMenu, tr("menu.cancel"), "menu:cancel-query", "CmdOrCtrl+.")

	// Window (macOS conventional) — role items get OS-localised labels.
	winMenu := m.AddSubmenu(tr("menu.window"))
	winMenu.AddRole(application.Minimise)
	winMenu.AddRole(application.Zoom)
	winMenu.AddRole(application.BringAllToFront)

	// Help
	helpMenu := m.AddSubmenu(tr("menu.help"))
	emitItem(helpMenu, tr("menu.documentation"), "menu:open-docs", "")

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
