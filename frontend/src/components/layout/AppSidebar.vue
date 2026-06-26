<script setup lang="ts">
// AppSidebar — left sidebar pane. When a connection is active it shows the
// connection list (top) + object tree (bottom) in a vertical split; otherwise
// just the connection list fills the pane.
//
// Width is user-resizable via a thin handle on the right edge:
//   - default = 300px
//   - min     = 150px (clamped on drag)
//   - max     = 50% of current window width (re-clamped on window resize)
//   - drag past 50px → emit `collapse` so the shell hides the sidebar
//     entirely. The gap between min (150) and the collapse threshold (50)
//     is intentional: it lets you snap to min without accidentally
//     collapsing — you have to clearly intend to drag further.
import { NSplit } from 'naive-ui'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ConnectionSidebar from '../connection/ConnectionSidebar.vue'
import ObjectTree from './ObjectTree.vue'
import ResizeHandle from '../shared/ResizeHandle.vue'
import type { ConnectionProfile } from '../../api/connections'

// On macOS, the traffic lights occupy the top-left corner, so we leave padding
// above the sidebar content. On Windows (frameless) the sidebar extends to the
// very top since custom caption buttons float at the top-right.
const isWin = !navigator.platform.includes('Mac')

defineProps<{
  activeConn: ConnectionProfile | null
  // Driven by AppShell. When true the pane animates to width 0 and fades
  // out — kept mounted so the user's last width and ObjectTree's expanded
  // state survive a collapse/expand cycle.
  collapsed?: boolean
}>()

const emit = defineEmits<{
  (e: 'select', conn: ConnectionProfile): void
  (e: 'edit', conn: ConnectionProfile): void
  (e: 'openData', payload: { db: string; table: string }): void
  (e: 'openStructure', payload: { db: string; table: string }): void
  (e: 'openTablesOverview', payload: { db: string }): void
  (e: 'collapse'): void
}>()

const DEFAULT_WIDTH = 200
const MIN_WIDTH = 180
const COLLAPSE_THRESHOLD = 50
const maxWidth = () => Math.max(MIN_WIDTH, Math.floor(window.innerWidth * 0.3))

const width = ref(DEFAULT_WIDTH)
const dragging = ref(false)
let startX = 0
let startWidth = 0
let handleEl: HTMLElement | null = null
let activePointerId: number | null = null

function onWindowResize() {
  const cap = maxWidth()
  if (width.value > cap) width.value = cap
}

function onPointerDown(ev: PointerEvent) {
  // Left button only.
  if (ev.button !== 0) return
  ev.preventDefault()
  startX = ev.clientX
  startWidth = width.value
  dragging.value = true
  handleEl = ev.currentTarget as HTMLElement
  activePointerId = ev.pointerId
  try { handleEl.setPointerCapture(ev.pointerId) } catch { /* ignore */ }
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onPointerMove(ev: PointerEvent) {
  if (!dragging.value) return
  const raw = startWidth + (ev.clientX - startX)
  if (raw < COLLAPSE_THRESHOLD) {
    cleanupDrag()
    emit('collapse')
    return
  }
  width.value = Math.min(maxWidth(), Math.max(MIN_WIDTH, raw))
}

function onPointerUp() {
  cleanupDrag()
}

function cleanupDrag() {
  if (!dragging.value) return
  dragging.value = false
  if (handleEl && activePointerId !== null) {
    try { handleEl.releasePointerCapture(activePointerId) } catch { /* ignore */ }
  }
  handleEl = null
  activePointerId = null
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
}

onMounted(() => {
  window.addEventListener('resize', onWindowResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onWindowResize)
  cleanupDrag()
})
</script>

<template>
  <aside
    class="sider"
    :class="{ collapsed, dragging, win: isWin }"
    :style="{
      width: collapsed ? '0px' : width + 'px',
      flexBasis: collapsed ? '0px' : width + 'px',
    }"
  >
    <div class="sider-body" :class="{ hidden: collapsed }">
      <n-split
        v-if="activeConn"
        direction="vertical"
        :max="0.7"
        :min="0.2"
        :default-size="0.4"
        :resize-trigger-size="4"
        class="sider-split"
      >
        <template #1>
          <ConnectionSidebar
            @select="(c) => emit('select', c)"
            @edit="(c) => emit('edit', c)"
          />
        </template>
        <template #2>
          <ObjectTree
            :connection="activeConn"
            @open-data="(p) => emit('openData', p)"
            @open-structure="(p) => emit('openStructure', p)"
            @open-tables-overview="(p) => emit('openTablesOverview', p)"
          />
        </template>
        <template #resize-trigger>
          <ResizeHandle orientation="horizontal" />
        </template>
      </n-split>
      <ConnectionSidebar
        v-else
        @select="(c) => emit('select', c)"
        @edit="(c) => emit('edit', c)"
      />
    </div>
    <ResizeHandle
      v-show="!collapsed"
      orientation="vertical"
      :active="dragging"
      :title="$t('appSidebar.resizeHint')"
      @pointerdown="onPointerDown"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
    />
  </aside>
</template>

<style scoped>
.sider {
  flex: 0 0 200px; /* must match DEFAULT_WIDTH in <script> */
  width: 200px;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  border-right: 1px solid var(--n-border-color);
  background: var(--n-color);
  display: flex;
  flex-direction: column;
  position: relative;
  /* Reserve top space for the floating window controls (toggle button +
     macOS traffic lights), so sidebar content lines up below them. */
  padding-top: 35px;
  /* Smooth collapse/expand animation matching the demo's cadence. */
  transition:
    width 0.35s cubic-bezier(0.4, 0, 0.2, 1),
    flex-basis 0.35s cubic-bezier(0.4, 0, 0.2, 1),
    padding 0.35s cubic-bezier(0.4, 0, 0.2, 1),
    border-color 0.25s ease;
}
/* Windows frameless: no traffic lights at top-left, sidebar content can
   extend all the way to the top edge. */
.sider.win {
  padding-top: 0;
}
.sider.collapsed {
  /* Hide the right divider — there's no pane edge to mark while collapsed. */
  border-right-color: transparent;
  /* Keep the top inset but flatten horizontal padding so width truly collapses. */
  padding-left: 0;
  padding-right: 0;
}
/* While the user is dragging the resize handle, the width changes every
   pointermove. The 0.35s collapse/expand transition would make those
   per-frame updates animate, so the handle feels laggy and unresponsive.
   Suppress all transitions during drag — they snap back in once dragging
   ends, so the collapse/expand animation still works. */
.sider.dragging {
  transition: none;
}
.sider-body {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  transition: opacity 0.2s ease, visibility 0.2s ease;
}
.sider-body.hidden {
  opacity: 0;
  visibility: hidden;
}
.sider-body > * { flex: 1 1 0; min-width: 0; min-height: 0; }

.sider-split { height: 100%; min-height: 0; }
.sider-split :deep(.n-split-pane) { overflow: hidden; min-width: 0; min-height: 0; }

/* n-split's resize-trigger slot positions our ResizeHandle inside the
   trigger wrapper. The wrapper is a flex item (position: static by
   default); promote it to a positioning context so the handle's
   absolute layout doesn't escape. Wrapper height already matches the
   handle (set via :resize-trigger-size). */
.sider-split :deep(.n-split__resize-trigger-wrapper) { position: relative; }
</style>
