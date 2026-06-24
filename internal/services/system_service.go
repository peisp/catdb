package services

import (
	"context"
	"net/url"
	"strings"

	"catdb/wailsbridge"
)

// SystemService exposes a small set of OS-level helpers to the front-end:
// native file dialogs, message dialogs. Routes through wailsbridge so the
// Wails alpha API never leaks into the components directly.
type SystemService struct{}

func NewSystemService() *SystemService       { return &SystemService{} }
func (s *SystemService) ServiceName() string { return "SystemService" }

// FileFilterDescriptor is the JSON-shaped filter the front-end sends.
type FileFilterDescriptor struct {
	DisplayName string `json:"displayName"`
	Pattern     string `json:"pattern"`
}

// PickSaveFile shows the native Save dialog and returns the chosen path, or
// "" if the user cancelled.
func (s *SystemService) PickSaveFile(_ context.Context, title, defaultName string, filters []FileFilterDescriptor) (string, error) {
	return wailsbridge.SaveFileSimple(title, defaultName, toSimpleFilters(filters))
}

// PickOpenFile shows the native Open dialog and returns the chosen path, or
// "" if the user cancelled.
func (s *SystemService) PickOpenFile(_ context.Context, title string, filters []FileFilterDescriptor) (string, error) {
	return wailsbridge.OpenFileSimple(title, toSimpleFilters(filters))
}

// ShowInfo / ShowError trigger native message dialogs.
func (s *SystemService) ShowInfo(_ context.Context, title, message string) {
	wailsbridge.Info(title, message)
}
func (s *SystemService) ShowError(_ context.Context, title, message string) {
	wailsbridge.Error(title, message)
}

// SetDirtyTabs reports how many unsaved query tabs exist. The Go-side
// close-guard reads this on WindowClosing to decide whether to block.
func (s *SystemService) SetDirtyTabs(_ context.Context, count int) {
	wailsbridge.SetDirtyTabs(count)
}

// AllowNextClose lets the next WindowClosing succeed even with dirty tabs.
// Call after the front-end has shown its confirm dialog and the user
// accepted "Discard & Close".
func (s *SystemService) AllowNextClose(_ context.Context) {
	wailsbridge.AllowNextClose()
}

// OpenConnectionEditor pops the connection editor as its own native window.
// `driver` is the driver name (e.g. "mysql") and `connID` is the profile id
// to edit (empty string for a new-connection flow).
//
// The auxiliary window is keyed by name "connection-editor", so re-opening
// while it is already on screen just brings it forward with the new params
// instead of stacking duplicates.
func (s *SystemService) OpenConnectionEditor(_ context.Context, driver, connID string) {
	q := url.Values{}
	if driver != "" {
		q.Set("driver", driver)
	}
	if connID != "" {
		q.Set("id", connID)
	}
	// Hash route — same `index.html` is served, the SPA picks the editor page
	// off the hash. Query lives after the hash so the browser doesn't try to
	// look up `?driver=…` as a real query string.
	target := "/#/connection-editor"
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	title := wailsbridge.Tr("window.newConnection")
	if connID != "" {
		title = wailsbridge.Tr("window.editConnection")
	}
	if strings.TrimSpace(driver) != "" {
		title += " — " + driver
	}
	wailsbridge.OpenChildWindow("connection-editor", title, target, 720, 600)
}

// OpenDatabaseEditor pops the "新建/编辑数据库" form as its own native window,
// reusing the catdb-tree-database right-click flow. Pass an empty dbName for
// create mode; a non-empty dbName for edit mode.
//
// The auxiliary window is keyed by name "database-editor" so re-opening it
// (e.g. user right-clicks another DB while it's already open) brings the
// existing window forward with the new params instead of stacking duplicates.
func (s *SystemService) OpenDatabaseEditor(_ context.Context, connID, dbName string) {
	q := url.Values{}
	if connID != "" {
		q.Set("connId", connID)
	}
	if dbName != "" {
		q.Set("db", dbName)
	}
	target := "/#/database-editor"
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	title := wailsbridge.Tr("window.newDatabase")
	if dbName != "" {
		title = wailsbridge.Tr("window.editDatabase") + " — " + dbName
	}
	wailsbridge.OpenChildWindow("database-editor", title, target, 600, 520)
}

// BroadcastDatabaseSaved tells every window that a database was created or
// altered. The main window's ObjectTree listens for this and refreshes the
// matching connection's tree.
func (s *SystemService) BroadcastDatabaseSaved(_ context.Context, connID, dbName string) {
	wailsbridge.Emit("database:saved", map[string]any{"connId": connID, "db": dbName})
}

// OpenExternalURL opens the given URL in the user's default browser.
// Used by features like the update dialog's "view on GitHub" link — a plain
// <a target="_blank"> inside the WebView either no-ops or navigates the
// WebView itself, neither of which is what we want.
func (s *SystemService) OpenExternalURL(_ context.Context, target string) error {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	return wailsbridge.OpenURL(target)
}

// BroadcastConnectionSaved tells every window that a connection was saved.
// Used by the connection-editor child window to nudge the main window into
// refreshing its sidebar list.
func (s *SystemService) BroadcastConnectionSaved(_ context.Context, connID string) {
	wailsbridge.Emit("connection:saved", map[string]any{"id": connID})
}

func toSimpleFilters(in []FileFilterDescriptor) []wailsbridge.SimpleFilter {
	out := make([]wailsbridge.SimpleFilter, len(in))
	for i, f := range in {
		out[i] = wailsbridge.SimpleFilter{DisplayName: f.DisplayName, Pattern: f.Pattern}
	}
	return out
}
