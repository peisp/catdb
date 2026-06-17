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
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NSelect, NSpin, NTag, useMessage } from 'naive-ui'
import { edit as editApi, metadata as metaApi } from '../api'
import { on } from '../api/events'
import { setActiveGridContext } from '../api/gridContextMenu'
import { useTableSelection, type SelectionRange } from '../composables/useTableSelection'
import type { BrowseResult, ColumnMeta } from '../api/metadata'
import DataGrid from './data-grid/DataGrid.vue'
import ExportDialog from './ExportDialog.vue'

const props = defineProps<{
  connId: string
  db: string
  table: string
}>()

const message = useMessage()

// pageSize == -1 means "all rows" (passed through to backend as a sentinel).
const ALL_ROWS = -1
const pageSize = ref<number>(200)
const page = ref(1)
// Decoupled from `page` so the user can type freely and only commit on Enter.
const pageInput = ref<string>('1')
const pageSizeOptions = [
  { label: '200', value: 200 },
  { label: '500', value: 500 },
  { label: '1000', value: 1000 },
  { label: '全部', value: ALL_ROWS },
]

// ---- sort state ----
interface SortState { field: number; order: 'asc' | 'desc' }
const sortColumn = ref<number>(-1)  // -1 = 未排序; 列下标
const sortOrder = ref<'asc' | 'desc' | ''>('')

const browse = ref<BrowseResult | null>(null)
const loading = ref(false)
const exportOpen = ref(false)

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
const readOnly = computed(() => {
  if (!browse.value) return false  // 数据还没加载到，不显示只读提示
  return !browse.value.hasUniqueKey
})

// ---- selection + copy ----
const sel = useTableSelection()

function colNames(): string[] { return columns.value.map((c) => c.name) }
function fullTableName(): string { return `\`${props.db}\`.\`${props.table}\`` }

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
  })
}

async function copyToClipboard(text: string) {
  if (!text) return
  try { await navigator.clipboard.writeText(text) } catch { /* ignore */ }
}

function onDocKeyDown(e: KeyboardEvent) {
  if (!sel.hasSelection()) return
  if ((e.metaKey || e.ctrlKey) && e.key === 'c') {
    e.preventDefault()
    copyToClipboard(sel.formatTSV(rows.value, colNames(), false))
  }
}
onMounted(() => document.addEventListener('keydown', onDocKeyDown))
onBeforeUnmount(() => document.removeEventListener('keydown', onDocKeyDown))

// ---- load + pagination ----

async function load() {
  loading.value = true
  try {
    const isAll = pageSize.value === ALL_ROWS
    const limit = isAll ? ALL_ROWS : pageSize.value
    const offset = isAll ? 0 : (page.value - 1) * pageSize.value
    browse.value = await metaApi.browseTable(
      props.connId, props.db, props.table, limit, offset,
      orderByName.value, sortOrder.value,
    )
  } catch (e) {
    message.error(`browse failed: ${String(e)}`)
  } finally {
    loading.value = false
  }
}

onMounted(load)
// 监听来自右键菜单（设置为NULL）的数据变更事件，自动刷新
let unsubDataChanged: (() => void) | undefined
onMounted(() => { unsubDataChanged = on('ctx:grid-data-changed', load) })
onBeforeUnmount(() => unsubDataChanged?.())

watch(
  () => [props.connId, props.db, props.table, page.value, pageSize.value, orderByName.value, sortOrder.value],
  load,
)

// 切换表/数据库时清除排序状态
watch(
  () => [props.connId, props.db, props.table],
  () => {
    sortColumn.value = -1
    sortOrder.value = ''
    page.value = 1
  },
)

// 排序变化处理：来自 DataGrid 表头点击
function onSortChange(sort: { field: number; order: 'asc' | 'desc' } | null) {
  if (!sort) {
    sortColumn.value = -1
    sortOrder.value = ''
  } else {
    sortColumn.value = sort.field
    sortOrder.value = sort.order
  }
  page.value = 1  // 排序换页时回到第一页
}

watch(page, (v) => { pageInput.value = String(v) }, { immediate: true })
watch(pageSize, () => { page.value = 1 })

const isAllRows = computed(() => pageSize.value === ALL_ROWS)
const hasPrev = computed(() => !isAllRows.value && page.value > 1)
const hasNext = computed(() => !isAllRows.value && rows.value.length >= pageSize.value)

function goPrev() { if (hasPrev.value) page.value -= 1 }
function goNext() { if (hasNext.value) page.value += 1 }
function commitPageInput() {
  const n = Math.floor(Number(pageInput.value))
  if (!Number.isFinite(n) || n < 1) { pageInput.value = String(page.value); return }
  if (n === page.value) return
  page.value = n
}

const sqlHover = ref(false)
async function copySql() {
  const sql = browse.value?.sql
  if (!sql) return
  try { await navigator.clipboard.writeText(sql); message.success('SQL copied') }
  catch (e) { message.error(`copy failed: ${String(e)}`) }
}

const rowsStart = computed(() => {
  if (rows.value.length === 0) return 0
  return isAllRows.value ? 1 : (page.value - 1) * pageSize.value + 1
})
const rowsEnd = computed(() => {
  if (rows.value.length === 0) return 0
  return isAllRows.value
    ? rows.value.length
    : (page.value - 1) * pageSize.value + rows.value.length
})

// ---- edit pipeline (DataGrid → edit-commit) ----

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
  if (newValue === p.oldValue) return
  // VTable 已乐观更新了它的内部 record。我们的 rows 是 computed 但拿到的是
  // 同一个底层数组，所以 VTable 的修改也反映到我们这边 —— 这是我们想要的。
  try {
    const res = await editApi.applyChange(props.connId, {
      op: 'update',
      db: props.db,
      table: props.table,
      pk: pkValuesOf(p.row),
      values: { [p.column.name]: newValue },
    })
    if (res.rowsAffected === 0) throw new Error('row not found — likely modified by another session')
    message.success(`updated (${res.rowsAffected} row)`)
  } catch (err) {
    // 失败 → 重新拉本页恢复真值（既同步本地 rows 也同步 VTable）
    message.error(`update failed: ${String(err)}`)
    await load()
  }
}
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
      <DataGrid
        :columns="columns"
        :rows="rows"
        :editable="!readOnly"
        :pk-columns="pk"
        :fetching="loading"
        :sort-remote="true"
        :sort-state="sortState"
        @selection-change="onSelectionChange"
        @cell-context-menu="onCellContextMenu"
        @edit-commit="onEditCommit"
        @sort-change="onSortChange"
      />
    </n-spin>

    <div class="footer">
      <div class="pager">
        <button
          class="pgbtn"
          :disabled="!hasPrev"
          title="上一页"
          @click="goPrev"
        >‹</button>
        <input
          v-model="pageInput"
          class="page-input mono"
          inputmode="numeric"
          :disabled="isAllRows"
          @keydown.enter.prevent="commitPageInput"
          @blur="commitPageInput"
        />
        <button
          class="pgbtn"
          :disabled="!hasNext"
          title="下一页"
          @click="goNext"
        >›</button>
      </div>

      <div
        class="sql-display"
        @mouseenter="sqlHover = true"
        @mouseleave="sqlHover = false"
      >
        <code class="sql-text mono" :title="browse?.sql || ''">{{ browse?.sql || '' }}</code>
        <button
          v-if="browse?.sql"
          class="copy-btn"
          :class="{ visible: sqlHover }"
          title="复制 SQL"
          @click="copySql"
        >复制</button>
      </div>

      <div class="footer-right">
        <span class="mono mute">rows {{ rowsStart }} – {{ rowsEnd }}</span>
        <n-select
          v-model:value="pageSize"
          :options="pageSizeOptions"
          size="small"
          class="size-select"
        />
      </div>
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

.footer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 10px;
  border-top: 1px solid var(--n-border-color);
  background: var(--n-color);
  flex: 0 0 auto;
  min-width: 0;
}

.pager {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
}
.pgbtn {
  width: 22px;
  height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 3px;
  font-size: 14px;
  line-height: 1;
  color: inherit;
  cursor: default;
  padding: 0;
  transition: background-color 120ms ease, border-color 120ms ease;
}
.pgbtn:hover:not(:disabled) {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}
.pgbtn:disabled {
  opacity: 0.3;
  cursor: default;
}
.page-input {
  width: 44px;
  height: 22px;
  text-align: center;
  font-size: 12px;
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  background: transparent;
  color: inherit;
  padding: 0 4px;
  outline: none;
  transition: border-color 120ms ease;
}
.page-input:focus {
  border-color: var(--n-primary-color, #18a058);
}
.page-input:disabled {
  opacity: 0.4;
}

.sql-display {
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  position: relative;
}
.sql-text {
  flex: 1 1 0;
  min-width: 0;
  font-size: 11px;
  opacity: 0.7;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.copy-btn {
  flex: 0 0 auto;
  height: 20px;
  padding: 0 8px;
  font-size: 11px;
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  background: var(--n-color, transparent);
  color: inherit;
  cursor: default;
  opacity: 0;
  pointer-events: none;
  transition: opacity 120ms ease, background-color 120ms ease;
}
.copy-btn.visible {
  opacity: 1;
  pointer-events: auto;
}
.copy-btn:hover {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}

.footer-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}
.size-select {
  width: 80px;
}
.mute { opacity: 0.55; font-size: 10px; }
</style>
