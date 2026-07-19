<script setup lang="ts">
// AppShell — desktop three-pane scaffold (DESIGN.md): left sidebar
// (connections at top, object tree below when a connection is active),
// main tabbed content (per-connection query workspace + table tabs),
// bottom status bar.
//
// M4 additions: routes native menu Emits to the active tab, intercepts
// the window-close request when there are unsaved tabs, and keeps the
// dirty-tab counter in the Go side current.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Window } from '@wailsio/runtime'
import { useMessage } from 'naive-ui'
import AppSidebar from './AppSidebar.vue'
import ConnectionWelcome from '../connection/ConnectionWelcome.vue'
import QueryWorkspace from '../workspace/QueryWorkspace.vue'
import StatusBar from './StatusBar.vue'
import UpdateDialog from '../update/UpdateDialog.vue'
import PromptOverlay from '../common/PromptOverlay.vue'
import AppToolbar from './AppToolbar.vue'
import type { ConnectionProfile } from '../../api/connections'
import { useConnectionsStore } from '../../stores/connections'
import { useQueryStore } from '../../stores/query'
import { useUpdatesStore } from '../../stores/updates'
import { system as systemApi, dialogs } from '../../api'
import { t } from '../../i18n'
import sidebarLeftIcon from '../../assets/icons/sidebar.left.svg?raw'

const store = useConnectionsStore()
const queryStore = useQueryStore()
const updates = useUpdatesStore()
const message = useMessage()

const activeConn = ref<ConnectionProfile | null>(null)

const sidebarVisible = ref(true)

// macOS draws traffic lights at the top-left; offset the floating toggle
// to the right of them. Windows (frameless) caption buttons live in the
// toolbar now (AppToolbar.vue).
const isMac = navigator.platform.includes('Mac')
const isWin = !isMac

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

  offHandlers.push(systemApi.onCloseBlocked(async ({ dirtyTabs }) => {
    const choice = await dialogs.confirm({
      title: t('appShell.closeGuard.title'),
      message: t('appShell.closeGuard.message', { n: dirtyTabs }),
      buttons: [
        { value: 'cancel', label: t('common.cancel'), isCancel: true },
        { value: 'discard', label: t('appShell.closeGuard.discard') },
      ],
    })
    if (choice !== 'discard') return
    await systemApi.allowNextClose()
    try { await Window.Close() } catch (e) { message.error(String(e)) }
  }))

  // The standalone connection-editor window broadcasts this after Save.
  offHandlers.push(systemApi.onConnectionSaved(() => {
    void store.refreshAll()
  }))

  // Auto-check for updates: once shortly after the shell mounts, then every
  // 8 hours in the background (production only — the store no-ops in dev).
  // The store keeps the badge state; the user can also trigger a check
  // manually by clicking the version in the StatusBar. We deliberately do NOT
  // auto-open the dialog — the badge dot is the non-intrusive signal; opening
  // waits for a click.
  offHandlers.push(updates.startAutoCheck())
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

// Track unsaved SQL: a tab is "dirty" iff its SQL diverges from the last
// saved baseline (never-saved tab with typed SQL, or edited-since-save).
const dirtyCount = computed(() => {
  return queryStore.tabs.filter((t) => queryStore.isQueryDirty(t)).length
})
watch(dirtyCount, (v) => { void systemApi.setDirtyTabs(v) }, { immediate: true })

// --- existing wiring ---

function onEditConnection(conn: ConnectionProfile) {
  const d = store.driverByName.get(conn.driver)
  if (!d) return
  void systemApi.openConnectionEditor(d.name, conn.id)
}
function onSelectConnection(conn: ConnectionProfile) {
  activeConn.value = conn
}
// Open the editor with no preselected driver — the form's left rail picks
// the first available one (currently mysql). Used by the welcome-screen
// "新建" button and the top-bar "+" button.
function openNewConnection() {
  void systemApi.openConnectionEditor('', '')
}
function onOpenData(payload: { db: string; schema?: string; table: string }) {
  if (!activeConn.value) return
  queryStore.openTableTab(activeConn.value.id, payload.db, payload.table, 'table', payload.schema ?? '')
}
function onOpenStructure(payload: { db: string; schema?: string; table: string }) {
  if (!activeConn.value) return
  queryStore.openTableTab(activeConn.value.id, payload.db, payload.table, 'structure', payload.schema ?? '')
}
function onOpenTablesOverview(payload: { db: string; schema?: string }) {
  if (!activeConn.value) return
  queryStore.openTablesOverviewTab(activeConn.value.id, payload.db, payload.schema ?? '')
}
</script>

<template>
  <div class="root">
    <div class="shell">
      <!-- Floating controls overlay: sidebar toggle. Absolutely positioned
           so the sidebar can extend all the way to the top of the window
           (demo pattern). On macOS, offset right of the system traffic
           lights; elsewhere, anchored flush at top-left. -->
      <div class="floating-controls" :class="{ mac: isMac, 'sidebar-closed': isWin && !sidebarVisible }">
        <button
          type="button"
          class="sidebar-toggle glass"
          :class="{ collapsed: !sidebarVisible }"
          :title="sidebarVisible ? $t('appShell.hideSidebar') : $t('appShell.showSidebar')"
          @click="sidebarVisible = !sidebarVisible"
        >
          <span class="glass-specular" aria-hidden="true" />
          <!-- SF Symbols sidebar.left -->
          <span
            class="glass-icon"
            aria-hidden="true"
            v-html="sidebarLeftIcon"
          />
        </button>
      </div>

      <!-- Right-side floating control: "+" opens the connection editor with
           no preselected driver. The form's left rail handles type picking. -->
<!--      <div class="floating-controls-right" :class="{ win: isWin }">-->
<!--        <button-->
<!--          type="button"-->
<!--          class="sidebar-toggle glass new-conn"-->
<!--          :title="$t('appShell.newConnection')"-->
<!--          @click="openNewConnection"-->
<!--        >-->
<!--          <span class="glass-specular" aria-hidden="true" />-->
<!--          <span class="glass-icon" aria-hidden="true">-->
<!--            <svg viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg">-->
<!--              <path-->
<!--                d="M8 3.2v9.6M3.2 8h9.6"-->
<!--                stroke="currentColor"-->
<!--                stroke-width="1.6"-->
<!--                stroke-linecap="round"-->
<!--                fill="none"-->
<!--              />-->
<!--            </svg>-->
<!--          </span>-->
<!--        </button>-->
<!--      </div>-->

      <AppSidebar
        :active-conn="activeConn"
        :collapsed="!sidebarVisible"
        @select="onSelectConnection"
        @edit="onEditConnection"
        @open-data="onOpenData"
        @open-structure="onOpenStructure"
        @open-tables-overview="onOpenTablesOverview"
        @collapse="sidebarVisible = false"
      />
      <div class="main" :class="{ win: isWin }">
        <AppToolbar :active-conn="activeConn" :sidebar-visible="sidebarVisible" />
        <main class="content">
          <QueryWorkspace
            v-if="activeConn"
            :connection="activeConn"
            :tab-command="tabCmdBus"
          />
          <ConnectionWelcome v-else @new="openNewConnection" />
        </main>
        <div class="status">
          <StatusBar :active-conn="activeConn" />
        </div>
      </div>
    </div>
    <UpdateDialog />
    <PromptOverlay />
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

/* Row 1: sider + main side-by-side. The shell is `position: relative` so
   .top-drag-region and .floating-controls (absolute) anchor here. */
.shell {
  position: relative;
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: row;
}

/* Floating controls overlay (toggle button). Stays anchored to .shell
   regardless of sidebar collapsed state, so the button visually floats
   over whichever pane is below — matching the macOS demo. */
.floating-controls {
  position: absolute;
  left: 110px;
  top: 10px;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 8px;
  --wails-draggable: drag;
  transition: left 0.35s cubic-bezier(0.4, 0, 0.2, 1);
}
/* clear of system traffic lights */
/*
.floating-controls.mac {
  top: 10px;
}*/
/* On Windows, when sidebar is closed, move the toggle away from the
   left edge so it doesn't sit flush against the window frame. */
.floating-controls.sidebar-closed { left: 10px; }

/* Right-side floating control mirror. macOS pins to right:12px; Windows
   pushes left of the three caption buttons (3 × 46px = 138px) so the
   "+" doesn't sit on top of close/maximise. */
/*
.floating-controls-right {
  position: absolute;
  top: 10px;
  right: 12px;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 8px;
  --wails-draggable: drag;
}
.floating-controls-right.win {
  right: 150px;
}
 */


.main {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: var(--catdb-surface-content);
}
.main.win { background: var(--catdb-surface-content); }

/* --- Round liquid-glass sidebar toggle ---
   1. translucent gradient fill (base material)
   2. backdrop-filter blur+saturate (refracts behind)
   3. inset top highlight + bottom shadow line (specular edge)
   4. hairline outer ring + soft drop shadow (depth)
   5. .glass-specular = top-half sheen, brightens on hover
   6. .glass-icon rotates 180° when sidebar is collapsed */
.sidebar-toggle {
  --wails-draggable: no-drag;
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 35px;
  height: 35px;
  padding: 0;
  margin: 0;
  font: inherit;
  color: inherit;
  cursor: default;
  border: none;
  border-radius: 50%;
  background:
    linear-gradient(180deg,
      rgba(255, 255, 255, 0.6) 0%,
      rgba(255, 255, 255, 0.25) 100%);
  backdrop-filter: blur(18px) saturate(180%);
  -webkit-backdrop-filter: blur(18px) saturate(180%);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.85),
    inset 0 -1px 0 rgba(0, 0, 0, 0.06),
    0 0 0 0.5px rgba(0, 0, 0, 0.14),
    0 1px 2px rgba(0, 0, 0, 0.1);
  transition: background 120ms ease, box-shadow 120ms ease;
}
.sidebar-toggle:hover {
  background:
    linear-gradient(180deg,
      rgba(255, 255, 255, 0.75) 0%,
      rgba(255, 255, 255, 0.35) 100%);
}
.sidebar-toggle:active {
  background:
    linear-gradient(180deg,
      rgba(255, 255, 255, 0.4) 0%,
      rgba(255, 255, 255, 0.2) 100%);
  box-shadow:
    inset 0 1px 1.5px rgba(0, 0, 0, 0.08),
    inset 0 -1px 0 rgba(255, 255, 255, 0.45),
    0 0 0 0.5px rgba(0, 0, 0, 0.16);
}
.sidebar-toggle:focus-visible {
  outline: 2px solid var(--catdb-accent);
  outline-offset: 1px;
}
.sidebar-toggle .glass-icon {
  position: relative;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 17px;
  height: 13px;
  opacity: 0.72;
  transition: opacity 120ms ease;
}
.sidebar-toggle .glass-icon :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
}
/* The "+" icon is square — the sidebar glyph is 17×13 — so size it 14×14
   to read the same optical weight inside the same 35px circle. */
.sidebar-toggle.new-conn .glass-icon {
  width: 14px;
  height: 14px;
}
.sidebar-toggle.new-conn .glass-icon svg {
  display: block;
  width: 100%;
  height: 100%;
}
.sidebar-toggle:hover .glass-icon { opacity: 1; }

/* Circular top-half sheen. */
.sidebar-toggle .glass-specular {
  position: absolute;
  inset: 1px;
  border-radius: 50%;
  background: linear-gradient(180deg,
    rgba(255, 255, 255, 0.55) 0%,
    rgba(255, 255, 255, 0) 55%);
  pointer-events: none;
  opacity: 0.9;
  transition: opacity 120ms ease;
}
.sidebar-toggle:hover .glass-specular { opacity: 1; }
.sidebar-toggle:active .glass-specular { opacity: 0.4; }

@media (prefers-color-scheme: dark) {
  .sidebar-toggle {
    background:
      linear-gradient(180deg,
        rgba(255, 255, 255, 0.14) 0%,
        rgba(255, 255, 255, 0.05) 100%);
    box-shadow:
      inset 0 1px 0 rgba(255, 255, 255, 0.22),
      inset 0 -1px 0 rgba(0, 0, 0, 0.35),
      0 0 0 0.5px rgba(255, 255, 255, 0.08),
      0 1px 2px rgba(0, 0, 0, 0.35);
  }
  .sidebar-toggle:hover {
    background:
      linear-gradient(180deg,
        rgba(255, 255, 255, 0.22) 0%,
        rgba(255, 255, 255, 0.09) 100%);
  }
  .sidebar-toggle:active {
    background:
      linear-gradient(180deg,
        rgba(255, 255, 255, 0.08) 0%,
        rgba(255, 255, 255, 0.03) 100%);
    box-shadow:
      inset 0 1px 1.5px rgba(0, 0, 0, 0.4),
      inset 0 -1px 0 rgba(255, 255, 255, 0.1),
      0 0 0 0.5px rgba(255, 255, 255, 0.06);
  }
  .sidebar-toggle .glass-specular {
    background: linear-gradient(180deg,
      rgba(255, 255, 255, 0.18) 0%,
      rgba(255, 255, 255, 0) 55%);
  }
}

/* Graceful fallback when backdrop-filter isn't supported. */
@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  .sidebar-toggle { background: rgba(255, 255, 255, 0.7); }
  @media (prefers-color-scheme: dark) {
    .sidebar-toggle { background: rgba(255, 255, 255, 0.12); }
  }
}

.content {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  border-top: 1px solid var(--catdb-separator);
}
.content > * { flex: 1 1 0; min-width: 0; min-height: 0; }

/* Status bar inside main (right work area only — sidebar extends full height). */
.status {
  flex: 0 0 var(--catdb-statusbar-height);
  height: var(--catdb-statusbar-height);
  border-top: 1px solid var(--catdb-separator);
  background: var(--n-color, transparent);
}
</style>
