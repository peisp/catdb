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
import settingsIcon from '../../assets/icons/settings.svg?raw'
import botIcon from '../../assets/icons/bot.svg?raw'

const queryStore = useQueryStore()

const props = defineProps<{ activeConn: ConnectionProfile | null; sidebarVisible: boolean; agentOpen: boolean }>()
const emit = defineEmits<{ (e: 'toggle-agent'): void }>()

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
function openSettings() {
  void systemApi.openSettingsWindow()
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
    <button
      type="button"
      class="toolbar-btn toolbar-btn-icon"
      :class="{ active: agentOpen }"
      :title="$t('agent.panel.toggle')"
      @click="emit('toggle-agent')"
    >
      <AppIcon :src="botIcon" :size="16" />
    </button>
    <button type="button" class="toolbar-btn toolbar-btn-icon" :title="$t('settingsWindow.openSettings')" @click="openSettings">
      <AppIcon :src="settingsIcon" :size="16" />
    </button>
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
/* DESIGN.md toolbar 规格：24×24 图标钮（button-toolbar），图标 16px、
   text-primary 着色。隐藏标题栏：titlebar+toolbar 融合为 55px 一行（双平台
   统一），图标与红绿灯/侧栏浮动开关（中心 27.5px）同轴。 */
.toolbar {
  flex: 0 0 var(--catdb-toolbar-height);
  background: var(--catdb-surface-chrome);

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
  height: var(--catdb-control-height);
  padding: 0 10px;
  border: none;
  border-radius: var(--catdb-rounded-sm);
  background: transparent;
  color: var(--catdb-text-primary);
  font: inherit;
  font-size: var(--catdb-fs-small);
  white-space: nowrap;
  cursor: default;
  transition: background 130ms ease-out;
}
.toolbar-btn:hover { background: var(--catdb-hover-fill); }
.toolbar-btn:active { background: var(--catdb-pressed-fill); }
.toolbar-btn:disabled { opacity: 0.4; pointer-events: none; }
.toolbar-btn-icon { width: 24px; padding: 0; justify-content: center; }
/* Toggle button open state: accent icon + accent-soft fill (DESIGN.md button-toolbar). */
.toolbar-btn-icon.active { background: var(--catdb-accent-soft); }
.toolbar-btn-icon.active :deep(.app-icon) { opacity: 1; color: var(--catdb-accent); }
.toolbar { transition: padding-left 0.35s cubic-bezier(0.4, 0, 0.2, 1); }
/* macOS: 给红绿灯+浮动按钮让位。用 padding（而非 margin）让 chrome 底通栏
   铺满，避免让位区露出 content 底的缺口。 */
.toolbar.sidebar-closed:not(.win) { padding-left: 150px; }
.toolbar.win.sidebar-closed { padding-left: 58px; }
.toolbar.win {
  /* 与 AppSidebar .sider 的宽度动画同曲线,折叠让位的 padding 与 sider 收合同步,避免先跳后推 */
  transition: padding-left 0.35s cubic-bezier(0.4, 0, 0.2, 1);
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
.win-btn:hover { background: var(--catdb-hover-fill); }
.win-btn:active { background: var(--catdb-pressed-fill); }
.win-btn-close:hover { background: var(--catdb-error); }
.win-btn-close:hover svg { opacity: 1; }
.win-btn-close:active { background: var(--catdb-error); }
.win-btn-close:active svg { opacity: 1; }
</style>
