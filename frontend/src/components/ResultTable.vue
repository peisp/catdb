<script setup lang="ts">
// ResultTable — Navicat-style virtualized data grid.
//
// Layout principles:
//   - Single scroll container (both axes); header is position: sticky so it
//     stays visible while scrolling vertically AND horizontally with the rows.
//   - Fixed column widths so a wide schema yields a horizontal scrollbar
//     instead of squashed text.
//   - The "rows area" has min-height = max(totalSize, available space), so
//     when the result has fewer rows than the visible area, the empty space
//     below shows a repeating row-line background — the table visually fills
//     the entire pane regardless of row count.
//   - Bottom-edge prefetch: when the user scrolls within `prefetchPx` of the
//     bottom we emit 'load-more' for the parent to call FetchMore.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { useThemeVars } from 'naive-ui'
import { useTableSelection } from '../composables/useTableSelection'
import type { QueryColumn } from '../stores/query'

const props = defineProps<{
  columns: QueryColumn[]
  rows: any[][]
  done: boolean
  fetching: boolean
  truncated: boolean
  rowsTotal: number
  /** Optional table name for INSERT/UPDATE generation. When omitted those
   *  context-menu items are disabled. */
  tableName?: string
  /** Primary-key column names for UPDATE generation. */
  pkColumns?: string[]
}>()
const emit = defineEmits<{
  (e: 'load-more'): void
}>()

const ROW_HEIGHT = 24
const IDX_COL = 56
const DATA_COL = 160
const MIN_COL_W = 60
const PREFETCH_PX = 400

const scrollerRef = ref<HTMLDivElement | null>(null)
const scrollerHeight = ref(0)

// Pull theme color for row hover — matches sidebar drag handle color.
// Naive UI's --n-* CSS vars aren't available in scoped styles on plain divs,
// so we use useThemeVars() and bind via inline style on the result container.
const themeVars = useThemeVars()
const hoverBg = computed(() => themeVars.value.primaryColorHover)

// ---- range selection + copy ----
const sel = useTableSelection()
const ctxMenu = ref<{ x: number; y: number } | null>(null)

function cellRowCol(e: MouseEvent): { row: number; col: number } | null {
  const cell = (e.target as HTMLElement).closest('[data-col-idx]')
  if (!cell) return null
  const rowEl = cell.closest('[data-row-idx]')
  if (!rowEl) return null
  const row = Number(rowEl.getAttribute('data-row-idx'))
  const col = Number(cell.getAttribute('data-col-idx'))
  if (isNaN(row) || isNaN(col)) return null
  return { row, col }
}

function onCellMouseDown(e: MouseEvent) {
  if (e.button !== 0) return
  const rc = cellRowCol(e)
  if (!rc) return
  // Don't interfere with resize handle
  if ((e.target as HTMLElement).closest('.col-resize')) return
  sel.startSelection(rc.row, rc.col)
}

function onRowsAreaMouseMove(e: MouseEvent) {
  if (!sel.selecting.value) return
  const rc = cellRowCol(e)
  if (!rc) return
  sel.extendSelection(rc.row, rc.col)
}

function onRowsAreaMouseUp() {
  if (sel.selecting.value) sel.endSelection()
}

// Document-level mouseup to catch releases outside the grid.
function onDocMouseUp() {
  if (sel.selecting.value) sel.endSelection()
}
onMounted(() => document.addEventListener('mouseup', onDocMouseUp))
onBeforeUnmount(() => document.removeEventListener('mouseup', onDocMouseUp))

// Cmd/Ctrl+C → copy selected range as TSV.
function onKeyDown(e: KeyboardEvent) {
  if (!sel.hasSelection()) return
  if ((e.metaKey || e.ctrlKey) && e.key === 'c') {
    e.preventDefault()
    e.stopPropagation()
    copyToClipboard(sel.formatTSV(props.rows, props.columns.map((c) => c.name), false))
  }
}

async function copyToClipboard(text: string) {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // Fallback: select a textarea approach could be added, but Wails webview
    // generally supports the async clipboard API.
  }
}

function onContextMenu(e: MouseEvent) {
  e.preventDefault()
  e.stopPropagation()
  ctxMenu.value = { x: e.clientX, y: e.clientY }
}

function closeCtxMenu() {
  ctxMenu.value = null
}

function ctxCopyTSV() {
  copyToClipboard(sel.formatTSV(props.rows, props.columns.map((c) => c.name), false))
  closeCtxMenu()
}
function ctxCopyInsert() {
  const tbl = props.tableName
  if (!tbl) return
  copyToClipboard(sel.formatInsert(props.rows, props.columns.map((c) => c.name), tbl))
  closeCtxMenu()
}
function ctxCopyUpdate() {
  const tbl = props.tableName
  if (!tbl) return
  copyToClipboard(sel.formatUpdate(props.rows, props.columns.map((c) => c.name), tbl, props.pkColumns ?? []))
  closeCtxMenu()
}
function ctxCopyColumns() {
  copyToClipboard(sel.formatColumns(props.columns.map((c) => c.name)))
  closeCtxMenu()
}
function ctxCopyDataPlusColumns() {
  copyToClipboard(sel.formatDataPlusColumns(props.rows, props.columns.map((c) => c.name)))
  closeCtxMenu()
}

// Per-column widths in pixels. Index 0..N-1 maps to columns[0..N-1].
// Resets on a new query (column set changes).
const colWidths = ref<number[]>([])

watch(
  () => props.columns,
  (cols) => {
    const old = colWidths.value
    colWidths.value = cols.map((_, i) => old[i] ?? DATA_COL)
  },
  { immediate: true },
)

function onColResizeDown(e: PointerEvent, colIdx: number) {
  e.preventDefault()
  e.stopPropagation()
  const startX = e.clientX
  const startW = colWidths.value[colIdx] ?? DATA_COL
  function onMove(ev: PointerEvent) {
    const dx = ev.clientX - startX
    colWidths.value[colIdx] = Math.max(MIN_COL_W, startW + dx)
  }
  function onUp() {
    document.removeEventListener('pointermove', onMove)
    document.removeEventListener('pointerup', onUp)
    document.body.style.cursor = ''
  }
  document.body.style.cursor = 'col-resize'
  document.addEventListener('pointermove', onMove)
  document.addEventListener('pointerup', onUp)
}

let ro: ResizeObserver | null = null
onMounted(() => {
  if (scrollerRef.value && typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver(() => {
      if (scrollerRef.value) scrollerHeight.value = scrollerRef.value.clientHeight
    })
    ro.observe(scrollerRef.value)
    scrollerHeight.value = scrollerRef.value.clientHeight
  }
})
onBeforeUnmount(() => { ro?.disconnect(); ro = null })

const rowVirtualizerOptions = computed(() => ({
  count: props.rows.length,
  getScrollElement: () => scrollerRef.value,
  estimateSize: () => ROW_HEIGHT,
  overscan: 10,
}))
const rowVirtualizer = useVirtualizer(rowVirtualizerOptions)
const virtualRows = computed(() => rowVirtualizer.value.getVirtualItems())
const totalSize = computed(() => rowVirtualizer.value.getTotalSize())

const gridWidth = computed(() => {
  let sum = IDX_COL
  for (const w of colWidths.value) sum += w
  return sum
})

/** Inner content height: max(totalSize, scrollerHeight - headerHeight) so
 *  the rows area always fills the visible region. Header is 24px. */
const contentHeight = computed(() => {
  const available = Math.max(0, scrollerHeight.value - ROW_HEIGHT)
  return Math.max(totalSize.value, available)
})

function onScroll(_: Event) {
  const el = scrollerRef.value
  if (!el) return
  if (props.done || props.fetching) return
  const remaining = el.scrollHeight - el.scrollTop - el.clientHeight
  if (remaining < PREFETCH_PX) {
    emit('load-more')
  }
}

watch(
  () => props.columns,
  () => {
    if (scrollerRef.value) {
      scrollerRef.value.scrollTop = 0
      scrollerRef.value.scrollLeft = 0
    }
  },
)

function renderCell(v: any): string {
  if (v == null) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'number') return String(v)
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  if (typeof v === 'object') {
    if (v.__type__ === 'bytes') return `bytes(${v.length})`
    if (v.__type__ === 'bigint') return v.value
    try { return JSON.stringify(v) } catch { return String(v) }
  }
  return String(v)
}
function isNull(v: any): boolean { return v == null }
</script>

<template>
  <div class="result" :style="{ '--hover-bg': hoverBg }" @keydown="onKeyDown" tabindex="-1">
    <div ref="scrollerRef" class="scroller" @scroll="onScroll">
      <div
        class="grid"
        :style="{
          width: gridWidth + 'px',
          minWidth: '100%',
          minHeight: '100%',
        }"
      >
        <div class="head-row" :style="{ width: gridWidth + 'px' }">
          <div class="cell head idx-cell">#</div>
          <div
            v-for="(c, i) in columns"
            :key="i"
            class="cell head"
            :style="{ width: (colWidths[i] ?? DATA_COL) + 'px' }"
            :title="c.nativeType"
          >
            <span class="col-name">{{ c.name }}</span>
            <span class="col-type mono">{{ c.nativeType }}</span>
            <!-- Resize handle. Wider invisible grab area, thin visible line. -->
            <div
              class="col-resize"
              @pointerdown="onColResizeDown($event, i)"
            />
          </div>
        </div>
        <div
          class="rows-area"
          :style="{
            width: gridWidth + 'px',
            height: contentHeight + 'px',
          }"
          @mousedown="onCellMouseDown"
          @mousemove="onRowsAreaMouseMove"
          @mouseup="onRowsAreaMouseUp"
          @contextmenu="onContextMenu"
        >
          <div
            v-for="vr in virtualRows"
            :key="vr.index"
            class="row"
            :class="{ zebra: vr.index % 2 === 1 }"
            :style="{ transform: `translateY(${vr.start}px)`, width: gridWidth + 'px' }"
            :data-row-idx="vr.index"
          >
            <div class="cell idx-cell mono mute" :class="{ selected: sel.isSelected(vr.index, -1) }" @mousedown.prevent="sel.selectRow(vr.index, columns.length)">{{ vr.index + 1 }}</div>
            <div
              v-for="(_c, j) in columns"
              :key="j"
              class="cell mono"
              :data-col-idx="j"
              :style="{ width: (colWidths[j] ?? DATA_COL) + 'px' }"
              :class="{
                'is-null': isNull(rows[vr.index]?.[j]),
                selected: sel.isSelected(vr.index, j),
              }"
            >
              <span v-if="isNull(rows[vr.index]?.[j])" class="null-tag">NULL</span>
              <span v-else>{{ renderCell(rows[vr.index]?.[j]) }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Context menu overlay -->
    <div
      v-if="ctxMenu"
      class="ctx-backdrop"
      @mousedown="closeCtxMenu"
      @contextmenu.prevent="closeCtxMenu"
    >
      <div
        class="ctx-menu"
        :style="{ left: ctxMenu.x + 'px', top: ctxMenu.y + 'px' }"
        @mousedown.stop
        @contextmenu.prevent
      >
        <div class="ctx-item" @click="ctxCopyTSV">Copy as TSV</div>
        <div class="ctx-item" :class="{ disabled: !tableName }" @click="ctxCopyInsert">Copy as INSERT</div>
        <div class="ctx-item" :class="{ disabled: !tableName }" @click="ctxCopyUpdate">Copy as UPDATE</div>
        <div class="ctx-sep" />
        <div class="ctx-item" @click="ctxCopyColumns">Copy column names</div>
        <div class="ctx-item" @click="ctxCopyDataPlusColumns">Copy data + column names</div>
      </div>
    </div>

    <div class="foot mono">
      <span>{{ rowsTotal }} rows</span>
      <span v-if="!done && !truncated" class="mute">loading more on scroll…</span>
      <span v-if="truncated" class="truncated">truncated to preview limit — use Export for full data</span>
    </div>
  </div>
</template>

<style scoped>
.result {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  min-height: 0;
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  overflow: hidden;
  background: var(--n-card-color, transparent);
}
/* Only this element scrolls — both horizontally (when columns are wide) and
   vertically (when rows exceed the visible area). Parent chain has
   min-width:0 so this never bleeds out and pushes the window. */
.scroller {
  flex: 1 1 auto;
  overflow: auto;
  min-width: 0;
  min-height: 0;
  position: relative;
}
.grid {
  position: relative;
  display: block;
}

/* Sticky header. OPAQUE background — when rows scroll up they must NOT show
   through the header. Uses light-dark() to stay opaque in both themes;
   `color-scheme: light dark` is set on :root in global.css. */
.head-row {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  height: 26px;
  /* Fallback for browsers without light-dark(); overridden by the next line
     when supported. Both values are OPAQUE so rows can never bleed through. */
  background-color: rgb(245, 246, 247);
  background-color: light-dark(rgb(245, 246, 247), rgb(40, 40, 42));
  border-bottom: 1px solid var(--n-border-color);
}
@media (prefers-color-scheme: dark) {
  .head-row { background-color: rgb(40, 40, 42); }
}

.rows-area {
  position: relative;
  /* Repeating horizontal lines that simulate empty grid rows beneath the
     actual data. Aligns with ROW_HEIGHT=24. */
  background-image: repeating-linear-gradient(
    to bottom,
    transparent 0,
    transparent 23px,
    var(--n-divider-color, rgba(127,127,127,0.18)) 23px,
    var(--n-divider-color, rgba(127,127,127,0.18)) 24px
  );
}

.row {
  position: absolute;
  top: 0;
  left: 0;
  display: flex;
  height: 24px;
  background-color: var(--n-card-color);
}
/* Zebra striping — applied to every other virtual row by index parity. */
.row.zebra {
  background-color: rgb(250, 250, 251);
  background-color: light-dark(rgb(250, 250, 251), rgb(34, 34, 36));
}
@media (prefers-color-scheme: dark) {
  .row.zebra { background-color: rgb(34, 34, 36); }
}
.row:hover { background-color: var(--hover-bg); }

.cell {
  flex: 0 0 auto;
  padding: 0 8px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  border-right: 1px solid var(--n-divider-color);
  border-bottom: 1px solid var(--n-divider-color);
  font-size: 12px;
  height: 24px;
  display: flex;
  align-items: center;
  position: relative;
}
.cell.head {
  border-bottom: none;
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
  font-weight: 500;
}
.cell.head .col-type { font-size: 10px; opacity: 0.55; line-height: 1;}
.cell.head .col-name { font-size: 12px; line-height: 1.2; }
.cell.idx-cell {
  flex: 0 0 56px;
  width: 56px;
  text-align: right;
  justify-content: flex-end;
  padding-right: 10px;
  color: var(--n-text-color-disabled);
}

/* Column resize handle — narrow visible line on hover, wider grab area. */
.col-resize {
  position: absolute;
  top: 0;
  right: -3px;
  width: 6px;
  height: 100%;
  cursor: col-resize;
  z-index: 3;
  user-select: none;
  -webkit-user-select: none;
}
.col-resize::after {
  content: '';
  position: absolute;
  top: 4px;
  bottom: 4px;
  left: 50%;
  width: 1px;
  background-color: transparent;
  transition: background-color 120ms ease-out;
}
.col-resize:hover::after,
.col-resize:active::after {
  background-color: var(--n-primary-color, #18a058);
}
.mute { opacity: 0.55; }
.is-null { opacity: 0.75; }
.null-tag {
  display: inline-block;
  padding: 0 4px;
  border: 1px solid var(--n-divider-color);
  border-radius: 2px;
  font-size: 10px;
  opacity: 0.6;
}

.foot {
  flex: 0 0 auto;
  font-size: 11px;
  padding: 4px 10px;
  border-top: 1px solid var(--n-border-color);
  background: var(--n-color);
  display: flex;
  gap: 12px;
  align-items: center;
  opacity: 0.85;
}
.truncated { color: #d0a000; }

/* ---- selection highlight ---- */
.selected {
  background-color: color-mix(in srgb, var(--n-primary-color, #2080f0) 18%, transparent) !important;
}

/* ---- context menu ---- */
.ctx-backdrop {
  position: fixed;
  inset: 0;
  z-index: 9999;
}
.ctx-menu {
  position: fixed;
  background: var(--n-color, #fff);
  border: 1px solid var(--n-border-color, #d0d0d0);
  border-radius: 4px;
  padding: 4px 0;
  min-width: 180px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.18);
  font-size: 12px;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  user-select: none;
}
.ctx-item {
  padding: 4px 16px;
  cursor: default;
  line-height: 20px;
}
.ctx-item:hover {
  background: var(--n-primary-color-hover, #2080f0);
  color: #fff;
}
.ctx-item.disabled {
  opacity: 0.4;
  pointer-events: none;
}
.ctx-sep {
  height: 1px;
  margin: 4px 8px;
  background: var(--n-divider-color, #d0d0d0);
}
</style>
