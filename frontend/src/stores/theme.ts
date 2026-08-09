// Theme store — owns the 浅色/深色/跟随系统 preference and the light/dark mode it
// resolves to (DESIGN.md). The preference is persisted in app_settings
// ["ui.theme"] and applied natively by wailsbridge (window appearance, titlebar,
// backdrop), which is what keeps the native chrome and the web content in step.
//
// Why `mode` is still derived from `preference` rather than trusting matchMedia:
// on macOS forcing NSAppearance also flips the WKWebView's prefers-color-scheme,
// so matchMedia and the preference agree. On Windows, WebView2's colour scheme
// (ICoreWebView2Profile.PreferredColorScheme) is not reachable from Wails
// v3.0.0-beta.4, so matchMedia keeps reporting the OS setting — the preference
// has to drive the CSS tokens there. Deriving covers both.
import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { applyThemeTokens } from '../styles/tokens'
import { settings as settingsApi, system as systemApi } from '../api'
import type { ThemePreference } from '../api/settings'

export type ThemeMode = 'light' | 'dark'

function isPreference(v: string): v is ThemePreference {
  return v === 'light' || v === 'dark' || v === 'system'
}

export const useThemeStore = defineStore('theme', () => {
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  const systemMode = ref<ThemeMode>(mq.matches ? 'dark' : 'light')
  mq.addEventListener('change', (e) => {
    systemMode.value = e.matches ? 'dark' : 'light'
  })

  const preference = ref<ThemePreference>('system')
  const mode = computed<ThemeMode>(() =>
    preference.value === 'system' ? systemMode.value : preference.value,
  )

  // 把 DESIGN.md token 注入 :root 的 --catdb-* 变量;mode 变化时整组替换。
  applyThemeTokens(mode.value)
  watch(mode, (m) => applyThemeTokens(m))

  /** 用户主动切换:立即生效 + 持久化(Go 侧再应用原生主题并广播其他窗口)。 */
  function setPreference(p: ThemePreference) {
    preference.value = p
    void settingsApi.setTheme(p)
  }

  let started = false
  /** 读回存量偏好并订阅跨窗口同步。挂载前调用一次即可。 */
  function init() {
    if (started) return
    started = true
    void settingsApi.getTheme().then((stored) => {
      if (isPreference(stored)) preference.value = stored
    })
    // 任一窗口通过 SettingsService.SetTheme 改主题后,Go 广播 app:theme-changed;
    // 这里只做本窗口的运行时切换,绝不能再调 settingsApi.setTheme(会造成
    // 持久化→广播→持久化 死循环)。
    systemApi.onThemeChanged(({ theme }) => {
      if (isPreference(theme)) preference.value = theme
    })
  }

  return { preference, mode, setPreference, init }
})
