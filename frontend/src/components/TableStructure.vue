<script setup lang="ts">
// TableStructure — editable Columns / Indexes / Foreign Keys / Options
// panels driven by MetadataService.GetTableSummary + GetCreateTable.
//
// Editing happens against a local StructureDraft (see lib/alterPlan.ts) that
// snapshots the original column/index/FK lists when the table is loaded. As
// the user edits, buildAlterPlan() diffs original-vs-draft and emits MySQL
// ALTER statements; those statements land in the AlterSqlPanel under each
// tab. Apply executes them sequentially via QueryService and reloads.
//
// We chose front-end-side ALTER generation deliberately: instant preview, no
// IPC roundtrip on each keystroke, and the diff is a pure-TS module that can
// be unit-tested. When a second driver lands (e.g. PostgreSQL), this should
// move to a Dialect.BuildAlterTable on the Go side. For MVP it lives here.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NSpin, NTabPane, NTabs, useMessage } from 'naive-ui'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { sql, MySQL } from '@codemirror/lang-sql'
import { syntaxHighlighting, defaultHighlightStyle } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { metadata as metaApi, query as queryApi } from '../api'
import type { TableSummary } from '../api/metadata'
import { useThemeStore } from '../stores/theme'
import {
  buildAlterPlan,
  parseTableCommentFromDDL,
  summaryToDraft,
  type StructureDraft,
} from '../lib/alterPlan'
import AlterSqlPanel from './structure/AlterSqlPanel.vue'
import ColumnsTab from './structure/ColumnsTab.vue'
import IndexesTab from './structure/IndexesTab.vue'
import ForeignKeysTab from './structure/ForeignKeysTab.vue'
import OptionsTab from './structure/OptionsTab.vue'

const props = defineProps<{
  connId: string
  db: string
  table: string
}>()

const message = useMessage()
const summary = ref<TableSummary | null>(null)
const origComment = ref<string>('')
const ddl = ref<string>('')
const loading = ref(false)
const busy = ref(false)
const activeTab = ref('cols')

// Draft state — the user-edited mirror of summary + origComment.
// Re-built whenever load() runs (initial mount, table switch, after Apply).
const draft = ref<StructureDraft>({
  columns: [],
  indexes: [],
  foreignKeys: [],
  options: { comment: '' },
})

async function load() {
  loading.value = true
  try {
    const [s, d] = await Promise.all([
      metaApi.getTableSummary(props.connId, props.db, props.table),
      metaApi.getCreateTable(props.connId, props.db, props.table),
    ])
    summary.value = s
    ddl.value = d
    origComment.value = parseTableCommentFromDDL(d)
    resetDraft()
  } catch (e) {
    message.error(`load structure failed: ${String(e)}`)
  } finally {
    loading.value = false
  }
}

function resetDraft() {
  if (!summary.value) return
  draft.value = summaryToDraft(summary.value, origComment.value)
}

onMounted(load)
watch(() => [props.connId, props.db, props.table], load)

// ---- alter plan (live diff) -----------------------------------------------

const plan = computed(() => {
  if (!summary.value) {
    return { columns: [], indexes: [], foreignKeys: [], options: [], all: [] }
  }
  return buildAlterPlan({
    db: props.db,
    table: props.table,
    origSummary: summary.value,
    origComment: origComment.value,
    draft: draft.value,
  })
})

// ---- apply ----------------------------------------------------------------

async function applyStatements(stmts: string[]) {
  if (stmts.length === 0 || busy.value) return
  busy.value = true
  let executed = 0
  try {
    for (const raw of stmts) {
      const trimmed = raw.trim().replace(/;$/, '')
      if (!trimmed) continue
      await queryApi.runQuery(props.connId, trimmed)
      executed++
    }
    message.success(`已应用 ${executed} 条语句`)
    await load()
  } catch (e) {
    message.error(
      `应用失败（已执行 ${executed}/${stmts.length} 条）：${String(e)}`,
    )
    // Reload anyway so the UI reflects whatever did land.
    await load()
  } finally {
    busy.value = false
  }
}

// ---- DDL read-only CodeMirror (kept as-is) --------------------------------

const themeStore = useThemeStore()
const ddlHost = ref<HTMLDivElement | null>(null)
const ddlView = ref<EditorView | null>(null)
const ddlThemeComp = new Compartment()

function initDdlEditor() {
  if (!ddlHost.value) return
  ddlView.value = new EditorView({
    state: EditorState.create({
      doc: ddl.value,
      extensions: [
        sql({ dialect: MySQL }),
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        EditorView.editable.of(false),
        EditorView.theme({
          '&': { height: '100%', fontSize: '12px' },
          '.cm-scroller': {
            fontFamily:
              'ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace',
            overflow: 'auto',
          },
        }),
        ddlThemeComp.of(themeStore.mode === 'dark' ? oneDark : []),
      ],
    }),
    parent: ddlHost.value,
  })
}

watch(ddlHost, (el) => {
  if (el && !ddlView.value) initDdlEditor()
})

watch(ddl, (val) => {
  if (!ddlView.value) return
  const cur = ddlView.value.state.doc.toString()
  if (val !== cur) {
    ddlView.value.dispatch({
      changes: { from: 0, to: cur.length, insert: val ?? '' },
    })
  }
})

watch(() => themeStore.mode, (mode) => {
  if (!ddlView.value) return
  ddlView.value.dispatch({
    effects: ddlThemeComp.reconfigure(mode === 'dark' ? oneDark : []),
  })
})

onBeforeUnmount(() => {
  ddlView.value?.destroy()
})
</script>

<template>
  <n-spin :show="loading" class="ts">
    <n-tabs
      v-model:value="activeTab"
      type="segment"
      size="small"
      class="group-tabs"
    >
      <!-- Columns -->
      <n-tab-pane name="cols" tab="Columns" display-directive="show:lazy">
        <div class="tab-body">
          <ColumnsTab v-model="draft.columns" :busy="busy" />
          <AlterSqlPanel
            :statements="plan.columns"
            :busy="busy"
            apply-confirm-title="应用字段变更"
            @apply="applyStatements(plan.columns)"
            @reset="resetDraft"
          />
        </div>
      </n-tab-pane>

      <!-- Indexes -->
      <n-tab-pane name="ix" tab="Indexes" display-directive="show:lazy">
        <div class="tab-body">
          <IndexesTab
            v-model="draft.indexes"
            :columns-draft="draft.columns"
            :busy="busy"
          />
          <AlterSqlPanel
            :statements="plan.indexes"
            :busy="busy"
            apply-confirm-title="应用索引变更"
            @apply="applyStatements(plan.indexes)"
            @reset="resetDraft"
          />
        </div>
      </n-tab-pane>

      <!-- Foreign Keys -->
      <n-tab-pane name="fk" tab="Foreign Keys" display-directive="show:lazy">
        <div class="tab-body">
          <ForeignKeysTab
            v-model="draft.foreignKeys"
            :columns-draft="draft.columns"
            :current-db="db"
            :busy="busy"
          />
          <AlterSqlPanel
            :statements="plan.foreignKeys"
            :busy="busy"
            apply-confirm-title="应用外键变更"
            @apply="applyStatements(plan.foreignKeys)"
            @reset="resetDraft"
          />
        </div>
      </n-tab-pane>

      <!-- Options (table-level: comment etc.) -->
      <n-tab-pane name="opts" tab="Options" display-directive="show:lazy">
        <div class="tab-body">
          <OptionsTab v-model="draft.options" :busy="busy" />
          <AlterSqlPanel
            :statements="plan.options"
            :busy="busy"
            apply-confirm-title="应用表选项"
            @apply="applyStatements(plan.options)"
            @reset="resetDraft"
          />
        </div>
      </n-tab-pane>

      <!-- DDL (read-only) -->
      <n-tab-pane name="ddl" tab="DDL" display-directive="show:lazy">
        <div ref="ddlHost" class="ddl-cm" />
      </n-tab-pane>
    </n-tabs>
  </n-spin>
</template>

<style scoped>
/* ---- root flex container ---- */
.ts { height: 100%; display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden; }
.ts :deep(.n-spin-container),
.ts :deep(.n-spin-content) { height: 100%; min-width: 0; min-height: 0; display: flex; flex-direction: column; }

/* ---- segmented tabs (matching ConnectionForm) ---- */
.group-tabs {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.group-tabs :deep(.n-tabs-nav) {
  display: flex;
  justify-content: center;
  flex: 0 0 auto;
  padding: 6px 8px 2px;
  border-bottom: 1px solid var(--n-border-color);
}
.group-tabs :deep(.n-tabs-rail) {
  min-width: 0;
  margin: 0 auto;
}
.group-tabs :deep(.n-tabs-tab) {
  padding: 2px 14px;
  font-size: 12px;
}
.group-tabs :deep(.n-tabs-pane-wrapper) {
  flex: 1;
  overflow: hidden;
  min-height: 0;
}
.group-tabs :deep(.n-tab-pane) {
  height: 100%;
  overflow: hidden;
  padding: 0;
}

/* tab-body: editor on top, AlterSqlPanel pinned at the bottom */
.tab-body {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

/* ---- DDL read-only editor ---- */
.ddl-cm {
  height: 100%;
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  overflow: hidden;
  background: var(--n-card-color);
  user-select: text;
  -webkit-user-select: text;
  margin: 8px;
}
</style>
