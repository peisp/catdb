<script setup lang="ts">
// ResultTable —— SQL 编辑器的结果网格。
// 业务装配只剩：选区追踪、剪贴板 Cmd+C、原生上下文菜单状态推送、底部 footer。
// 渲染（虚拟化、列宽、选区高亮、键盘导航）全部下沉到 DataGrid；
// 右键菜单走 Wails 原生（CLAUDE.md 规则 11），状态通过 setActiveGridContext 同步。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import DataGrid from '../data-grid/DataGrid.vue'
import ResultFooter from './ResultFooter.vue'
import { useTableSelection, type SelectionRange } from '../../composables/useTableSelection'
import { setActiveGridContext } from '../../api/gridContextMenu'
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
watch(() => props.columns, () => { page.value = 1 })
watch(pageSize, () => { page.value = 1 })

const sel = useTableSelection()
const rootRef = ref<HTMLElement | null>(null)

function colNames(): string[] { return props.columns.map((c) => c.name) }

function onSelectionChange(p: { range: SelectionRange | null }) {
  sel.selection.value = p.range
}

function onCellContextMenu(p: { row: number; col: number }) {
  if (!sel.hasSelection() || !sel.isSelected(p.row, p.col)) {
    sel.selectCell(p.row, p.col)
  }
  // Push the live state to the native-menu singleton so whichever item the
  // user clicks (in Wails' native menu) operates against this grid's current
  // selection. Done synchronously inside the contextmenu DOM event so the
  // singleton is current by the time Go fires its callback.
  setActiveGridContext({
    rows: pagedRows.value,
    columnNames: colNames(),
    selection: sel.selection.value,
    tableName: props.tableName,
    pkColumns: props.pkColumns ?? [],
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
    copyToClipboard(sel.formatTSV(pagedRows.value))
  }
}

onMounted(() => document.addEventListener('keydown', onDocKeyDown))
onBeforeUnmount(() => document.removeEventListener('keydown', onDocKeyDown))
</script>

<template>
  <div ref="rootRef" class="result">
    <div class="grid-wrap">
      <DataGrid
        :columns="columns"
        :rows="pagedRows"
        :fetching="fetching"
        :show-types="true"
        @selection-change="onSelectionChange"
        @cell-context-menu="onCellContextMenu"
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
</style>
