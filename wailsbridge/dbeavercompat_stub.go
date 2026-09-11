//go:build !windows

package wailsbridge

// Non-Windows half of the DBeaver UI-automation compatibility surface
// (docs/CLI_CON_DESIGN.md §6). The contracts it implements on Windows — a
// findable marker window and a native authentication dialog — are Win32
// concepts with no counterpart here, so both entry points are no-ops and the
// launch flow falls back to the front-end modal.
//
// These are deliberately plain functions taking plain arguments: wailsbridge
// must not import internal/launch or internal/services (that would close an
// import cycle through main), so the prompt types stay on the launch side.

// EnsureDBeaverMarker creates the hidden SWT_Window0 marker window external
// launchers probe for (docs/CLI_CON_DESIGN.md §6.1). Idempotent. No-op until
// the native implementation lands / on non-Windows.
func EnsureDBeaverMarker() {}

// PromptPasswordNative shows the Win32-native authentication dialog.
// handled=false means "no native dialog on this platform" — the caller falls
// back to the front-end modal.
func PromptPasswordNative(name, user, locale string, cancel <-chan struct{}) (password string, ok bool, handled bool) {
	return "", false, false
}
