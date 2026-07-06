<script setup lang="ts">
import { ref } from 'vue'
import type { ConnectionProfile } from '../../api/connections'
import { useQueryStore } from '../../stores/query'
import { Window } from '@wailsio/runtime'
import { system as systemApi } from '../../api'
import AppIcon from '../shared/AppIcon.vue'
import unplugIcon from '../../assets/icons/unplug.svg?raw'
import fileCodeCornerIcon from '../../assets/icons/file-code-corner.svg?raw'
import arrowLeftRightIcon from '../../assets/icons/arrow-left-right.svg?raw'
import tableIcon from '../../assets/icons/table-2.svg?raw'
import databaseZapIcon from '../../assets/icons/database-zap.svg?raw'

const queryStore = useQueryStore()

const props = defineProps<{ activeConn: ConnectionProfile | null; sidebarVisible: boolean }>()

const isWin = !navigator.platform.includes('Mac')
const isMaximised = ref(false)
async function onWindowCtrl(cmd: 'min' | 'max' | 'close') {
  if (cmd === 'min') { await Window.Minimise(); return }
  if (cmd === 'close') { await Window.Close(); return }
  await Window.ToggleMaximise()
  isMaximised.value = await Window.IsMaximised()
}
async function toggleMaximise() {
  await onWindowCtrl('max')
}

function openNewConnection() {
  void systemApi.openConnectionEditor('', '')
}
function openNewQuery() {
  if (!props.activeConn) return
  const n = queryStore.tabsForConn(props.activeConn.id).filter((t: any) => t.kind === 'query').length + 1
  queryStore.addTab(props.activeConn.id, { kind: 'query', title: `Query ${n}` })
}
function openTransferDialog() {
  void systemApi.openTransferDialog()
}
function openStructureSyncDialog() {
  void systemApi.openStructureSyncDialog()
}
function openDataSyncDialog() {
  void systemApi.openDataSyncDialog()
}
</script>

<template>
  <div class="toolbar" :class="{ 'sidebar-closed': !sidebarVisible, win: isWin }" @dblclick.self="toggleMaximise">
    <button type="button" class="toolbar-btn" @click="openNewConnection">
      <AppIcon :src="unplugIcon" :size="14" />
      {{ $t('appShell.newConnection') }}
    </button>
    <button type="button" class="toolbar-btn" @click="openTransferDialog">
      <AppIcon :src="arrowLeftRightIcon" :size="14" />
      {{ $t('transfer.dataTransfer') }}
    </button>
    <button type="button" class="toolbar-btn" @click="openStructureSyncDialog">
      <AppIcon :src="tableIcon" :size="14" />
      {{ $t('structSync.toolbarLabel') }}
    </button>
    <button type="button" class="toolbar-btn" @click="openDataSyncDialog">
      <AppIcon :src="databaseZapIcon" :size="14" />
      {{ $t('dataSync.toolbarLabel') }}
    </button>
    <button type="button" class="toolbar-btn" :disabled="!activeConn" @click="openNewQuery">
      <AppIcon :src="fileCodeCornerIcon" :size="14" />
      {{ $t('tabBar.newQuery') }}
    </button>
    <span class="toolbar-spacer"></span>
    <!-- Windows frameless caption buttons (minimise / maximise / close). -->
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
  </div>
</template>

<style scoped>
.toolbar {
  flex: 0 0 53px;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 8px;
  --wails-draggable: drag;
}
.toolbar-btn {
  --wails-draggable: no-drag;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 10px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 12px;
  cursor: default;
  white-space: nowrap;
  transition: background 80ms ease;
}
.toolbar-btn:hover { background: rgba(127, 127, 127, 0.12); }
.toolbar-btn:active { background: rgba(127, 127, 127, 0.2); }
.toolbar-btn:disabled { opacity: 0.35; pointer-events: none; }
.toolbar { transition: margin-left 0.35s cubic-bezier(0.4, 0, 0.2, 1); }
.toolbar.sidebar-closed { margin-left: 150px; }
.toolbar.win.sidebar-closed { margin-left: 50px; }
.toolbar.win { background: #fff; }
@media (prefers-color-scheme: dark) {
  .toolbar.win { background: #1e1e1e; }
}
/* Spacer pushes the window controls to the right side of the toolbar. */
.toolbar-spacer { flex: 1 1 0; min-width: 0; }

/* Windows frameless caption buttons — right-aligned in the toolbar. */
.window-controls {
  --wails-draggable: no-drag;
  display: flex;
  flex-direction: row;
  align-items: stretch;
  height: 100%;
}
.win-btn {
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
  .win-btn-close:active svg { opacity: 1; }
}
</style>
