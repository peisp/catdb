<script setup lang="ts">
// App root. Two top-level views, picked off the hash:
//   * `#/connection-editor[?driver=…&id=…]` — standalone connection editor
//     window. Spawned from the main window via SystemService.OpenConnectionEditor.
//   * anything else — the main app shell (sidebar + workspace + status bar).
// Both windows share this entry; the route picks which root component mounts.
import { computed, onMounted, ref } from 'vue'
import { NConfigProvider, NMessageProvider, darkTheme, enUS, dateEnUS, zhCN, dateZhCN } from 'naive-ui'
import { i18n } from './i18n'
import { useThemeStore } from './stores/theme'
import { themeOverrides, darkThemeOverrides } from './styles/theme'
import AppShell from './components/layout/AppShell.vue'
import ConnectionEditorWindow from './components/connection/ConnectionEditorWindow.vue'
import DatabaseEditorWindow from './components/database/DatabaseEditorWindow.vue'

const theme = useThemeStore()
const naiveTheme = computed(() => (theme.mode === 'dark' ? darkTheme : null))
const naiveOverrides = computed(() => (theme.mode === 'dark' ? darkThemeOverrides : themeOverrides))

// Drive Naive UI's built-in component locale (date picker, pagination, etc.)
// off the app locale owned by src/i18n.
const naiveLocale = computed(() => (i18n.global.locale.value === 'zh-CN' ? zhCN : enUS))
const naiveDateLocale = computed(() => (i18n.global.locale.value === 'zh-CN' ? dateZhCN : dateEnUS))

const route = ref<string>(currentRoute())

function currentRoute(): string {
  const h = window.location.hash || ''
  if (h.startsWith('#/connection-editor')) return 'connection-editor'
  if (h.startsWith('#/database-editor')) return 'database-editor'
  return 'shell'
}

onMounted(() => {
  // Keep the route reactive when the host window re-points (e.g. SetURL from
  // the Go side when re-opening the editor with new params).
  window.addEventListener('hashchange', () => {
    route.value = currentRoute()
  })

  // Globally suppress the macOS WebKit AutoFill bubble (e.g. "Root | ×") that
  // appears from saved keychain entries / Contacts. autocomplete="off" alone is
  // ignored by WebKit on password-looking fields, so password inputs get
  // "new-password" (tells WebKit this is a registration form → no autofill).
  // Force-overwrite even if Naive UI set its own value.
  document.addEventListener('focusin', (e) => {
    const el = e.target as HTMLElement | null
    if (!(el instanceof HTMLInputElement) && !(el instanceof HTMLTextAreaElement)) return
    const isPassword = el instanceof HTMLInputElement && el.type === 'password'
    el.setAttribute('autocomplete', isPassword ? 'new-password' : 'off')
    el.setAttribute('autocorrect', 'off')
    el.setAttribute('autocapitalize', 'off')
    el.setAttribute('spellcheck', 'false')
    el.setAttribute('data-1p-ignore', 'true')
    el.setAttribute('data-lpignore', 'true')
  })
})
</script>

<template>
  <n-config-provider :theme="naiveTheme" :theme-overrides="naiveOverrides" :locale="naiveLocale" :date-locale="naiveDateLocale">
    <n-message-provider>
      <ConnectionEditorWindow v-if="route === 'connection-editor'" />
      <DatabaseEditorWindow v-else-if="route === 'database-editor'" />
      <AppShell v-else />
    </n-message-provider>
  </n-config-provider>
</template>
