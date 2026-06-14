<script setup lang="ts">
// TableBrowser — paginated table data viewer with inline cell editing.
//
// Editing rules (CLAUDE.md #4, MVP.md M3):
//   - Tables with no primary/unique key are read-only — banner shown, no
//     edit affordances.
//   - Each cell edit is a one-row UPDATE via EditService.ApplyChange, keyed
//     on the original row's PK values.
//   - Optimistic: the new value is applied to the local row immediately.
//     If ApplyChange fails (network, constraint, RowsAffected==0 meaning
//     the row was changed under us), we roll back and show the error.
import { computed, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NInput,
  NInputNumber,
  NPagination,
  NSpin,
  NTag,
  useMessage,
} from 'naive-ui'
import { edit as editApi, metadata as metaApi } from '../api'
import type { BrowseResult, ColumnMeta } from '../api/metadata'
import ExportDialog from './ExportDialog.vue'

const props = defineProps<{
  connId: string
  db: string
  table: string
}>()

const message = useMessage()

const pageSize = ref(100)
const page = ref(1)

const browse = ref<BrowseResult | null>(null)
const loading = ref(false)

const columns = computed<ColumnMeta[]>(() => browse.value?.columns ?? [])
const rows = computed<any[][]>(() => browse.value?.rows ?? [])
const pk = computed<string[]>(() => browse.value?.primaryKey ?? [])
const readOnly = computed(() => !(browse.value?.hasUniqueKey ?? false))

// Per-column widths — reset when the column set changes.
const DEFAULT_COL_W = 160
const MIN_COL_W = 60
const colWidths = ref<number[]>([])

watch(
  columns,
  (cols) => {
    const old = colWidths.value
    colWidths.value = cols.map((_, i) => old[i] ?? DEFAULT_COL_W)
  },
  { immediate: true },
)

const gridTemplateColumns = computed(() =>
  `56px ${colWidths.value.map((w) => w + 'px').join(' ')}`,
)
const gridWidthPx = computed(() => {
  let sum = 56
  for (const w of colWidths.value) sum += w
  return sum
})

function onColResizeDown(e: PointerEvent, colIdx: number) {
  e.preventDefault()
  e.stopPropagation()
  const startX = e.clientX
  const startW = colWidths.value[colIdx] ?? DEFAULT_COL_W
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

async function load() {
  loading.value = true
  try {
    const result = await metaApi.browseTable(
      props.connId,
      props.db,
      props.table,
      pageSize.value,
      (page.value - 1) * pageSize.value,
    )
    browse.value = result
  } catch (e) {
    message.error(`browse failed: ${String(e)}`)
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(
  () => [props.connId, props.db, props.table, page.value, pageSize.value],
  load,
)

// Inline editing -----------------------------------------------------------

const editing = ref<{ rowIdx: number; colIdx: number } | null>(null)
const draft = ref<any>(null)
const exportOpen = ref(false)

function isEditableCell(rowIdx: number, colIdx: number): boolean {
  if (readOnly.value) return false
  const colName = columns.value[colIdx]?.name
  // PK columns are also editable but warn the user — changing a PK key
  // moves the row; for M3 we allow non-PK only to keep things tight.
  return !!colName && !pk.value.includes(colName)
}

function startEdit(rowIdx: number, colIdx: number) {
  if (!isEditableCell(rowIdx, colIdx)) return
  editing.value = { rowIdx, colIdx }
  draft.value = rows.value[rowIdx]?.[colIdx] ?? ''
}

function cancelEdit() {
  editing.value = null
  draft.value = null
}

function pkValuesOf(rowIdx: number): Record<string, any> {
  const map: Record<string, any> = {}
  const row = rows.value[rowIdx]
  if (!row) return map
  for (const k of pk.value) {
    const i = columns.value.findIndex((c) => c.name === k)
    if (i >= 0) map[k] = row[i]
  }
  return map
}

async function commitEdit() {
  const e = editing.value
  if (!e) return
  const col = columns.value[e.colIdx]
  if (!col) { cancelEdit(); return }
  const oldValue = rows.value[e.rowIdx]?.[e.colIdx]
  const newValue = coerceForType(draft.value, col)
  if (newValue === oldValue) { cancelEdit(); return }
  // Optimistic apply.
  const original = rows.value[e.rowIdx]
  const updated = original.slice()
  updated[e.colIdx] = newValue
  rows.value[e.rowIdx] = updated
  const pos = { ...e }
  cancelEdit()

  try {
    const res = await editApi.applyChange(props.connId, {
      op: 'update',
      db: props.db,
      table: props.table,
      pk: pkValuesOf(pos.rowIdx),
      values: { [col.name]: newValue },
    })
    if (res.rowsAffected === 0) {
      throw new Error('row not found — likely modified by another session')
    }
    message.success(`updated (${res.rowsAffected} row)`)
  } catch (err) {
    // Roll back the optimistic write.
    rows.value[pos.rowIdx] = original
    message.error(`update failed: ${String(err)}`)
  }
}

function coerceForType(raw: any, col: ColumnMeta): any {
  if (raw == null) return null
  const lt = (col as any).logicalType
  if (lt === 'int' || lt === 'bigint' || lt === 'float' || lt === 'decimal') {
    if (raw === '') return null
    const n = Number(raw)
    return Number.isFinite(n) ? n : raw
  }
  if (lt === 'bool') {
    if (typeof raw === 'boolean') return raw
    return String(raw).toLowerCase() === 'true' || raw === 1 || raw === '1'
  }
  return raw
}

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
  <div class="tb">
    <div class="toolbar">
      <span class="title mono">{{ db }}.{{ table }}</span>
      <n-tag v-if="readOnly" size="small" type="warning">read-only · no primary key</n-tag>
      <n-tag v-else size="small" type="info">PK: {{ pk.join(', ') }}</n-tag>
      <span class="grow" />
      <n-button size="tiny" @click="load" :disabled="loading">Refresh</n-button>
      <n-button size="tiny" @click="exportOpen = true">Export…</n-button>
    </div>

    <ExportDialog
      v-model:show="exportOpen"
      :source="{ kind: 'table', connId, db, table, defaultName: `${db}.${table}` }"
    />

    <n-alert v-if="readOnly" type="warning" :show-icon="false" class="banner">
      This table has no primary or unique key. Editing is disabled to avoid
      ambiguous UPDATE/DELETE statements.
    </n-alert>

    <n-spin :show="loading" class="data-spin">
      <div class="scroller">
        <div
          class="layout"
          :style="{ 'min-width': '100%', width: gridWidthPx + 'px' }"
        >
          <div
            class="grid"
            :style="{ 'grid-template-columns': gridTemplateColumns }"
          >
            <div class="hd idx">#</div>
            <div
              v-for="(c, i) in columns"
              :key="'h' + i"
              class="hd"
              :title="c.nativeType"
            >
              <span class="col-name">{{ c.name }}</span>
              <span class="col-type mono">{{ c.nativeType }}</span>
              <div class="col-resize" @pointerdown="onColResizeDown($event, i)" />
            </div>

            <template v-for="(row, rowIdx) in rows" :key="rowIdx">
              <div class="cell idx mono mute" :class="{ zebra: rowIdx % 2 === 1 }">{{ (page - 1) * pageSize + rowIdx + 1 }}</div>
              <div
                v-for="(c, colIdx) in columns"
                :key="colIdx"
                class="cell mono"
                :class="{
                  editable: isEditableCell(rowIdx, colIdx),
                  editing: editing?.rowIdx === rowIdx && editing?.colIdx === colIdx,
                  'is-null': isNull(row[colIdx]),
                  pk: pk.includes(c.name),
                  zebra: rowIdx % 2 === 1,
                }"
                @dblclick="startEdit(rowIdx, colIdx)"
              >
                <template v-if="editing?.rowIdx === rowIdx && editing?.colIdx === colIdx">
                  <n-input
                    v-model:value="draft"
                    size="tiny"
                    autofocus
                    @keydown.enter.prevent="commitEdit"
                    @keydown.esc.prevent="cancelEdit"
                    @blur="commitEdit"
                  />
                </template>
                <template v-else>
                  <span v-if="isNull(row[colIdx])" class="null-tag">NULL</span>
                  <span v-else>{{ renderCell(row[colIdx]) }}</span>
                </template>
              </div>
            </template>
          </div>
          <!-- Tail: fills remaining vertical space; bg gradient renders
               virtual empty rows so the table reaches the bottom of the
               pane even when there are few rows. -->
          <div class="filler" />
        </div>
      </div>
    </n-spin>

    <div class="footer">
      <n-pagination
        v-model:page="page"
        v-model:page-size="pageSize"
        :item-count="-1"
        :page-sizes="[50, 100, 200, 500]"
        show-size-picker
        size="small"
      />
      <span class="mono mute">rows {{ (page - 1) * pageSize + 1 }} – {{ (page - 1) * pageSize + rows.length }}</span>
    </div>
  </div>
</template>

<style scoped>
.tb { display: flex; flex-direction: column; height: 100%; min-width: 0; min-height: 0; overflow: hidden; }
.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--n-border-color);
  background: var(--n-color);
  font-size: 12px;
  min-width: 0;
  flex: 0 0 auto;
}
.title { font-size: 12px; }
.grow { flex: 1 1 auto; }
.banner { margin: 6px 8px; flex: 0 0 auto; }
.data-spin { flex: 1 1 auto; min-width: 0; min-height: 0; overflow: hidden; padding: 6px; }
.data-spin :deep(.n-spin-container),
.data-spin :deep(.n-spin-content) {
  height: 100%;
  min-width: 0;
  min-height: 0;
}
/* The ONLY element that scrolls in this view. Header is sticky inside .grid. */
.scroller {
  height: 100%;
  width: 100%;
  min-width: 0;
  overflow: auto;
  background: var(--n-card-color, transparent);
}
.layout {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}
.grid {
  display: grid;
  font-size: 12px;
  position: relative;
  flex: 0 0 auto;
  border: none;
}
/* OPAQUE sticky header — won't show data through on scroll. */
.hd {
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 0 8px;
  background-color: rgb(245, 246, 247);
  background-color: light-dark(rgb(245, 246, 247), rgb(40, 40, 42));
  border-bottom: 1px solid var(--n-border-color);
  border-right: 1px solid var(--n-divider-color);
  font-weight: 500;
  height: 26px;
  position: sticky;
  top: 0;
  z-index: 2;
}
@media (prefers-color-scheme: dark) {
  .hd { background-color: rgb(40, 40, 42); }
}
.hd .col-name { font-size: 12px; line-height: 1.2; }
.hd .col-type { font-size: 10px; opacity: 0.55; line-height: 1; }
.hd.idx { text-align: right; padding-right: 8px; justify-content: center; align-items: flex-end; }
.cell.idx { text-align: right; padding-right: 8px; justify-content: flex-end; opacity: 0.6; }
.cell {
  padding: 0 8px;
  border-bottom: 1px solid var(--n-divider-color);
  border-right: 1px solid var(--n-divider-color);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  height: 24px;
  display: flex;
  align-items: center;
  position: relative;
}
.cell.editable { cursor: text; }
.cell.editing :deep(.n-input) { font-size: 12px; }
.cell.is-null { opacity: 0.85; }
/* Background priority (low → high): zebra < pk < editing. Multi-selectors
   make sure each tier's specificity beats the lower tiers, regardless of
   which other classes the cell also has. */
.cell.zebra {
  background-color: rgb(250, 250, 251);
  background-color: light-dark(rgb(250, 250, 251), rgb(34, 34, 36));
}
@media (prefers-color-scheme: dark) {
  .cell.zebra { background-color: rgb(34, 34, 36); }
}
.cell.pk,
.cell.pk.zebra { background-color: rgba(255, 200, 0, 0.06); }
.cell.editing,
.cell.editing.zebra,
.cell.editing.pk,
.cell.editing.pk.zebra {
  background: var(--n-color-target);
  padding: 0;
  overflow: visible;
}

/* Column resize handle on the right edge of each header cell. */
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
  top: 5px;
  bottom: 5px;
  left: 50%;
  width: 1px;
  background-color: transparent;
  transition: background-color 120ms ease-out;
}
.col-resize:hover::after,
.col-resize:active::after {
  background-color: var(--n-primary-color, #18a058);
}
.null-tag {
  display: inline-block;
  padding: 0 4px;
  border: 1px solid var(--n-divider-color);
  border-radius: 2px;
  font-size: 10px;
  opacity: 0.5;
}
.mute { opacity: 0.55; font-size: 10px; }

/* Filler row at the bottom: extends down to fill remaining vertical space
   inside the scroller. The repeating-linear-gradient draws virtual empty
   row separators, aligned with 24px row height. */
.filler {
  flex: 1 1 auto;
  min-height: 24px;
  width: 100%;
  background-image: repeating-linear-gradient(
    to bottom,
    transparent 0,
    transparent 23px,
    var(--n-divider-color, rgba(127,127,127,0.18)) 23px,
    var(--n-divider-color, rgba(127,127,127,0.18)) 24px
  );
}
.footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 10px;
  border-top: 1px solid var(--n-border-color);
  background: var(--n-color);
}
</style>
