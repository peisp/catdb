package services

import (
	"context"

	"catdb/internal/storage"
	"catdb/wailsbridge"
)

const settingsKeyLocale = "ui.locale"

// SettingsService exposes user preferences (currently the UI locale) to the
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
	return nil
}
