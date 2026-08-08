<script setup lang="ts">
// ObjectTree — per-connection database/table/column tree. Lazy loads each
// level via MetadataService. Single-click a database node → open the
// tables overview tab. Double-click any non-leaf node → expand/collapse
// it. Right-click → Wails native context menu (registered in
// wailsbridge/contextmenu.go as `catdb-tree-*`, dispatched via
// api/{table,tree}ContextMenu.ts).
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CCheckbox, CInput, CPopover, CSpin, CTree, useMessage } from '../ui'
import type { CTreeNode } from '../ui'
import type { ConnectionProfile } from '../../api/connections'
import { metadata as metaApi } from '../../api'
import { savedQuery as savedQueryApi } from '../../api'
import { on as onEvent } from '../../api/events'
import { useMetadataStore } from '../../stores/metadata'
import { useConnectionsStore } from '../../stores/connections'
import { useQueryStore } from '../../stores/query'
import { setActiveTableContext } from '../../api/tableContextMenu'
import { setActiveTreeContext } from '../../api/treeContextMenu'
import { system as systemApi } from '../../api'
import { t } from '../../i18n'
import { namespaceTermOf } from '../../api/dialect'
import AppIcon from '../shared/AppIcon.vue'
import databaseIcon from '../../assets/icons/database.svg?raw'
import table2Icon from '../../assets/icons/table-2.svg?raw'
import scanEyeIcon from '../../assets/icons/scan-eye.svg?raw'
import fileCodeCornerFromIcon from '../../assets/icons/file-code-corner.svg?raw'
import tableOfContentsIcon from '../../assets/icons/table-of-contents.svg?raw'
import schemaIcon from '../../assets/icons/schema.svg?raw'

// Per-kind node icon (lucide). Group nodes share their category's icon.
const KIND_ICONS: Partial<Record<TreeMeta['kind'], string>> = {
  database: databaseIcon,
  schema: schemaIcon,
  tableGroup: table2Icon,
  viewGroup: scanEyeIcon,
  queryGroup: fileCodeCornerFromIcon,
  table: table2Icon,
  view: scanEyeIcon,
  query: fileCodeCornerFromIcon,
}

const metaOf = (node: CTreeNode) => node.extra as TreeMeta

// 列：主键用 🔑 标记（移到字段名前），普通列用 table-of-contents。
function isPkColumn(node: CTreeNode): boolean {
  const meta = metaOf(node)
  return meta.kind === 'column' && !!meta.pk
}

// 顶层节点按驱动的 NamespaceTerm 取 icon：DM 等驱动的顶层实为 schema。
// 返回 '' 表示无前缀图标。
function prefixIconFor(node: CTreeNode): string {
  const meta = metaOf(node)
  if (meta.kind === 'column') return meta.pk ? '' : tableOfContentsIcon
  const src = meta.kind === 'database' && nsTerm.value === 'schema'
    ? KIND_ICONS.schema
    : KIND_ICONS[meta.kind]
  return src ?? ''
}

const props = defineProps<{ connection: ConnectionProfile }>()
const emit = defineEmits<{
  (e: 'open-data', payload: { db: string; schema?: string; table: string }): void
  (e: 'open-structure', payload: { db: string; schema?: string; table: string }): void
  (e: 'open-tables-overview', payload: { db: string; schema?: string }): void
}>()

const store = useMetadataStore()
const connStore = useConnectionsStore()
const queryStore = useQueryStore()
const message = useMessage()

// Whether this connection's database has a schema level between database and
// table (Capabilities.schemas — Postgres yes, MySQL no). Declared by the
// driver; the tree inserts schema nodes when true.
const hasSchemas = computed(
  () => !!connStore.driverByName.get(props.connection.driver)?.capabilities?.schemas,
)

// What the tree's top level lists (UIDialect.NamespaceTerm) — picks the node
// icon and the `.database`/`.schema` variant of the filter panel's copy.
const nsTerm = computed(() =>
  namespaceTermOf(connStore.driverByName.get(props.connection.driver)?.ui),
)

// Whether the driver implements the DatabaseEditor extension (CREATE/ALTER
// DATABASE). Gates the header 「新建数据库」 button and the database-node
// context-menu variant; false for SQLite (a database is a file).
const supportsDatabaseEditor = computed(
  () => !!connStore.driverByName.get(props.connection.driver)?.capabilities?.databaseEditor,
)

// allRootNodes holds the database node objects for every schema on the
// server; `treeData` (bound to CTree) filters them by `selectedSchemas`.
// Filtering by reference — not re-mapping mkNode — keeps node identity so
// loaded children + expansion survive toggling the schema filter.
const allRootNodes = ref<CTreeNode[]>([])
const treeData = computed(() =>
  allRootNodes.value.filter((n) => selectedSchemas.value.has(metaOf(n).db!)),
)
const expandedKeys = ref<string[]>([])
// 常驻对象名搜索（DESIGN.md 搜索框规格）：过滤已加载的树节点（懒加载的
// 子层展开后即被纳入匹配），交给 CTree 的 pattern 匹配。
const treeFilter = ref('')
const loading = ref(false)
// `busy` covers the in-flight period of disconnect/reconnect/refresh — we
// disable the three action buttons while one is running so the user can't
// stack overlapping connect/disconnect calls.
const busy = ref(false)

const isLive = computed(() => connStore.isLive(props.connection.id))

// --- schema filter ---
//
// Lets the user pick which databases (schemas) appear in the tree. Pure UI
// hide — nothing is dropped server-side. Selection persists per connection in
// localStorage. `followAll` means "track every schema, including ones that
// appear later"; it stays true whenever all schemas are checked, so a new
// schema after a refresh is auto-included.
const filterKey = (id: string) => queryStore.schemaFilterKey(id)

const selectedSchemas = ref<Set<string>>(new Set())
const followAll = ref(true)
const panelOpen = ref(false)
const searchOpen = ref(false)
const searchText = ref('')
const listCollapsed = ref(false)
// busy flag for the in-panel schema refresh action.
const schemaBusy = ref(false)

const allSchemas = computed(() => allRootNodes.value.map((n) => metaOf(n).db!))
const totalCount = computed(() => allSchemas.value.length)
const selectedCount = computed(() => selectedSchemas.value.size)
const allChecked = computed(() => totalCount.value > 0 && selectedCount.value === totalCount.value)
const isChecked = (db: string) => selectedSchemas.value.has(db)

// Cap big counts to 99+ in the compact trigger; the panel shows full numbers.
const cap = (n: number) => (n > 99 ? '99+' : String(n))
const triggerLabel = computed(() => `${cap(selectedCount.value)}/${cap(totalCount.value)}`)

const visibleSchemas = computed(() => {
  const q = searchText.value.trim().toLowerCase()
  if (!q) return allSchemas.value
  return allSchemas.value.filter((s) => s.toLowerCase().includes(q))
})

function loadPersisted(id: string): { followAll: boolean; schemas: string[] } | null {
  try {
    const raw = localStorage.getItem(filterKey(id))
    if (!raw) return null
    const v = JSON.parse(raw)
    if (typeof v?.followAll === 'boolean' && Array.isArray(v?.schemas)) {
      return { followAll: v.followAll, schemas: v.schemas.filter((s: any) => typeof s === 'string') }
    }
  } catch {
    /* ignore corrupt entry — fall back to "show all" */
  }
  return null
}

function persistFilter() {
  try {
    localStorage.setItem(
      filterKey(props.connection.id),
      JSON.stringify({ followAll: followAll.value, schemas: [...selectedSchemas.value] }),
    )
  } catch {
    /* quota / disabled storage — selection just won't persist */
  }
}

// Reconcile the selection against the actual schema list after a (re)load:
// drop schemas that vanished, and re-select everything when in follow-all mode.
function reconcileSelection(dbs: string[]) {
  const avail = new Set(dbs)
  const next = followAll.value
    ? new Set(dbs)
    : new Set([...selectedSchemas.value].filter((s) => avail.has(s)))
  selectedSchemas.value = next
  followAll.value = dbs.length > 0 && next.size === dbs.length
  persistFilter()
}

function toggleAll(checked: boolean) {
  if (totalCount.value === 0) return
  selectedSchemas.value = checked ? new Set(allSchemas.value) : new Set()
  followAll.value = checked
  persistFilter()
}

function toggleOne(db: string, checked: boolean) {
  const next = new Set(selectedSchemas.value)
  if (checked) next.add(db)
  else next.delete(db)
  selectedSchemas.value = next
  // Checking the last unchecked schema re-arms follow-all (and the linked
  // "all schemas" checkbox); unchecking any one disarms it.
  followAll.value = totalCount.value > 0 && next.size === totalCount.value
  persistFilter()
}

function toggleSearch() {
  searchOpen.value = !searchOpen.value
  if (!searchOpen.value) searchText.value = ''
}

// Sync the schema filter to the shared store so QueryTab's dropdown respects it.
watch(selectedSchemas, (sel) => {
  queryStore.setSchemaFilter(props.connection.id, sel.size ? [...sel] : null)
})

async function onRefreshSchemas() {
  if (schemaBusy.value) return
  schemaBusy.value = true
  try {
    const dbs = await store.ensureDatabases(props.connection.id, true)
    allRootNodes.value = dbs.map((db) => mkNode(db, { kind: 'database', db }))
    reconcileSelection(dbs)
  } catch (e) {
    message.error(t('objectTree.refreshFailed', { error: String(e) }))
  } finally {
    schemaBusy.value = false
  }
}

interface TreeMeta {
  kind: 'database' | 'schema' | 'tableGroup' | 'viewGroup' | 'queryGroup' | 'table' | 'view' | 'column' | 'query'
  db?: string
  // Schema between db and table for schema-ful databases; '' / undefined for
  // databases without the level (MySQL).
  schema?: string
  table?: string
  // for kind === 'column': the column name (keeps sibling keys unique) and
  // whether the column is part of the primary key.
  column?: string
  pk?: boolean
  // for kind === 'query': the saved_query identity + payload.
  queryId?: string
  queryName?: string
  querySql?: string
}

function nodeKey(meta: TreeMeta): string {
  const ns = meta.schema ? `${meta.db}:${meta.schema}` : meta.db
  switch (meta.kind) {
    case 'database': return `db:${meta.db}`
    case 'schema': return `schema:${ns}`
    case 'tableGroup': return `dbtables:${ns}`
    case 'viewGroup': return `dbviews:${ns}`
    case 'queryGroup': return `dbqueries:${ns}`
    case 'table': return `tbl:${ns}:${meta.table}`
    case 'view': return `vw:${ns}:${meta.table}`
    case 'column': return `col:${ns}:${meta.table}:${meta.column}`
    case 'query': return `q:${meta.db}:${meta.queryId}`
  }
}

function mkNode(label: string, meta: TreeMeta, isLeaf = false): CTreeNode {
  return {
    key: nodeKey(meta),
    label,
    isLeaf,
    children: undefined,
    // store the meta tag for menu / dblclick handlers
    extra: meta,
  }
}

async function loadRoot() {
  loading.value = true
  try {
    const dbs = await store.ensureDatabases(props.connection.id)
    allRootNodes.value = dbs.map((db) => mkNode(db, { kind: 'database', db }))
    reconcileSelection(dbs)
  } catch (e) {
    message.error(t('objectTree.loadDatabasesFailed', { error: String(e) }))
  } finally {
    loading.value = false
  }
}

watch(
  () => props.connection.id,
  (id, prev) => {
    if (id === prev) return
    // Seed the saved schema selection before loadRoot() reconciles it against
    // the live schema list. Reset transient panel UI on connection switch.
    const saved = loadPersisted(id)
    selectedSchemas.value = new Set(saved?.schemas ?? [])
    followAll.value = saved ? saved.followAll : true
    panelOpen.value = false
    searchOpen.value = false
    searchText.value = ''
    listCollapsed.value = false
    treeFilter.value = ''
    loadRoot()
  },
  { immediate: true },
)

// The Tables/Views/Queries group nodes under one namespace (db or db.schema).
// Saved queries are scoped the same way (connId + db + schema), so the
// queries group lives at the namespace level: the database node for
// schema-less drivers, each schema node for schema-ful ones.
function groupNodes(db: string, schema: string | undefined): CTreeNode[] {
  return [
    mkNode(t('objectTree.tables'), { kind: 'tableGroup', db, schema }),
    mkNode(t('objectTree.views'), { kind: 'viewGroup', db, schema }),
    mkNode(t('objectTree.queries'), { kind: 'queryGroup', db, schema }),
  ]
}

// 分组节点的译文在渲染时解析(labelPartsFor 在 #label 插槽里被调用,t()
// 在渲染上下文里响应 locale),切语言即刷新,无需重建树;数据节点
// (库/表/查询名)保持构建时的 label。
const GROUP_LABEL_KEYS: Record<string, string> = {
  tableGroup: 'objectTree.tables',
  viewGroup: 'objectTree.views',
  queryGroup: 'objectTree.queries',
}

// 显示 label 拆成 [前段, 命中段, 后段]:搜索时只加粗命中的子串(与 CTree
// 过滤一致:不区分大小写的 includes);无命中时命中段为 ''。
function labelPartsFor(node: CTreeNode): [string, string, string] {
  const kind = metaOf(node).kind
  const key = GROUP_LABEL_KEYS[kind]
  const label = key ? t(key) : node.label
  const q = treeFilter.value.trim()
  if (!q) return [label, '', '']
  const idx = label.toLowerCase().indexOf(q.toLowerCase())
  if (idx < 0) return [label, '', '']
  return [label.slice(0, idx), label.slice(idx, idx + q.length), label.slice(idx + q.length)]
}

async function onLoad(node: CTreeNode): Promise<boolean> {
  const meta = metaOf(node)
  try {
    if (meta.kind === 'database') {
      if (hasSchemas.value) {
        // Schema-ful database: database → schema nodes.
        const schemaList = await store.ensureSchemas(props.connection.id, meta.db!)
        node.children = (schemaList ?? []).map((s) =>
          mkNode(s, { kind: 'schema', db: meta.db, schema: s }),
        )
        return true
      }
      node.children = groupNodes(meta.db!, undefined)
      return true
    }
    if (meta.kind === 'schema') {
      node.children = groupNodes(meta.db!, meta.schema)
      return true
    }
    if (meta.kind === 'queryGroup') {
      const list = await savedQueryApi.list(props.connection.id, meta.db!, meta.schema ?? '')
      node.children = (list ?? []).map((q) =>
        mkNode(
          q.name,
          { kind: 'query', db: meta.db, schema: meta.schema, queryId: q.id, queryName: q.name, querySql: q.sqlText },
          true,
        ),
      )
      return true
    }
    if (meta.kind === 'tableGroup') {
      const tables = await store.ensureTables(props.connection.id, meta.db!, false, meta.schema ?? '')
      node.children = tables.map((t) => {
        const child = mkNode(t.name, { kind: 'table', db: meta.db, schema: meta.schema, table: t.name })
        child.isLeaf = false
        return child
      })
      return true
    }
    if (meta.kind === 'viewGroup') {
      // Views are lighter — we expose them as leaves for now (M3 doesn't
      // need columns under a view to satisfy MVP acceptance).
      const views = await metaApi.listViews(props.connection.id, meta.db!, meta.schema ?? '')
      node.children = (views ?? []).map((v) =>
        mkNode(v.name, { kind: 'view', db: meta.db, schema: meta.schema, table: v.name }, true),
      )
      return true
    }
    if (meta.kind === 'table') {
      const cols = await store.ensureColumns(props.connection.id, meta.db!, meta.table!, false, meta.schema ?? '')
      node.children = cols.map((c) =>
        mkNode(
          `${c.name}  ${c.nativeType}`,
          { kind: 'column', db: meta.db, schema: meta.schema, table: meta.table, column: c.name, pk: c.isPrimaryKey },
          true,
        ),
      )
      return true
    }
    return true
  } catch (e) {
    message.error(t('objectTree.loadFailed', { error: String(e) }))
    // CTree 语义:返回 false 即加载失败,children 保持 undefined —— CTree
    // 会把该节点移出 expandedKeys 并停止触发,下次展开自动重试(n-tree/
    // treemate 时代需要 children=[] 断重试链的坑已由 CTree 内部规避)。
    return false
  }
}

// --- context menu (Wails native) ---
//
// Per CLAUDE.md rule 11, right-click uses Wails native menus, not HTML
// overlays. The flow:
//   1. onContextMenu sets `--custom-contextmenu` on `paneRef` based on the
//      node kind (table / view / tableGroup / viewGroup / database).
//   2. setActiveTableContext / setActiveTreeContext push the node identity
//      and per-action callbacks into shared module singletons.
//   3. The corresponding listener (api/tableContextMenu.ts, api/treeContextMenu.ts)
//      receives the `ctx:tbl-*` / `ctx:tree-*` event from Go and acts.
const paneRef = ref<HTMLElement | null>(null)

function setMenu(name: string) {
  if (name) {
    paneRef.value?.style.setProperty('--custom-contextmenu', name)
  } else {
    paneRef.value?.style.removeProperty('--custom-contextmenu')
  }
}

// Walk the reactive tree to find a node by key. Returns the node (so callers
// can mutate `children`) or null.
function findNodeByKey(key: string): CTreeNode | null {
  const stack: CTreeNode[] = [...allRootNodes.value]
  while (stack.length) {
    const n = stack.shift()!
    if (n.key === key) return n
    if (n.children) stack.push(...n.children)
  }
  return null
}

// The refresh helpers reset `children` to undefined and let CTree's loader
// take over: an expanded node reloads immediately (its store cache was
// force-refreshed first where applicable), a collapsed one lazy-loads on the
// next expand. No direct onLoad calls — that would race CTree's own trigger.

async function refreshTableGroup(db: string, schema?: string) {
  await store.ensureTables(props.connection.id, db, true, schema ?? '')
  const node = findNodeByKey(nodeKey({ kind: 'tableGroup', db, schema }))
  if (node) node.children = undefined
}

async function refreshViewGroup(db: string, schema?: string) {
  const node = findNodeByKey(nodeKey({ kind: 'viewGroup', db, schema }))
  if (node) node.children = undefined
}

async function refreshQueryGroup(db: string, schema?: string) {
  const node = findNodeByKey(nodeKey({ kind: 'queryGroup', db, schema }))
  if (node) node.children = undefined
}

async function refreshColumns(db: string, table: string, schema?: string) {
  await store.ensureColumns(props.connection.id, db, table, true, schema ?? '')
  const node = findNodeByKey(nodeKey({ kind: 'table', db, schema, table }))
  if (node) node.children = undefined
}

async function refreshDatabase() {
  await store.ensureDatabases(props.connection.id, true)
  await loadRoot()
}

function onContextMenu(event: MouseEvent, node: CTreeNode) {
  // Always preventDefault so the WebView's developer-tools menu never shows.
  // Wails' native menu opens off `--custom-contextmenu`, NOT off the default
  // browser menu, so suppressing the latter is safe.
  event.preventDefault()
  const m = metaOf(node)
  const connId = props.connection.id

  switch (m.kind) {
    case 'table':
      if (!m.db || !m.table) return
      setActiveTableContext({
        connId,
        db: m.db,
        schema: m.schema,
        table: m.table,
        onAfterMutate: () => refreshTableGroup(m.db!, m.schema),
      })
      setActiveTreeContext({
        connId,
        db: m.db,
        schema: m.schema,
        table: m.table,
        onRefreshColumns: () => refreshColumns(m.db!, m.table!, m.schema),
      })
      setMenu('catdb-tree-table')
      break
    case 'view':
      if (!m.db || !m.table) return
      // Reuse the table-open event — "打开" on a view also opens a data tab.
      setActiveTableContext({
        connId,
        db: m.db,
        schema: m.schema,
        table: m.table,
      })
      setMenu('catdb-tree-view')
      break
    case 'tableGroup':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        schema: m.schema,
        onRefreshTables: () => refreshTableGroup(m.db!, m.schema),
      })
      setMenu('catdb-tree-table-group')
      break
    case 'viewGroup':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        schema: m.schema,
        onRefreshViews: () => refreshViewGroup(m.db!, m.schema),
      })
      setMenu('catdb-tree-view-group')
      break
    case 'database':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        onRefreshDb: () => refreshDatabase(),
      })
      setMenu(supportsDatabaseEditor.value ? 'catdb-tree-database' : 'catdb-tree-database-basic')
      break
    case 'schema':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        schema: m.schema,
        onRefreshSchema: async () => {
          await refreshTableGroup(m.db!, m.schema)
          await refreshViewGroup(m.db!, m.schema)
        },
      })
      setMenu('catdb-tree-schema')
      break
    case 'queryGroup':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        schema: m.schema,
        onRefreshQueries: () => refreshQueryGroup(m.db!, m.schema),
      })
      setMenu('catdb-tree-query-group')
      break
    case 'query':
      if (!m.db || !m.queryId) return
      setActiveTreeContext({
        connId,
        db: m.db,
        schema: m.schema,
        queryId: m.queryId,
        queryName: m.queryName,
        querySql: m.querySql,
        onRefreshQueries: () => refreshQueryGroup(m.db!, m.schema),
      })
      setMenu('catdb-tree-query')
      break
    case 'column':
      // Columns have no menu — clear so nothing shows.
      setMenu('')
      break
  }
}

function onDblclick(_: MouseEvent, node: CTreeNode) {
  const m = metaOf(node)
  // 表/视图 → 打开数据浏览
  if (m.kind === 'table' || m.kind === 'view') {
    if (m.db && m.table) emit('open-data', { db: m.db, schema: m.schema, table: m.table })
    return
  }
  // 保存的查询 → 打开查询 tab（带 SQL）
  if (m.kind === 'query') {
    if (m.queryId) {
      queryStore.openSavedQuery(props.connection.id, {
        id: m.queryId,
        name: m.queryName ?? t('objectTree.queries'),
        sqlText: m.querySql ?? '',
        dbName: m.db ?? '',
        schemaName: m.schema ?? '',
      })
    }
    return
  }
  // 叶子节点：没有可展开的内容
  if (m.kind === 'column') return
  // 切换展开/收起状态
  const key = node.key
  const idx = expandedKeys.value.indexOf(key)
  if (idx >= 0) {
    const keys = [...expandedKeys.value]
    keys.splice(idx, 1)
    expandedKeys.value = keys
  } else {
    expandedKeys.value = [...expandedKeys.value, key]
  }
}

function onClick(_: MouseEvent, node: CTreeNode) {
  const m = metaOf(node)
  // Schema-less database → the overview lists its tables directly. With a
  // schema level, the database node only expands; the overview belongs to
  // the schema nodes.
  if (m.kind === 'database' && !hasSchemas.value) {
    queryStore.setSelectedDb(props.connection.id, m.db!)
    emit('open-tables-overview', { db: m.db! })
  }
  if (m.kind === 'schema') {
    queryStore.setSelectedDb(props.connection.id, m.db!)
    emit('open-tables-overview', { db: m.db!, schema: m.schema })
  }
}

// --- header actions: refresh + new database ---
//
// 连接/断开 由侧栏与右键菜单管理；树头只保留与对象树相关的两个动作。

async function onRefresh() {
  if (busy.value) return
  busy.value = true
  try {
    store.invalidate(props.connection.id)
    await loadRoot()
  } catch (e) {
    message.error(t('objectTree.refreshFailed', { error: String(e) }))
  } finally {
    busy.value = false
  }
}

function onNewDatabase() {
  if (!isLive.value) return
  void systemApi.openDatabaseEditor(props.connection.id, '')
}

// The database-editor child window broadcasts `database:saved` after a
// CREATE/ALTER DATABASE succeeds. Re-pull the root nodes for the matching
// connection so the new entry appears (or the renamed/altered entry stays
// consistent with server state).
let offDbSaved: (() => void) | null = null
let offQuerySaved: (() => void) | null = null
onMounted(() => {
  offDbSaved = systemApi.onDatabaseSaved(({ connId }) => {
    if (connId !== props.connection.id) return
    void (async () => {
      try {
        await store.ensureDatabases(props.connection.id, true)
        await loadRoot()
      } catch (e) {
        message.error(t('objectTree.refreshFailed', { error: String(e) }))
      }
    })()
  })
  // QueryTab broadcasts this after saving a query; refresh the matching
  // 「查询」 group so the new/renamed entry shows without a manual refresh.
  offQuerySaved = onEvent<{ connId: string; db: string; schema?: string }>(
    'saved-query:changed',
    ({ connId, db, schema }) => {
      if (connId !== props.connection.id) return
      void refreshQueryGroup(db, schema || undefined)
    },
  )
})
onBeforeUnmount(() => {
  offDbSaved?.()
  offDbSaved = null
  offQuerySaved?.()
  offQuerySaved = null
})
</script>

<template>
  <div ref="paneRef" class="tree-pane">
    <div class="header">
      <span class="status-dot" :class="{ live: isLive }" />
      <span class="title">{{ connection.name }}</span>
      <span class="spacer" />
      <CPopover v-if="isLive" v-model:show="panelOpen" placement="bottom-start">
        <template #trigger>
          <span class="schema-trigger" :title="$t(`objectTree.schemaFilter.tooltip.${nsTerm}`)">
            <span class="st-count">{{ triggerLabel }}</span>
            <svg class="st-caret" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M4 6l4 4 4-4" />
            </svg>
          </span>
        </template>
        <div class="schema-panel">
          <div class="sp-head">
            <span class="sp-title">{{ $t(`objectTree.schemaFilter.title.${nsTerm}`) }}</span>
            <span class="sp-spacer" />
            <button
              class="sp-ico"
              type="button"
              :class="{ spinning: schemaBusy }"
              :disabled="schemaBusy"
              :title="$t(`objectTree.schemaFilter.refresh.${nsTerm}`)"
              @click="onRefreshSchemas"
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M13.5 3v3.5H10" />
                <path d="M13 6.5A5.5 5.5 0 1 0 13 11" />
              </svg>
            </button>
            <button
              class="sp-ico"
              type="button"
              :title="listCollapsed ? $t('objectTree.schemaFilter.expand') : $t('objectTree.schemaFilter.collapse')"
              @click="listCollapsed = !listCollapsed"
            >
              <svg :class="{ flip: listCollapsed }" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M4 10l4-4 4 4" />
              </svg>
            </button>
            <button
              class="sp-ico"
              type="button"
              :title="$t('objectTree.schemaFilter.close')"
              @click="panelOpen = false"
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M4 4l8 8M12 4l-8 8" />
              </svg>
            </button>
          </div>
          <div class="sp-toolbar">
            <CCheckbox
              :model-value="allChecked"
              :disabled="totalCount === 0"
              @update:model-value="toggleAll"
            >
              {{ $t(`objectTree.schemaFilter.all.${nsTerm}`) }}
            </CCheckbox>
            <span class="sp-count">{{ selectedCount }}/{{ totalCount }}</span>
            <span class="sp-spacer" />
            <button
              class="sp-filter-btn"
              type="button"
              :class="{ active: searchOpen }"
              :title="$t('objectTree.schemaFilter.filter')"
              @click="toggleSearch"
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <circle cx="7" cy="7" r="4" />
                <path d="M10 10l3 3" />
              </svg>
              <span>{{ $t('objectTree.schemaFilter.filter') }}</span>
            </button>
          </div>
          <div v-if="searchOpen" class="sp-search">
            <CInput
              size="small"
              v-model="searchText"
              :placeholder="$t(`objectTree.schemaFilter.searchPlaceholder.${nsTerm}`)"
            />
          </div>
          <div v-show="!listCollapsed" class="sp-list">
            <CSpin :show="schemaBusy">
              <div class="sp-scroll">
                <template v-if="visibleSchemas.length">
                  <label v-for="s in visibleSchemas" :key="s" class="sp-row">
                    <CCheckbox
                      :model-value="isChecked(s)"
                      @update:model-value="(c: boolean) => toggleOne(s, c)"
                    >
                      {{ s }}
                    </CCheckbox>
                  </label>
                </template>
                <div v-else class="sp-empty">{{ $t(`objectTree.schemaFilter.empty.${nsTerm}`) }}</div>
              </div>
            </CSpin>
          </div>
        </div>
      </CPopover>
      <div class="actions">
        <button
          v-if="supportsDatabaseEditor"
          class="hbtn"
          type="button"
          :disabled="!isLive || busy"
          :title="isLive ? $t('objectTree.newDatabase') : $t('objectTree.notConnected')"
          @click="onNewDatabase"
        >
          <svg
            class="ico"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M8 3.5v9M3.5 8h9" />
          </svg>
        </button>
        <button
          class="hbtn"
          type="button"
          :disabled="busy || !isLive"
          :title="isLive ? $t('objectTree.refreshTree') : $t('objectTree.notConnected')"
          @click="onRefresh"
        >
          <svg
            class="ico"
            :class="{ spinning: busy }"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M13.5 3v3.5H10" />
            <path d="M13 6.5A5.5 5.5 0 1 0 13 11" />
          </svg>
        </button>
      </div>
    </div>
    <div class="tree-search">
      <svg class="ts-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="7" cy="7" r="4.5" />
        <path d="M10.5 10.5L14 14" />
      </svg>
      <input
        v-model="treeFilter"
        class="ts-input"
        type="text"
        spellcheck="false"
        :placeholder="$t('objectTree.searchPlaceholder')"
      />
      <button
        v-if="treeFilter"
        class="ts-clear"
        type="button"
        :title="$t('common.clear')"
        @click="treeFilter = ''"
      >×</button>
    </div>
    <div class="body">
      <CSpin :show="loading" class="tree-spin">
        <CTree
          :data="treeData"
          :pattern="treeFilter"
          v-model:expanded-keys="expandedKeys"
          :on-load="onLoad"
          @node-click="(n, e) => onClick(e, n)"
          @node-dblclick="(n, e) => onDblclick(e, n)"
          @node-contextmenu="(n, e) => onContextMenu(e, n)"
        >
          <template #prefix="{ node }">
            <span v-if="isPkColumn(node)" class="tree-pk">🔑</span>
            <AppIcon v-else-if="prefixIconFor(node)" :src="prefixIconFor(node)" />
          </template>
          <template #label="{ node }">
            <template v-if="labelPartsFor(node)[1]">{{ labelPartsFor(node)[0] }}<span class="tree-match">{{ labelPartsFor(node)[1] }}</span>{{ labelPartsFor(node)[2] }}</template>
            <template v-else>{{ labelPartsFor(node)[0] }}</template>
          </template>
        </CTree>
      </CSpin>
    </div>
  </div>
</template>

<style scoped>
.tree-pane { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 6px 4px 10px;
  font-size: var(--catdb-fs-small);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-top: 1px solid var(--catdb-separator);
  border-bottom: 1px solid var(--catdb-separator);
  min-height: 30px;
}
.header .title {
  opacity: 0.75;
  min-width: 0;
  flex: 0 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.header .spacer { flex: 1 1 0; }

/* Tiny pulse indicator — green when the connection is live, gray when not. */
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--catdb-text-tertiary);
  flex: 0 0 auto;
}
.status-dot.live { background: var(--catdb-accent); box-shadow: 0 0 0 2px color-mix(in srgb, var(--catdb-accent) 18%, transparent); }

/* Schema-filter trigger chip — "3/40" next to the connection name. */
.schema-trigger {
  --wails-draggable: no-drag;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
  height: 18px;
  padding: 0 4px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  font-size: var(--catdb-fs-mini);
  font-variant-numeric: tabular-nums;
  letter-spacing: 0;
  text-transform: none;
  opacity: 0.7;
  cursor: pointer;
  user-select: none;
}
.schema-trigger:hover { opacity: 1; background: var(--catdb-hover-fill); }
.st-count { line-height: 1; }
.st-caret { width: 10px; height: 10px; display: block; opacity: 0.7; }

/* Schema-filter dropdown panel — hosted inside CPopover's raised panel
   (surface-raised + md 圆角 + shadow-menu 由 .c-pop 提供);负 margin 抵掉
   .c-pop 的 12px 内边距,让面板贴边自排(原 n-popover raw 形态)。 */
.schema-panel {
  --wails-draggable: no-drag;
  margin: calc(-1 * var(--catdb-space-md));
  width: 230px;
  color: var(--catdb-text-primary);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-md);
  overflow: hidden;
  font-size: var(--catdb-fs-small);
  text-transform: none;
  letter-spacing: 0;
}
.sp-head {
  display: flex;
  align-items: center;
  gap: 2px;
  height: 26px;
  padding: 0 4px 0 8px;
  border-bottom: 1px solid var(--catdb-separator);
}
.sp-title { font-size: var(--catdb-fs-mini); opacity: 0.6; text-transform: uppercase; letter-spacing: 0.04em; }
.sp-spacer { flex: 1 1 0; }
.sp-ico {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  border-radius: var(--catdb-rounded-sm);
  opacity: 0.6;
  cursor: pointer;
}
.sp-ico:hover:not(:disabled) { opacity: 1; background: var(--catdb-hover-fill); }
.sp-ico:disabled { opacity: 0.35; cursor: default; }
.sp-ico svg { width: 13px; height: 13px; display: block; }
.sp-ico svg.flip { transform: rotate(180deg); }
.sp-ico.spinning svg { animation: spin 0.8s linear infinite; }
.sp-toolbar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border-bottom: 1px solid var(--catdb-separator);
}
.sp-count { font-size: var(--catdb-fs-mini); opacity: 0.55; font-variant-numeric: tabular-nums; }
.sp-filter-btn {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  height: 20px;
  padding: 0 6px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: transparent;
  color: inherit;
  font-size: var(--catdb-fs-mini);
  cursor: pointer;
  opacity: 0.75;
}
.sp-filter-btn:hover { opacity: 1; }
.sp-filter-btn.active {
  opacity: 1;
  border-color: var(--catdb-accent);
  color: var(--catdb-accent);
}
.sp-filter-btn svg { width: 12px; height: 12px; display: block; }
.sp-search { padding: 6px 8px 0; }
.sp-list { padding: 4px 0; }
.sp-scroll { max-height: 240px; overflow-y: auto; }
.sp-row {
  display: flex;
  align-items: center;
  height: 24px;
  padding: 0 8px;
  cursor: pointer;
}
.sp-row:hover { background: var(--catdb-hover-fill); }
.sp-row :deep(.c-check) { width: 100%; min-width: 0; }
.schema-panel :deep(.c-check .lbl) {
  font-size: var(--catdb-fs-small);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sp-empty { padding: 12px 8px; text-align: center; font-size: var(--catdb-fs-mini); opacity: 0.5; }

/* 常驻对象搜索框 — DESIGN.md 搜索框规格：24px 高、前置 14px 放大镜、sm 圆角。 */
.tree-search {
  position: relative;
  display: flex;
  align-items: center;
  flex: 0 0 auto;
  padding: 6px 8px 0;
}
.ts-icon {
  position: absolute;
  left: 15px;
  width: 14px;
  height: 14px;
  color: var(--catdb-text-tertiary);
  pointer-events: none;
}
.ts-input {
  flex: 1 1 0;
  min-width: 0;
  height: var(--catdb-control-height);
  padding: 0 22px 0 25px;
  font: inherit;
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
  background: var(--catdb-surface-content);
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  outline: none;
}
.ts-input::placeholder { color: var(--catdb-text-tertiary); }
.ts-input:focus {
  border-color: var(--catdb-accent);
  box-shadow: var(--catdb-focus-ring);
}
.ts-clear {
  position: absolute;
  right: 12px;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  border-radius: var(--catdb-rounded-xs);
  background: transparent;
  color: inherit;
  opacity: 0.4;
  font-size: var(--catdb-fs-body);
  line-height: 1;
  cursor: default;
}
.ts-clear:hover { opacity: 0.8; background: var(--catdb-hover-fill); }

.actions { display: flex; align-items: center; gap: 2px; flex: 0 0 auto; }
/* Header icon buttons — DESIGN.md button-toolbar(自绘无边框图标钮)。 */
.hbtn {
  --wails-draggable: no-drag;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  border-radius: var(--catdb-rounded-sm);
  opacity: 0.65;
}
.hbtn:hover:not(:disabled) { opacity: 1; background: var(--catdb-hover-fill); }
.hbtn:active:not(:disabled) { background: var(--catdb-pressed-fill); }
.hbtn:disabled { opacity: 0.3; }
.hbtn .ico {
  width: 13px;
  height: 13px;
  display: block;
}
.hbtn .ico.spinning { animation: spin 0.8s linear infinite; }
@keyframes spin {
  to { transform: rotate(360deg); }
}
.body { flex: 1 1 auto; min-height: 0; padding: 6px; display: flex; }
.tree-spin { flex: 1 1 0; min-width: 0; min-height: 0; }
/* Primary-key marker — emoji sized to line up with the lucide AppIcon set. */
.tree-pk {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  flex: 0 0 auto;
  font-size: var(--catdb-fs-mini);
  line-height: 1;
}
/* Search match — only the matched substring goes bold (no underline). */
.tree-match { font-weight: 600; color: var(--catdb-accent); }
</style>
