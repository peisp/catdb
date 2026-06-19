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
import { useConnectionsStore } from '../../stores/connections'
import { connections as connectionsApi, system as systemApi } from '../../api'
import type { ConnectionProfile, DriverInfo } from '../../api/connections'

const store = useConnectionsStore()
const message = useMessage()

const driver = ref<DriverInfo | null>(null)
const initial = ref<ConnectionProfile | null>(null)
const loading = ref(true)
const errorMessage = ref('')

const isMac = navigator.platform.includes('Mac')
const isWin = !isMac

const isMaximised = ref(false)
async function onWindowCtrl(cmd: 'min' | 'max' | 'close') {
  if (cmd === 'min') { await Window.Minimise(); return }
  if (cmd === 'close') { await Window.Close(); return }
  await Window.ToggleMaximise()
  isMaximised.value = await Window.IsMaximised()
}

const title = computed(() => {
  if (initial.value && driver.value) return `编辑 ${driver.value.name} 连接`
  if (initial.value) return '编辑连接'
  if (driver.value) return `新建 ${driver.value.name} 连接`
  return '新建连接'
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
    // No drvName → form picks the first available driver from its own rail.
    await store.refreshDrivers()
    if (drvName) {
      const drv = store.driverByName.get(drvName)
      if (!drv) {
        errorMessage.value = `未知驱动: ${drvName}`
        loading.value = false
        return
      }
      driver.value = drv
    }

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
    <header class="titlebar" :class="{ win: isWin }" @dblclick="toggleMaximise">
      <span class="title">{{ title }}</span>
      <!-- Windows frameless caption buttons -->
      <div v-if="isWin" class="window-controls">
        <button type="button" class="win-btn win-btn-min" title="最小化" @click="onWindowCtrl('min')">
          <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="0" y="4.5" width="10" height="1" fill="currentColor" /></svg>
        </button>
        <button type="button" class="win-btn win-btn-max" :title="isMaximised ? '还原' : '最大化'" @click="onWindowCtrl('max')">
          <svg v-if="isMaximised" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1.5" y="3.5" width="6" height="6" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
            <path d="M3.5 3.5V2A0.5 0.5 0 0 1 4 1.5h4A0.5 0.5 0 0 1 8.5 2v4a0.5 0.5 0 0 1-.5.5H7.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
          <svg v-else viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1" y="1" width="8" height="8" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
        </button>
        <button type="button" class="win-btn win-btn-close" title="关闭" @click="onWindowCtrl('close')">
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" />
          </svg>
        </button>
      </div>
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
        v-else
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
.titlebar.win .title {
  /* On Windows the caption buttons sit at the right side of the titlebar;
     push the centred title clear of the 3 × 46px button strip. */
  padding-right: 150px;
}
/* Window controls container — pinned to the right inside the titlebar. */
.titlebar .window-controls {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 10;
  display: flex;
  flex-direction: row;
  align-items: stretch;
  height: 100%;
  -webkit-app-region: no-drag;
}
.titlebar .win-btn {
  --wails-draggable: no-drag;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  padding: 0;
  margin: 0;
  border: none;
  border-radius: 0;
  font: inherit;
  color: inherit;
  cursor: default;
  background: transparent;
  transition: background 80ms ease;
}
.titlebar .win-btn svg {
  width: 14px;
  height: 14px;
  opacity: 0.75;
}
.titlebar .win-btn:hover { background: rgba(127, 127, 127, 0.15); }
.titlebar .win-btn:active { background: rgba(127, 127, 127, 0.25); }
.titlebar .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
.titlebar .win-btn-close:hover svg { opacity: 1; }
.titlebar .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
.titlebar .win-btn-close:active svg { opacity: 1; }
@media (prefers-color-scheme: dark) {
  .titlebar .win-btn:hover { background: rgba(255, 255, 255, 0.1); }
  .titlebar .win-btn:active { background: rgba(255, 255, 255, 0.16); }
  .titlebar .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
  .titlebar .win-btn-close:hover svg { opacity: 1; }
  .titlebar .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
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
