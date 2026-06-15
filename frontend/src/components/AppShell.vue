<script setup lang="ts">
// AppShell — desktop three-pane scaffold (UI_SPEC.md §3): left sidebar
// (connections at top, object tree below when a connection is active),
// main tabbed content (per-connection query workspace + table tabs),
// bottom status bar.
//
// M4 additions: routes native menu Emits to the active tab, intercepts
// the window-close request when there are unsaved tabs, and keeps the
// dirty-tab counter in the Go side current.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Window } from '@wailsio/runtime'
import { NButton, useDialog, useMessage } from 'naive-ui'
import AppSidebar from './AppSidebar.vue'
import ConnectionWelcome from './ConnectionWelcome.vue'
import QueryWorkspace from './QueryWorkspace.vue'
import StatusBar from './StatusBar.vue'
import type { ConnectionProfile, DriverInfo } from '../api/connections'
import { useConnectionsStore } from '../stores/connections'
import { useQueryStore } from '../stores/query'
import { system as systemApi } from '../api'

const store = useConnectionsStore()
const queryStore = useQueryStore()
const dialog = useDialog()
const message = useMessage()

const activeConn = ref<ConnectionProfile | null>(null)

const sidebarVisible = ref(true)

// --- menu / close-guard hookup ---

const offHandlers: Array<() => void> = []

function onMenu(cmd: string, handler: () => void) {
  offHandlers.push(systemApi.onMenu(cmd as any, handler))
}

onMounted(() => {
  void store.refreshDrivers()

  onMenu('menu:new-tab', () => {
    if (activeConn.value) {
      const n = queryStore.tabsForConn(activeConn.value.id).filter((t: any) => t.kind === 'query').length + 1
      queryStore.addTab(activeConn.value.id, { kind: 'query', title: `Query ${n}` })
    }
  })
  onMenu('menu:close-tab', () => {
    const t = activeConn.value ? queryStore.activeTab(activeConn.value.id) : null
    if (t) void queryStore.closeTab(t.id)
  })
  onMenu('menu:run-query', () => emitTabCommand('run'))
  onMenu('menu:run-selection', () => emitTabCommand('run-selection'))
  onMenu('menu:explain', () => emitTabCommand('explain'))
  onMenu('menu:cancel-query', () => {
    const t = activeConn.value ? queryStore.activeTab(activeConn.value.id) : null
    if (t) void queryStore.cancel(t.id)
  })
  onMenu('menu:toggle-sidebar', () => { sidebarVisible.value = !sidebarVisible.value })

  offHandlers.push(systemApi.onCloseBlocked(({ dirtyTabs }) => {
    dialog.warning({
      title: 'Discard unsaved SQL?',
      content: `You have ${dirtyTabs} unsaved tab(s). Closing will lose them.`,
      positiveText: 'Discard & close',
      negativeText: 'Cancel',
      onPositiveClick: async () => {
        await systemApi.allowNextClose()
        try { await Window.Close() } catch (e) { message.error(String(e)) }
      },
    })
  }))

  // The standalone connection-editor window broadcasts this after Save.
  offHandlers.push(systemApi.onConnectionSaved(() => {
    void store.refreshAll()
  }))
})

onBeforeUnmount(() => {
  for (const off of offHandlers) off()
  offHandlers.length = 0
})

// Run/Run-Selection/Explain need to reach the active tab's component. We use a
// tiny event bus on the queryStore: per-tab key = command. QueryTab listens.
const tabCmdBus = ref<{ tabId: string; cmd: string; nonce: number } | null>(null)
function emitTabCommand(cmd: string) {
  const t = activeConn.value ? queryStore.activeTab(activeConn.value.id) : null
  if (!t) return
  tabCmdBus.value = { tabId: t.id, cmd, nonce: Date.now() }
}

// Track unsaved SQL: a tab is "dirty" iff its SQL is non-empty and hasn't
// been run yet (heuristic for "unsaved work" — we don't have a save-to-file
// concept yet, so this errs on the side of caution).
const dirtyCount = computed(() => {
  return queryStore.tabs.filter((t: any) => t.kind === 'query' && t.sql.trim() && t.status === 'idle').length
})
watch(dirtyCount, (v) => { void systemApi.setDirtyTabs(v) }, { immediate: true })

// --- existing wiring ---

function onNewConnection(driver: DriverInfo) {
  void systemApi.openConnectionEditor(driver.name, '')
}
function onEditConnection(conn: ConnectionProfile) {
  const d = store.driverByName.get(conn.driver)
  if (!d) return
  void systemApi.openConnectionEditor(d.name, conn.id)
}
function onSelectConnection(conn: ConnectionProfile) {
  activeConn.value = conn
}
function pickFirstDriver() {
  const d = store.drivers[0]
  if (d) onNewConnection(d)
}
function onOpenData(payload: { db: string; table: string }) {
  if (!activeConn.value) return
  queryStore.openTableTab(activeConn.value.id, payload.db, payload.table, 'table')
}
function onOpenStructure(payload: { db: string; table: string }) {
  if (!activeConn.value) return
  queryStore.openTableTab(activeConn.value.id, payload.db, payload.table, 'structure')
}
</script>

<template>
  <div class="root">
    <div class="shell">
      <AppSidebar
        v-if="sidebarVisible"
        :active-conn="activeConn"
        @select="onSelectConnection"
        @new="onNewConnection"
        @edit="onEditConnection"
        @open-data="onOpenData"
        @open-structure="onOpenStructure"
        @collapse="sidebarVisible = false"
      />
      <div class="main">
        <header class="titlebar">
          <n-button
            class="sidebar-toggle"
            size="tiny"
            quaternary
            :title="sidebarVisible ? '隐藏侧边栏' : '显示侧边栏'"
            @click="sidebarVisible = !sidebarVisible"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.2"
              aria-hidden="true"
            >
              <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" />
              <line :x1="sidebarVisible ? 6 : 5" :y1="2.5" :x2="sidebarVisible ? 6 : 5" :y2="13.5" />
              <line
                v-if="sidebarVisible"
                x1="3"
                y1="5"
                x2="4.5"
                y2="5"
              />
              <line
                v-if="sidebarVisible"
                x1="3"
                y1="7"
                x2="4.5"
                y2="7"
              />
            </svg>
          </n-button>
        </header>
        <main class="content">
          <QueryWorkspace
            v-if="activeConn"
            :connection="activeConn"
            :tab-command="tabCmdBus"
          />
          <ConnectionWelcome v-else @new="pickFirstDriver" />
        </main>
        <div class="status">
          <StatusBar />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* All-plain-div layout. No NLayout/NScrollbar in the chain so every container
   has a known display mode and definite height. `overflow: hidden` on every
   level ensures the window can never be pushed past 100vh / 100vw.
   `flex: 1 1 0` (basis 0) prevents tall content from being adopted as the
   intrinsic basis of its parent — only the parent's available extent matters. */
.root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  width: 100vw;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

/* Row 1: sider + main side-by-side. */
.shell {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: row;
}
.main {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: var(--n-color);
}
.titlebar {
  flex: 0 0 30px;
  height: 30px;
  border-bottom: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  display: flex;
  align-items: center;
  padding: 0 6px;
  gap: 4px;
  --wails-draggable: drag;
}
/* Interactive children must opt out of the drag region or clicks get
   swallowed by the OS window-move handler. */
.sidebar-toggle {
  --wails-draggable: no-drag;
  opacity: 0.75;
}
.sidebar-toggle:hover { opacity: 1; }
.content {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
}
.content > * { flex: 1 1 0; min-width: 0; min-height: 0; }

/* Status bar inside main (right work area only — sidebar extends full height). */
.status {
  flex: 0 0 22px;
  height: 22px;
  border-top: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  background: var(--n-color, transparent);
}
</style>
