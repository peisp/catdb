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
	grid.Add(tr("ctx.grid.copyTsv")).OnClick(emitContextEvent("ctx:grid-copy-tsv"))
	grid.Add(tr("ctx.grid.copyInsert")).OnClick(emitContextEvent("ctx:grid-copy-insert"))
	grid.Add(tr("ctx.grid.copyUpdate")).OnClick(emitContextEvent("ctx:grid-copy-update"))
	grid.AddSeparator()
	grid.Add(tr("ctx.grid.copyColumns")).OnClick(emitContextEvent("ctx:grid-copy-columns"))
	grid.Add(tr("ctx.grid.copyDataPlusColumns")).OnClick(emitContextEvent("ctx:grid-copy-data-plus-columns"))
	grid.Update()

	// Menu for editable table browser contexts — includes "Set to NULL".
	editGrid := application.NewContextMenu("catdb-grid-cell-edit")
	editGrid.Add(tr("ctx.grid.setNull")).OnClick(emitContextEvent("ctx:grid-set-null"))
	editGrid.AddSeparator()
	editGrid.Add(tr("ctx.grid.copyTsv")).OnClick(emitContextEvent("ctx:grid-copy-tsv"))
	editGrid.Add(tr("ctx.grid.copyInsert")).OnClick(emitContextEvent("ctx:grid-copy-insert"))
	editGrid.Add(tr("ctx.grid.copyUpdate")).OnClick(emitContextEvent("ctx:grid-copy-update"))
	editGrid.AddSeparator()
	editGrid.Add(tr("ctx.grid.copyColumns")).OnClick(emitContextEvent("ctx:grid-copy-columns"))
	editGrid.Add(tr("ctx.grid.copyDataPlusColumns")).OnClick(emitContextEvent("ctx:grid-copy-data-plus-columns"))
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
			{tr("ctx.tab.close"), "ctx:tab-close"},
			{"", ""}, // separator
			{tr("ctx.tab.closeOthers"), "ctx:tab-close-others"},
			{tr("ctx.tab.closeLeft"), "ctx:tab-close-left"},
			{tr("ctx.tab.closeRight"), "ctx:tab-close-right"},
		}},
		{"catdb-tab-first", []struct{ label, event string }{
			{tr("ctx.tab.close"), "ctx:tab-close"},
			{"", ""},
			{tr("ctx.tab.closeOthers"), "ctx:tab-close-others"},
			{tr("ctx.tab.closeRight"), "ctx:tab-close-right"},
		}},
		{"catdb-tab-last", []struct{ label, event string }{
			{tr("ctx.tab.close"), "ctx:tab-close"},
			{"", ""},
			{tr("ctx.tab.closeOthers"), "ctx:tab-close-others"},
			{tr("ctx.tab.closeLeft"), "ctx:tab-close-left"},
		}},
		{"catdb-tab-only", []struct{ label, event string }{
			{tr("ctx.tab.close"), "ctx:tab-close"},
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
	overview.Add(tr("ctx.open")).OnClick(emitContextEvent("ctx:tbl-open"))
	overview.Add(tr("ctx.table.edit")).OnClick(emitContextEvent("ctx:tbl-edit"))
	overview.Add(tr("ctx.rename")).OnClick(emitContextEvent("ctx:tbl-rename"))
	overview.AddSeparator()
	overview.Add(tr("ctx.table.truncate")).OnClick(emitContextEvent("ctx:tbl-truncate"))
	overview.Add(tr("ctx.delete")).OnClick(emitContextEvent("ctx:tbl-drop"))
	overview.Update()

	// Object-tree right-click menus — one per node kind. The front-end
	// (ObjectTree.vue) sets `--custom-contextmenu` based on the right-clicked
	// node and pushes context (db/table) into the singletons before the
	// native menu opens. Table-node Open/Edit/Truncate/Drop reuse the
	// `ctx:tbl-*` events above. Tree-only actions emit `ctx:tree-*`.

	treeTable := application.NewContextMenu("catdb-tree-table")
	treeTable.Add(tr("ctx.open")).OnClick(emitContextEvent("ctx:tbl-open"))
	treeTable.Add(tr("ctx.table.edit")).OnClick(emitContextEvent("ctx:tbl-edit"))
	treeTable.Add(tr("ctx.rename")).OnClick(emitContextEvent("ctx:tbl-rename"))
	treeTable.AddSeparator()
	treeTable.Add(tr("ctx.table.truncate")).OnClick(emitContextEvent("ctx:tbl-truncate"))
	treeTable.Add(tr("ctx.delete")).OnClick(emitContextEvent("ctx:tbl-drop"))
	treeTable.AddSeparator()
	treeTable.Add(tr("ctx.table.refreshCols")).OnClick(emitContextEvent("ctx:tree-refresh-cols"))
	treeTable.Update()

	treeView := application.NewContextMenu("catdb-tree-view")
	treeView.Add(tr("ctx.open")).OnClick(emitContextEvent("ctx:tbl-open"))
	treeView.Update()

	treeTableGroup := application.NewContextMenu("catdb-tree-table-group")
	treeTableGroup.Add(tr("ctx.tree.newTable")).OnClick(emitContextEvent("ctx:tree-new-table"))
	treeTableGroup.Add(tr("ctx.refresh")).OnClick(emitContextEvent("ctx:tree-refresh-tables"))
	treeTableGroup.Update()

	treeViewGroup := application.NewContextMenu("catdb-tree-view-group")
	treeViewGroup.Add(tr("ctx.refresh")).OnClick(emitContextEvent("ctx:tree-refresh-views"))
	treeViewGroup.Update()

	treeDb := application.NewContextMenu("catdb-tree-database")
	treeDb.Add(tr("ctx.tree.newDatabase")).OnClick(emitContextEvent("ctx:tree-db-new"))
	treeDb.Add(tr("ctx.tree.editDatabase")).OnClick(emitContextEvent("ctx:tree-db-edit"))
	treeDb.AddSeparator()
	treeDb.Add(tr("ctx.tree.newTable")).OnClick(emitContextEvent("ctx:tree-new-table"))
	treeDb.Add(tr("ctx.refresh")).OnClick(emitContextEvent("ctx:tree-refresh-db"))
	treeDb.Update()

	// Saved-query group + leaf menus. The group lives under each database node
	// alongside Tables/Views; leaves are individual saved SQL snippets.
	treeQueryGroup := application.NewContextMenu("catdb-tree-query-group")
	treeQueryGroup.Add(tr("ctx.tree.newQuery")).OnClick(emitContextEvent("ctx:tree-new-query"))
	treeQueryGroup.Add(tr("ctx.refresh")).OnClick(emitContextEvent("ctx:tree-refresh-queries"))
	treeQueryGroup.Update()

	treeQuery := application.NewContextMenu("catdb-tree-query")
	treeQuery.Add(tr("ctx.open")).OnClick(emitContextEvent("ctx:query-open"))
	treeQuery.Add(tr("ctx.rename")).OnClick(emitContextEvent("ctx:query-rename"))
	treeQuery.AddSeparator()
	treeQuery.Add(tr("ctx.delete")).OnClick(emitContextEvent("ctx:query-delete"))
	treeQuery.Update()

	// Connection-sidebar right-click — connect / disconnect / edit / delete.
	// The front-end decides which actions apply based on connection state.
	conn := application.NewContextMenu("catdb-connection")
	conn.Add(tr("ctx.conn.connect")).OnClick(emitContextEvent("ctx:conn-connect"))
	conn.Add(tr("ctx.conn.disconnect")).OnClick(emitContextEvent("ctx:conn-disconnect"))
	conn.AddSeparator()
	conn.Add(tr("ctx.conn.edit")).OnClick(emitContextEvent("ctx:conn-edit"))
	conn.AddSeparator()
	conn.Add(tr("ctx.delete")).OnClick(emitContextEvent("ctx:conn-delete"))
	conn.Update()

	// Sidebar blank-area right-click — only 新建分组 for now. Extracted so the
	// menu can grow without colliding with the connection / group variants.
	sbEmpty := application.NewContextMenu("catdb-sidebar-empty")
	sbEmpty.Add(tr("ctx.sidebar.newGroup")).OnClick(emitContextEvent("ctx:sb-new-group"))
	sbEmpty.Update()

	// Group-label right-click — always offers 重命名 and 删除. The backend
	// (storage.ErrGroupNotEmpty) refuses deletes on non-empty groups, and the
	// frontend handler short-circuits with a friendlier message before calling
	// — so showing the entry unconditionally costs nothing and keeps the menu
	// shape stable regardless of contents.
	sbGroup := application.NewContextMenu("catdb-sidebar-group")
	sbGroup.Add(tr("ctx.sidebar.newGroup")).OnClick(emitContextEvent("ctx:sb-new-group"))
	sbGroup.AddSeparator()
	sbGroup.Add(tr("ctx.rename")).OnClick(emitContextEvent("ctx:sb-group-rename"))
	sbGroup.Add(tr("ctx.delete")).OnClick(emitContextEvent("ctx:sb-group-delete"))
	sbGroup.Update()
}

func emitContextEvent(event string) func(*application.Context) {
	return func(_ *application.Context) { Emit(event, nil) }
}
