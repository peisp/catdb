// api/settings — front-end facade over SettingsService bindings.
//
// Components import from here, never from `bindings/` directly (CLAUDE.md #1).
// Exposes the persisted UI locale — the i18n module (src/i18n) reads it at boot
// and writes it when the user picks a language — and the theme preference,
// owned by the theme store (src/stores/theme).
import { SettingsService } from '../../bindings/catdb/internal/services'

export type ThemePreference = 'light' | 'dark' | 'system'

export function getLocale(): Promise<string> {
  return SettingsService.GetLocale() as unknown as Promise<string>
}

export function setLocale(locale: string): Promise<void> {
  return SettingsService.SetLocale(locale) as unknown as Promise<void>
}

export function getTheme(): Promise<ThemePreference> {
  return SettingsService.GetTheme() as unknown as Promise<ThemePreference>
}

export function setTheme(theme: ThemePreference): Promise<void> {
  return SettingsService.SetTheme(theme) as unknown as Promise<void>
}
