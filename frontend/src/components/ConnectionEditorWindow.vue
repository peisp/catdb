<script setup lang="ts">
// ConnectionEditorWindow — the root view of the standalone connection-editor
// child window. Loaded when location.hash starts with #/connection-editor.
//
// Lifecycle:
//   1. parse driver + optional id out of the hash query string.
//   2. fetch driver list (need the schema for the form) and, if editing, the
//      profile to seed initial values.
//   3. host the existing ConnectionForm component.
//   4. on save: broadcast `connection:saved` to all windows (so the main
//      window refreshes its sidebar) then close this window.
//
// We deliberately do NOT bring up AppShell or the connection sidebar here —
// this window is a focused editor, not another shell instance.
import { computed, onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { NSpin, useMessage } from 'naive-ui'
import ConnectionForm from './ConnectionForm.vue'
import { useConnectionsStore } from '../stores/connections'
import { connections as connectionsApi, system as systemApi } from '../api'
import type { ConnectionProfile, DriverInfo } from '../api/connections'

const store = useConnectionsStore()
const message = useMessage()

const driver = ref<DriverInfo | null>(null)
const initial = ref<ConnectionProfile | null>(null)
const loading = ref(true)
const errorMessage = ref('')

const title = computed(() => {
  if (!driver.value) return '连接'
  return initial.value ? `编辑 ${driver.value.name} 连接` : `新建 ${driver.value.name} 连接`
})

function parseHashQuery(): { driver?: string; id?: string } {
  // Expect hash shaped like `#/connection-editor?driver=mysql&id=abc`. Use
  // URLSearchParams on the slice after `?` — no third-party router needed.
  const h = window.location.hash || ''
  const qIdx = h.indexOf('?')
  if (qIdx < 0) return {}
  const params = new URLSearchParams(h.slice(qIdx + 1))
  return { driver: params.get('driver') ?? undefined, id: params.get('id') ?? undefined }
}

onMounted(async () => {
  try {
    const { driver: drvName, id } = parseHashQuery()

    // Need the driver schema for the form. Pull the full list once and pick.
    await store.refreshDrivers()
    const drv = drvName ? store.driverByName.get(drvName) : null
    if (!drv) {
      errorMessage.value = drvName
        ? `未知驱动: ${drvName}`
        : '缺少 driver 参数'
      loading.value = false
      return
    }
    driver.value = drv

    if (id) {
      try {
        initial.value = await connectionsApi.getConnection(id)
      } catch (e: any) {
        errorMessage.value = `读取连接失败: ${e?.message ?? e}`
      }
    }
  } finally {
    loading.value = false
  }
})

async function onSaved(profile: ConnectionProfile) {
  // Tell every other window (read: the main shell) that the list moved.
  try {
    await systemApi.broadcastConnectionSaved(profile.id)
  } catch (e) {
    // Non-fatal — the main window can still pick up changes on refresh.
    console.warn('connection:saved broadcast failed', e)
  }
  message.success('已保存')
  // Short delay so the success toast is at least briefly visible.
  setTimeout(() => { void Window.Close() }, 200)
}

function onCancel() {
  void Window.Close()
}

// Mirror the native OS gesture: double-clicking the drag strip toggles
// between maximised and the previous size. Wails alpha-96 doesn't do this
// for us — we have to bind it explicitly.
function toggleMaximise() {
  void Window.ToggleMaximise()
}
</script>

<template>
  <div class="root">
    <header class="titlebar" @dblclick="toggleMaximise">
      <span class="title">{{ title }}</span>
    </header>
    <main class="body">
      <div v-if="loading" class="loading">
        <n-spin size="small" />
        <span>加载中…</span>
      </div>
      <div v-else-if="errorMessage" class="error">
        {{ errorMessage }}
      </div>
      <ConnectionForm
        v-else-if="driver"
        :driver="driver"
        :initial="initial"
        @saved="onSaved"
        @cancel="onCancel"
      />
    </main>
  </div>
</template>

<style scoped>
.root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  width: 100vw;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--n-color);
}
/* Titlebar — flush with the form below (no hard border) so the entire
   window reads as one card. The platform's native traffic lights / window
   controls overlay this strip on macOS via TitleBarHiddenInset. */
.titlebar {
  flex: 0 0 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.2px;
  opacity: 0.85;
  --wails-draggable: drag;
}
.titlebar .title {
  /* Leave clear space on the macOS traffic-light side. */
  padding-left: 60px;
  padding-right: 12px;
}
/* Body hands the form 100% of the remaining height so the form's bottom
   action bar pins to the window bottom. Scrolling happens inside the form's
   own scrollable region, not here. */
.body {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
}
.body > * { flex: 1 1 0; min-width: 0; min-height: 0; }
.loading,
.error {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 20px;
  font-size: 13px;
  opacity: 0.8;
}
.error { color: var(--n-error-color, #d03050); }
</style>
