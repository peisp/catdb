<script setup lang="ts">
// ResultTable —— SQL 编辑器的结果网格。
// 业务装配只剩：选区追踪、剪贴板 Cmd+C、原生上下文菜单状态推送、底部 footer。
// 渲染（虚拟化、列宽、选区高亮、键盘导航）全部下沉到 DataGrid；
// 右键菜单走 Wails 原生（CLAUDE.md 规则 11），状态通过 setActiveGridContext 同步。
import { onBeforeUnmount, onMounted } from 'vue'
import DataGrid from '../data-grid/DataGrid.vue'
import { useTableSelection, type SelectionRange } from '../../composables/useTableSelection'
import { setActiveGridContext } from '../../api/gridContextMenu'
import type { QueryColumn } from '../../stores/query'

const props = defineProps<{
  columns: QueryColumn[]
  rows: any[][]
  done: boolean
  fetching: boolean
  truncated: boolean
  rowsTotal: number
  /** Optional table name for INSERT/UPDATE generation. When omitted those
   *  native context-menu items silently no-op. */
  tableName?: string
  /** Primary-key column names for UPDATE generation. */
  pkColumns?: string[]
}>()
const emit = defineEmits<{
  (e: 'load-more'): void
}>()

const sel = useTableSelection()

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
    rows: props.rows,
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
  if ((e.metaKey || e.ctrlKey) && e.key === 'c') {
    e.preventDefault()
    copyToClipboard(sel.formatTSV(props.rows, colNames(), false))
  }
}

onMounted(() => document.addEventListener('keydown', onDocKeyDown))
onBeforeUnmount(() => document.removeEventListener('keydown', onDocKeyDown))
</script>

<template>
  <div class="result">
    <div class="grid-wrap">
      <DataGrid
        :columns="columns"
        :rows="rows"
        :fetching="fetching"
        @selection-change="onSelectionChange"
        @cell-context-menu="onCellContextMenu"
        @load-more="emit('load-more')"
      />
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
}
.grid-wrap {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
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
.mute { opacity: 0.55; }
.truncated { color: #d0a000; }
</style>
