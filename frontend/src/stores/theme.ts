// Theme store — owns light/dark state and subscribes to prefers-color-scheme
// (DESIGN.md). Wails v3 has no native theme-change API yet (issue #4665),
// so we use matchMedia as the fallback. When Wails adds one, swap the
// subscription out HERE — components stay untouched.
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { applyThemeTokens } from '../styles/tokens'

export type ThemeMode = 'light' | 'dark'

export const useThemeStore = defineStore('theme', () => {
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  const mode = ref<ThemeMode>(mq.matches ? 'dark' : 'light')
  // 把 DESIGN.md token 注入 :root 的 --catdb-* 变量;mode 变化时整组替换。
  applyThemeTokens(mode.value)

  const handler = (e: MediaQueryListEvent) => {
    mode.value = e.matches ? 'dark' : 'light'
    applyThemeTokens(mode.value)
  }
  mq.addEventListener('change', handler)

  return { mode }
})
