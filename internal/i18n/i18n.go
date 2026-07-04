// Package i18n is the Go-side message catalog for native UI strings (the
// app menu and right-click context menus built in wailsbridge). The Vue
// front-end has its own vue-i18n catalog; this one only covers what the OS
// renders natively and therefore can't be translated in the WebView.
//
// English is the base/reference locale. Keep keys in sync with the menu
// builders in wailsbridge/menu.go and wailsbridge/contextmenu.go.
package i18n

// Locale is a supported UI locale tag.
type Locale string

const (
	LocaleEN Locale = "en-US"
	LocaleZH Locale = "zh-CN"

	// Default is used when no locale is stored or an unknown one is requested.
	Default = LocaleEN
)

// Normalize maps an arbitrary locale string (e.g. from app_settings or the
// front-end) onto a supported Locale, falling back to Default.
func Normalize(s string) Locale {
	switch {
	case len(s) >= 2 && s[:2] == "zh":
		return LocaleZH
	case len(s) >= 2 && s[:2] == "en":
		return LocaleEN
	default:
		return Default
	}
}

// T returns the message for key in locale loc, falling back to the Default
// locale and finally to the key itself when nothing matches.
func T(loc Locale, key string) string {
	if m, ok := messages[loc]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if v, ok := messages[Default][key]; ok {
		return v
	}
	return key
}

// messages holds every native-UI string keyed by locale then message key.
var messages = map[Locale]map[string]string{
	LocaleEN: {
		// --- application menu (wailsbridge/menu.go) ---
		"menu.file":           "File",
		"menu.newTab":         "New Tab",
		"menu.closeTab":       "Close Tab",
		"menu.saveSql":        "Save SQL…",
		"menu.openSql":        "Open SQL…",
		"menu.exportResult":   "Export Result…",
		"menu.import":         "Import…",
		"menu.edit":           "Edit",
		"menu.find":           "Find…",
		"menu.view":           "View",
		"menu.toggleSidebar":  "Toggle Sidebar",
		"menu.toggleDevtools": "Toggle DevTools",
		"menu.language":       "Language",
		"menu.query":          "Query",
		"menu.run":            "Run",
		"menu.runSelection":   "Run Selection",
		"menu.explain":        "EXPLAIN",
		"menu.cancel":         "Cancel",
		"menu.window":         "Window",
		"menu.help":           "Help",
		"menu.documentation":  "Documentation",

		// --- shared context-menu verbs ---
		"ctx.open":    "Open",
		"ctx.rename":  "Rename",
		"ctx.delete":  "Delete",
		"ctx.refresh": "Refresh",

		// --- result grid (wailsbridge/contextmenu.go) ---
		"ctx.grid.copyTsv":             "Copy as TSV",
		"ctx.grid.copyInsert":          "Copy as INSERT",
		"ctx.grid.copyUpdate":          "Copy as UPDATE",
		"ctx.grid.copyColumns":         "Copy column names",
		"ctx.grid.copyDataPlusColumns": "Copy data + column names",
		"ctx.grid.setNull":             "Set to NULL",

		// --- tab strip ---
		"ctx.tab.close":       "Close",
		"ctx.tab.closeOthers": "Close Others",
		"ctx.tab.closeLeft":   "Close to the Left",
		"ctx.tab.closeRight":  "Close to the Right",

		// --- table node / overview ---
		"ctx.table.edit":        "Modify",
		"ctx.table.truncate":    "Truncate",
		"ctx.table.refreshCols": "Refresh Columns",

		// --- object tree groups ---
		"ctx.tree.newTable":     "New Table",
		"ctx.tree.newDatabase":  "New Database",
		"ctx.tree.editDatabase": "Edit Database",
		"ctx.tree.newQuery":     "New Query",

		// --- connection row ---
		"ctx.conn.connect":    "Connect",
		"ctx.conn.disconnect": "Disconnect",
		"ctx.conn.edit":       "Edit",

		// --- connection sidebar groups ---
		"ctx.sidebar.newGroup": "New Group",

		// --- native child-window titles (internal/services/system_service.go) ---
		"window.newConnection":  "New Connection",
		"window.editConnection": "Edit Connection",
		"window.newDatabase":    "New Database",
		"window.editDatabase":   "Edit Database",
		"window.dataTransfer":   "Data Sync",
	},
	LocaleZH: {
		"menu.file":           "文件",
		"menu.newTab":         "新建标签页",
		"menu.closeTab":       "关闭标签页",
		"menu.saveSql":        "保存 SQL…",
		"menu.openSql":        "打开 SQL…",
		"menu.exportResult":   "导出结果…",
		"menu.import":         "导入…",
		"menu.edit":           "编辑",
		"menu.find":           "查找…",
		"menu.view":           "视图",
		"menu.toggleSidebar":  "切换侧边栏",
		"menu.toggleDevtools": "切换开发者工具",
		"menu.language":       "语言",
		"menu.query":          "查询",
		"menu.run":            "运行",
		"menu.runSelection":   "运行选中",
		"menu.explain":        "EXPLAIN",
		"menu.cancel":         "取消",
		"menu.window":         "窗口",
		"menu.help":           "帮助",
		"menu.documentation":  "文档",

		"ctx.open":    "打开",
		"ctx.rename":  "重命名",
		"ctx.delete":  "删除",
		"ctx.refresh": "刷新",

		"ctx.grid.copyTsv":             "复制为 TSV",
		"ctx.grid.copyInsert":          "复制为 INSERT",
		"ctx.grid.copyUpdate":          "复制为 UPDATE",
		"ctx.grid.copyColumns":         "复制列名",
		"ctx.grid.copyDataPlusColumns": "复制数据 + 列名",
		"ctx.grid.setNull":             "设置为 NULL",

		"ctx.tab.close":       "关闭",
		"ctx.tab.closeOthers": "关闭其他",
		"ctx.tab.closeLeft":   "关闭左侧",
		"ctx.tab.closeRight":  "关闭右侧",

		"ctx.table.edit":        "修改",
		"ctx.table.truncate":    "清空",
		"ctx.table.refreshCols": "刷新列",

		"ctx.tree.newTable":     "新建表",
		"ctx.tree.newDatabase":  "新建数据库",
		"ctx.tree.editDatabase": "编辑数据库",
		"ctx.tree.newQuery":     "新建查询",

		"ctx.conn.connect":    "打开连接",
		"ctx.conn.disconnect": "断开连接",
		"ctx.conn.edit":       "编辑",

		"ctx.sidebar.newGroup": "新建分组",

		"window.newConnection":  "新建连接",
		"window.editConnection": "编辑连接",
		"window.newDatabase":    "新建数据库",
		"window.editDatabase":   "编辑数据库",
		"window.dataTransfer":   "数据同步",
	},
}
