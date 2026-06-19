<script setup lang="ts">
// App root. Two top-level views, picked off the hash:
//   * `#/connection-editor[?driver=…&id=…]` — standalone connection editor
//     window. Spawned from the main window via SystemService.OpenConnectionEditor.
//   * anything else — the main app shell (sidebar + workspace + status bar).
// Both windows share this entry; the route picks which root component mounts.
import { computed, onMounted, ref } from 'vue'
import { NConfigProvider, NMessageProvider, darkTheme } from 'naive-ui'
import { useThemeStore } from './stores/theme'
import { themeOverrides, darkThemeOverrides } from './styles/theme'
import AppShell from './components/layout/AppShell.vue'
import ConnectionEditorWindow from './components/connection/ConnectionEditorWindow.vue'

const theme = useThemeStore()
const naiveTheme = computed(() => (theme.mode === 'dark' ? darkTheme : null))
const naiveOverrides = computed(() => (theme.mode === 'dark' ? darkThemeOverrides : themeOverrides))

const route = ref<string>(currentRoute())

function currentRoute(): string {
  const h = window.location.hash || ''
  if (h.startsWith('#/connection-editor')) return 'connection-editor'
  return 'shell'
}

onMounted(() => {
  // Keep the route reactive when the host window re-points (e.g. SetURL from
  // the Go side when re-opening the editor with new params).
  window.addEventListener('hashchange', () => {
    route.value = currentRoute()
  })

  // Globally disable autocomplete to suppress the macOS autofill bubble
  // ("Root | ×") that WebKit shows on focused password / text inputs.
  document.addEventListener('focusin', (e) => {
    const el = (e.target as HTMLElement).closest('input')
    if (el && !el.hasAttribute('autocomplete')) {
      el.setAttribute('autocomplete', 'off')
    }
  })
})
</script>

<template>
  <n-config-provider :theme="naiveTheme" :theme-overrides="naiveOverrides">
    <n-message-provider>
      <ConnectionEditorWindow v-if="route === 'connection-editor'" />
      <AppShell v-else />
    </n-message-provider>
  </n-config-provider>
</template>
