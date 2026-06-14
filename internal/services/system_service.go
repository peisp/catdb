package services

import (
	"context"

	"catdb/wailsbridge"
)

// SystemService exposes a small set of OS-level helpers to the front-end:
// native file dialogs, message dialogs. Routes through wailsbridge so the
// Wails alpha API never leaks into the components directly.
type SystemService struct{}

func NewSystemService() *SystemService    { return &SystemService{} }
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

func toSimpleFilters(in []FileFilterDescriptor) []wailsbridge.SimpleFilter {
	out := make([]wailsbridge.SimpleFilter, len(in))
	for i, f := range in {
		out[i] = wailsbridge.SimpleFilter{DisplayName: f.DisplayName, Pattern: f.Pattern}
	}
	return out
}
