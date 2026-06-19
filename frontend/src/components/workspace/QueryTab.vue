<script setup lang="ts">
// QueryTab — one editor tab: SQL editor + toolbar + result panel.
//
// Layout philosophy (FORCED-TRACK GRID):
//   .qt is a CSS Grid with rows = `auto 1fr` — the toolbar gets its content
//   height, the body gets ALL remaining space, no more, no less. The body
//   is then ALSO a grid: in state A it has one track (editor), in state B
//   it has three tracks (editor% / 3px splitter / 1fr result). Track sizes
//   are definite percentages/pixels, NOT flex-basis-auto, so the editor or
//   result content can never push the body taller than the available space.
//
// Why not NSplit anymore: NSplit applies `height: 100%` to itself while
// being a `flex: 1 1 auto` child. With basis=auto, a tall inner table can
// turn the basis into a content-height, breaking the cascade and pushing
// the whole tab out of the viewport. CSS grid with explicit tracks
// sidesteps the entire flex circularity problem.
import { computed, onMounted, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCheckbox,
  NSpace,
  NTag,
  useMessage,
} from 'naive-ui'
import SqlEditor from './SqlEditor.vue'
import ResultTable from './ResultTable.vue'
import ExportDialog from './ExportDialog.vue'
import { format } from 'sql-formatter'
import { useQueryStore } from '../../stores/query'
import { useMetadataStore } from '../../stores/metadata'
import type { Capabilities } from '../../api/query'
import type { SQLNamespace } from '@codemirror/lang-sql'

const props = defineProps<{
  tabId: string
  /** Driver name from the connection, for capability lookup. */
  driver: string
  /** Command bus from AppShell: { tabId, cmd, nonce } when the user invokes
   *  Run/RunSelection/Explain from the native menu. */
  command?: { tabId: string; cmd: string; nonce: number } | null
}>()

const store = useQueryStore()
const metaStore = useMetadataStore()
const message = useMessage()

const tab = computed(() => store.getTab(props.tabId)!)
const caps = ref<Capabilities | null>(null)

const currentDb = ref<string | null>(null)
/**
 * Nested SQLNamespace: the current DB gets a full {table: [cols]} body so
 * SELECT-from / qualified `table.col` lookups can complete. Other DBs are
 * listed as empty namespaces so `dbname.` triggers a database hint even if
 * we haven't fetched its tables yet. Falling back to a flat shape is fine —
 * lang-sql accepts either form.
 */
const schemaMap = computed<SQLNamespace>(() => {
  const connId = tab.value?.connId
  if (!connId) return {} as SQLNamespace
  const dbs = metaStore.databases[connId] ?? []
  // Build as a loose record then cast — SQLNamespace's recursive shape
  // doesn't structurally infer from a plain object literal, but the
  // runtime shape we produce matches it exactly.
  const out: Record<string, unknown> = {}
  for (const db of dbs) {
    const snap = metaStore.snapshotFor(connId, db)
    if (snap && snap.tables) {
      const inner: Record<string, string[]> = {}
      for (const t of snap.tables) {
        inner[t.name] = t.columns ?? []
      }
      out[db] = inner
    } else {
      // No snapshot yet — surface the DB name so the user still gets it as
      // a completion candidate; tables/columns will fill in once the
      // snapshot loads (the watch on currentDb prefetches the active one).
      out[db] = {}
    }
  }
  return out as SQLNamespace
})

const dbOptions = computed(() => {
  const connId = tab.value?.connId
  if (!connId) return []
  const list = metaStore.databases[connId] ?? []
  return list.map((d) => ({ label: d, value: d }))
})

async function ensureAutocomplete() {
  const connId = tab.value?.connId
  if (!connId) return
  try {
    const dbs = await metaStore.ensureDatabases(connId)
    if (dbs.length && !currentDb.value) currentDb.value = dbs[0]
    if (currentDb.value) {
      await metaStore.ensureSnapshot(connId, currentDb.value)
    }
  } catch {
    // No-op: autocomplete is a nice-to-have; query editor still works.
  }
}

// When the user picks a different schema from the toolbar dropdown, prefetch
// its autocomplete snapshot so the editor's tab-completion catches up.
watch(currentDb, (db) => {
  const connId = tab.value?.connId
  if (!connId || !db) return
  void metaStore.ensureSnapshot(connId, db).catch(() => { /* nice-to-have */ })
})

onMounted(async () => {
  if (props.driver) {
    try { caps.value = await store.loadCapabilities(props.driver) } catch { /* ignore */ }
  }
  await ensureAutocomplete()
})

// Bridge for native-menu commands targeting this tab.
watch(
  () => props.command?.nonce,
  () => {
    if (!props.command) return
    switch (props.command.cmd) {
      case 'run': runFull(); break
      case 'run-selection': run(); break
      case 'explain': explain(); break
    }
  },
)

const editor = ref<InstanceType<typeof SqlEditor> | null>(null)

function runOpts() {
  return currentDb.value ? { defaultSchema: currentDb.value } : {}
}

async function run() {
  const sel = editor.value?.selectionText() ?? ''
  const sqlToRun = sel.trim() || tab.value.sql
  if (!sqlToRun.trim()) {
    message.warning('SQL is empty')
    return
  }
  // Temporarily swap sql so the run path picks up the selection.
  const orig = tab.value.sql
  tab.value.sql = sqlToRun
  try {
    await store.runActive(tab.value.id, runOpts())
  } finally {
    tab.value.sql = orig
  }
}

async function runFull() {
  await store.runActive(tab.value.id, runOpts())
}

async function explain() {
  if (!caps.value?.explainPlan) {
    message.warning('Driver does not support EXPLAIN')
    return
  }
  await store.explain(tab.value.id, runOpts())
}

function cancel() {
  void store.cancel(tab.value.id)
}

const exportOpen = ref(false)
function openExport() {
  if (!tab.value.sql.trim()) {
    message.warning('Run something first or type SQL to export')
    return
  }
  exportOpen.value = true
}

function onLoadMore() {
  void store.fetchMore(tab.value.id)
}

function onSqlUpdate(v: string) {
  tab.value.sql = v
}

function formatSql() {
  const sql = tab.value.sql.trim()
  if (!sql) return
  try {
    const formatted = format(sql, {
      language: 'mysql',
      tabWidth: 2,
      useTabs: false,
      keywordCase: 'upper',
      linesBetweenQueries: 2,
    })
    editor.value?.setDoc(formatted)
  } catch {
    message.warning('Could not format SQL')
  }
}

const statusBadge = computed(() => {
  const t = tab.value
  switch (t.status) {
    case 'running': return { label: 'Running', type: 'info' as const }
    case 'done':
      if (t.truncated) return { label: 'Done (truncated)', type: 'warning' as const }
      return { label: 'Done', type: 'success' as const }
    case 'error': return { label: 'Error', type: 'error' as const }
    case 'canceled': return { label: 'Canceled', type: 'warning' as const }
    default: return { label: 'Idle', type: 'default' as const }
  }
})

const errorKind = computed<'canceled' | 'timeout' | 'sql' | null>(() => {
  const t = tab.value
  if (t.status !== 'error' && t.status !== 'canceled') return null
  const m = t.errorMessage.toLowerCase()
  if (t.status === 'canceled' || m.startsWith('canceled')) return 'canceled'
  if (m.startsWith('timeout')) return 'timeout'
  return 'sql'
})

/** Two-state layout:
 *  State A (showResultPane = false): tab is still idle and nothing has ever
 *  been run on it → editor fills 100% of the body.
 *  State B (showResultPane = true): a query has run (running/done/error/
 *  canceled, or there's an in-flight result set / affected-rows / error) →
 *  body splits into editor (top, %) / splitter (3px) / result (1fr).
 *  Once we enter State B we stay there for the tab's lifetime — the result
 *  pane is the source of truth for "what came back". */
const showResultPane = computed(() => {
  const t = tab.value
  return t.status !== 'idle'
    || t.isResultSet
    || t.execAffected !== null
    || !!t.errorMessage
})

// --- Custom vertical splitter (replaces NSplit) ---------------------------
// editorPct: percentage of .body-b allocated to the editor track. The
// result track is the remaining `1fr`. Default 60%/40%.
const editorPct = ref(60)
const bodyBRef = ref<HTMLDivElement | null>(null)
const dragging = ref(false)

const MIN_PANE_PX = 100   // minimum height for either pane while dragging

function onSplitDown(e: PointerEvent) {
  const el = bodyBRef.value
  if (!el) return
  e.preventDefault()
  const rect = el.getBoundingClientRect()
  const bodyH = rect.height
  if (bodyH <= 0) return
  const startY = e.clientY
  const startPct = editorPct.value
  dragging.value = true
  document.body.style.cursor = 'row-resize'

  // Convert MIN_PANE_PX to percent dynamically.
  const minPct = Math.min(45, (MIN_PANE_PX / bodyH) * 100)
  const maxPct = 100 - minPct

  function onMove(ev: PointerEvent) {
    const dy = ev.clientY - startY
    const dPct = (dy / bodyH) * 100
    let next = startPct + dPct
    if (next < minPct) next = minPct
    if (next > maxPct) next = maxPct
    editorPct.value = next
  }
  function onUp() {
    dragging.value = false
    document.body.style.cursor = ''
    document.removeEventListener('pointermove', onMove)
    document.removeEventListener('pointerup', onUp)
  }
  document.addEventListener('pointermove', onMove)
  document.addEventListener('pointerup', onUp)
}
</script>

<template>
  <div class="qt">
    <div class="toolbar">
      <n-space :size="8" align="center">
        <n-button size="small" type="primary" :disabled="tab.status === 'running'" @click="runFull">
          Run
        </n-button>
        <n-button size="small" :disabled="tab.status === 'running'" @click="run">
          Run Selection
        </n-button>
        <n-button size="small" :disabled="tab.status === 'running'" @click="formatSql">
          Format
        </n-button>
        <n-button v-if="caps?.explainPlan" size="small" :disabled="tab.status === 'running'" @click="explain">
          EXPLAIN
        </n-button>
        <n-button size="small" :disabled="tab.status === 'running' || !tab.isResultSet" @click="openExport">
          Export…
        </n-button>
        <n-button v-if="tab.status === 'running'" size="small" type="warning" @click="cancel">
          Cancel
        </n-button>
        <span class="sep" />
        <select
          v-model="currentDb"
          :disabled="tab.status === 'running' || dbOptions.length === 0"
          class="schema-select"
        >
          <option value="" disabled>{{ dbOptions.length ? 'Schema' : 'No schemas' }}</option>
          <option v-for="opt in dbOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>
        <span class="sep" />
        <n-tag size="small" :type="statusBadge.type">{{ statusBadge.label }}</n-tag>
        <span v-if="tab.elapsedMs > 0" class="mono mute">{{ tab.elapsedMs }} ms</span>
        <span v-if="tab.isResultSet && tab.rowsTotal > 0" class="mono mute">{{ tab.rowsTotal }} rows</span>
        <span v-if="!tab.isResultSet && tab.execAffected !== null" class="mono mute">
          {{ tab.execAffected }} affected
        </span>
      </n-space>
      <n-space :size="6" align="center" class="hint mono">
        <span>Cmd/Ctrl+Enter</span>
      </n-space>
    </div>

    <!-- State A: editor fills the body. Body is a single-track grid. -->
    <div v-if="!showResultPane" class="body body-a">
      <div class="editor-slot">
        <SqlEditor
          ref="editor"
          :model-value="tab.sql"
          :on-run="runFull"
          :schema="schemaMap"
          :default-schema="currentDb ?? undefined"
          @update:model-value="onSqlUpdate"
        />
      </div>
    </div>

    <!-- State B: 3-track grid = editor% / 3px splitter / result(1fr).
         Tracks are DEFINITE sizes — neither editor nor result can push the
         body beyond its allotted 1fr in the .qt grid. -->
    <div
      v-else
      ref="bodyBRef"
      class="body body-b"
      :class="{ dragging }"
      :style="{ '--editor-pct': editorPct + '%' }"
    >
      <div class="editor-slot">
        <SqlEditor
          ref="editor"
          :model-value="tab.sql"
          :on-run="runFull"
          :schema="schemaMap"
          :default-schema="currentDb ?? undefined"
          @update:model-value="onSqlUpdate"
        />
      </div>

      <div
        class="splitter"
        :class="{ active: dragging }"
        @pointerdown="onSplitDown"
      />

      <div class="result-slot">
        <n-alert
          v-if="errorKind === 'canceled'"
          type="warning"
          :show-icon="false"
          class="alert"
        >
          {{ tab.errorMessage || 'Query canceled.' }}
        </n-alert>
        <n-alert
          v-else-if="errorKind === 'timeout'"
          type="error"
          :show-icon="false"
          class="alert"
        >
          Query timed out. Increase the timeout or narrow the query.<br />
          <span class="mono">{{ tab.errorMessage }}</span>
        </n-alert>
        <n-alert
          v-else-if="errorKind === 'sql'"
          type="error"
          :show-icon="false"
          class="alert"
        >
          <span class="mono">{{ tab.errorMessage }}</span>
        </n-alert>

        <ResultTable
          v-if="tab.isResultSet"
          :columns="tab.columns"
          :rows="tab.rows"
          :done="tab.done"
          :fetching="tab.fetching"
          :truncated="tab.truncated"
          :rows-total="tab.rowsTotal"
          class="result-table"
          @load-more="onLoadMore"
        />
        <div v-else-if="!errorKind && tab.status === 'done'" class="exec-result">
          <div class="ok">{{ tab.execAffected }} row(s) affected</div>
          <div v-if="tab.execLastInsertId" class="mute mono">last insert id: {{ tab.execLastInsertId }}</div>
        </div>
      </div>
    </div>

    <ExportDialog
      v-model:show="exportOpen"
      :source="{ kind: 'query', connId: tab.connId, sql: tab.sql, defaultName: 'query-' + tab.id }"
    />
  </div>
</template>

<style scoped>
/* Outer container — definite 2-row grid: toolbar (auto), body (1fr).
   `overflow: hidden` is the hard ceiling: nothing inside this element can
   make the tab taller than its allotted slot in the workspace. */
.qt {
  display: grid;
  grid-template-rows: auto 1fr;
  height: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  border-bottom: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  background: var(--n-color);
  min-width: 0;
}
.sep { display: inline-block; width: 1px; height: 12px; background: currentColor; opacity: 0.15; }
.mute { opacity: 0.6; font-size: 12px; }
.hint { opacity: 0.4; font-size: 11px; }
/* Schema dropdown — native select styled to match toolbar density. */
.schema-select {
  width: 160px;
  font-size: 12px;
  padding: 1px 6px;
  border: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  border-radius: 3px;
  background: var(--n-color);
  color: var(--n-text-color);
  height: 26px;
  outline: none;
  cursor: pointer;
  font-family: inherit;
}
.schema-select:focus {
  border-color: var(--n-primary-color);
}
.schema-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ---- Body: 1fr of .qt's grid (i.e. all remaining vertical space) ---- */

.body {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: grid;
}
/* State A: single row → editor fills everything. */
.body-a { grid-template-rows: 1fr; }
/* State B: editor% / 3px splitter / 1fr result. Tracks are explicit so
   neither slot can grow past its track height — content scrolls inside. */
.body-b {
  grid-template-rows: var(--editor-pct, 60%) 3px 1fr;
}
.body-b.dragging { user-select: none; -webkit-user-select: none; }

/* ---- Slots ---- */

.editor-slot {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  padding: 6px;
  display: flex;
}
/* basis: 0 → editor's CodeMirror can never push the slot taller than its grid track */
.editor-slot > * { flex: 1 1 0; min-width: 0; min-height: 0; }

.result-slot {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.alert { flex: 0 0 auto; }
/* basis: 0 → result table can NEVER push the result-slot taller than its
   grid track. All vertical scrolling lives inside ResultTable's .scroller. */
.result-table { flex: 1 1 0; min-width: 0; min-height: 0; }
.exec-result { padding: 12px; display: flex; flex-direction: column; gap: 4px; }
.ok { font-size: 13px; }

/* ---- Splitter ---- */

.splitter {
  background: var(--n-divider-color);
  cursor: row-resize;
  transition: background-color 120ms ease-out;
  position: relative;
}
.splitter::after {
  /* Wider invisible hit area, easier to grab without thickening visual line. */
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: -3px;
  bottom: -3px;
}
.splitter:hover,
.splitter.active {
  background: var(--n-primary-color, #18a058);
}
</style>
