//go:build windows

package wailsbridge

// Win32 plumbing for the DBeaver UI-automation compatibility surface
// (docs/CLI_CON_DESIGN.md §6): raw user32/gdi32/kernel32 bindings plus the
// dedicated OS thread that owns every window this file creates.
//
// Everything here is deliberately hand-rolled on top of golang.org/x/sys/windows
// (already an indirect dependency of Wails) — no third-party Win32 wrapper.
// Constant values are copied from the Windows SDK headers noted per block.

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Window / control styles — WinUser.h.
const (
	wsOverlapped uintptr = 0x00000000
	wsCaption    uintptr = 0x00C00000
	wsSysMenu    uintptr = 0x00080000
	wsVisible    uintptr = 0x10000000
	wsChild      uintptr = 0x40000000
	wsBorder     uintptr = 0x00800000
	wsTabStop    uintptr = 0x00010000
	wsGroup      uintptr = 0x00020000

	esLeft       uintptr = 0x0000
	esAutoHScr   uintptr = 0x0080
	esPassword   uintptr = 0x0020
	esReadOnly   uintptr = 0x0800
	ssLeft       uintptr = 0x0000
	bsDefPushBtn uintptr = 0x0001
)

// Window messages — WinUser.h.
const (
	wmDestroy       uintptr = 0x0002
	wmClose         uintptr = 0x0010
	wmGetText       uintptr = 0x000D
	wmGetTextLength uintptr = 0x000E
	wmSetFont       uintptr = 0x0030
	wmCommand       uintptr = 0x0111

	// DM_GETDEFID is WM_USER+0; answering it makes IsDialogMessageW treat our
	// plain window like a dialog box, so Enter fires the default button.
	dmGetDefID uintptr = 0x0400
	dcHasDefID uintptr = 0x534B

	// WM_APP+1: thread message used to wake the win32 pump for queued work.
	wmAppRunTasks uint32 = 0x8000 + 1
)

// Misc — WinUser.h / WinGDI.h.
const (
	idOK     uintptr = 1
	idCancel uintptr = 2

	csVRedraw uint32 = 0x0001
	csHRedraw uint32 = 0x0002

	idcArrow      uintptr = 32512
	colorBtnFace  uintptr = 15
	swShow        uintptr = 5
	smCXScreen    uintptr = 0
	smCYScreen    uintptr = 1
	defaultGUIFnt uintptr = 17
	pmNoRemove    uintptr = 0x0000
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type win32Point struct {
	x, y int32
}

type win32Rect struct {
	left, top, right, bottom int32
}

type win32Msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      win32Point
	private uint32
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW    = user32.NewProc("CreateWindowExW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procDefWindowProcW     = user32.NewProc("DefWindowProcW")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPeekMessageW       = user32.NewProc("PeekMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procIsDialogMessageW   = user32.NewProc("IsDialogMessageW")
	procPostMessageW       = user32.NewProc("PostMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procSendMessageW       = user32.NewProc("SendMessageW")
	procShowWindow         = user32.NewProc("ShowWindow")
	procSetForegroundWin   = user32.NewProc("SetForegroundWindow")
	procSetFocus           = user32.NewProc("SetFocus")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procAdjustWindowRect   = user32.NewProc("AdjustWindowRect")
	procLoadCursorW        = user32.NewProc("LoadCursorW")

	procGetStockObject = gdi32.NewProc("GetStockObject")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

// newWndProc turns a Go window procedure into a stdcall function pointer.
// Only ever called from package-level initialisers: syscall.NewCallback
// allocates a permanent thunk from a small fixed pool.
func newWndProc(fn func(hwnd, msg, wParam, lParam uintptr) uintptr) uintptr {
	return windows.NewCallback(fn)
}

func adjustWindowRect(rc *win32Rect, style uintptr) {
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(rc)), style, 0)
}

func defWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func sendMessage(hwnd, msg, wParam, lParam uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(hwnd, msg, wParam, lParam)
	return r
}

func destroyWindow(hwnd uintptr) {
	procDestroyWindow.Call(hwnd)
}

func getSystemMetrics(index uintptr) int32 {
	r, _, _ := procGetSystemMetrics.Call(index)
	return int32(r)
}

func moduleHandle() uintptr {
	h, _, _ := procGetModuleHandleW.Call(0)
	return h
}

func loword(v uintptr) uintptr {
	return v & 0xFFFF
}

// utf16 converts a Go string to a NUL-terminated UTF-16 buffer. It never fails:
// embedded NULs are dropped rather than rejected, because a connection name is
// caller data and a bad byte must not take the dialog down.
func utf16(s string) []uint16 {
	cleaned := make([]rune, 0, len(s))
	for _, r := range s {
		if r != 0 {
			cleaned = append(cleaned, r)
		}
	}
	units := windows.StringToUTF16(string(cleaned))
	return units
}

func utf16Ptr(s string) *uint16 {
	return &utf16(s)[0]
}

// registerWindowClass registers a top-level window class; it tolerates the
// "already registered" case so callers can be idempotent.
func registerWindowClass(class string, wndProc uintptr) bool {
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	wc := wndClassExW{
		style:         csHRedraw | csVRedraw,
		lpfnWndProc:   wndProc,
		hInstance:     moduleHandle(),
		hCursor:       cursor,
		hbrBackground: colorBtnFace + 1,
		lpszClassName: utf16Ptr(class),
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	return atom != 0
}

func createWindow(exStyle uintptr, class, title string, style uintptr, x, y, w, h int32, parent, menu uintptr) uintptr {
	// Both UTF-16 buffers must stay referenced as Go POINTERS until the call
	// returns: converting to uintptr ahead of the call would leave the buffer
	// collectable in the gap before the syscall reads it.
	classBuf := utf16(class)
	titleBuf := utf16(title) // "" yields just the NUL terminator — an empty title
	hwnd, _, _ := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(&classBuf[0])),
		uintptr(unsafe.Pointer(&titleBuf[0])),
		style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, menu, moduleHandle(), 0,
	)
	runtime.KeepAlive(classBuf)
	runtime.KeepAlive(titleBuf)
	return hwnd
}

// controlText reads a control's text via WM_GETTEXTLENGTH + WM_GETTEXT. It is
// the same path the external launcher uses to identify controls, and the only
// way to read an ES_PASSWORD edit's contents.
func controlText(hwnd uintptr) string {
	n := sendMessage(hwnd, wmGetTextLength, 0, 0)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	got := sendMessage(hwnd, wmGetText, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if got > uintptr(len(buf)-1) {
		got = uintptr(len(buf) - 1)
	}
	return windows.UTF16ToString(buf[:got])
}

// applyGUIFont gives a control the standard shell dialog font; without it
// CreateWindowExW children render in the ancient bitmap system font.
func applyGUIFont(hwnd uintptr) {
	font, _, _ := procGetStockObject.Call(defaultGUIFnt)
	if font == 0 {
		return
	}
	// lParam = MAKELPARAM(TRUE, 0) → redraw immediately.
	sendMessage(hwnd, wmSetFont, font, 1)
}

// ---------------------------------------------------------------------------
// Dedicated Win32 thread
//
// Every window created by this package lives on one lazily started goroutine
// locked to an OS thread: Win32 delivers a window's messages only to the thread
// that created it, so creation, destruction and the message pump must all
// happen there. Other goroutines hand work over through win32Tasks and wake the
// pump with PostThreadMessageW.
// ---------------------------------------------------------------------------

var (
	win32Once  sync.Once
	win32Ready = make(chan struct{})
	win32Tasks = make(chan func(), 16)
	win32TID   uint32
	win32Dead  atomic.Bool

	// activeDialog is read/written only on the win32 thread.
	activeDialog uintptr
)

// win32Do queues f for execution on the win32 thread. It reports false when the
// thread could not be started or has died, in which case f never runs.
func win32Do(f func()) bool {
	win32Once.Do(func() { go win32Thread() })
	<-win32Ready
	if win32TID == 0 || win32Dead.Load() {
		return false
	}
	win32Tasks <- f
	procPostThreadMessageW.Call(uintptr(win32TID), uintptr(wmAppRunTasks), 0, 0)
	return true
}

func win32Thread() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// PeekMessage forces the thread message queue into existence before anyone
	// can PostThreadMessageW to it (posts to a queue-less thread are dropped).
	var probe win32Msg
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&probe)), 0, uintptr(wmAppRunTasks), uintptr(wmAppRunTasks), pmNoRemove)
	win32TID = windows.GetCurrentThreadId()
	close(win32Ready) // publishes win32TID to every win32Do caller

	win32Pump()
}

func win32Pump() {
	var msg win32Msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		switch int32(r) {
		case -1:
			// Only reachable on an invalid parameter, which cannot happen with
			// the arguments above. Bail out rather than spin the CPU.
			win32Dead.Store(true)
			return
		case 0:
			win32Dead.Store(true)
			return // WM_QUIT
		}
		if msg.hwnd == 0 {
			if msg.message == wmAppRunTasks {
				drainWin32Tasks()
			}
			continue
		}
		// IsDialogMessageW gives our plain window dialog keyboard semantics:
		// Tab/Shift-Tab between WS_TABSTOP controls, Enter → default button,
		// Esc → WM_COMMAND(IDCANCEL). It swallows what it handles.
		if activeDialog != 0 {
			if handled, _, _ := procIsDialogMessageW.Call(activeDialog, uintptr(unsafe.Pointer(&msg))); handled != 0 {
				continue
			}
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func drainWin32Tasks() {
	for {
		select {
		case f := <-win32Tasks:
			f()
		default:
			return
		}
	}
}
