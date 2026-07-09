<script setup lang="ts">
// TableBrowser —— 对象树 → "浏览数据" 入口。
//
// 这里只剩业务装配：分页器、SQL 显示、剪贴板格式化、编辑提交流水线。
// 真正的表格渲染（虚拟化、列宽、选区、内置编辑器）下沉到 DataGrid。
//
// 编辑规则（CLAUDE.md #4, MVP.md M3）：
//   - 表没有 PK/Unique → 整张表只读，banner 提示
//   - 每次单元格编辑 = 一次基于原行 PK 的 UPDATE
//   - 乐观：先本地写入，applyChange 失败则 reload 整页恢复真值
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NInput, NSpin, NTag, useMessage } from 'naive-ui'
import { edit as editApi, metadata as metaApi } from '../../api'
import { genericUIDialect, quoteIdentWith, uiDialectForConnection, type UIDialect } from '../../api/dialect'
import { on } from '../../api/events'
import { setActiveGridContext } from '../../api/gridContextMenu'
import { useTableSelection, type SelectionRange } from '../../composables/useTableSelection'
import type { BrowseResult, ColumnMeta } from '../../api/metadata'
import DataGrid from '../data-grid/DataGrid.vue'
import { startExport } from '../../composables/useExport'
import FilterBar from './FilterBar.vue'
import ResultFooter from './ResultFooter.vue'
import { t } from '../../i18n'
import DdlPanel from '../shared/DdlPanel.vue'
import ResizeHandle from "../shared/ResizeHandle.vue";

const props = defineProps<{
  connId: string
  db: string
  /** Schema between db and table for schema-ful databases; '' otherwise. */
  schema?: string
  table: string
}>()

const message = useMessage()

// Driver UI descriptor — identifier quoting for clipboard SQL, DDL dialect.
const uiDialect = ref<UIDialect>(genericUIDialect())
watch(() => props.connId, async (id) => {
  uiDialect.value = id ? await uiDialectForConnection(id) : genericUIDialect()
}, { immediate: true })

// pageSize == -1 means "all rows" (passed through to backend as a sentinel).
const ALL_ROWS = -1
const pageSize = ref<number>(200)
const page = ref(1)
const pageSizeOptions = computed(() => [
  { label: '200', value: 200 },
  { label: '500', value: 500 },
  { label: '1000', value: 1000 },
  { label: t('resultFooter.allRows'), value: ALL_ROWS },
])

// ---- sort state ----
interface SortState { field: number; order: 'asc' | 'desc' }
const sortColumn = ref<number>(-1)  // -1 = 未排序; 列下标
const sortOrder = ref<'asc' | 'desc' | ''>('')

// ---- filter state ----
const filterWhere = ref('')
const filterOrderBy = ref('')

const browse = ref<BrowseResult | null>(null)
const loading = ref(false)
const gridRef = ref<InstanceType<typeof DataGrid> | null>(null)
const sortState = computed<SortState | null>(() => {
  if (sortColumn.value < 0) return null
  return { field: sortColumn.value, order: sortOrder.value as 'asc' | 'desc' }
})

const orderByName = computed(() => {
  if (sortColumn.value < 0) return ''
  return columns.value[sortColumn.value]?.name ?? ''
})

const columns = computed<ColumnMeta[]>(() => browse.value?.columns ?? [])
const rows = computed<any[][]>(() => browse.value?.rows ?? [])
const pk = computed<string[]>(() => browse.value?.primaryKey ?? [])
// 无主键/唯一键但列数 ≥2 → 后端标记为「整行匹配」可编辑（对齐 dbx）。
const keylessEditable = computed(() => browse.value?.keylessEditable ?? false)
// 定位一行用的「标识列」：有主键用主键；否则无键整行匹配时用全部列。
const idCols = computed<string[]>(() => {
  if (pk.value.length) return pk.value
  return keylessEditable.value ? columns.value.map((c) => c.name) : []
})
const readOnly = computed(() => {
  if (!browse.value) return false  // 数据还没加载到，不显示只读提示
  return !browse.value.hasUniqueKey && !keylessEditable.value
})

// ---- add row state ----
const addingRow = ref(false)
const newRowValues = ref<any[]>([])

// When adding, append newRowValues as the last row so VTable edits go directly
// into the newRowValues ref (persistent reference across recomputes).
const allRows = computed<any[][]>(() => {
  if (!addingRow.value) return rows.value
  return [...rows.value, newRowValues.value]
})

// ---- pending changes (cell edits queued for batch save) ----
interface PendingChange {
  row: number
  col: number
  oldValue: any
  newValue: any
  columnName: string
}
const pendingChanges = ref<Map<string, PendingChange>>(new Map())
const hasPendingChanges = computed(() => pendingChanges.value.size > 0)
// Set of "row:col" keys for cells with unsaved edits (body coords)
const dirtyCells = computed(() => new Set(pendingChanges.value.keys()))

// ---- pending row deletions ----
const deletedRows = ref<Set<number>>(new Set())
const hasDeletedRows = computed(() => deletedRows.value.size > 0)
const hasUnsavedChanges = computed(() => hasPendingChanges.value || hasDeletedRows.value)
// Rows that have any unsaved cell edits (for # column styling)
const dirtyRows = computed(() => {
  const s = new Set<number>()
  for (const key of pendingChanges.value.keys()) {
    const row = parseInt(key.split(':')[0], 10)
    s.add(row)
  }
  return s
})

// ---- export dropdown ----
function onExportSelect(ev: Event) {
  const val = (ev.target as HTMLSelectElement).value
  if (!val) return
  startExport({ kind: 'table', connId: props.connId, db: props.db, schema: props.schema ?? '', table: props.table, defaultName: `${props.db}.${props.table}` }, val as any)
  // Reset so the same format can be re-selected next time.
  ;(ev.target as HTMLSelectElement).value = ''
}

// ---- selection + copy ----
const sel = useTableSelection()
const rootRef = ref<HTMLElement | null>(null)
const hasSelection = computed(() => sel.selection.value !== null)

function colNames(): string[] { return columns.value.map((c) => c.name) }
function fullTableName(): string {
  const d = uiDialect.value
  return [props.db, props.schema, props.table]
    .filter(Boolean)
    .map((part) => quoteIdentWith(d, part!))
    .join('.')
}

function onSelectionChange(p: { range: SelectionRange | null }) {
  sel.selection.value = p.range
}

function onCellContextMenu(p: { row: number; col: number }) {
  if (!sel.hasSelection() || !sel.isSelected(p.row, p.col)) {
    sel.selectCell(p.row, p.col)
  }
  // Push live state to the native-menu singleton; Wails opens the registered
  // "catdb-grid-cell" menu and its item handlers operate against this state.
  setActiveGridContext({
    rows: rows.value,
    columnNames: colNames(),
    selection: sel.selection.value,
    connId: props.connId,
    db: props.db,
    table: props.table,
    tableName: fullTableName(),
    pkColumns: pk.value,
    idColumns: idCols.value,
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
  if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.key.toLowerCase() === 'c') {
    e.preventDefault()
    copyToClipboard(sel.formatTSV(rows.value))
  }
}
onMounted(() => document.addEventListener('keydown', onDocKeyDown))
onBeforeUnmount(() => document.removeEventListener('keydown', onDocKeyDown))

// ---- load + pagination ----

async function load() {
  pendingChanges.value = new Map()
  deletedRows.value = new Set()
  dmlSql.value = ''
  dmlLabel.value = ''
  loading.value = true
  try {
    const isAll = pageSize.value === ALL_ROWS
    const limit = isAll ? ALL_ROWS : pageSize.value
    const offset = isAll ? 0 : (page.value - 1) * pageSize.value
    browse.value = await metaApi.browseTable(
      props.connId, props.db, props.table, limit, offset,
      filterOrderBy.value ? '' : orderByName.value,
      filterOrderBy.value ? '' : sortOrder.value,
      filterWhere.value,
      filterOrderBy.value,
      props.schema ?? '',
    )
  } catch (e) {
    message.error(t('tableBrowser.browseFailed', { error: String(e) }))
  } finally {
    loading.value = false
  }
}

onMounted(load)
// 监听来自右键菜单「设置为NULL」的批量变更事件，加入待保存队列
let unsubSetNullQueue: (() => void) | undefined
onMounted(() => {
  unsubSetNullQueue = on<Array<{ row: number; col: number; oldValue: any; columnName: string }>>(
    'ctx:grid-set-null-queue',
    (changes) => {
      if (!changes.length || !browse.value?.rows) return
      const map = pendingChanges.value
      const rawRows = browse.value.rows
      for (const ch of changes) {
        const key = `${ch.row}:${ch.col}`
        map.set(key, {
          row: ch.row,
          col: ch.col,
          oldValue: ch.oldValue,
          newValue: null,
          columnName: ch.columnName,
        })
        // 直接修改源数据让 VTable 感知变化
        if (rawRows[ch.row]) rawRows[ch.row][ch.col] = null
      }
      pendingChanges.value = new Map(map)
      // 强制新引用触发 VTable 重新渲染
      browse.value = { ...browse.value, rows: [...rawRows] }
    },
  )
})
onBeforeUnmount(() => unsubSetNullQueue?.())

watch(
  () => [props.connId, props.db, props.table, page.value, pageSize.value, orderByName.value, sortOrder.value, filterWhere.value, filterOrderBy.value],
  load,
)

// 切换表/数据库时清除排序和过滤状态
watch(
  () => [props.connId, props.db, props.table],
  () => {
    sortColumn.value = -1
    sortOrder.value = ''
    filterWhere.value = ''
    filterOrderBy.value = ''
    page.value = 1
    addingRow.value = false
  },
)

// 排序变化处理：来自 DataGrid 表头点击
function onSortChange(sort: { field: number; order: 'asc' | 'desc' } | null) {
  // 过滤条 ORDER BY 激活时忽略列头点击，阻止与过滤条 ORDER BY 冲突
  if (filterOrderBy.value) return
  if (!sort) {
    sortColumn.value = -1
    sortOrder.value = ''
  } else {
    sortColumn.value = sort.field
    sortOrder.value = sort.order
  }
  page.value = 1  // 排序换页时回到第一页
}

watch(pageSize, () => { page.value = 1 })

const isAllRows = computed(() => pageSize.value === ALL_ROWS)
const hasPrev = computed(() => !isAllRows.value && page.value > 1)
const hasNext = computed(() => {
  if (isAllRows.value) return false
  if (totalRows.value !== null) return page.value * pageSize.value < totalRows.value
  return rows.value.length >= pageSize.value
})

const dmlSql = ref('')
const dmlLabel = ref('')

// ---- on-demand total row count (COUNT(*) can be a slow scan → user-triggered) ----
const totalRows = ref<number | null>(null)
const countLoading = ref(false)

async function loadTotal() {
  countLoading.value = true
  try {
    totalRows.value = await metaApi.countTableRows(props.connId, props.db, props.table, filterWhere.value, props.schema ?? '')
  } catch (e) {
    message.error(t('resultFooter.countFailed', { error: String(e) }))
  } finally {
    countLoading.value = false
  }
}

// 换表或过滤条件变化 → 总数失效
watch(
  () => [props.connId, props.db, props.table, filterWhere.value],
  () => { totalRows.value = null },
)

// ---- edit pipeline (DataGrid → edit-commit) ----

// 返回行的「原始」PK 值，用作 UPDATE/DELETE 的 WHERE 条件。
// 允许编辑 PK 列后，rows[rowIdx] 已被乐观更新成新值，不能直接拿来定位行；
// 改过的 PK 列要回退到 pendingChanges 里记录的改前值。
function pkValuesOf(rowIdx: number): Record<string, any> {
  const map: Record<string, any> = {}
  const row = rows.value[rowIdx]
  if (!row) return map
  for (const k of idCols.value) {
    const i = columns.value.findIndex((c) => c.name === k)
    if (i < 0) continue
    const pending = pendingChanges.value.get(`${rowIdx}:${i}`)
    map[k] = pending ? pending.oldValue : row[i]
  }
  return map
}

function coerceForType(raw: any, col: ColumnMeta): any {
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

async function onEditCommit(p: {
  row: number; col: number; oldValue: any; newValue: any; column: ColumnMeta
}) {
  const newValue = coerceForType(p.newValue, p.column)
  // null → "" = 单元格原本为 NULL，用户点进去未做修改就退出，不算变更
  if (newValue === p.oldValue || (p.oldValue == null && newValue === '')) return
  // New row edit: update newRowValues in-place, no pending tracking.
  if (addingRow.value && p.row >= rows.value.length) {
    newRowValues.value[p.col] = newValue
    return
  }
  // Queue the change — VTable has optimistically updated the record in-place.
  const map = pendingChanges.value
  const key = `${p.row}:${p.col}`
  map.set(key, {
    row: p.row,
    col: p.col,
    oldValue: p.oldValue,
    newValue,
    columnName: p.column.name,
  })
  pendingChanges.value = new Map(map) // trigger reactivity
  // Clear previous DML display since this edit isn't saved yet
  dmlSql.value = ''
  dmlLabel.value = ''
}

// ---- delete rows ----

function deleteSelectedRows() {
  const range = sel.selection.value
  if (!range) return
  const minR = Math.min(range.startRow, range.endRow)
  const maxR = Math.max(range.startRow, range.endRow)
  const newSet = new Set(deletedRows.value)
  for (let r = minR; r <= maxR; r++) {
    if (r < rows.value.length) newSet.add(r)
  }
  deletedRows.value = newSet
  // 删除行：把该行所有未保存编辑恢复为原值并清出待保存队列，
  // 避免保存时执行无效的 UPDATE（行马上要被 DELETE）。
  const map = pendingChanges.value
  const rawRows = browse.value?.rows
  for (const [key, ch] of map) {
    if (!newSet.has(ch.row)) continue
    if (rawRows?.[ch.row]) rawRows[ch.row][ch.col] = ch.oldValue
    map.delete(key)
  }
  pendingChanges.value = new Map(map)
  // Clear selection
  sel.selection.value = null
  // 强制新 rows 引用触发 VTable 重绘以应用灰色背景（同 set-null 的模式）
  if (browse.value) {
    browse.value = { ...browse.value, rows: [...browse.value.rows] }
  }
  dmlSql.value = ''
  dmlLabel.value = ''
}

// ---- add row handlers ----

function startAddRow() {
  newRowValues.value = columns.value.map(() => null)
  addingRow.value = true
  // 等新增行渲染后滚动到底部，让其可见
  nextTick(() => gridRef.value?.scrollToBottom())
}

async function saveNewRow() {
  // Collect non-null values into a column-name-keyed map.
  const values: Record<string, any> = {}
  for (let i = 0; i < columns.value.length; i++) {
    const v = newRowValues.value[i]
    if (v !== null && v !== undefined && v !== '') {
      values[columns.value[i].name] = v
    }
  }
  try {
    const res = await editApi.applyChange(props.connId, {
      op: 'insert',
      db: props.db,
      schema: props.schema ?? '',
      table: props.table,
      values,
    })
    message.success(t('tableBrowser.rowInserted'))
    dmlSql.value = res.sql
    dmlLabel.value = 'INSERT'
    addingRow.value = false
    await load()
  } catch (err) {
    message.error(t('tableBrowser.insertFailed', { error: String(err) }))
  }
}

function cancelAddRow() {
  addingRow.value = false
  load()
}

// ---- batch save / discard ----

async function saveChanges() {
  const changes = Array.from(pendingChanges.value.values())
  const deletes = Array.from(deletedRows.value)
  if (!changes.length && !deletes.length) return
  loading.value = true
  let saved = 0
  let lastSQL = ''
  let lastLabel = ''
  // Process cell edits (UPDATE) —— 按行合并成一条 UPDATE：一行的多个改动放进
  // 同一个 SET，WHERE 用改前的原始 PK。否则若一行同时改了 PK 和其他列，PK 先
  // 单独更新后，后续按旧 PK 定位的 UPDATE 就会落空。
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
        db: props.db,
        table: props.table,
        pk: pkValuesOf(rowIdx),
        values,
      })
      if (res.rowsAffected > 0) {
        saved++
        lastSQL = res.sql
        lastLabel = 'UPDATE'
      }
    } catch (err) {
      message.error(t('common.saveFailed', { error: String(err) }))
      pendingChanges.value = new Map()
      deletedRows.value = new Set()
      await load()
      return
    }
  }
  // Process row deletions (DELETE)
  for (const rowIdx of deletes) {
    try {
      const res = await editApi.applyChange(props.connId, {
        op: 'delete',
        db: props.db,
        table: props.table,
        pk: pkValuesOf(rowIdx),
      })
      if (res.rowsAffected > 0) {
        saved++
        lastSQL = res.sql
        lastLabel = 'DELETE'
      }
    } catch (err) {
      message.error(t('common.deleteFailed', { error: String(err) }))
      pendingChanges.value = new Map()
      deletedRows.value = new Set()
      await load()
      return
    }
  }
  message.success(t('tableBrowser.savedChanges', { n: saved }))
  dmlSql.value = lastSQL
  dmlLabel.value = lastLabel
  pendingChanges.value = new Map()
  deletedRows.value = new Set()
  if (!addingRow.value) await load()
}

function discardChanges() {
  pendingChanges.value = new Map()
  deletedRows.value = new Set()
  load()
}

// ---- columns drawer ----
const columnsDrawerOpen = ref(false)
const columnFilter = ref('')
// browse 的列元数据来自查询结果（rs.Columns()），不含表字段注释；
// 注释需另外通过 listColumns 拉取，按列名建映射。
const commentMap = ref<Map<string, string>>(new Map())

async function loadColumnComments() {
  try {
    const cols = await metaApi.listColumns(props.connId, props.db, props.table, props.schema ?? '')
    const map = new Map<string, string>()
    for (const c of cols) if (c.comment) map.set(c.name, c.comment)
    commentMap.value = map
  } catch { /* 注释拉取失败不影响列表本身 */ }
}

function toggleColumnsDrawer() {
  columnsDrawerOpen.value = !columnsDrawerOpen.value
  if (columnsDrawerOpen.value) {
    ddlPanelOpen.value = false  // 字段面板与 DDL 侧栏互斥，最多开一个
    if (!commentMap.value.size) loadColumnComments()
  }
}

// ---- 字段面板宽度（左边缘可拖动） ----
const MIN_PANEL_W = 160
const MAX_PANEL_W = 520
const panelWidth = ref(240)
const resizing = ref(false)
let dragStartX = 0
let dragStartW = 0

function onResizePointerDown(ev: PointerEvent) {
  if (ev.button !== 0) return
  resizing.value = true
  dragStartX = ev.clientX
  dragStartW = panelWidth.value
  ;(ev.currentTarget as HTMLElement).setPointerCapture(ev.pointerId)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onResizePointerMove(ev: PointerEvent) {
  if (!resizing.value) return
  // 面板在右侧、把手在其左边缘：指针左移 → 面板变宽
  const raw = dragStartW + (dragStartX - ev.clientX)
  panelWidth.value = Math.max(MIN_PANEL_W, Math.min(MAX_PANEL_W, raw))
  // 网格跟手重绘由 DataGrid 内置的 ResizeObserver 负责，这里不必手动触发
}

function onResizePointerUp() {
  if (!resizing.value) return
  resizing.value = false
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
}

// 携带原始列下标，定位时直接用作 body 列号
const filteredColumns = computed(() => {
  const q = columnFilter.value.trim().toLowerCase()
  const list = columns.value.map((c, index) => ({
    col: c,
    index,
    comment: commentMap.value.get(c.name) ?? c.comment ?? '',
  }))
  if (!q) return list
  return list.filter(({ col, comment }) =>
    col.name.toLowerCase().includes(q) || comment.toLowerCase().includes(q),
  )
})

// 切换表/数据库时收起抽屉、清空筛选与注释缓存
watch(
  () => [props.connId, props.db, props.table],
  () => { columnsDrawerOpen.value = false; columnFilter.value = ''; commentMap.value = new Map() },
)

function jumpToColumn(index: number) {
  gridRef.value?.scrollToColumn(index)
}

// ---- filter handlers ----
function onFilterApply(where: string, orderByClause: string) {
  filterWhere.value = where
  filterOrderBy.value = orderByClause
  page.value = 1  // 回到第1页
}

function onFilterClear() {
  filterWhere.value = ''
  filterOrderBy.value = ''
  page.value = 1  // 回到第1页
}

// ---- DDL 侧栏 ----
const ddlPanelOpen = ref(false)
const ddl = ref('')
const ddlLoading = ref(false)

function toggleDdlPanel() {
  ddlPanelOpen.value = !ddlPanelOpen.value
  if (ddlPanelOpen.value) {
    columnsDrawerOpen.value = false  // 字段面板与 DDL 侧栏互斥，最多开一个
    void loadDdl()
  }
}

async function loadDdl() {
  ddlLoading.value = true
  try {
    ddl.value = await metaApi.getCreateTable(props.connId, props.db, props.table, props.schema ?? '')
  } catch (e: any) {
    ddl.value = ''
    message.error(t('tablesOverview.ddlFailed', { error: String(e) }))
  } finally {
    ddlLoading.value = false
  }
}
</script>

<template>
  <div ref="rootRef" class="tb">
    <div class="toolbar">
      <span class="title mono">{{ db }}.{{ table }}</span>
      <n-tag v-if="readOnly" size="small" type="warning">{{ $t('tableBrowser.readOnlyTag') }}</n-tag>
      <n-tag v-else-if="keylessEditable" size="small" type="warning">{{ $t('tableBrowser.keylessTag') }}</n-tag>
      <n-tag v-else size="small" type="info">PK: {{ pk.join(', ') }}</n-tag>
      <span class="grow"/>
      <template v-if="addingRow">
        <n-button size="tiny" type="primary" :disabled="loading" @click="saveNewRow">{{ $t('common.save') }}</n-button>
        <n-button size="tiny" :disabled="loading" @click="cancelAddRow">{{ $t('common.cancel') }}</n-button>
      </template>
      <n-button v-else size="tiny" :disabled="loading || readOnly" @click="startAddRow">+</n-button>
      <n-button
          v-if="!addingRow"
          size="tiny"
          :disabled="loading || readOnly || !hasSelection"
          @click="deleteSelectedRows"
      >-
      </n-button>
      <template v-if="hasUnsavedChanges && !addingRow">
        <n-button size="tiny" type="primary" :disabled="loading" @click="saveChanges">{{ $t('common.save') }}</n-button>
        <n-button size="tiny" :disabled="loading" @click="discardChanges">{{ $t('common.cancel') }}</n-button>
      </template>
      <n-button size="tiny" @click="load" :disabled="loading">{{ $t('common.refresh') }}</n-button>
      <n-button size="tiny" :title="$t('tableBrowser.columnsPanel')" @click="toggleColumnsDrawer">
        {{ $t('tableBrowser.columnsPanel') }}
      </n-button>
      <n-button size="tiny" :type="ddlPanelOpen ? 'primary' : 'default'" @click="toggleDdlPanel">
        {{ $t('tablesOverview.action.ddl') }}
      </n-button>
      <select class="export-select" @change="onExportSelect">
        <option value="" disabled selected>{{ $t('common.exportPlaceholder') }}</option>
        <option value="csv">CSV</option>
        <option value="xlsx">Excel</option>
        <option value="json">JSON</option>
        <option value="sql">SQL</option>
      </select>
    </div>

    <n-alert v-if="readOnly" type="warning" :show-icon="false" class="banner">
      {{ $t('tableBrowser.readOnlyBanner') }}
    </n-alert>
    <n-alert v-else-if="keylessEditable" type="warning" :show-icon="false" class="banner">
      {{ $t('tableBrowser.keylessBanner') }}
    </n-alert>

    <div class="data-area">
      <FilterBar
          :conn-id="connId"
          :db="db"
          :table="table"
          :columns="columns"
          @apply="onFilterApply"
          @clear="onFilterClear"
      />
      <div class="data-body">
        <DataGrid
            ref="gridRef"
            :columns="columns"
            :rows="allRows"
            :editable="!readOnly"
            :pk-columns="pk"
            :dirty-cells="dirtyCells"
            :deleted-rows="deletedRows"
            :dirty-rows="dirtyRows"
            :fetching="loading"
            :sort-remote="true"
            :sort-state="sortState"
            :show-types="true"
            @selection-change="onSelectionChange"
            @cell-context-menu="onCellContextMenu"
            @edit-commit="onEditCommit"
            @sort-change="onSortChange"
        />

        <aside
            v-if="columnsDrawerOpen"
            class="cols-panel"
            :style="{ width: panelWidth + 'px', flexBasis: panelWidth + 'px' }"
        >
          <ResizeHandle
              orientation="vertical"
              class="cols-resize"
              :active="resizing"
              @pointerdown="onResizePointerDown"
              @pointermove="onResizePointerMove"
              @pointerup="onResizePointerUp"
              @pointercancel="onResizePointerUp"
          />
          <div class="cols-head">
            <span class="cols-title">{{ $t('tableBrowser.columnsTitle') }}</span>
            <button class="cols-close" :title="$t('common.close')" @click="columnsDrawerOpen = false">×</button>
          </div>
          <div class="cols-filter">
            <n-input
                v-model:value="columnFilter"
                size="small"
                clearable
                :placeholder="$t('tableBrowser.columnsFilter')"
            />
          </div>
          <div class="cols-list">
            <button
                v-for="item in filteredColumns"
                :key="item.index"
                class="col-item"
                @click="jumpToColumn(item.index)"
            >
              <span class="col-name mono">{{ item.col.name }}</span>
              <span v-if="item.comment" class="col-comment">{{ item.comment }}</span>
            </button>
            <div v-if="!filteredColumns.length" class="cols-empty mute">{{ $t('tableBrowser.columnsEmpty') }}</div>
          </div>
        </aside>

        <DdlPanel
            variant="panel"
            :ddl="ddl"
            :dialect="uiDialect"
            :loading="ddlLoading"
            :table="table"
            :active="ddlPanelOpen"
            @close="ddlPanelOpen = false"
        />
      </div>
    </div>

    <ResultFooter
        v-model:page="page"
        v-model:page-size="pageSize"
        :page-size-options="pageSizeOptions"
        :has-prev="hasPrev"
        :has-next="hasNext"
        :pager-disabled="isAllRows"
        :total="isAllRows ? rows.length : totalRows"
        :can-load-total="!isAllRows"
        :count-loading="countLoading"
        :sql="browse?.sql || ''"
        :dml-sql="dmlSql"
        :dml-label="dmlLabel"
        @load-total="loadTotal"
    />
  </div>
</template>

<style scoped>
.tb { display: flex; flex-direction: column; height: 100%; min-width: 0; min-height: 0; overflow: hidden; }

.data-area {
  margin: 6px;
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--app-content-bg);
}
.data-body {
  flex: 1 1 auto;
  display: flex;
  flex-direction: row;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.toolbar {
  height: 35px;
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
.export-select {
  font-size: 12px;
  height: 22px;
  padding: 0 4px;
  border-radius: 3px;
  border: 1px solid var(--n-border-color, rgba(127,127,127,0.25));
  background: transparent;
  color: inherit;
  cursor: pointer;
  outline: none;
  font-family: inherit;
}
.export-select:hover:not(:disabled) {
  background: var(--n-color-target, rgba(127,127,127,0.12));
}
.export-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.banner { margin: 6px 8px; flex: 0 0 auto; }

.mute { opacity: 0.55; font-size: 10px; }

.cols-panel {
  position: relative;
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-left: 1px solid var(--n-border-color);
  background: var(--n-color);
}
/* 把手贴在面板左边缘（覆盖 ResizeHandle 默认 .is-vertical 的 right:0） */
.cols-panel > .cols-resize.is-vertical { right: auto; left: 0; }
.cols-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 8px 6px 12px;
  border-bottom: 1px solid var(--n-border-color);
  flex: 0 0 auto;
}
.cols-title { font-size: 12px; font-weight: 500; }
.cols-close {
  width: 20px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: inherit;
  font-size: 16px;
  line-height: 1;
  cursor: default;
  opacity: 0.6;
  transition: background-color 120ms ease, opacity 120ms ease;
}
.cols-close:hover { background: var(--n-color-target, rgba(127, 127, 127, 0.12)); opacity: 1; }
.cols-filter {
  padding: 8px 10px;
  border-bottom: 1px solid var(--n-border-color);
  flex: 0 0 auto;
}
.cols-list { flex: 1 1 auto; min-height: 0; overflow-y: auto; padding: 4px 0; }
.col-item {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 1px;
  width: 100%;
  text-align: left;
  padding: 5px 12px;
  border: none;
  border-left: 2px solid transparent;
  background: transparent;
  color: inherit;
  cursor: default;
  font-family: inherit;
  transition: background-color 120ms ease, border-color 120ms ease;
}
.col-item:hover {
  background: var(--n-color-target, rgba(127, 127, 127, 0.1));
  border-left-color: var(--n-primary-color, #18a058);
}
.col-name { font-size: 12px; }
.col-comment {
  font-size: 11px;
  opacity: 0.55;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}
.cols-empty { padding: 12px; text-align: center; }

</style>
