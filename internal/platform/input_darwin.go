//go:build darwin

package platform

/*
#cgo LDFLAGS: -framework Carbon
#include <Carbon/Carbon.h>

static void switchToEnglishInput(void) {
    CFArrayRef sources = TISCreateInputSourceList(NULL, false);
    if (!sources) return;

    CFIndex count = CFArrayGetCount(sources);
    for (CFIndex i = 0; i < count; i++) {
        TISInputSourceRef src = (TISInputSourceRef)CFArrayGetValueAtIndex(sources, i);
        if (!src) continue;
        CFStringRef sid = (CFStringRef)TISGetInputSourceProperty(src, kTISPropertyInputSourceID);
        if (!sid) continue;
        if (CFStringHasPrefix(sid, CFSTR("com.apple.keylayout.US")) ||
            CFStringHasPrefix(sid, CFSTR("com.apple.keylayout.ABC"))) {
            TISSelectInputSource(src);
            break;
        }
    }
    CFRelease(sources);
}
*/
import "C"

// SwitchToEnglishInputSource switches the current macOS input source to the US
// English keyboard layout if available. No-op if US/ABC English layout is not
// installed (uncommon on macOS).
func SwitchToEnglishInputSource() {
	C.switchToEnglishInput()
}
