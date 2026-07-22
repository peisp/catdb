<script setup lang="ts">
// AgentResultTable — a compact inline table for a run_sql SELECT result on the
// user path (§7). Monospace, tight rows, scrolls inside its own container.
// Renders at most ROW_CAP rows with a "showing first N of M" note; the
// backend's own truncation flag adds a separate "truncated" hint (data
// incomplete vs. this view capped are different facts).
import { computed } from 'vue'
import { t } from '../../i18n'
import type { ResultEntry } from './types'

const props = defineProps<{ entry: ResultEntry }>()

const ROW_CAP = 100

const total = computed(() => props.entry.rows.length)
const shown = computed(() => props.entry.rows.slice(0, ROW_CAP))
const capped = computed(() => total.value > ROW_CAP)

function fmt(v: unknown): string {
  if (v === null || v === undefined) return 'NULL'
  if (typeof v === 'object') {
    try { return JSON.stringify(v) } catch { return String(v) }
  }
  return String(v)
}
function isNull(v: unknown): boolean {
  return v === null || v === undefined
}

const note = computed(() =>
  capped.value
    ? t('agent.result.showingCapped', { shown: ROW_CAP, total: total.value })
    : t('agent.result.rowCount', { n: total.value }),
)
</script>

<template>
  <div class="result">
    <div class="scroll">
      <table class="grid mono">
        <thead>
          <tr>
            <th v-for="(c, i) in entry.columns" :key="i">{{ c }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, r) in shown" :key="r">
            <td v-for="(cell, c) in row" :key="c" :class="{ null: isNull(cell) }">{{ fmt(cell) }}</td>
          </tr>
          <tr v-if="entry.columns.length === 0 && shown.length === 0">
            <td class="empty">{{ $t('agent.result.empty') }}</td>
          </tr>
        </tbody>
      </table>
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
.scroll {
  max-height: 260px;
  overflow: auto;
}
.grid {
  border-collapse: collapse;
  width: max-content;
  min-width: 100%;
  font-size: var(--catdb-fs-mono-small);
}
.grid th, .grid td {
  border-right: 1px solid var(--catdb-separator);
  border-bottom: 1px solid var(--catdb-separator);
  padding: 2px 8px;
  height: var(--catdb-grid-row-height);
  text-align: left;
  white-space: nowrap;
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.grid th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--catdb-surface-chrome);
  color: var(--catdb-text-secondary);
  font-weight: 600;
  height: var(--catdb-grid-header-height);
}
.grid td { color: var(--catdb-text-primary); }
.grid td.null { color: var(--catdb-text-tertiary); font-style: italic; }
.grid td.empty { color: var(--catdb-text-tertiary); text-align: center; }

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
