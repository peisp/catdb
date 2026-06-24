// api/settings — front-end facade over SettingsService bindings.
//
// Components import from here, never from `bindings/` directly (CLAUDE.md #1).
// Currently exposes the persisted UI locale; the i18n module (src/i18n) reads
// it at boot and writes it when the user picks a language.
import { SettingsService } from '../../bindings/catdb/internal/services'

export function getLocale(): Promise<string> {
  return SettingsService.GetLocale() as unknown as Promise<string>
}

export function setLocale(locale: string): Promise<void> {
  return SettingsService.SetLocale(locale) as unknown as Promise<void>
}
