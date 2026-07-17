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
  NSpace,
  NTag,
  useMessage,
} from 'naive-ui'
import SqlEditor from './SqlEditor.vue'
import ResultTable from './ResultTable.vue'
import AppIcon from '../shared/AppIcon.vue'
import checkIcon from '../../assets/icons/check.svg?raw'
import rotateCcwIcon from '../../assets/icons/rotate-ccw.svg?raw'
import { startExport } from '../../composables/useExport'
import { format } from 'sql-formatter'
import { useQueryStore } from '../../stores/query'
import { useMetadataStore } from '../../stores/metadata'
import type { Capabilities } from '../../api/query'
import { genericUIDialect, namespaceTermOf, uiDialectForDriver, type UIDialect } from '../../api/dialect'
import type { CompletionCatalog, SchemaTable } from '../../editor/sqlCompletion'
import { t } from '../../i18n'

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

const isMac = navigator.platform.includes('Mac')
const modifierKey = isMac ? 'Cmd' : 'Ctrl'

const tab = computed(() => store.getTab(props.tabId)!)
const caps = ref<Capabilities | null>(null)

// The driver's UI descriptor (SQL dialect, completion catalogs) — resolves
// async; the editor renders with the generic fallback until it lands.
const uiDialect = ref<UIDialect>(genericUIDialect())
watch(() => props.driver, async (d) => {
  uiDialect.value = d ? await uiDialectForDriver(d) : genericUIDialect()
}, { immediate: true })

// What the toolbar dropdown lists (UIDialect.NamespaceTerm) — picks the
// `.database`/`.schema` variant of its placeholder/empty copy.
const nsTerm = computed(() => namespaceTermOf(uiDialect.value))

const currentDb = ref<string | null>(null)
/** SchemaTable[] view of one cached snapshot, or null if not loaded yet. */
function snapshotTables(connId: string, db: string): SchemaTable[] | null {
  const snap = metaStore.snapshotFor(connId, db)
  if (!snap?.tables) return null
  return snap.tables.map((t) => ({
    name: t.name,
    kind: t.kind,
    columns: (t.columns ?? []).map((c) => ({
      name: c.name,
      type: c.type,
      pk: c.pk,
      notNull: c.notNull,
      comment: c.comment,
    })),
  }))
}

// Databases with the object tree's schema filter applied — shared by the
// toolbar dropdown and the completion engine so both match the tree.
function filteredDatabases(connId: string): string[] {
  const list = metaStore.databases[connId] ?? []
  const filter = store.schemaFilter[connId]
  return filter ? list.filter((d) => filter.includes(d)) : list
}

// Live metadata view for the editor's completion engine. Closures read the
// store at completion time; `ensureTables` lets `otherdb.` load on demand.
const catalog: CompletionCatalog = {
  databases: () => {
    const connId = tab.value?.connId
    return connId ? (metaStore.databases[connId] ?? []) : []
  },
  visibleDatabases: () => {
    const connId = tab.value?.connId
    return connId ? filteredDatabases(connId) : []
  },
  currentDb: () => currentDb.value ?? undefined,
  tablesFor: (db) => {
    const connId = tab.value?.connId
    return connId ? snapshotTables(connId, db) : null
  },
  ensureTables: async (db) => {
    const connId = tab.value?.connId
    if (!connId) return null
    try {
      await metaStore.ensureSnapshot(connId, db)
    } catch {
      return null
    }
    return snapshotTables(connId, db)
  },
}

const dbOptions = computed(() => {
  const connId = tab.value?.connId
  if (!connId) return []
  return filteredDatabases(connId).map((d) => ({ label: d, value: d }))
})

async function ensureAutocomplete() {
  const connId = tab.value?.connId
  if (!connId) return
  try {
    const dbs = await metaStore.ensureDatabases(connId)
    if (dbs.length && !currentDb.value) {
      // Prefer the tab's anchored db (saved query / 新建查询 from a db node);
      // otherwise sync with the object tree's last-selected database, or stay
      // in "no selection" state if nothing was selected.
      const anchored = tab.value?.db
      currentDb.value = (anchored && dbs.includes(anchored)) ? anchored : (store.selectedDb[connId] ?? null)
    }
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
  // Keep the tab's anchor db in sync so 保存 lands under the selected schema.
  if (tab.value?.kind === 'query') tab.value.db = db
  void metaStore.ensureSnapshot(connId, db).catch(() => { /* nice-to-have */ })
})

// Sync this tab's schema-selector when the object tree selects a different
// database. Only the active tab follows the tree selection — non-active tabs
// keep their own schema.
watch(
  () => {
    const connId = tab.value?.connId
    return connId ? store.selectedDb[connId] : undefined
  },
  (newDb) => {
    if (!newDb) return
    const connId = tab.value?.connId
    if (!connId) return
    // Only update if this tab is the active tab for its connection, and the
    // new selection actually differs from the current one.
    if (store.activeByConn[connId] !== props.tabId) return
    if (newDb !== currentDb.value) {
      currentDb.value = newDb
    }
  },
)

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
      case 'run': run(); break
      case 'run-selection': run(); break
      case 'explain': explain(); break
    }
  },
)

const editor = ref<InstanceType<typeof SqlEditor> | null>(null)

function runOpts() {
  if (!currentDb.value) return {}
  // Schema-ful databases (Postgres) are isolation boundaries: the picked
  // database routes to its own session (defaultDatabase) and unqualified
  // names resolve via the server's default search_path. On MySQL the picker
  // value IS the namespace (USE db → defaultSchema).
  return caps.value?.schemas
    ? { defaultDatabase: currentDb.value }
    : { defaultSchema: currentDb.value }
}

async function run() {
  const sel = editor.value?.selectionText() ?? ''
  const sqlToRun = sel.trim() || tab.value.sql
  if (!sqlToRun.trim()) {
    message.warning(t('queryTab.sqlEmpty'))
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
    message.warning(t('queryTab.explainUnsupported'))
    return
  }
  await store.explain(tab.value.id, runOpts())
}

function cancel() {
  void store.cancel(tab.value.id)
}

function onLoadMore() {
  void store.fetchMore(tab.value.id)
}

function onResultExport(format: string) {
  if (!tab.value.sql.trim()) {
    message.warning(t('queryTab.exportNeedsSql'))
    return
  }
  startExport({ kind: 'query', connId: tab.value.connId, sql: tab.value.sql, db: caps.value?.schemas ? (currentDb.value ?? '') : '', defaultName: 'query-' + tab.value.id }, format as any)
}

function onSqlUpdate(v: string) {
  tab.value.sql = v
}

// Map the driver's editor dialect id onto sql-formatter's language id so
// formatting follows the active connection instead of assuming MySQL.
function formatterLanguage() {
  switch (uiDialect.value.editorDialect) {
    case 'mysql': return 'mysql' as const
    case 'mariadb': return 'mariadb' as const
    case 'postgresql': return 'postgresql' as const
    case 'sqlite': return 'sqlite' as const
    case 'mssql': return 'transactsql' as const
    case 'plsql': return 'plsql' as const
    default: return 'sql' as const
  }
}

function formatSql() {
  const sql = tab.value.sql.trim()
  if (!sql) return
  try {
    const formatted = format(sql, {
      language: formatterLanguage(),
      tabWidth: 2,
      useTabs: false,
      keywordCase: 'upper',
      linesBetweenQueries: 2,
    })
    editor.value?.setDoc(formatted)
  } catch {
    message.warning(t('queryTab.formatFailed'))
  }
}

async function saveQuery() {
  if (!tab.value.sql.trim()) {
    message.warning(t('queryTab.sqlEmpty'))
    return
  }
  try {
    if (await store.saveTabQuery(tab.value.id)) message.success(t('common.saved'))
  } catch (e) {
    message.error(t('common.saveFailed', { error: String(e) }))
  }
}

// Returns an i18n key (resolved with $t in the template) + tag type. Returning
// a key rather than text keeps the i18n `t` import out of this computed, whose
// local `t` is the tab.
const statusBadge = computed(() => {
  const t = tab.value
  switch (t.status) {
    case 'running': return { key: 'queryTab.status.running', type: 'info' as const }
    case 'done':
      return { key: 'queryTab.status.done', type: 'success' as const }
    case 'error': return { key: 'queryTab.status.error', type: 'error' as const }
    case 'canceled': return { key: 'queryTab.status.canceled', type: 'warning' as const }
    default: return { key: 'queryTab.status.idle', type: 'default' as const }
  }
})

const isAutoCommit = computed(() => tab.value.autoCommit)
const hasTxn = computed(() => !!tab.value.txnId)
const supportsTxn = computed(() => caps.value?.transactions ?? false)

// Toggle between "result" and "summary" output view.
const showResultView = ref(true)

async function onToggleAutoCommit() {
  await store.toggleAutoCommit(props.tabId)
}

async function onCommit() {
  try {
    await store.commitTransaction(props.tabId)
    message.success(t('queryTab.commit'))
  } catch (e: any) {
    message.error(t('queryTab.txnError', { msg: String(e) }))
  }
}

async function onRollback() {
  try {
    await store.rollbackTransaction(props.tabId)
    message.info(t('queryTab.rollback'))
  } catch (e: any) {
    message.error(t('queryTab.txnError', { msg: String(e) }))
  }
}

// While the streaming cursor is still draining, rowsTotal is only what we've
// loaded so far. When the parallel COUNT(*) has answered, show "N / total";
// before that fall back to "N+" (DataGrip-style). Exact count once drained.
const rowsLabel = computed(() => {
  const t = tab.value
  if (!t.isResultSet || t.rowsTotal <= 0) return null
  if (t.done) return { key: 'queryTab.rowsCount', n: t.rowsTotal, total: 0 }
  if (t.exactTotal != null) {
    // The count runs on its own connection/snapshot; concurrent writes can
    // leave it below the rows already drained. Never show n > total.
    return { key: 'queryTab.rowsCountOfTotal', n: t.rowsTotal, total: Math.max(t.exactTotal, t.rowsTotal) }
  }
  return { key: 'queryTab.rowsCountPartial', n: t.rowsTotal, total: 0 }
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
        <n-button size="tiny" type="primary" :disabled="tab.status === 'running'" @click="runFull">
          {{ $t('queryTab.run') }}
        </n-button>
        <n-button size="tiny" :disabled="tab.status === 'running'" @click="run">
          {{ $t('queryTab.runSelection') }}
        </n-button>
        <n-button size="tiny" :disabled="tab.status === 'running'" @click="formatSql">
          {{ $t('queryTab.format') }}
        </n-button>
        <n-button size="tiny" :disabled="tab.status === 'running'" @click="saveQuery">
          {{ $t('common.save') }}
        </n-button>
        <n-button v-if="caps?.explainPlan" size="tiny" :disabled="tab.status === 'running'" @click="explain">
          EXPLAIN
        </n-button>
        <!-- Transaction controls -->
        <template v-if="supportsTxn">
          <span class="sep" />
          <span class="txn-group">
            <n-button
              size="tiny"
              quaternary
              :type="hasTxn ? 'warning' : 'default'"
              :disabled="tab.status === 'running'"
              @click="onToggleAutoCommit"
              class="txn-btn"
            >
              {{ $t(isAutoCommit ? 'queryTab.autoCommit' : 'queryTab.manualCommit') }}
            </n-button>
            <n-button
              v-if="!isAutoCommit"
              size="tiny"
              quaternary
              type="success"
              :disabled="!hasTxn || tab.status === 'running'"
              @click="onCommit"
              class="txn-icon-btn"
            >
              <template #icon><AppIcon :src="checkIcon" :size="13" /></template>
            </n-button>
            <n-button
              v-if="!isAutoCommit"
              size="tiny"
              quaternary
              type="error"
              :disabled="!hasTxn || tab.status === 'running'"
              @click="onRollback"
              class="txn-icon-btn"
            >
              <template #icon><AppIcon :src="rotateCcwIcon" :size="13" /></template>
            </n-button>
          </span>
        </template>
        <span class="sep" />
        <select
          v-model="currentDb"
          :disabled="tab.status === 'running' || dbOptions.length === 0"
          class="schema-select"
        >
          <option value="" disabled>{{ dbOptions.length ? $t(`queryTab.namespace.${nsTerm}`) : $t(`queryTab.noNamespaces.${nsTerm}`) }}</option>
          <option v-for="opt in dbOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>
      </n-space>
      <n-space :size="6" align="center" class="hint mono">
        <n-button v-if="tab.status === 'running'" size="tiny" type="warning" @click="cancel">
          {{ $t('common.cancel') }}
        </n-button>
        <span>{{ modifierKey }}+Enter</span>
      </n-space>
    </div>

    <!-- 始终只有一个 SqlEditor 实例，防止执行查询时 CodeMirror 被销毁重建。
         首次执行后通过 v-if 追加分割线 + 结果面板，不换实例。 -->
    <div
      ref="bodyBRef"
      class="body"
      :class="[showResultPane ? 'body-b' : 'body-a', { dragging }]"
      :style="showResultPane ? { '--editor-pct': editorPct + '%' } : {}"
    >
      <div class="editor-slot">
        <SqlEditor
          ref="editor"
          :model-value="tab.sql"
          :on-run="run"
          :on-save="saveQuery"
          :catalog="catalog"
          :dialect="uiDialect"
          @update:model-value="onSqlUpdate"
        />
      </div>

      <template v-if="showResultPane">
        <div
          class="splitter"
          :class="{ active: dragging }"
          @pointerdown="onSplitDown"
        />

        <div class="result-slot">
          <!-- Result / Summary toggle bar + execution status (always visible with the pane) -->
          <div class="result-tabs">
            <template v-if="tab.status === 'done' && (tab.isResultSet || tab.execAffected !== null)">
              <button
                class="result-tab"
                :class="{ active: showResultView }"
                @click="showResultView = true"
              >
                {{ $t('queryTab.resultTab') }}
              </button>
              <button
                class="result-tab"
                :class="{ active: !showResultView }"
                @click="showResultView = false"
              >
                {{ $t('queryTab.summaryTab') }}
              </button>
            </template>
            <span class="result-status">
              <n-tag size="small" :type="statusBadge.type">{{ $t(statusBadge.key) }}</n-tag>
              <span v-if="tab.elapsedMs > 0" class="mono mute">{{ tab.elapsedMs }} ms</span>
              <span v-if="rowsLabel" class="mono mute">{{ $t(rowsLabel.key, { n: rowsLabel.n, total: rowsLabel.total }) }}</span>
              <span v-if="!tab.isResultSet && tab.execAffected !== null" class="mono mute">
                {{ $t('queryTab.affectedCount', { n: tab.execAffected }) }}
              </span>
            </span>
          </div>

          <!-- Error alerts always visible -->
          <n-alert
            v-if="errorKind === 'canceled'"
            type="warning"
            :show-icon="false"
            class="alert"
          >
            {{ tab.errorMessage || $t('queryTab.queryCanceled') }}
          </n-alert>
          <n-alert
            v-else-if="errorKind === 'timeout'"
            type="error"
            :show-icon="false"
            class="alert"
          >
            {{ $t('queryTab.queryTimedOut') }}<br />
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

          <!-- Result view -->
          <template v-if="showResultView && !errorKind">
            <ResultTable
              v-if="tab.isResultSet"
              :columns="tab.columns"
              :rows="tab.rows"
              :done="tab.done"
              :fetching="tab.fetching"
              :rows-total="tab.rowsTotal"
              :sql="tab.lastRunSql"
              :conn-id="tab.connId"
              :edit-table="tab.editTable ?? null"
              class="result-table"
              @load-more="onLoadMore"
              @export="onResultExport"
            />
            <div v-else-if="tab.status === 'done'" class="exec-result">
              <div class="ok">{{ $t('queryTab.rowsAffected', { n: tab.execAffected }) }}</div>
              <div v-if="tab.execLastInsertId" class="mute mono">last insert id: {{ tab.execLastInsertId }}</div>
            </div>
          </template>

          <!-- Execution summary view -->
          <div v-if="!showResultView && !errorKind && tab.status === 'done'" class="summary-view">
            <div class="summary-header">{{ $t('queryTab.execSummary') }}</div>
            <div class="summary-grid">
              <div class="summary-item">
                <span class="summary-label">{{ $t('queryTab.execTime') }}</span>
                <span class="summary-value mono">{{ tab.elapsedMs }} ms</span>
              </div>
              <div v-if="tab.isResultSet" class="summary-item">
                <span class="summary-label">{{ $t('queryTab.execRows') }}</span>
                <span class="summary-value mono">{{ tab.rowsTotal }}</span>
              </div>
              <div v-if="!tab.isResultSet && tab.execAffected !== null" class="summary-item">
                <span class="summary-label">{{ $t('queryTab.execAffected') }}</span>
                <span class="summary-value mono">{{ tab.execAffected }}</span>
              </div>
              <div class="summary-item">
                <span class="summary-label">{{ $t('queryTab.execStatements') }}</span>
                <span class="summary-value mono">{{ tab.statementCount || 1 }}</span>
              </div>
            </div>
            <div class="summary-sql">
              <pre class="mono">{{ tab.lastRunSql }}</pre>
            </div>
          </div>
        </div>
      </template>
    </div>
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
  padding: 2px 10px;
  background: var(--n-color);
  min-width: 0;
  border-bottom: 1px solid var(--catdb-separator);
  height: 35px;
}
.sep { display: inline-block; width: 1px; height: 12px; background: currentColor; opacity: 0.15; }
.mute { opacity: 0.6; font-size: var(--catdb-fs-small); }
.hint { opacity: 0.4; font-size: var(--catdb-fs-mini); }
/* Schema dropdown — native select styled to match toolbar density. */
.schema-select {
  width: 160px;
  font-size: var(--catdb-fs-small);
  padding: 1px 6px;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
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
.body.dragging { user-select: none; -webkit-user-select: none; }

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

/* No padding here: ResultTable owns its own inset (grid) and keeps the footer
   edge-to-edge, matching TableBrowser. Alerts carry their own margin instead. */
.result-slot {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.alert {
  flex: 0 0 auto;
  margin: 6px 6px 0;
  /* 错误信息可选中复制（全局默认 user-select: none）。 */
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
/* basis: 0 → result table can NEVER push the result-slot taller than its
   grid track. All vertical scrolling lives inside ResultTable's .scroller. */
.result-table { flex: 1 1 0; min-width: 0; min-height: 0; }
.exec-result { padding: 12px; display: flex; flex-direction: column; gap: 4px; }
.ok { font-size: var(--catdb-fs-body); }

/* ---- Splitter ---- */

/* 视觉与 shared/ResizeHandle 一致:半透明 accent-soft 垫底 + 中央 accent 握把 */
.splitter {
  cursor: row-resize;
  transition: background-color 0.2s ease;
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
.splitter::before {
  content: '';
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  width: 32px;
  height: 2px;
  border-radius: 1px;
  background: transparent;
  transition: background-color 0.2s ease;
}
.splitter:hover,
.splitter.active {
  background: var(--catdb-accent-soft);
}
.splitter:hover::before,
.splitter.active::before {
  background: var(--catdb-accent);
}

/* ---- Transaction controls ---- */
.txn-group { display: inline-flex; align-items: center; gap: 1px; }
.txn-btn { font-size: var(--catdb-fs-mini) !important; }
.txn-icon-btn { padding: 0 2px !important; }
.txn-icon-btn .app-icon { display: flex; }

/* ---- Result / Summary tab bar ---- */
.result-tabs {
  display: flex;
  padding: 0px 6px;
  gap: 0;
  flex: 0 0 auto;
  border-bottom: 1px solid var(--catdb-separator);
  background: var(--n-color);
}
.result-tab {
  font-size: var(--catdb-fs-small);
  padding: 4px 14px;
  border: none;
  background: transparent;
  color: var(--n-text-color);
  opacity: 0.5;
  cursor: pointer;
  border-bottom: 2px solid transparent;
  transition: opacity 120ms, border-color 120ms;
  font-family: inherit;
}
.result-tab:hover { opacity: 0.8; }
.result-tab.active {
  opacity: 1;
  border-bottom-color: var(--catdb-accent);
}
/* 执行状态（原 toolbar）— 靠右排布，无 tab 按钮时撑起栏高。 */
.result-tabs { min-height: 27px; }
.result-status {
  margin-left: auto;
  align-self: center;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 2px 4px;
}

/* ---- Execution summary ---- */
.summary-view {
  padding: 16px 20px;
  flex: 1 1 0;
  overflow-y: auto;
}
.summary-header {
  font-size: var(--catdb-fs-body);
  font-weight: 600;
  margin-bottom: 12px;
}
.summary-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}
.summary-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  background: var(--n-color);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-md);
}
.summary-label {
  font-size: var(--catdb-fs-mini);
  opacity: 0.6;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.summary-value {
  font-size: 18px;
  font-weight: 600;
  color: var(--n-text-color);
}
.summary-sql {
  margin-top: 4px;
}
.summary-sql pre {
  /* SQL 可选中复制（全局默认 user-select: none）。 */
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
  font-size: var(--catdb-fs-mono);
  background: var(--n-color);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-xs);
  padding: 10px 12px;
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
  color: var(--n-text-color);
}
</style>
