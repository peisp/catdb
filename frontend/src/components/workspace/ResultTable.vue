<script setup lang="ts">
// ResultTable —— SQL 编辑器的结果网格。
// 业务装配只剩：选区追踪、剪贴板 Cmd+C、原生上下文菜单状态推送、底部 footer。
// 渲染（虚拟化、列宽、选区高亮、键盘导航）全部下沉到 DataGrid；
// 右键菜单走 Wails 原生（CLAUDE.md 规则 11），状态通过 setActiveGridContext 同步。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import DataGrid from '../data-grid/DataGrid.vue'
import ResultFooter from './ResultFooter.vue'
import { useTableSelection, type SelectionRange } from '../../composables/useTableSelection'
import { setActiveGridContext } from '../../api/gridContextMenu'
import { on as onEvent } from '../../api/events'
import * as editApi from '../../api/edit'
import type { QueryColumn } from '../../stores/query'
import { t } from '../../i18n'

const props = defineProps<{
  columns: QueryColumn[]
  rows: any[][]
  done: boolean
  fetching: boolean
  rowsTotal: number
  /** SQL that produced this result — shown in the footer. */
  sql?: string
  /** Optional table name for INSERT/UPDATE generation. When omitted those
   *  native context-menu items silently no-op. */
  tableName?: string
  /** Primary-key column names for UPDATE generation. */
  pkColumns?: string[]
  /** Connection id, required for inline editing. */
  connId: string
  /** When non-null, the result maps to a single table and inline editing
   *  may be available (subject to PK detection). */
  editTable?: { db: string; table: string } | null
}>()
const emit = defineEmits<{
  (e: 'load-more'): void
  (e: 'export', format: string): void
}>()

// ---- client-side paging（结果集已全量驻留内存，这里只做切片展示） ----
const ALL_ROWS = -1
const page = ref(1)
const pageSize = ref(500)
const pageSizeOptions = computed(() => [
  { label: '200', value: 200 },
  { label: '500', value: 500 },
  { label: '1000', value: 1000 },
  { label: t('resultFooter.allRows'), value: ALL_ROWS },
])
const isAllRows = computed(() => pageSize.value === ALL_ROWS)
const pagedRows = computed<any[][]>(() => {
  if (isAllRows.value) return props.rows
  const start = (page.value - 1) * pageSize.value
  return props.rows.slice(start, start + pageSize.value)
})
const hasPrev = computed(() => !isAllRows.value && page.value > 1)
const hasNext = computed(() => !isAllRows.value && page.value * pageSize.value < props.rows.length)
// 新一次执行会换掉 columns 数组引用 → 回到第 1 页
watch(() => props.columns, () => {
  page.value = 1
  pendingChanges.value = new Map()
  deletedRows.value = new Set()
})
watch(pageSize, () => { page.value = 1 })

const sel = useTableSelection()
const rootRef = ref<HTMLElement | null>(null)
const message = useMessage()

// ---- inline editing state (used when editTable is set) ----
interface PendingChange {
  row: number; col: number; oldValue: any; newValue: any; columnName: string
}
const pendingChanges = ref<Map<string, PendingChange>>(new Map())
const deletedRows = ref<Set<number>>(new Set())
const pkColumns = ref<string[]>([])
const saving = ref(false)
const dirtyCells = computed(() => new Set(pendingChanges.value.keys()))
const dirtyRows = computed(() => {
  const s = new Set<number>()
  for (const key of pendingChanges.value.keys()) {
    s.add(parseInt(key.split(':')[0], 10))
  }
  return s
})
const hasPending = computed(() => pendingChanges.value.size > 0)
const editable = computed(() => {
  if (!props.editTable || saving.value) return false
  if (pkColumns.value.length === 0) return false
  // All PK columns must be present in the result (case-insensitive,
  // matching MySQL's case-insensitive column names).
  const names = new Set(props.columns.map((c) => c.name.toLowerCase()))
  return pkColumns.value.every((k) => names.has(k.toLowerCase()))
})

function colNames(): string[] { return props.columns.map((c) => c.name) }

function onSelectionChange(p: { range: SelectionRange | null }) {
  sel.selection.value = p.range
}

function onCellContextMenu(p: { row: number; col: number }) {
  if (!sel.hasSelection() || !sel.isSelected(p.row, p.col)) {
    sel.selectCell(p.row, p.col)
  }
  const et = props.editTable
  const fullName = et ? `\`${et.db}\`.\`${et.table}\`` : props.tableName
  setActiveGridContext({
    rows: pagedRows.value,
    columnNames: colNames(),
    selection: sel.selection.value,
    tableName: fullName,
    pkColumns: pkColumns.value,
    connId: et ? props.connId : undefined,
    db: et?.db,
    table: et?.table,
  })
}

async function copyToClipboard(text: string) {
  if (!text) return
  try { await navigator.clipboard.writeText(text) } catch { /* ignore */ }
}

function onDocKeyDown(e: KeyboardEvent) {
  if (!sel.hasSelection()) return
  // 隐藏标签页（v-show 的 show:lazy 面板）不响应，避免多个 grid 抢 Cmd+C
  if (!rootRef.value?.offsetParent) return
  // 焦点在 CodeMirror / input / textarea 中时不拦截 Cmd+C，让本地复制正常工作
  const el = e.target as HTMLElement | null
  if (el?.closest?.('.cm-editor') || el?.tagName === 'INPUT' || el?.tagName === 'TEXTAREA') return
  // 网格失焦（点击 DDL 面板等其他区域后）释放 Cmd+C，让原生复制选中文本
  const active = document.activeElement
  if (!active || !rootRef.value.contains(active)) return
  if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.key.toLowerCase() === 'c') {
    e.preventDefault()
    copyToClipboard(sel.formatTSV(pagedRows.value))
  }
}

// ---- inline editing helpers ----

function actualRow(pagedRow: number): number {
  return isAllRows.value ? pagedRow : pagedRow + (page.value - 1) * pageSize.value
}

function coerceForType(raw: any, col: any): any {
  if (raw == null) return null
  const lt = col.logicalType
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

async function onEditCommit(p: { row: number; col: number; oldValue: any; newValue: any; column: any }) {
  const r = actualRow(p.row)
  const newValue = coerceForType(p.newValue, p.column)
  if (newValue === p.oldValue || (p.oldValue == null && newValue === '')) return
  const map = pendingChanges.value
  const key = `${r}:${p.col}`
  map.set(key, { row: r, col: p.col, oldValue: p.oldValue, newValue, columnName: p.column.name })
  pendingChanges.value = new Map(map)
}

function pkValuesOf(rowIdx: number): Record<string, any> {
  const map: Record<string, any> = {}
  const row = props.rows[rowIdx]
  if (!row) return map
  for (const k of pkColumns.value) {
    const i = colIndex(k)
    if (i < 0) continue
    const pending = pendingChanges.value.get(`${rowIdx}:${i}`)
    map[k] = pending ? pending.oldValue : row[i]
  }
  return map
}

function colIndex(name: string): number {
  const lower = name.toLowerCase()
  return props.columns.findIndex((c) => c.name.toLowerCase() === lower)
}

async function saveEditChanges() {
  const changes = Array.from(pendingChanges.value.values())
  if (!changes.length) return
  const et = props.editTable
  if (!et) return
  saving.value = true
  let saved = 0
  let lastSQL = ''
  const byRow = new Map<number, PendingChange[]>()
  for (const ch of changes) {
    const arr = byRow.get(ch.row) ?? []
    arr.push(ch)
    byRow.set(ch.row, arr)
  }
  for (const [rowIdx, rowChanges] of byRow) {
    const values: Record<string, any> = {}
    for (const ch of rowChanges) values[ch.columnName] = ch.newValue
    try {
      const res = await editApi.applyChange(props.connId, {
        op: 'update',
        db: et.db,
        table: et.table,
        pk: pkValuesOf(rowIdx),
        values,
      })
      if (res.rowsAffected > 0) {
        saved++
        lastSQL = res.sql
      }
    } catch (err) {
      message.error(t('common.saveFailed', { error: String(err) }))
      pendingChanges.value = new Map()
      saving.value = false
      return
    }
  }
  if (saved > 0) message.success(t('tableBrowser.savedChanges', { n: saved }))
  pendingChanges.value = new Map()
  saving.value = false
}

function discardEditChanges() {
  for (const ch of pendingChanges.value.values()) {
    if (props.rows[ch.row]) props.rows[ch.row][ch.col] = ch.oldValue
  }
  pendingChanges.value = new Map()
  deletedRows.value = new Set()
}

// ---- subscriptions ----

let unsubSetNullQueue: (() => void) | undefined

onMounted(() => {
  document.addEventListener('keydown', onDocKeyDown)
  unsubSetNullQueue = onEvent<Array<{ row: number; col: number; oldValue: any; columnName: string }>>(
    'ctx:grid-set-null-queue',
    (changes) => {
      if (!changes.length) return
      const map = pendingChanges.value
      for (const ch of changes) {
        const r = actualRow(ch.row)
        const key = `${r}:${ch.col}`
        map.set(key, { row: r, col: ch.col, oldValue: ch.oldValue, newValue: null, columnName: ch.columnName })
        if (props.rows[r]) props.rows[r][ch.col] = null
      }
      pendingChanges.value = new Map(map)
    },
  )
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onDocKeyDown)
  unsubSetNullQueue?.()
})

// Watch editTable changes → fetch PKs for inline editing.
watch(() => props.editTable, async (et) => {
  pendingChanges.value = new Map()
  deletedRows.value = new Set()
  if (!et) {
    pkColumns.value = []
    return
  }
  try {
    const pks = await editApi.getPrimaryKey(props.connId, et.db, et.table)
    pkColumns.value = pks
  } catch {
    pkColumns.value = []
  }
}, { immediate: true })
</script>

<template>
  <div ref="rootRef" class="result">
    <div v-if="editable && hasPending" class="edit-actions">
      <button class="edit-btn save-btn" :disabled="saving" @click="saveEditChanges">
        {{ saving ? $t('common.saving') : $t('common.save') }}
      </button>
      <button class="edit-btn disc-btn" :disabled="saving" @click="discardEditChanges">
        {{ $t('common.discard') }}
      </button>
      <span class="mute" style="margin-left:8px;font-size:11px">{{ pendingChanges.size }} {{ $t('common.cellsChanged') }}</span>
    </div>
    <div class="grid-wrap">
      <DataGrid
        :columns="columns"
        :rows="pagedRows"
        :editable="editable"
        :pk-columns="pkColumns"
        :dirty-cells="dirtyCells"
        :dirty-rows="dirtyRows"
        :deleted-rows="deletedRows"
        :fetching="fetching"
        :show-types="true"
        @selection-change="onSelectionChange"
        @cell-context-menu="onCellContextMenu"
        @edit-commit="onEditCommit"
        @load-more="emit('load-more')"
      />
    </div>

    <ResultFooter
      v-model:page="page"
      v-model:page-size="pageSize"
      :page-size-options="pageSizeOptions"
      :has-prev="hasPrev"
      :has-next="hasNext"
      :pager-disabled="isAllRows"
      :total="rowsTotal"
      :total-partial="!done"
      :sql="sql"
      show-export
      :export-disabled="fetching"
      @export="emit('export', $event)"
    >
      <span v-if="!done" class="mute mono">{{ $t('queryTab.loadingMore') }}</span>
    </ResultFooter>
  </div>
</template>

<style scoped>
.result {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
/* Mirror TableBrowser's .data-spin: the grid area owns the 6px inset, while
   the footer below stays edge-to-edge with its own top border. */
.grid-wrap {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  padding: 6px;
}
.mute { opacity: 0.55; font-size: 10px; }

/* ---- inline edit toolbar ---- */
.edit-actions {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  padding: 4px 12px;
  gap: 6px;
  border-bottom: 1px solid var(--n-divider-color);
}
.edit-btn {
  font-size: 11px;
  padding: 2px 10px;
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  background: var(--n-color);
  color: var(--n-text-color);
  cursor: pointer;
  font-family: inherit;
  line-height: 20px;
}
.edit-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.save-btn { background: var(--n-primary-color, #18a058); color: #fff; border-color: transparent; }
.save-btn:disabled { background: var(--n-primary-color-disabled, #82c7a2); }
</style>
