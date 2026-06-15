//go:build !darwin

package platform

// SwitchToEnglishInputSource is a no-op on non-macOS platforms.
func SwitchToEnglishInputSource() {}
