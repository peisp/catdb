<script setup lang="ts">
// TableStructure — Columns | Indexes | Foreign Keys | DDL panels driven by
// MetadataService.GetTableSummary + GetCreateTable. Read-only — actual
// schema changes (ALTER TABLE) are M5+ territory.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NEmpty, NSpin, NTabPane, NTabs, useMessage } from 'naive-ui'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { sql, MySQL } from '@codemirror/lang-sql'
import { syntaxHighlighting, defaultHighlightStyle } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { metadata as metaApi } from '../api'
import { LogicalType } from '../../bindings/catdb/internal/dbdriver/models'
import type { ColumnMeta, TableSummary } from '../api/metadata'
import { useThemeStore } from '../stores/theme'
import DataGrid from './data-grid/DataGrid.vue'

const props = defineProps<{
  connId: string
  db: string
  table: string
}>()

const message = useMessage()
const summary = ref<TableSummary | null>(null)
const ddl = ref<string>('')
const loading = ref(false)
const activeTab = ref('cols')

async function load() {
  loading.value = true
  try {
    const [s, d] = await Promise.all([
      metaApi.getTableSummary(props.connId, props.db, props.table),
      metaApi.getCreateTable(props.connId, props.db, props.table),
    ])
    summary.value = s
    ddl.value = d
  } catch (e) {
    message.error(`load structure failed: ${String(e)}`)
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(() => [props.connId, props.db, props.table], load)

function formatColName(c: ColumnMeta): string {
  const tags: string[] = []
  if (c.isPrimaryKey) tags.push('PK')
  if (c.isAutoIncrement) tags.push('AI')
  return tags.length ? `${c.name}  [${tags.join(', ')}]` : c.name
}

// ---- Columns DataGrid ----
const colHeaders: ColumnMeta[] = [
  { name: 'Name', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Type', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Null', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Default', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: true },
  { name: 'Extra', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: true },
  { name: 'Comment', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: true },
]

const colRows = computed<any[][]>(() =>
  summary.value?.columns.map(c => [
    formatColName(c),
    c.nativeType,
    c.nullable ? 'YES' : 'NO',
    c.default ?? '',
    c.isAutoIncrement ? 'auto_increment' : '',
    c.comment ?? '',
  ]) ?? []
)

// ---- Indexes DataGrid ----
const ixHeaders: ColumnMeta[] = [
  { name: 'Name', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Columns', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Unique', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Primary', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Type', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: true },
]

const ixRows = computed<any[][]>(() =>
  summary.value?.indexes.map(ix => [
    ix.name,
    (ix.columns ?? []).join(', '),
    ix.unique ? 'YES' : 'NO',
    ix.primary ? 'YES' : 'NO',
    ix.type ?? '',
  ]) ?? []
)

// ---- Foreign Keys DataGrid ----
const fkHeaders: ColumnMeta[] = [
  { name: 'Name', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'Columns', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'References', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: false },
  { name: 'On Update', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: true },
  { name: 'On Delete', nativeType: 'VARCHAR', logicalType: LogicalType.TypeString, nullable: true },
]

const fkRows = computed<any[][]>(() =>
  summary.value?.foreignKeys.map(fk => [
    fk.name,
    (fk.columns ?? []).join(', '),
    (fk.referencedSchema ? fk.referencedSchema + '.' : '') + fk.referencedTable + '(' + (fk.referencedColumns ?? []).join(', ') + ')',
    fk.onUpdate ?? '',
    fk.onDelete ?? '',
  ]) ?? []
)

// ---- DDL read-only CodeMirror ----------------------------------------------
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

// Init the editor when DDL host element first appears (lazy pane mount).
watch(ddlHost, (el) => {
  if (el && !ddlView.value) initDdlEditor()
})

// Update content when DDL changes (e.g. table switch).
watch(ddl, (val) => {
  if (!ddlView.value) return
  const cur = ddlView.value.state.doc.toString()
  if (val !== cur) {
    ddlView.value.dispatch({
      changes: { from: 0, to: cur.length, insert: val ?? '' },
    })
  }
})

// Follow theme changes.
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
      <n-tab-pane name="cols" tab="Columns" display-directive="show:lazy">
        <DataGrid v-if="colRows.length" :columns="colHeaders" :rows="colRows" />
        <div v-else class="empty"><n-empty size="small" /></div>
      </n-tab-pane>
      <n-tab-pane name="ix" tab="Indexes" display-directive="show:lazy">
        <DataGrid v-if="ixRows.length" :columns="ixHeaders" :rows="ixRows" />
        <div v-else class="empty"><n-empty size="small" /></div>
      </n-tab-pane>
      <n-tab-pane name="fk" tab="Foreign Keys" display-directive="show:lazy">
        <DataGrid v-if="fkRows.length" :columns="fkHeaders" :rows="fkRows" />
        <div v-else class="empty"><n-empty size="small" /></div>
      </n-tab-pane>
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
  padding: 8px;
}

/* ---- shared ---- */
.empty { padding: 16px; display: flex; justify-content: center; }
/* ---- DDL read-only editor ---- */
.ddl-cm {
  height: 100%;
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  overflow: hidden;
  background: var(--n-card-color);
  user-select: text;
  -webkit-user-select: text;
}
</style>
