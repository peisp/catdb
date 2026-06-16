package wailsbridge

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// File dialog filters used by import / export flows. Add filters here rather
// than scattering them across Services.
var (
	FilterSQL = application.FileFilter{
		DisplayName: "SQL files (*.sql)",
		Pattern:     "*.sql",
	}
	FilterCSV = application.FileFilter{
		DisplayName: "CSV files (*.csv)",
		Pattern:     "*.csv",
	}
	FilterJSON = application.FileFilter{
		DisplayName: "JSON files (*.json)",
		Pattern:     "*.json",
	}
	FilterXLSX = application.FileFilter{
		DisplayName: "Excel files (*.xlsx)",
		Pattern:     "*.xlsx",
	}
	FilterAll = application.FileFilter{
		DisplayName: "All files",
		Pattern:     "*",
	}
)

// OpenFile shows a native open-file dialog. Returns "" if the user cancels.
func OpenFile(title string, filters ...application.FileFilter) (string, error) {
	a := App()
	if a == nil {
		return "", nil
	}
	d := a.Dialog.OpenFile()
	if title != "" {
		d.SetTitle(title)
	}
	for _, f := range filters {
		d.AddFilter(f.DisplayName, f.Pattern)
	}
	return d.PromptForSingleSelection()
}

// SaveFile shows a native save-file dialog. Returns "" if the user cancels.
// Note: SaveFileDialogStruct has no SetTitle in alpha.96; the title field is
// presented in OS-specific ways and can be added once the API stabilizes.
func SaveFile(message, defaultFilename string, filters ...application.FileFilter) (string, error) {
	a := App()
	if a == nil {
		return "", nil
	}
	d := a.Dialog.SaveFile()
	if message != "" {
		d.SetMessage(message)
	}
	if defaultFilename != "" {
		d.SetFilename(defaultFilename)
	}
	for _, f := range filters {
		d.AddFilter(f.DisplayName, f.Pattern)
	}
	return d.PromptForSingleSelection()
}

// SimpleFilter is a JSON-friendly file filter shape callers can pass through
// without importing the Wails application package. Re-exported via
// (Open|Save)FileSimple.
type SimpleFilter struct {
	DisplayName string `json:"displayName"`
	Pattern     string `json:"pattern"`
}

// OpenFileSimple is OpenFile with the SimpleFilter shape.
func OpenFileSimple(title string, filters []SimpleFilter) (string, error) {
	return OpenFile(title, simpleToFilters(filters)...)
}

// SaveFileSimple is SaveFile with the SimpleFilter shape.
func SaveFileSimple(message, defaultFilename string, filters []SimpleFilter) (string, error) {
	return SaveFile(message, defaultFilename, simpleToFilters(filters)...)
}

func simpleToFilters(in []SimpleFilter) []application.FileFilter {
	out := make([]application.FileFilter, len(in))
	for i, f := range in {
		out[i] = application.FileFilter{DisplayName: f.DisplayName, Pattern: f.Pattern}
	}
	return out
}

// Info shows a native information dialog.
func Info(title, message string) {
	a := App()
	if a == nil {
		return
	}
	a.Dialog.Info().SetTitle(title).SetMessage(message).Show()
}

// Error shows a native error dialog.
func Error(title, message string) {
	a := App()
	if a == nil {
		return
	}
	a.Dialog.Error().SetTitle(title).SetMessage(message).Show()
}

// Question shows a native Yes/No confirmation dialog. Returns true when the
// user clicks Yes. Defaults to false when App() is nil or the dialog is
// dismissed without clicking any button.
func Question(title, message string) bool {
	a := App()
	if a == nil {
		return false
	}
	result := make(chan bool, 1)
	d := a.Dialog.Question()
	d.SetTitle(title).SetMessage(message)
	yesBtn := d.AddButton("Yes")
	yesBtn.OnClick(func() {
		select {
		case result <- true:
		default:
		}
	})
	noBtn := d.AddButton("No")
	noBtn.OnClick(func() {
		select {
		case result <- false:
		default:
		}
	})
	noBtn.SetAsCancel()
	yesBtn.SetAsDefault()
	d.Show()
	select {
	case r := <-result:
		return r
	default:
		return false
	}
}
