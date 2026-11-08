package services

import (
	"context"

	"catdb/internal/storage"
	"catdb/wailsbridge"
)

const (
	settingsKeyLocale = "ui.locale"
	settingsKeyTheme  = "ui.theme"
)

// themeSystem is the default theme preference: follow the OS appearance.
const themeSystem = "system"

// normalizeTheme maps an arbitrary stored/incoming value onto the three legal
// theme preferences, falling back to "system" for empty or unknown input.
func normalizeTheme(theme string) string {
	switch theme {
	case "light", "dark", themeSystem:
		return theme
	default:
		return themeSystem
	}
}

// SettingsService exposes user preferences (UI locale, theme) to the
// front-end. Values are persisted in the app_settings key/value table.
type SettingsService struct {
	store *storage.Store
}

func NewSettingsService(store *storage.Store) *SettingsService { return &SettingsService{store: store} }
func (s *SettingsService) ServiceName() string                 { return "SettingsService" }

// GetLocale returns the persisted UI locale (e.g. "en-US", "zh-CN"), or ""
// when the user has never chosen one — the front-end then falls back to the
// system locale.
func (s *SettingsService) GetLocale(ctx context.Context) (string, error) {
	return s.store.GetSetting(ctx, settingsKeyLocale)
}

// SetLocale persists the chosen UI locale and rebuilds the native menus
// (app menu + context menus) so they switch language in step with the WebView.
func (s *SettingsService) SetLocale(ctx context.Context, locale string) error {
	if err := s.store.SetSetting(ctx, settingsKeyLocale, locale); err != nil {
		return err
	}
	wailsbridge.SetMenuLocale(locale)
	// Broadcast so every window's WebView switches locale at runtime, even the
	// ones that didn't trigger the change (e.g. the settings child window).
	wailsbridge.Emit("app:locale-changed", map[string]any{"locale": locale})
	return nil
}

// GetTheme returns the persisted theme preference — "light", "dark" or
// "system". Never stored/unknown values normalise to "system".
func (s *SettingsService) GetTheme(ctx context.Context) (string, error) {
	stored, err := s.store.GetSetting(ctx, settingsKeyTheme)
	if err != nil {
		return "", err
	}
	return normalizeTheme(stored), nil
}

// SetTheme persists the chosen theme preference and broadcasts it so every
// window's WebView re-applies its design tokens at runtime.
func (s *SettingsService) SetTheme(ctx context.Context, theme string) error {
	theme = normalizeTheme(theme)
	if err := s.store.SetSetting(ctx, settingsKeyTheme, theme); err != nil {
		return err
	}
	// Flip the native appearance first (titlebar / backdrop / webview colour
	// scheme), then tell every WebView so the CSS tokens follow.
	wailsbridge.ApplyTheme(theme)
	wailsbridge.Emit("app:theme-changed", map[string]any{"theme": theme})
	return nil
}
