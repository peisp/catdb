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

	// Table-level right-click — used by both TablesOverview (table list) and
	// ObjectTree (table node). Open / Edit structure / Truncate / Drop.
	// Selected table is tracked in the front-end (see api/tableContextMenu.ts).
	overview := application.NewContextMenu("catdb-tables-overview")
	overview.Add("打开").OnClick(emitContextEvent("ctx:tbl-open"))
	overview.Add("修改").OnClick(emitContextEvent("ctx:tbl-edit"))
	overview.Add("重命名").OnClick(emitContextEvent("ctx:tbl-rename"))
	overview.AddSeparator()
	overview.Add("清空").OnClick(emitContextEvent("ctx:tbl-truncate"))
	overview.Add("删除").OnClick(emitContextEvent("ctx:tbl-drop"))
	overview.Update()

	// Object-tree right-click menus — one per node kind. The front-end
	// (ObjectTree.vue) sets `--custom-contextmenu` based on the right-clicked
	// node and pushes context (db/table) into the singletons before the
	// native menu opens. Table-node Open/Edit/Truncate/Drop reuse the
	// `ctx:tbl-*` events above. Tree-only actions emit `ctx:tree-*`.

	treeTable := application.NewContextMenu("catdb-tree-table")
	treeTable.Add("打开").OnClick(emitContextEvent("ctx:tbl-open"))
	treeTable.Add("修改").OnClick(emitContextEvent("ctx:tbl-edit"))
	treeTable.Add("重命名").OnClick(emitContextEvent("ctx:tbl-rename"))
	treeTable.AddSeparator()
	treeTable.Add("清空").OnClick(emitContextEvent("ctx:tbl-truncate"))
	treeTable.Add("删除").OnClick(emitContextEvent("ctx:tbl-drop"))
	treeTable.AddSeparator()
	treeTable.Add("刷新列").OnClick(emitContextEvent("ctx:tree-refresh-cols"))
	treeTable.Update()

	treeView := application.NewContextMenu("catdb-tree-view")
	treeView.Add("打开").OnClick(emitContextEvent("ctx:tbl-open"))
	treeView.Update()

	treeTableGroup := application.NewContextMenu("catdb-tree-table-group")
	treeTableGroup.Add("新建表").OnClick(emitContextEvent("ctx:tree-new-table"))
	treeTableGroup.Add("刷新").OnClick(emitContextEvent("ctx:tree-refresh-tables"))
	treeTableGroup.Update()

	treeViewGroup := application.NewContextMenu("catdb-tree-view-group")
	treeViewGroup.Add("刷新").OnClick(emitContextEvent("ctx:tree-refresh-views"))
	treeViewGroup.Update()

	treeDb := application.NewContextMenu("catdb-tree-database")
	treeDb.Add("新建表").OnClick(emitContextEvent("ctx:tree-new-table"))
	treeDb.Add("刷新").OnClick(emitContextEvent("ctx:tree-refresh-db"))
	treeDb.Update()

	// Connection-sidebar right-click — connect / disconnect / edit / delete.
	// The front-end decides which actions apply based on connection state.
	conn := application.NewContextMenu("catdb-connection")
	conn.Add("打开连接").OnClick(emitContextEvent("ctx:conn-connect"))
	conn.Add("断开连接").OnClick(emitContextEvent("ctx:conn-disconnect"))
	conn.AddSeparator()
	conn.Add("编辑").OnClick(emitContextEvent("ctx:conn-edit"))
	conn.AddSeparator()
	conn.Add("删除").OnClick(emitContextEvent("ctx:conn-delete"))
	conn.Update()
}

func emitContextEvent(event string) func(*application.Context) {
	return func(_ *application.Context) { Emit(event, nil) }
}
