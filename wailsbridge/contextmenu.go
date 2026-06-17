package wailsbridge

import "github.com/wailsapp/wails/v3/pkg/application"

// RegisterContextMenus wires the native context menus exposed to the
// front-end (CLAUDE.md rule 11: right-click must use Wails native menus).
//
// The data-grid context menu is triggered by setting the CSS property
// `--custom-contextmenu` on the DataGrid wrapper. Two menus are registered:
//
//	catdb-grid-cell      — copy items only (for SQL results, read-only, PK columns)
//	catdb-grid-cell-edit — includes "Set to NULL" (editable table browser with non-PK selection)
//
// The front-end switches between them synchronously inside the
// contextmenu_cell event handler before Wails reads the CSS property.
//
// Must be called AFTER SetApp so globalApplication is non-nil.
func RegisterContextMenus(app *application.App) {
	if app == nil {
		return
	}

	// Menu for read-only / non-editable contexts (SQL results, PK column
	// selected in table browser, etc.)
	grid := application.NewContextMenu("catdb-grid-cell")
	grid.Add("Copy as TSV").OnClick(emitContextEvent("ctx:grid-copy-tsv"))
	grid.Add("Copy as INSERT").OnClick(emitContextEvent("ctx:grid-copy-insert"))
	grid.Add("Copy as UPDATE").OnClick(emitContextEvent("ctx:grid-copy-update"))
	grid.AddSeparator()
	grid.Add("Copy column names").OnClick(emitContextEvent("ctx:grid-copy-columns"))
	grid.Add("Copy data + column names").OnClick(emitContextEvent("ctx:grid-copy-data-plus-columns"))
	grid.Update()

	// Menu for editable table browser contexts — includes "Set to NULL".
	editGrid := application.NewContextMenu("catdb-grid-cell-edit")
	editGrid.Add("Set to NULL").OnClick(emitContextEvent("ctx:grid-set-null"))
	editGrid.AddSeparator()
	editGrid.Add("Copy as TSV").OnClick(emitContextEvent("ctx:grid-copy-tsv"))
	editGrid.Add("Copy as INSERT").OnClick(emitContextEvent("ctx:grid-copy-insert"))
	editGrid.Add("Copy as UPDATE").OnClick(emitContextEvent("ctx:grid-copy-update"))
	editGrid.AddSeparator()
	editGrid.Add("Copy column names").OnClick(emitContextEvent("ctx:grid-copy-columns"))
	editGrid.Add("Copy data + column names").OnClick(emitContextEvent("ctx:grid-copy-data-plus-columns"))
	editGrid.Update()
}

func emitContextEvent(event string) func(*application.Context) {
	return func(_ *application.Context) { Emit(event, nil) }
}
