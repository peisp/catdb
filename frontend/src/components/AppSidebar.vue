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
import ConnectionSidebar from './ConnectionSidebar.vue'
import ObjectTree from './ObjectTree.vue'
import type { ConnectionProfile, DriverInfo } from '../api/connections'

defineProps<{ activeConn: ConnectionProfile | null }>()

const emit = defineEmits<{
  (e: 'select', conn: ConnectionProfile): void
  (e: 'new', driver: DriverInfo): void
  (e: 'edit', conn: ConnectionProfile): void
  (e: 'openData', payload: { db: string; table: string }): void
  (e: 'openStructure', payload: { db: string; table: string }): void
  (e: 'collapse'): void
}>()

const DEFAULT_WIDTH = 200
const MIN_WIDTH = 150
const COLLAPSE_THRESHOLD = 50
const maxWidth = () => Math.max(MIN_WIDTH, Math.floor(window.innerWidth * 0.5))

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
    :style="{ width: width + 'px', flexBasis: width + 'px' }"
  >
    <div class="sider-body">
      <n-split
        v-if="activeConn"
        direction="vertical"
        :max="0.7"
        :min="0.2"
        :default-size="0.4"
        class="sider-split"
      >
        <template #1>
          <ConnectionSidebar
            @select="(c) => emit('select', c)"
            @new="(d) => emit('new', d)"
            @edit="(c) => emit('edit', c)"
          />
        </template>
        <template #2>
          <ObjectTree
            :connection="activeConn"
            @open-data="(p) => emit('openData', p)"
            @open-structure="(p) => emit('openStructure', p)"
          />
        </template>
      </n-split>
      <ConnectionSidebar
        v-else
        @select="(c) => emit('select', c)"
        @new="(d) => emit('new', d)"
        @edit="(c) => emit('edit', c)"
      />
    </div>
    <div
      class="resize-handle"
      :class="{ active: dragging }"
      title="拖动调整宽度，拖出最小宽度可折叠"
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
  border-right: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  background: var(--n-color);
  display: flex;
  flex-direction: column;
  position: relative;
}
.sider-body {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.sider-body > * { flex: 1 1 0; min-width: 0; min-height: 0; }

.sider-split { height: 100%; min-height: 0; }
.sider-split :deep(.n-split-pane) { overflow: hidden; min-width: 0; min-height: 0; }

/* Handle sits flush over the right border line, 6px wide, half on each side.
   Transparent until hover/drag so the chrome stays calm. */
.resize-handle {
  position: absolute;
  top: 0;
  right: -3px;
  width: 6px;
  height: 100%;
  cursor: col-resize;
  z-index: 10;
  touch-action: none;
  background: transparent;
  transition: background-color 120ms ease;
}
.resize-handle:hover,
.resize-handle.active {
  background: var(--n-primary-color, rgba(100, 130, 220, 0.35));
}
</style>
