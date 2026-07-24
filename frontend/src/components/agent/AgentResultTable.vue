<script setup lang="ts">
// AgentResultTable — a compact inline table for a run_sql SELECT result on the
// user path (§7), rendered with the shared canvas DataGrid (the app-wide table
// component). Read-only; height grows with rows up to a cap and then scrolls
// inside the grid. The backend's truncation flag adds a "truncated" hint.
import { computed, ref } from 'vue'
import DataGrid from '../data-grid/DataGrid.vue'
import { setActiveGridContext } from '../../api/gridContextMenu'
import { LogicalType, type ColumnMeta } from '../../api/metadata'
import type { SelectionRange } from '../../composables/useTableSelection'
import { t } from '../../i18n'
import type { ResultEntry } from './types'

const props = defineProps<{ entry: ResultEntry }>()

// Synthetic column metadata: result columns arrive as bare names (§7 user
// path), so everything renders as string cells.
const columns = computed<ColumnMeta[]>(() =>
  props.entry.columns.map((name) => ({
    name,
    nativeType: '',
    logicalType: LogicalType.TypeString,
    nullable: true,
  } as ColumnMeta)),
)
const rows = computed(() => props.entry.rows as unknown[][] as any[][])

// Grid sizing: header 28 + 24 per row, capped — the grid scrolls internally
// past the cap (canvas-virtualized, no need to slice rows).
const ROW_H = 24
const HEADER_H = 28
const MAX_H = 260
const gridHeight = computed(() => Math.min(MAX_H, HEADER_H + rows.value.length * ROW_H + 4))

// Native copy menu support (catdb-grid-cell): push rows + selection into the
// grid context singleton on right-click, same pattern as ResultTable.
const selection = ref<SelectionRange | null>(null)
function onSelectionChange(p: { range: SelectionRange | null }) {
  selection.value = p.range
}
function onCellContextMenu() {
  setActiveGridContext({
    rows: rows.value,
    columnNames: props.entry.columns,
    selection: selection.value,
  })
}

const note = computed(() => t('agent.result.rowCount', { n: rows.value.length }))
</script>

<template>
  <div class="result">
    <div v-if="entry.columns.length === 0" class="empty">{{ $t('agent.result.empty') }}</div>
    <div v-else class="grid-wrap" :style="{ height: gridHeight + 'px' }">
      <!-- default-column-width 0: columns start at their header's minimum
           width (the chat dock is narrow) — widen by drag / double-click. -->
      <DataGrid
        :columns="columns"
        :rows="rows"
        :editable="false"
        :sortable="true"
        :sort-remote="false"
        :row-height="ROW_H"
        :default-column-width="0"
        @selection-change="onSelectionChange"
        @cell-context-menu="onCellContextMenu"
      />
    </div>
    <div class="foot">
      <span class="count">{{ note }}</span>
      <span v-if="entry.truncated" class="truncated">{{ $t('agent.result.truncated') }}</span>
    </div>
  </div>
</template>

<style scoped>
.result {
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  margin: 6px 0;
  overflow: hidden;
}
.grid-wrap {
  min-height: 0;
}
.empty {
  padding: 8px;
  font-size: var(--catdb-fs-mono-small);
  color: var(--catdb-text-tertiary);
  text-align: center;
}

.foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 3px 8px;
  border-top: 1px solid var(--catdb-separator);
  background: var(--catdb-surface-chrome);
}
.count { font-size: var(--catdb-fs-mini); color: var(--catdb-text-secondary); }
.truncated { font-size: var(--catdb-fs-mini); color: var(--catdb-warning); font-weight: 600; }
</style>
