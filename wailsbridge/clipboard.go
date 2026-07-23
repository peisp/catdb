package wailsbridge

// SetClipboardText writes text to the native system clipboard. The WebView's
// navigator.clipboard is permission-gated (WKWebView denies it outright with
// NotAllowedError), so all front-end copy actions route through here instead.
// Returns false when the platform write failed or the app is not up yet.
func SetClipboardText(text string) bool {
	a := App()
	if a == nil {
		return false
	}
	return a.Clipboard.SetText(text)
}
