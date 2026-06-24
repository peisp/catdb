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
import { Dialogs, Window } from '@wailsio/runtime'
import { useMessage } from 'naive-ui'
import AppSidebar from './AppSidebar.vue'
import ConnectionWelcome from '../connection/ConnectionWelcome.vue'
import QueryWorkspace from '../workspace/QueryWorkspace.vue'
import StatusBar from './StatusBar.vue'
import UpdateDialog from '../update/UpdateDialog.vue'
import PromptOverlay from '../common/PromptOverlay.vue'
import type { ConnectionProfile } from '../../api/connections'
import { useConnectionsStore } from '../../stores/connections'
import { useQueryStore } from '../../stores/query'
import { useUpdatesStore } from '../../stores/updates'
import { system as systemApi } from '../../api'
import sidebarLeftIcon from '../../assets/icons/sidebar.left.svg?raw'

const store = useConnectionsStore()
const queryStore = useQueryStore()
const updates = useUpdatesStore()
const message = useMessage()

const activeConn = ref<ConnectionProfile | null>(null)

const sidebarVisible = ref(true)

// macOS draws traffic lights at the top-left; offset the floating toggle
// to the right of them. Windows (frameless) gets custom caption buttons
// at the top-right.
const isMac = navigator.platform.includes('Mac')
const isWin = !isMac

// Maximise state tracking for the restore/maximise toggle icon.
const isMaximised = ref(false)
async function onWindowCtrl(cmd: 'min' | 'max' | 'close') {
  if (cmd === 'min') { await Window.Minimise(); return }
  if (cmd === 'close') { await Window.Close(); return }
  // maximise / restore
  await Window.ToggleMaximise()
  isMaximised.value = await Window.IsMaximised()
}
// Mirror double-click on drag region toggles maximise
function toggleMaximise() {
  void onWindowCtrl('max')
}

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
    const btn = await Dialogs.Warning({
      Title: 'Discard unsaved SQL?',
      Message: `You have ${dirtyTabs} unsaved tab(s). Closing will lose them.`,
      Buttons: [
        { Label: 'Cancel', IsCancel: true },
        { Label: 'Discard & close' },
      ],
    })
    if (btn !== 'Discard & close') return
    await systemApi.allowNextClose()
    try { await Window.Close() } catch (e) { message.error(String(e)) }
  }))

  // The standalone connection-editor window broadcasts this after Save.
  offHandlers.push(systemApi.onConnectionSaved(() => {
    void store.refreshAll()
  }))

  // Auto-check for updates 60s after the shell mounts. The store keeps the
  // badge state; the user can also trigger a check manually by clicking the
  // version in the StatusBar. We deliberately do NOT auto-open the dialog —
  // the badge dot is the non-intrusive signal; opening waits for a click.
  const checkTimer = window.setTimeout(() => {
    void updates.check()
  }, 10_000)
  offHandlers.push(() => window.clearTimeout(checkTimer))
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
function onOpenData(payload: { db: string; table: string }) {
  if (!activeConn.value) return
  queryStore.openTableTab(activeConn.value.id, payload.db, payload.table, 'table')
}
function onOpenStructure(payload: { db: string; table: string }) {
  if (!activeConn.value) return
  queryStore.openTableTab(activeConn.value.id, payload.db, payload.table, 'structure')
}
function onOpenTablesOverview(payload: { db: string }) {
  if (!activeConn.value) return
  queryStore.openTablesOverviewTab(activeConn.value.id, payload.db)
}
</script>

<template>
  <div class="root">
    <div class="shell">
      <!-- Invisible drag strip pinned to the top of the window. Lets the user
           drag the window (Wails consumes --wails-draggable: drag) and
           double-click to toggle maximise. The floating-controls layer sits
           above it (z-index) so the toggle button stays clickable. -->
      <div class="top-drag-region" @dblclick.self="toggleMaximise"></div>

      <!-- Floating controls overlay: sidebar toggle. Absolutely positioned
           so the sidebar can extend all the way to the top of the window
           (demo pattern). On macOS, offset right of the system traffic
           lights; elsewhere, anchored flush at top-left. -->
      <div class="floating-controls" :class="{ mac: isMac }">
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
      <div class="floating-controls-right" :class="{ win: isWin }">
        <button
          type="button"
          class="sidebar-toggle glass new-conn"
          :title="$t('appShell.newConnection')"
          @click="openNewConnection"
        >
          <span class="glass-specular" aria-hidden="true" />
          <span class="glass-icon" aria-hidden="true">
            <svg viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg">
              <path
                d="M8 3.2v9.6M3.2 8h9.6"
                stroke="currentColor"
                stroke-width="1.6"
                stroke-linecap="round"
                fill="none"
              />
            </svg>
          </span>
        </button>
      </div>

      <!-- Windows frameless caption buttons (minimise / maximise / close).
           Absolutely positioned at top-right, outside the drag region so
           each button is individually clickable (--wails-draggable: no-drag). -->
      <div v-if="isWin" class="window-controls">
        <button type="button" class="win-btn win-btn-min" :title="$t('appShell.minimize')" @click="onWindowCtrl('min')">
          <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="0" y="4.5" width="10" height="1" fill="currentColor" /></svg>
        </button>
        <button type="button" class="win-btn win-btn-max" :title="isMaximised ? $t('appShell.restore') : $t('appShell.maximize')" @click="onWindowCtrl('max')">
          <svg v-if="isMaximised" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1.5" y="3.5" width="6" height="6" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
            <path d="M3.5 3.5V2A0.5 0.5 0 0 1 4 1.5h4A0.5 0.5 0 0 1 8.5 2v4a0.5 0.5 0 0 1-.5.5H7.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
          <svg v-else viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1" y="1" width="8" height="8" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
        </button>
        <button type="button" class="win-btn win-btn-close" :title="$t('common.close')" @click="onWindowCtrl('close')">
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" />
          </svg>
        </button>
      </div>

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
      <div class="main">
        <main class="content">
          <QueryWorkspace
            v-if="activeConn"
            :connection="activeConn"
            :tab-command="tabCmdBus"
          />
          <ConnectionWelcome v-else @new="openNewConnection" />
        </main>
        <div class="status">
          <StatusBar />
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

/* Invisible drag strip across the top of the shell. Sidebar and main
   extend behind it — this just intercepts drag + dblclick. */
.top-drag-region {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 50px;
  z-index: 5;
  --wails-draggable: drag;
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
}
/* clear of system traffic lights */
/*
.floating-controls.mac {
  top: 10px;
}*/

/* Right-side floating control mirror. macOS pins to right:12px; Windows
   pushes left of the three caption buttons (3 × 46px = 138px) so the
   "+" doesn't sit on top of close/maximise. */
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


.main {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: var(--n-color);
  padding-top: 50px; /* keep tabs/content clear of floating controls */
}

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
  outline: 2px solid rgba(10, 132, 255, 0.55);
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
  border-top: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
}
.content > * { flex: 1 1 0; min-width: 0; min-height: 0; }

/* Status bar inside main (right work area only — sidebar extends full height). */
.status {
  flex: 0 0 22px;
  height: 22px;
  border-top: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  background: var(--n-color, transparent);
}

/* --- Windows frameless caption buttons ---
   Positioned at top-right, sized to match Windows 11 caption button
   convention (46×32 hit target). Each button has a subtle hover
   background; the close button turns red on hover. */
.window-controls {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 20;
  display: flex;
  flex-direction: row;
  align-items: stretch;
  height: 50px; /* match drag-region height */
  -webkit-app-region: no-drag;
}

.win-btn {
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
.win-btn svg {
  width: 14px;
  height: 14px;
  opacity: 0.75;
}
.win-btn:hover { background: rgba(127, 127, 127, 0.15); }
.win-btn:active { background: rgba(127, 127, 127, 0.25); }

.win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
.win-btn-close:hover svg { opacity: 1; }
.win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
.win-btn-close:active svg { opacity: 1; }

@media (prefers-color-scheme: dark) {
  .win-btn:hover { background: rgba(255, 255, 255, 0.1); }
  .win-btn:active { background: rgba(255, 255, 255, 0.16); }
  .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
  .win-btn-close:hover svg { opacity: 1; }
  .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
}
</style>
