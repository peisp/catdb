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

	// Tab strip context menu — 4 variants by tab position.
	// The front-end decides which variant to show based on tab index and total.
	//
	//   catdb-tab          — middle position: all 4 items
	//   catdb-tab-first    — first tab: no 「关闭左侧」
	//   catdb-tab-last     — last tab: no 「关闭右侧」
	//   catdb-tab-only     — lone tab: only 「关闭」
	tabs := []struct {
		name  string
		items []struct{ label, event string }
	}{
		{"catdb-tab", []struct{ label, event string }{
			{"关闭", "ctx:tab-close"},
			{"", ""}, // separator
			{"关闭其他", "ctx:tab-close-others"},
			{"关闭左侧", "ctx:tab-close-left"},
			{"关闭右侧", "ctx:tab-close-right"},
		}},
		{"catdb-tab-first", []struct{ label, event string }{
			{"关闭", "ctx:tab-close"},
			{"", ""},
			{"关闭其他", "ctx:tab-close-others"},
			{"关闭右侧", "ctx:tab-close-right"},
		}},
		{"catdb-tab-last", []struct{ label, event string }{
			{"关闭", "ctx:tab-close"},
			{"", ""},
			{"关闭其他", "ctx:tab-close-others"},
			{"关闭左侧", "ctx:tab-close-left"},
		}},
		{"catdb-tab-only", []struct{ label, event string }{
			{"关闭", "ctx:tab-close"},
		}},
	}
	for _, t := range tabs {
		m := application.NewContextMenu(t.name)
		for _, it := range t.items {
			if it.label == "" {
				m.AddSeparator()
			} else {
				m.Add(it.label).OnClick(emitContextEvent(it.event))
			}
		}
		m.Update()
	}
}

func emitContextEvent(event string) func(*application.Context) {
	return func(_ *application.Context) { Emit(event, nil) }
}
