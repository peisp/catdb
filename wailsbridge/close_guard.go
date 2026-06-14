package wailsbridge

import (
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// dirtyTabs is the count of unsaved SQL tabs across the front-end. Updated
// by SystemService.SetDirtyTabs from the JS layer. The window-close hook
// reads this to decide whether to block the close.
var dirtyTabs atomic.Int32

// SetDirtyTabs is called from the front-end via SystemService. Negative
// values are clamped to zero.
func SetDirtyTabs(n int) {
	if n < 0 {
		n = 0
	}
	dirtyTabs.Store(int32(n))
}

// DirtyTabs returns the current dirty-tab count.
func DirtyTabs() int { return int(dirtyTabs.Load()) }

// closeOverride is set by the front-end through SystemService.AllowNextClose
// when the user confirmed the discard prompt. It causes the next
// WindowClosing event to bypass the guard.
var (
	closeOverrideMu sync.Mutex
	closeOverride   bool
)

// AllowNextClose makes the next window-close attempt succeed even with dirty
// tabs. Called from the front-end after the user confirms the discard prompt.
func AllowNextClose() {
	closeOverrideMu.Lock()
	defer closeOverrideMu.Unlock()
	closeOverride = true
}

// consumeCloseOverride checks-and-clears the override flag atomically.
func consumeCloseOverride() bool {
	closeOverrideMu.Lock()
	defer closeOverrideMu.Unlock()
	if closeOverride {
		closeOverride = false
		return true
	}
	return false
}

// AttachCloseGuard wires the close-guard hook on the given window. The hook
// cancels the close and emits `window:close-blocked` when dirty tabs exist,
// unless AllowNextClose has been set.
func AttachCloseGuard(w *application.WebviewWindow) {
	w.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if DirtyTabs() == 0 {
			return
		}
		if consumeCloseOverride() {
			return
		}
		event.Cancel()
		Emit("window:close-blocked", map[string]any{
			"dirtyTabs": DirtyTabs(),
		})
	})
}
