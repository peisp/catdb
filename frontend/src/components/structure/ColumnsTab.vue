<script setup lang="ts">
// ColumnsTab — editable list of column definitions.
//
// Edit model: the parent owns the ColumnDraft[] array; this component mutates
// items in place + uses Vue's reactivity to propagate. New/dropped rows go
// through splice() / push() / a single `update:modelValue` emit so the parent
// stays in sync even when re-renders are needed.
//
// Reorder: native HTML5 drag-and-drop, but only the leftmost drag-handle cell
// is `draggable="true"`. That keeps input fields fully clickable + selectable;
// only the handle initiates a drag.
//
// Drop-visualization layers (the user has to see exactly where the column
// will land at a glance):
//  1. The dragged row dims + gets a dashed outline (no doubt which one moves).
//  2. The drop-target row gets a 3 px primary-color bar on top or bottom edge,
//     plus a filled circular marker anchored to the drag-handle column at the
//     same edge — easy to spot even when the row is wide.
//  3. The whole table wrap shifts to `cursor: grabbing` while a drag is in
//     flight so the user feels the modal state.
//  4. A live status chip in the toolbar reads "拖动: 第 N → 第 M" with the
//     computed final landing slot (post-splice compensation), so the user can
//     double-check the destination before releasing.
import { computed, ref } from 'vue'
import {
  NButton,
  NCheckbox,
  NInput,
  NPopover,
  NText,
} from 'naive-ui'
import {
  emptyColumnDraft,
  type ColumnDraft,
} from '../../lib/alterPlan'

const props = defineProps<{
  modelValue: ColumnDraft[]
  /** Disable editing while we're in the middle of an Apply. */
  busy?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: ColumnDraft[]): void
}>()

// We mutate in place + emit the array reference back. The parent's
// reactivity tracking picks up the change.
function commit() {
  emit('update:modelValue', props.modelValue)
}

// ---- row actions ----------------------------------------------------------

function addRow(insertAfter?: number) {
  const list = props.modelValue.slice()
  const row = emptyColumnDraft()
  if (insertAfter == null || insertAfter >= list.length - 1) {
    list.push(row)
  } else {
    list.splice(insertAfter + 1, 0, row)
  }
  emit('update:modelValue', list)
}

function deleteRow(idx: number) {
  const list = props.modelValue.slice()
  list.splice(idx, 1)
  emit('update:modelValue', list)
}

// ---- drag-to-reorder ------------------------------------------------------

const dragFrom = ref<number | null>(null)
const dragOverIdx = ref<number | null>(null)
const dragOverPos = ref<'top' | 'bottom'>('top')

function onDragStart(e: DragEvent, idx: number) {
  if (props.busy) return
  if (!e.dataTransfer) return
  dragFrom.value = idx
  e.dataTransfer.effectAllowed = 'move'
  // Firefox requires *some* data on the transfer for the drag to start at all.
  e.dataTransfer.setData('text/plain', String(idx))

  // Only the handle cell is `draggable="true"`, so the browser's default drag
  // image would be that tiny ⋮⋮ chip alone — looks broken on a wide row.
  // Build a one-off offscreen table containing just this row and feed it to
  // setDragImage so the *whole row* follows the cursor.
  const handle = e.currentTarget as HTMLElement
  const tr = handle.closest('tr') as HTMLTableRowElement | null
  if (tr) {
    const ghost = buildDragGhost(tr)
    const rect = tr.getBoundingClientRect()
    e.dataTransfer.setDragImage(
      ghost,
      Math.max(0, Math.min(rect.width, e.clientX - rect.left)),
      Math.max(0, Math.min(rect.height, e.clientY - rect.top)),
    )
    // The browser snapshots synchronously inside setDragImage; remove the
    // node next frame so we don't leak detached DOM into the body.
    requestAnimationFrame(() => ghost.remove())
  }
}

// Build a standalone `<table>` wrapping a clone of `row`, positioned offscreen
// but rendered, suitable for use as a setDragImage source. Critical details:
//  - cloneNode misses live `<input>` values; we copy them via setAttribute.
//  - the parent table's <colgroup> defines column widths via fixed layout, so
//    we clone it into the ghost or all cells would collapse to content width.
//  - is-dragging / is-drop-* classes on the clone would override the ghost
//    look-and-feel, so we strip them.
function buildDragGhost(row: HTMLTableRowElement): HTMLElement {
  const origTable = row.closest('table')
  const refRect = (origTable ?? row).getBoundingClientRect()

  const wrap = document.createElement('div')
  wrap.style.position = 'fixed'
  wrap.style.top = '0'
  wrap.style.left = '-10000px'
  wrap.style.pointerEvents = 'none'
  wrap.style.width = `${refRect.width}px`
  wrap.style.background = 'var(--n-card-color, #fff)'
  wrap.style.boxShadow = '0 8px 24px rgba(0, 0, 0, 0.25)'
  wrap.style.borderRadius = '4px'
  wrap.style.overflow = 'hidden'
  wrap.style.opacity = '0.95'

  const table = document.createElement('table')
  table.style.width = '100%'
  table.style.tableLayout = 'fixed'
  table.style.borderCollapse = 'separate'
  table.style.fontSize = '12px'

  if (origTable) {
    const cg = origTable.querySelector('colgroup')
    if (cg) table.appendChild(cg.cloneNode(true))
  }

  const tbody = document.createElement('tbody')
  const clone = row.cloneNode(true) as HTMLTableRowElement
  clone.className = ''
  const origInputs = row.querySelectorAll('input')
  const cloneInputs = clone.querySelectorAll('input')
  origInputs.forEach((src, i) => {
    const dst = cloneInputs[i] as HTMLInputElement | undefined
    if (dst) dst.setAttribute('value', src.value)
  })
  tbody.appendChild(clone)
  table.appendChild(tbody)
  wrap.appendChild(table)
  document.body.appendChild(wrap)
  return wrap
}

function onRowDragOver(e: DragEvent, idx: number) {
  if (dragFrom.value == null) return
  // preventDefault on dragover is what tells the browser this is a valid drop
  // target. Without it onDrop never fires.
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  const tr = e.currentTarget as HTMLElement
  const rect = tr.getBoundingClientRect()
  const mid = rect.top + rect.height / 2
  dragOverIdx.value = idx
  dragOverPos.value = e.clientY < mid ? 'top' : 'bottom'
}

function onRowDrop(e: DragEvent, idx: number) {
  if (dragFrom.value == null) {
    resetDragState()
    return
  }
  e.preventDefault()
  const from = dragFrom.value
  // Target slot (gap between rows) — `top` means insert above idx, `bottom`
  // means insert below idx.
  let to = idx + (dragOverPos.value === 'bottom' ? 1 : 0)
  // Dropping below the dragged row's original position shifts the target by
  // one once we splice the row out — compensate so the visual indicator
  // matches the actual landing slot.
  if (from < to) to--
  if (from !== to) {
    const list = props.modelValue.slice()
    const [row] = list.splice(from, 1)
    list.splice(to, 0, row)
    emit('update:modelValue', list)
  }
  resetDragState()
}

// Container-level fallback. Row-level @dragover only fires while the cursor
// sits *on* a row — so the gap between the last row's bottom and the wrap's
// bottom is a dead zone, which made it impossible to drop a column at the
// very end. We catch that here: if the cursor is past the last row, force
// dragOverIdx = lastIdx + pos = 'bottom' so the same drop pipeline runs.
function onWrapDragOver(e: DragEvent) {
  if (dragFrom.value == null) return
  if (props.modelValue.length === 0) return
  const wrap = e.currentTarget as HTMLElement
  const lastRow = wrap.querySelector('tbody tr:last-of-type') as HTMLElement | null
  if (!lastRow) return
  if (e.clientY > lastRow.getBoundingClientRect().bottom) {
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    dragOverIdx.value = props.modelValue.length - 1
    dragOverPos.value = 'bottom'
  }
}

function onWrapDrop(e: DragEvent) {
  if (dragFrom.value == null) {
    resetDragState()
    return
  }
  // If a row's @drop already handled this, it cleared dragFrom in
  // resetDragState; the early-return above takes that path.
  const wrap = e.currentTarget as HTMLElement
  const lastRow = wrap.querySelector('tbody tr:last-of-type') as HTMLElement | null
  if (lastRow && e.clientY > lastRow.getBoundingClientRect().bottom) {
    onRowDrop(e, props.modelValue.length - 1)
  } else {
    resetDragState()
  }
}

function onDragEnd() {
  resetDragState()
}

function resetDragState() {
  dragFrom.value = null
  dragOverIdx.value = null
}

// Computed landing slot — replicates the same splice-compensation logic used
// in onRowDrop so the status chip and the drop indicator agree on the final
// position even when dragging downward.
const dropTargetIndex = computed<number | null>(() => {
  if (dragFrom.value == null || dragOverIdx.value == null) return null
  const from = dragFrom.value
  let to = dragOverIdx.value + (dragOverPos.value === 'bottom' ? 1 : 0)
  if (from < to) to--
  return to
})
const isDragging = computed(() => dragFrom.value != null)
const showStatusChip = computed(
  () =>
    isDragging.value &&
    dropTargetIndex.value != null &&
    dropTargetIndex.value !== dragFrom.value,
)

function rowClass(idx: number, row: ColumnDraft): Record<string, boolean> {
  return {
    'is-new': !row.origName,
    'is-renamed': !!row.origName && row.origName !== row.name,
    'is-dragging': dragFrom.value === idx,
    'is-drop-top': dragOverIdx.value === idx && dragOverPos.value === 'top' && dragFrom.value !== idx,
    'is-drop-bottom': dragOverIdx.value === idx && dragOverPos.value === 'bottom' && dragFrom.value !== idx,
  }
}

// ---- default-clause widget binding ----------------------------------------

function hasDefault(row: ColumnDraft): boolean {
  return row.default !== undefined
}
function toggleDefault(row: ColumnDraft, on: boolean) {
  row.default = on ? '' : undefined
  commit()
}
function setDefault(row: ColumnDraft, val: string) {
  row.default = val
  commit()
}

// ---- type quick-pick options ----------------------------------------------
// User can still type any string in n-input; the popover is just a hint list.

const COMMON_TYPES = [
  'TINYINT',
  'SMALLINT',
  'INT',
  'BIGINT',
  'INT UNSIGNED',
  'BIGINT UNSIGNED',
  'DECIMAL(10,2)',
  'FLOAT',
  'DOUBLE',
  'VARCHAR(255)',
  'CHAR(64)',
  'TEXT',
  'LONGTEXT',
  'JSON',
  'DATE',
  'TIME',
  'DATETIME',
  'TIMESTAMP',
  'TINYINT(1)',
  'BLOB',
  'LONGBLOB',
] as const
function applyType(row: ColumnDraft, t: string) {
  row.nativeType = t
  commit()
}

// ---- header tooltips ------------------------------------------------------

const COL_TITLES: Record<string, string> = {
  pk: '主键 — 启用后字段进入 PRIMARY KEY 子句',
  ai: '自增 — 仅整型列有效，且每表只能有一个',
  null: '允许 NULL',
  default: '默认值；勾选后启用输入框，输入 NULL/CURRENT_TIMESTAMP 等不会被加引号',
  drag: '按住此处拖动以重新排序字段',
}
</script>

<template>
  <div class="cols-tab" :class="{ 'drag-active': isDragging }">
    <div
      class="cols-table-wrap"
      @dragend="onDragEnd"
      @dragover="onWrapDragOver"
      @drop="onWrapDrop"
    >
      <table class="cols-table">
        <colgroup>
          <col style="width: 22px" />
          <col style="width: 32px" />
          <col style="width: 28%" />
          <col style="width: 22%" />
          <col style="width: 44px" />
          <col style="width: 44px" />
          <col style="width: 44px" />
          <col style="width: 24%" />
          <col style="width: 26%" />
          <col style="width: 40px" />
        </colgroup>
        <thead>
          <tr>
            <th class="th-drag" :title="COL_TITLES.drag"></th>
            <th class="th-idx">#</th>
            <th>列名</th>
            <th>类型</th>
            <th :title="COL_TITLES.pk">PK</th>
            <th :title="COL_TITLES.null">NN</th>
            <th :title="COL_TITLES.ai">AI</th>
            <th :title="COL_TITLES.default">默认值</th>
            <th>注释</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, i) in modelValue"
            :key="row._key"
            :class="rowClass(i, row)"
            @dragover="onRowDragOver($event, i)"
            @drop="onRowDrop($event, i)"
          >
            <td
              class="td-drag"
              :draggable="!busy"
              :title="COL_TITLES.drag"
              @dragstart="onDragStart($event, i)"
            >
              <span class="drag-handle">⋮⋮</span>
            </td>
            <td class="td-idx">{{ i + 1 }}</td>
            <td>
              <n-input
                v-model:value="row.name"
                size="tiny"
                placeholder="column_name"
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td>
              <n-popover trigger="focus" placement="bottom-start" :show-arrow="false" :width="220">
                <template #trigger>
                  <n-input
                    v-model:value="row.nativeType"
                    size="tiny"
                    placeholder="VARCHAR(255)"
                    :disabled="busy"
                    @update:value="commit"
                  />
                </template>
                <div class="type-picker">
                  <div class="type-picker-hint">
                    <n-text depth="3" style="font-size: 11px">常用类型（点选填入）</n-text>
                  </div>
                  <div class="type-picker-grid">
                    <n-button
                      v-for="t in COMMON_TYPES"
                      :key="t"
                      size="tiny"
                      quaternary
                      @click="applyType(row, t)"
                    >{{ t }}</n-button>
                  </div>
                </div>
              </n-popover>
            </td>
            <td class="td-center">
              <n-checkbox
                :checked="row.isPrimaryKey"
                :disabled="busy"
                @update:checked="(v) => { row.isPrimaryKey = !!v; commit() }"
              />
            </td>
            <td class="td-center">
              <n-checkbox
                :checked="!row.nullable"
                :disabled="busy"
                @update:checked="(v) => { row.nullable = !v; commit() }"
              />
            </td>
            <td class="td-center">
              <n-checkbox
                :checked="row.isAutoIncrement"
                :disabled="busy"
                @update:checked="(v) => { row.isAutoIncrement = !!v; commit() }"
              />
            </td>
            <td>
              <div class="default-cell">
                <n-checkbox
                  :checked="hasDefault(row)"
                  :disabled="busy"
                  @update:checked="(v) => toggleDefault(row, !!v)"
                />
                <n-input
                  :value="row.default ?? ''"
                  size="tiny"
                  placeholder="无"
                  :disabled="busy || !hasDefault(row)"
                  @update:value="(v: string) => setDefault(row, v)"
                />
              </div>
            </td>
            <td>
              <n-input
                v-model:value="row.comment"
                size="tiny"
                placeholder=""
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td class="td-actions">
              <n-button size="tiny" quaternary :disabled="busy" title="删除" @click="deleteRow(i)">✕</n-button>
            </td>
          </tr>
          <tr v-if="modelValue.length === 0" class="empty-row">
            <td colspan="10" style="text-align: center; color: var(--n-text-color-3); padding: 16px">
              暂无字段，点击下方“添加字段”
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="cols-toolbar">
      <n-button size="tiny" :disabled="busy" @click="addRow()">+ 添加字段</n-button>
      <transition name="fade">
        <span v-if="showStatusChip" class="drag-status">
          拖动：第 <b>{{ (dragFrom ?? 0) + 1 }}</b> 列 → 第
          <b>{{ (dropTargetIndex ?? 0) + 1 }}</b> 列
        </span>
      </transition>
    </div>
  </div>
</template>

<style scoped>
.cols-tab {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}
.cols-table-wrap {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}
.cols-table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  table-layout: fixed;
}
.cols-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--n-table-header-color);
  color: var(--n-text-color-2);
  font-weight: 500;
  text-align: left;
  padding: 4px 6px;
  border-bottom: 1px solid var(--n-divider-color);
  white-space: nowrap;
  user-select: none;
}
.cols-table thead th.th-drag {
  padding: 0;
  width: 22px;
}
.cols-table thead th.th-idx {
  text-align: right;
  color: var(--n-text-color-3);
}
.cols-table tbody td {
  padding: 3px 6px;
  vertical-align: middle;
  border-bottom: 1px solid var(--n-divider-color);
}
.cols-table tbody td.td-drag {
  padding: 0 2px 0 4px;
  cursor: grab;
  user-select: none;
  color: var(--n-text-color-3);
  text-align: center;
  font-size: 13px;
  line-height: 1;
}
.cols-table tbody td.td-drag:active {
  cursor: grabbing;
}
.drag-handle {
  display: inline-block;
  letter-spacing: -3px; /* visually tighten the two-column dot pattern */
}
.cols-table tbody td.td-idx {
  text-align: right;
  color: var(--n-text-color-2);
  user-select: none;
}
/* Changed rows (new or renamed) — grey out the row number as a subtle
   "this row differs from server state" hint. Replaces the earlier +/~
   markers in the drag-handle cell. */
.cols-table tbody tr.is-new td.td-idx,
.cols-table tbody tr.is-renamed td.td-idx {
  color: var(--n-text-color-3);
}
.cols-table tbody td.td-center {
  text-align: center;
}
.cols-table tbody td.td-actions {
  white-space: nowrap;
  text-align: right;
}
.cols-table tbody td.td-actions :deep(.n-button) {
  padding: 0 4px;
  min-width: 20px;
}
.cols-table tbody tr:hover td {
  background: var(--n-hover-color);
}

/* ---- Drag visuals --------------------------------------------------------
   Layered cues so the user can't miss the destination:
   - dragged row: dimmed + dashed-feel outline via inset shadow
   - drop target: 3 px primary bar on top/bottom + filled circle anchored to
     the drag-handle column at that same edge
   - while any drag is in flight, the whole table dims slightly + cursor flips
     to grabbing so the modal state is obvious
*/
.cols-tab.drag-active .cols-table-wrap {
  cursor: grabbing;
}
.cols-tab.drag-active .cols-table tbody tr:not(.is-dragging) td {
  /* Subtle de-emphasis on non-target rows so the indicator pops harder */
  background: transparent;
}
.cols-table tbody tr.is-dragging td {
  opacity: 0.4;
  background: var(--n-table-header-color) !important;
  box-shadow: inset 0 0 0 1px var(--n-divider-color);
}
.cols-table tbody tr.is-dragging td.td-drag {
  color: var(--n-primary-color);
}

/* Drop indicator: 3px primary bar across the entire row */
.cols-table tbody tr.is-drop-top td {
  box-shadow: inset 0 3px 0 0 var(--n-primary-color);
}
.cols-table tbody tr.is-drop-bottom td {
  box-shadow: inset 0 -3px 0 0 var(--n-primary-color);
}
/* Highlighted drop target row gets a faint primary tint as backdrop */
.cols-table tbody tr.is-drop-top td,
.cols-table tbody tr.is-drop-bottom td {
  background: color-mix(in srgb, var(--n-primary-color) 8%, transparent);
}

/* Filled circle marker anchored to the drag-handle column edge — gives the
   bar a clear "anchor point" and is visible even on very wide tables. */
.cols-table tbody td.td-drag {
  position: relative;
}
.cols-table tbody tr.is-drop-top td.td-drag::before,
.cols-table tbody tr.is-drop-bottom td.td-drag::before {
  content: '';
  position: absolute;
  left: 2px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--n-primary-color);
  box-shadow: 0 0 0 2px var(--n-card-color);
  pointer-events: none;
  z-index: 2;
}
.cols-table tbody tr.is-drop-top td.td-drag::before {
  top: -5px;
}
.cols-table tbody tr.is-drop-bottom td.td-drag::before {
  bottom: -5px;
}

.default-cell {
  display: flex;
  gap: 6px;
  align-items: center;
}
.default-cell :deep(.n-input) {
  flex: 1 1 auto;
  min-width: 0;
}
.type-picker {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.type-picker-hint {
  padding-bottom: 4px;
  border-bottom: 1px solid var(--n-divider-color);
}
.type-picker-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 2px;
}
.type-picker-grid :deep(.n-button) {
  justify-content: flex-start;
}
.cols-toolbar {
  padding: 6px 8px;
  border-top: 1px solid var(--n-divider-color);
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 10px;
}
/* Live "from N → to M" chip while dragging */
.drag-status {
  font-size: 11px;
  color: var(--n-text-color-2);
  padding: 2px 8px;
  border-radius: 10px;
  background: color-mix(in srgb, var(--n-primary-color) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--n-primary-color) 30%, transparent);
  white-space: nowrap;
  user-select: none;
}
.drag-status b {
  color: var(--n-primary-color);
  font-weight: 600;
  padding: 0 1px;
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 120ms ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
