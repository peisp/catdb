<script setup lang="ts">
// ObjectTree — per-connection database/table/column tree. Lazy loads each
// level via MetadataService. Single-click a database node → open the
// tables overview tab. Double-click any non-leaf node → expand/collapse
// it. Right-click → Wails native context menu (registered in
// wailsbridge/contextmenu.go as `catdb-tree-*`, dispatched via
// api/{table,tree}ContextMenu.ts).
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  NButton,
  NScrollbar,
  NSpin,
  NTree,
  useMessage,
} from 'naive-ui'
import type { TreeOption } from 'naive-ui'
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

const props = defineProps<{ connection: ConnectionProfile }>()
const emit = defineEmits<{
  (e: 'open-data', payload: { db: string; table: string }): void
  (e: 'open-structure', payload: { db: string; table: string }): void
  (e: 'open-tables-overview', payload: { db: string }): void
}>()

const store = useMetadataStore()
const connStore = useConnectionsStore()
const queryStore = useQueryStore()
const message = useMessage()

const treeData = ref<TreeOption[]>([])
const expandedKeys = ref<string[]>([])
const loading = ref(false)
// `busy` covers the in-flight period of disconnect/reconnect/refresh — we
// disable the three action buttons while one is running so the user can't
// stack overlapping connect/disconnect calls.
const busy = ref(false)

const isLive = computed(() => connStore.isLive(props.connection.id))

interface TreeMeta {
  kind: 'database' | 'tableGroup' | 'viewGroup' | 'queryGroup' | 'table' | 'view' | 'column' | 'query'
  db?: string
  table?: string
  // for kind === 'query': the saved_query identity + payload.
  queryId?: string
  queryName?: string
  querySql?: string
}

function nodeKey(meta: TreeMeta): string {
  switch (meta.kind) {
    case 'database': return `db:${meta.db}`
    case 'tableGroup': return `dbtables:${meta.db}`
    case 'viewGroup': return `dbviews:${meta.db}`
    case 'queryGroup': return `dbqueries:${meta.db}`
    case 'table': return `tbl:${meta.db}:${meta.table}`
    case 'view': return `vw:${meta.db}:${meta.table}`
    case 'column': return `col:${meta.db}:${meta.table}:${meta.table}`
    case 'query': return `q:${meta.db}:${meta.queryId}`
  }
}

function mkNode(label: string, meta: TreeMeta, isLeaf = false): TreeOption {
  return {
    key: nodeKey(meta),
    label,
    isLeaf,
    children: undefined,
    // store the meta tag for menu / dblclick handlers
    extra: meta as any,
  } as TreeOption
}

async function loadRoot() {
  loading.value = true
  try {
    const dbs = await store.ensureDatabases(props.connection.id)
    treeData.value = dbs.map((db) => mkNode(db, { kind: 'database', db }))
  } catch (e) {
    message.error(`load databases failed: ${String(e)}`)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.connection.id,
  (id, prev) => {
    if (id !== prev) loadRoot()
  },
  { immediate: true },
)

async function onLoad(node: TreeOption): Promise<boolean> {
  const meta = (node as any).extra as TreeMeta
  try {
    if (meta.kind === 'database') {
      // Show Tables + Views + saved-queries groups under the database.
      node.children = [
        mkNode('Tables', { kind: 'tableGroup', db: meta.db }),
        mkNode('Views', { kind: 'viewGroup', db: meta.db }),
        mkNode('查询', { kind: 'queryGroup', db: meta.db }),
      ]
      return true
    }
    if (meta.kind === 'queryGroup') {
      const list = await savedQueryApi.list(props.connection.id, meta.db!)
      node.children = (list ?? []).map((q) =>
        mkNode(q.name, { kind: 'query', db: meta.db, queryId: q.id, queryName: q.name, querySql: q.sqlText }, true),
      )
      return true
    }
    if (meta.kind === 'tableGroup') {
      const tables = await store.ensureTables(props.connection.id, meta.db!)
      node.children = tables.map((t) => {
        const child = mkNode(t.name, { kind: 'table', db: meta.db, table: t.name })
        child.isLeaf = false
        return child
      })
      return true
    }
    if (meta.kind === 'viewGroup') {
      // Views are lighter — we expose them as leaves for now (M3 doesn't
      // need columns under a view to satisfy MVP acceptance).
      const views = await metaApi.listViews(props.connection.id, meta.db!)
      node.children = (views ?? []).map((v) => mkNode(v.name, { kind: 'view', db: meta.db, table: v.name }, true))
      return true
    }
    if (meta.kind === 'table') {
      const cols = await store.ensureColumns(props.connection.id, meta.db!, meta.table!)
      node.children = cols.map((c) =>
        mkNode(
          `${c.name}  ${c.nativeType}` + (c.isPrimaryKey ? '  🔑' : ''),
          { kind: 'column', db: meta.db, table: meta.table },
          true,
        ),
      )
      return true
    }
    return true
  } catch (e) {
    message.error(`load failed: ${String(e)}`)
    // 失败时必须把 children 写成空数组 + 返回 false，否则会陷入无限重试：
    //   1) children 留空 → treemate 认为 shallowLoaded=false → n-tree
    //      内部 watchEffect 每次 expandedKeys 变动都会再次 triggerLoading
    //   2) 返回 undefined → n-tree 的 TreeNode.handleSwitcherClick 视为
    //      加载成功，把 key push 到 expandedKeys，又触发上一条
    // 设为空数组 + return false 同时切断两条触发路径；想重试走顶部 Refresh。
    node.children = []
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
function findNodeByKey(key: string): TreeOption | null {
  const stack: TreeOption[] = [...treeData.value]
  while (stack.length) {
    const n = stack.shift()!
    if (n.key === key) return n
    if (n.children) stack.push(...(n.children as TreeOption[]))
  }
  return null
}

async function refreshTableGroup(db: string) {
  await store.ensureTables(props.connection.id, db, true)
  const node = findNodeByKey(nodeKey({ kind: 'tableGroup', db }))
  if (!node) return
  node.children = undefined
  await onLoad(node)
}

async function refreshViewGroup(db: string) {
  const node = findNodeByKey(nodeKey({ kind: 'viewGroup', db }))
  if (!node) return
  node.children = undefined
  await onLoad(node)
}

async function refreshQueryGroup(db: string) {
  const node = findNodeByKey(nodeKey({ kind: 'queryGroup', db }))
  if (!node) return
  node.children = undefined
  // Only reload if currently expanded — otherwise it lazy-loads on next open.
  if (expandedKeys.value.includes(node.key as string)) {
    await onLoad(node)
  }
}

async function refreshColumns(db: string, table: string) {
  await store.ensureColumns(props.connection.id, db, table, true)
  const node = findNodeByKey(nodeKey({ kind: 'table', db, table }))
  if (!node) return
  node.children = undefined
  if (expandedKeys.value.includes(node.key as string)) {
    await onLoad(node)
  }
}

async function refreshDatabase() {
  await store.ensureDatabases(props.connection.id, true)
  await loadRoot()
}

function onContextMenu(event: MouseEvent, node: TreeOption) {
  // Always preventDefault so the WebView's developer-tools menu never shows.
  // Wails' native menu opens off `--custom-contextmenu`, NOT off the default
  // browser menu, so suppressing the latter is safe.
  event.preventDefault()
  const m = (node as any).extra as TreeMeta
  const connId = props.connection.id

  switch (m.kind) {
    case 'table':
      if (!m.db || !m.table) return
      setActiveTableContext({
        connId,
        db: m.db,
        table: m.table,
        onAfterMutate: () => refreshTableGroup(m.db!),
      })
      setActiveTreeContext({
        connId,
        db: m.db,
        table: m.table,
        onRefreshColumns: () => refreshColumns(m.db!, m.table!),
      })
      setMenu('catdb-tree-table')
      break
    case 'view':
      if (!m.db || !m.table) return
      // Reuse the table-open event — "打开" on a view also opens a data tab.
      setActiveTableContext({
        connId,
        db: m.db,
        table: m.table,
      })
      setMenu('catdb-tree-view')
      break
    case 'tableGroup':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        onRefreshTables: () => refreshTableGroup(m.db!),
      })
      setMenu('catdb-tree-table-group')
      break
    case 'viewGroup':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        onRefreshViews: () => refreshViewGroup(m.db!),
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
      setMenu('catdb-tree-database')
      break
    case 'queryGroup':
      if (!m.db) return
      setActiveTreeContext({
        connId,
        db: m.db,
        onRefreshQueries: () => refreshQueryGroup(m.db!),
      })
      setMenu('catdb-tree-query-group')
      break
    case 'query':
      if (!m.db || !m.queryId) return
      setActiveTreeContext({
        connId,
        db: m.db,
        queryId: m.queryId,
        queryName: m.queryName,
        querySql: m.querySql,
        onRefreshQueries: () => refreshQueryGroup(m.db!),
      })
      setMenu('catdb-tree-query')
      break
    case 'column':
      // Columns have no menu — clear so nothing shows.
      setMenu('')
      break
  }
}

function onDblclick(_: MouseEvent, node: TreeOption) {
  const m = (node as any).extra as TreeMeta
  // 表/视图 → 打开数据浏览
  if (m.kind === 'table' || m.kind === 'view') {
    if (m.db && m.table) emit('open-data', { db: m.db, table: m.table })
    return
  }
  // 保存的查询 → 打开查询 tab（带 SQL）
  if (m.kind === 'query') {
    if (m.queryId) {
      queryStore.openSavedQuery(props.connection.id, {
        id: m.queryId,
        name: m.queryName ?? '查询',
        sqlText: m.querySql ?? '',
        dbName: m.db ?? '',
      })
    }
    return
  }
  // 叶子节点：没有可展开的内容
  if (m.kind === 'column') return
  // 切换展开/收起状态
  const key = node.key as string
  const idx = expandedKeys.value.indexOf(key)
  if (idx >= 0) {
    const keys = [...expandedKeys.value]
    keys.splice(idx, 1)
    expandedKeys.value = keys
  } else {
    expandedKeys.value = [...expandedKeys.value, key]
  }
}

function onClick(_: MouseEvent, node: TreeOption) {
  const m = (node as any).extra as TreeMeta
  if (m.kind === 'database') {
    emit('open-tables-overview', { db: m.db! })
  }
}

const nodeProps = ({ option }: { option: TreeOption }) => ({
  onClick: (e: MouseEvent) => onClick(e, option),
  onContextmenu: (e: MouseEvent) => onContextMenu(e, option),
  onDblclick: (e: MouseEvent) => onDblclick(e, option),
})

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
    message.error(`刷新失败: ${String(e)}`)
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
        message.error(`刷新失败: ${String(e)}`)
      }
    })()
  })
  // QueryTab broadcasts this after saving a query; refresh the matching db's
  // 「查询」 group so the new/renamed entry shows without a manual refresh.
  offQuerySaved = onEvent<{ connId: string; db: string }>('saved-query:changed', ({ connId, db }) => {
    if (connId !== props.connection.id) return
    void refreshQueryGroup(db)
  })
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
      <div class="actions">
        <n-button
          class="hbtn"
          size="tiny"
          quaternary
          :disabled="!isLive || busy"
          :title="isLive ? '新建数据库' : '未连接'"
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
        </n-button>
        <n-button
          class="hbtn"
          size="tiny"
          quaternary
          :disabled="busy || !isLive"
          :title="isLive ? '刷新对象树' : '未连接'"
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
        </n-button>
      </div>
    </div>
    <div class="body">
      <n-scrollbar class="scroll">
        <n-spin :show="loading">
          <n-tree
            block-line
            virtual-scroll
            :data="treeData"
            :expanded-keys="expandedKeys"
            :on-load="onLoad"
            :node-props="nodeProps"
            :style="{ height: '100%' }"
            :indent="14"
            @update:expanded-keys="(keys) => (expandedKeys = keys)"
          />
        </n-spin>
      </n-scrollbar>
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
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-top: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  border-bottom: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  min-height: 30px;
}
.header .title { opacity: 0.75; }
.header .spacer { flex: 1 1 0; }

/* Tiny pulse indicator — green when the connection is live, gray when not. */
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: rgba(127, 127, 127, 0.45);
  flex: 0 0 auto;
}
.status-dot.live { background: #2eb872; box-shadow: 0 0 0 2px rgba(46, 184, 114, 0.18); }

.actions { display: flex; align-items: center; gap: 2px; }
.hbtn {
  --wails-draggable: no-drag;
  opacity: 0.65;
}
.hbtn:hover:not(:disabled) { opacity: 1; }
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
.scroll { flex: 1 1 0; min-width: 0; min-height: 0; }
.body :deep(.n-tree-node-content) { font-size: 12px; }
.body :deep(.n-spin-container),
.body :deep(.n-spin-content) { height: 100%; min-height: 0; }
</style>
