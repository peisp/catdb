// vue-i18n setup — the single owner of the app's UI locale.
//
// Locale resolution order (i18n plan P0):
//   1. persisted preference (app_settings["ui.locale"], applied async at boot)
//   2. system locale (navigator.languages)
//   3. DEFAULT_LOCALE ("en-US")
//
// Components read translations via `$t` / `useI18n`; Naive UI's own component
// locale is driven off `i18n.global.locale` in App.vue. The Go native menus
// localise separately (plan P2).
import { createI18n } from 'vue-i18n'
import enUS from './locales/en-US'
import zhCN from './locales/zh-CN'

export const SUPPORTED_LOCALES = ['en-US', 'zh-CN'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]
export const DEFAULT_LOCALE: Locale = 'en-US'

export function isSupportedLocale(v: string): v is Locale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(v)
}

/** Best-effort match of the OS/browser locale to a supported one. */
export function detectLocale(): Locale {
  const prefs = navigator.languages?.length ? navigator.languages : [navigator.language]
  for (const p of prefs) {
    const lc = (p || '').toLowerCase()
    if (lc.startsWith('zh')) return 'zh-CN'
    if (lc.startsWith('en')) return 'en-US'
  }
  return DEFAULT_LOCALE
}

export const i18n = createI18n({
  legacy: false,
  // Expose $t / $i18n in component templates so most components need no
  // useI18n() boilerplate — template uses `$t('key')`, <script setup> imports
  // the `t` below (same call shape as the .ts api modules).
  globalInjection: true,
  locale: detectLocale(),
  fallbackLocale: DEFAULT_LOCALE,
  messages: { 'en-US': enUS, 'zh-CN': zhCN },
})

/**
 * Global `t` for use outside component setup (plain .ts modules: api facades,
 * event handlers, stores). Inside components prefer `$t` / `useI18n`. Bound to
 * the global composer so it stays reactive to locale changes.
 */
export const t = i18n.global.t

/** Switch the active locale (does not persist — callers handle that). */
export function setLocale(locale: Locale) {
  i18n.global.locale.value = locale
  document.documentElement.lang = locale
}

export function currentLocale(): Locale {
  return i18n.global.locale.value as Locale
}
