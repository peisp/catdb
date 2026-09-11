//go:build windows

package wailsbridge

// Windows half of the DBeaver UI-automation compatibility surface
// (docs/CLI_CON_DESIGN.md §6). It exists to satisfy the four observable
// contracts an external launcher (bhmultauth-style bastion client) relies on:
//
//  1. FindWindow('SWT_Window0', None) finds a top-level window → EnsureDBeaverMarker
//     registers that class and creates one hidden, never-destroyed window
//     titled "DBeaver Compatibility (catdb)". Cross-process GetWindowText reads
//     the cached title, so the probe needs no cooperation from our pump.
//  2. FindWindow(None, "'<name>' 身份验证") finds a top-level dialog → the title
//     built by authDialogTitle is byte-for-byte that string for zh locales, and
//     "Authentication for '<name>'" otherwise.
//  3. EnumChildWindows finds, among the direct children: an Edit whose text
//     equals the account (read-only, pre-filled), an Edit whose text differs
//     (the ES_PASSWORD one, initially empty), and a control whose text is the
//     literal "OK" (never translated).
//  4. The launcher injects with SendMessage(WM_SETTEXT) on the password Edit
//     and SendMessage(WM_LBUTTONDOWN/WM_LBUTTONUP, MK_LBUTTON) on the OK button.
//     Both are native control behaviour and need no code here: Edit stores the
//     text, and BUTTON captures on the down and emits BN_CLICKED → WM_COMMAND
//     on the up. The launcher sleeps ~1s between them, so the WM_GETTEXT we do
//     when handling WM_COMMAND always sees the injected password.
//
// Same rule as the stub file: plain arguments only, no import of
// internal/launch or internal/services.

import (
	"strings"
	"sync"
	"sync/atomic"
)

const (
	markerClass = "SWT_Window0"
	markerTitle = "DBeaver Compatibility (catdb)"

	authDialogClass = "catdbAuthDialog"

	// Control ids. idOK doubles as the OK button's id so that both a real click
	// and IsDialogMessageW's Enter handling arrive as WM_COMMAND(IDOK).
	idUserEdit     uintptr = 100
	idPasswordEdit uintptr = 101
)

// EnsureDBeaverMarker creates the hidden SWT_Window0 marker window external
// launchers probe for (docs/CLI_CON_DESIGN.md §6.1). Idempotent.
func EnsureDBeaverMarker() {
	markerOnce.Do(func() {
		done := make(chan struct{})
		if !win32Do(func() {
			defer close(done)
			createMarkerWindow()
		}) {
			return
		}
		<-done
	})
}

var (
	markerOnce sync.Once
	markerHWND uintptr // win32 thread only
)

func createMarkerWindow() {
	if markerHWND != 0 {
		return
	}
	if !registerWindowClass(markerClass, markerWndProcPtr) {
		return
	}
	// A real top-level window, not a message-only one: HWND_MESSAGE children
	// are invisible to FindWindow. It is never shown (no WS_VISIBLE) and never
	// destroyed — the process exit reclaims it.
	markerHWND = createWindow(0, markerClass, markerTitle, wsOverlapped|wsCaption, 0, 0, 1, 1, 0, 0)
}

var markerWndProcPtr = newWndProc(func(hwnd, msg, wParam, lParam uintptr) uintptr {
	return defWindowProc(hwnd, msg, wParam, lParam)
})

// ---------------------------------------------------------------------------
// Native authentication dialog
// ---------------------------------------------------------------------------

type promptResult struct {
	password string
	ok       bool
}

type dialogState struct {
	passwordEdit uintptr
	result       chan promptResult
	sent         bool
}

var (
	// promptMu serialises prompts: only one authentication dialog may exist at
	// a time, otherwise FindWindow-by-title automation could hit the wrong one.
	promptMu sync.Mutex

	// dialogStates is touched only on the win32 thread.
	dialogStates = map[uintptr]*dialogState{}

	authClassOnce sync.Once
	authClassOK   bool
)

// PromptPasswordNative shows the Win32-native authentication dialog and blocks
// until the user (or the external launcher) confirms, cancels, or the caller
// cancels. handled is always true on Windows: the native dialog is the only
// prompt here, for automation and humans alike.
func PromptPasswordNative(name, user, locale string, cancel <-chan struct{}) (password string, ok bool, handled bool) {
	promptMu.Lock()
	defer promptMu.Unlock()

	result := make(chan promptResult, 1) // buffered: the WndProc must never block
	created := make(chan uintptr, 1)

	if !win32Do(func() { created <- createAuthDialog(name, user, locale, result) }) {
		return "", false, true
	}
	hwnd := <-created
	if hwnd == 0 {
		return "", false, true
	}

	// Watcher: turn caller cancellation into a WM_CLOSE on the win32 thread.
	// It always exits — either the cancel fires or the result arrives.
	done := make(chan struct{})
	defer close(done)
	var closed atomic.Bool
	go func() {
		select {
		case <-cancel:
			if !closed.Load() {
				// Fails harmlessly if the window is already gone.
				procPostMessageW.Call(hwnd, wmClose, 0, 0)
			}
		case <-done:
		}
	}()

	r := <-result
	closed.Store(true)
	return r.password, r.ok, true
}

func createAuthDialog(name, user, locale string, result chan promptResult) uintptr {
	authClassOnce.Do(func() {
		authClassOK = registerWindowClass(authDialogClass, authWndProcPtr)
	})
	if !authClassOK {
		return 0
	}

	const clientW, clientH int32 = 360, 170
	style := wsOverlapped | wsCaption | wsSysMenu
	x, y, w, h := centeredWindowRect(clientW, clientH, style)

	hwnd := createWindow(0, authDialogClass, authDialogTitle(name, locale), style, x, y, w, h, 0, 0)
	if hwnd == 0 {
		return 0
	}

	zh := isZhLocale(locale)
	userLabel, passwordLabel := "User:", "Password:"
	if zh {
		userLabel, passwordLabel = "用户：", "密码："
	}

	// Direct children only — that is all EnumChildWindows needs to walk.
	mkStatic := func(text string, top int32) {
		c := createWindow(0, "STATIC", text, wsChild|wsVisible|ssLeft, 16, top, 76, 20, hwnd, 0)
		applyGUIFont(c)
	}
	mkStatic(userLabel, 22)
	mkStatic(passwordLabel, 62)

	// Contract 3a: text equals the account, character for character.
	userEdit := createWindow(0, "Edit", user,
		wsChild|wsVisible|wsBorder|wsGroup|esLeft|esAutoHScr|esReadOnly,
		100, 18, 244, 24, hwnd, idUserEdit)
	applyGUIFont(userEdit)

	// Contract 3b: an Edit whose text differs from the account (empty).
	passwordEdit := createWindow(0, "Edit", "",
		wsChild|wsVisible|wsBorder|wsTabStop|esLeft|esAutoHScr|esPassword,
		100, 58, 244, 24, hwnd, idPasswordEdit)
	applyGUIFont(passwordEdit)

	// Contract 3c: text is the literal "OK" in every locale.
	okButton := createWindow(0, "BUTTON", "OK",
		wsChild|wsVisible|wsTabStop|bsDefPushBtn,
		248, 112, 96, 30, hwnd, idOK)
	applyGUIFont(okButton)

	dialogStates[hwnd] = &dialogState{passwordEdit: passwordEdit, result: result}
	activeDialog = hwnd

	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWin.Call(hwnd) // best effort; ignored when we lack focus rights
	procSetFocus.Call(passwordEdit)
	return hwnd
}

// centeredWindowRect converts a desired client size into a screen-centered
// window rect for the given style.
func centeredWindowRect(clientW, clientH int32, style uintptr) (x, y, w, h int32) {
	rc := win32Rect{0, 0, clientW, clientH}
	adjustWindowRect(&rc, style)
	w, h = rc.right-rc.left, rc.bottom-rc.top
	if w <= 0 || h <= 0 {
		w, h = clientW, clientH
	}
	x = (getSystemMetrics(smCXScreen) - w) / 2
	y = (getSystemMetrics(smCYScreen) - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y, w, h
}

var authWndProcPtr = newWndProc(authWndProc)

func authWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case dmGetDefID:
		// Makes IsDialogMessageW treat the OK button as the default button, so
		// Enter produces WM_COMMAND(IDOK) exactly like a click does.
		return dcHasDefID<<16 | idOK

	case wmCommand:
		st := dialogStates[hwnd]
		if st == nil {
			break
		}
		switch loword(wParam) {
		case idOK:
			// Reached both from a real BN_CLICKED (including the launcher's
			// synthesised WM_LBUTTONDOWN/UP pair) and from Enter.
			finishDialog(st, promptResult{password: controlText(st.passwordEdit), ok: true})
			destroyWindow(hwnd)
			return 0
		case idCancel: // Esc, synthesised by IsDialogMessageW
			finishDialog(st, promptResult{})
			destroyWindow(hwnd)
			return 0
		}

	case wmClose:
		if st := dialogStates[hwnd]; st != nil {
			finishDialog(st, promptResult{})
		}
		destroyWindow(hwnd)
		return 0

	case wmDestroy:
		// Safety net: whatever tore the window down, the waiting caller gets an
		// answer instead of blocking forever.
		if st := dialogStates[hwnd]; st != nil {
			finishDialog(st, promptResult{})
			delete(dialogStates, hwnd)
		}
		if activeDialog == hwnd {
			activeDialog = 0
		}
		return 0
	}
	return defWindowProc(hwnd, msg, wParam, lParam)
}

func finishDialog(st *dialogState, r promptResult) {
	if st.sent {
		return
	}
	st.sent = true
	st.result <- r // buffered, never blocks the win32 thread
}

// authDialogTitle builds the title the external launcher matches exactly.
// The zh form is U+0027 quote + name + U+0027 + one ASCII space + 身份验证.
func authDialogTitle(name, locale string) string {
	if isZhLocale(locale) {
		return "'" + name + "' 身份验证"
	}
	return "Authentication for '" + name + "'"
}

// isZhLocale matches the -nl values DBeaver accepts for Chinese: zh, zh_CN,
// zh-CN, any case.
func isZhLocale(locale string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "zh")
}
