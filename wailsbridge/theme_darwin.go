//go:build darwin

package wailsbridge

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include <string.h>
#import <Cocoa/Cocoa.h>

static NSAppearance *catdbAppearance(const char *name) {
	if (name == NULL || strlen(name) == 0) {
		return nil; // nil restores "inherit / follow the system"
	}
	return [NSAppearance appearanceNamed:[NSString stringWithUTF8String:name]];
}

// catdbSetAppAppearance drives NSApp, which covers native panels, popups and
// any window that has not been given an explicit appearance of its own.
static void catdbSetAppAppearance(const char *name) {
	if (NSApp == nil) {
		return;
	}
	[NSApp setAppearance:catdbAppearance(name)];
}

// catdbSetWindowAppearance is the same call Wails makes at window-creation time
// (webview_window_darwin.go:617 windowSetAppearanceTypeByName), applied to an
// already-open window so the switch is live.
static void catdbSetWindowAppearance(void *nsWindow, const char *name) {
	if (nsWindow == NULL) {
		return;
	}
	[(NSWindow *)nsWindow setAppearance:catdbAppearance(name)];
}

static bool catdbSystemIsDark(void) {
	if (NSApp == nil) {
		return false;
	}
	NSString *style = [[NSUserDefaults standardUserDefaults] stringForKey:@"AppleInterfaceStyle"];
	return style != nil && [style caseInsensitiveCompare:@"Dark"] == NSOrderedSame;
}
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// macAppearanceName is the NSAppearance name for a preference; "" = follow system.
func macAppearanceName(pref string) string {
	switch pref {
	case ThemeLight:
		return string(application.NSAppearanceNameAqua)
	case ThemeDark:
		return string(application.NSAppearanceNameDarkAqua)
	default:
		return ""
	}
}

// applyNativeTheme flips the appearance of NSApp and every open window. Because
// WKWebView derives `prefers-color-scheme` from its window's effective
// appearance, this also flips the front-end's media query — the titlebar,
// translucent backdrop, scrollbars and web content all switch together.
//
// Wails v3.0.0-beta.4 has no public runtime theme setter (Mac.Appearance is
// creation-time only, and WebviewWindow exposes no SetTheme), so we drive the
// NSWindow handle it *does* expose — Window.NativeWindow() (window.go:91).
func applyNativeTheme(pref string) {
	a := App()
	if a == nil {
		return
	}
	name := macAppearanceName(pref)
	// Native AppKit calls must run on the main thread.
	application.InvokeAsync(func() {
		cName := C.CString(name)
		defer C.free(unsafe.Pointer(cName))
		C.catdbSetAppAppearance(cName)
		for _, w := range a.Window.GetAll() {
			if h := w.NativeWindow(); h != nil {
				C.catdbSetWindowAppearance(h, cName)
			}
		}
	})
}

func isDarkTheme() bool {
	switch CurrentTheme() {
	case ThemeDark:
		return true
	case ThemeLight:
		return false
	default:
		return bool(C.catdbSystemIsDark())
	}
}
