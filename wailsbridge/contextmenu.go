package wailsbridge

import "github.com/wailsapp/wails/v3/pkg/application"

// RegisterContextMenus wires the native context menus exposed to the
// front-end (CLAUDE.md rule 11: right-click must use Wails native menus).
//
// The data-grid context menu is triggered by setting the CSS property
// `--custom-contextmenu: catdb-grid-cell` on the DataGrid wrapper. Right-click
// then opens this menu at the cursor position; each item Emits a `ctx:grid-…`
// event the front-end listens for to perform the action against the currently
// active grid (selection + rows + column names + table + PK live in
// frontend/src/api/gridContextMenu.ts).
//
// We do NOT toggle item enabled-state from JS — keeping the menu static keeps
// the bridge thin. The front-end listener silently no-ops actions that aren't
// applicable to the active grid (e.g. Copy-as-INSERT on SQL editor results
// where no table name is known).
//
// Must be called AFTER SetApp so globalApplication is non-nil.
func RegisterContextMenus(app *application.App) {
	if app == nil {
		return
	}
	grid := application.NewContextMenu("catdb-grid-cell")
	grid.Add("Copy as TSV").OnClick(emitContextEvent("ctx:grid-copy-tsv"))
	grid.Add("Copy as INSERT").OnClick(emitContextEvent("ctx:grid-copy-insert"))
	grid.Add("Copy as UPDATE").OnClick(emitContextEvent("ctx:grid-copy-update"))
	grid.AddSeparator()
	grid.Add("Copy column names").OnClick(emitContextEvent("ctx:grid-copy-columns"))
	grid.Add("Copy data + column names").OnClick(emitContextEvent("ctx:grid-copy-data-plus-columns"))
	// Rebuild native menu with the items we just added — NewContextMenu's
	// internal Update runs before any Add() calls.
	grid.Update()
}

func emitContextEvent(event string) func(*application.Context) {
	return func(_ *application.Context) { Emit(event, nil) }
}
