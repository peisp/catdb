<script setup lang="ts">
// ObjectTree — per-connection database/table/column tree. Lazy loads each
// level via MetadataService. Double-click a table → open the data browser
// in the workspace. Right-click → action menu (M4 will replace this with
// the native Wails context menu).
import { computed, ref, watch } from 'vue'
import {
  NDropdown,
  NIcon,
  NScrollbar,
  NSpin,
  NTree,
  useMessage,
} from 'naive-ui'
import type { TreeOption } from 'naive-ui'
import type { ConnectionProfile } from '../api/connections'
import { metadata as metaApi } from '../api'
import { useMetadataStore } from '../stores/metadata'

const props = defineProps<{ connection: ConnectionProfile }>()
const emit = defineEmits<{
  (e: 'open-data', payload: { db: string; table: string }): void
  (e: 'open-structure', payload: { db: string; table: string }): void
}>()

const store = useMetadataStore()
const message = useMessage()

const treeData = ref<TreeOption[]>([])
const loading = ref(false)

interface TreeMeta {
  kind: 'database' | 'tableGroup' | 'viewGroup' | 'table' | 'view' | 'column'
  db?: string
  table?: string
}

function nodeKey(meta: TreeMeta): string {
  switch (meta.kind) {
    case 'database': return `db:${meta.db}`
    case 'tableGroup': return `dbtables:${meta.db}`
    case 'viewGroup': return `dbviews:${meta.db}`
    case 'table': return `tbl:${meta.db}:${meta.table}`
    case 'view': return `vw:${meta.db}:${meta.table}`
    case 'column': return `col:${meta.db}:${meta.table}:${meta.table}`
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

async function onLoad(node: TreeOption): Promise<void> {
  const meta = (node as any).extra as TreeMeta
  try {
    if (meta.kind === 'database') {
      // Show Tables + Views groups under the database.
      node.children = [
        mkNode('Tables', { kind: 'tableGroup', db: meta.db }),
        mkNode('Views', { kind: 'viewGroup', db: meta.db }),
      ]
      return
    }
    if (meta.kind === 'tableGroup') {
      const tables = await store.ensureTables(props.connection.id, meta.db!)
      node.children = tables.map((t) => {
        const child = mkNode(t.name, { kind: 'table', db: meta.db, table: t.name })
        child.isLeaf = false
        return child
      })
      return
    }
    if (meta.kind === 'viewGroup') {
      // Views are lighter — we expose them as leaves for now (M3 doesn't
      // need columns under a view to satisfy MVP acceptance).
      const views = await metaApi.listViews(props.connection.id, meta.db!)
      node.children = (views ?? []).map((v) => mkNode(v.name, { kind: 'view', db: meta.db, table: v.name }, true))
      return
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
      return
    }
  } catch (e) {
    message.error(`load failed: ${String(e)}`)
  }
}

// --- context menu (NDropdown manual) ---

const ctxX = ref(0)
const ctxY = ref(0)
const ctxOpen = ref(false)
const ctxNode = ref<TreeMeta | null>(null)

const ctxOptions = computed(() => {
  const m = ctxNode.value
  if (!m) return []
  if (m.kind === 'table') {
    return [
      { label: 'Browse data', key: 'browse' },
      { label: 'View structure', key: 'structure' },
      { label: 'Refresh columns', key: 'refresh-cols' },
    ]
  }
  if (m.kind === 'view') {
    return [{ label: 'Browse data', key: 'browse' }]
  }
  if (m.kind === 'tableGroup' || m.kind === 'viewGroup') {
    return [{ label: 'Refresh', key: 'refresh-group' }]
  }
  if (m.kind === 'database') {
    return [{ label: 'Refresh', key: 'refresh-db' }]
  }
  return []
})

function onContextMenu(event: MouseEvent, node: TreeOption) {
  event.preventDefault()
  ctxNode.value = (node as any).extra as TreeMeta
  ctxX.value = event.clientX
  ctxY.value = event.clientY
  ctxOpen.value = false
  requestAnimationFrame(() => (ctxOpen.value = true))
}

async function onCtxSelect(key: string) {
  ctxOpen.value = false
  const m = ctxNode.value
  if (!m) return
  switch (key) {
    case 'browse':
      if (m.db && m.table) emit('open-data', { db: m.db, table: m.table })
      break
    case 'structure':
      if (m.db && m.table) emit('open-structure', { db: m.db, table: m.table })
      break
    case 'refresh-cols':
      if (m.db && m.table) await store.ensureColumns(props.connection.id, m.db, m.table, true)
      break
    case 'refresh-group':
      if (m.kind === 'tableGroup' && m.db) await store.ensureTables(props.connection.id, m.db, true)
      // re-trigger load by clearing children handled via NTree's load callback
      break
    case 'refresh-db':
      await store.ensureDatabases(props.connection.id, true)
      await loadRoot()
      break
  }
}

function onDblclick(_: MouseEvent, node: TreeOption) {
  const m = (node as any).extra as TreeMeta
  if (m.kind === 'table' || m.kind === 'view') {
    emit('open-data', { db: m.db!, table: m.table! })
  }
}

const nodeProps = ({ option }: { option: TreeOption }) => ({
  onContextmenu: (e: MouseEvent) => onContextMenu(e, option),
  onDblclick: (e: MouseEvent) => onDblclick(e, option),
})
</script>

<template>
  <div class="tree-pane">
    <div class="header">
      <span class="title">{{ connection.name }}</span>
    </div>
    <n-scrollbar class="body">
      <n-spin :show="loading">
        <n-tree
          block-line
          virtual-scroll
          :data="treeData"
          :on-load="onLoad"
          :node-props="nodeProps"
          :style="{ height: '100%' }"
          :indent="14"
        />
      </n-spin>
    </n-scrollbar>
    <n-dropdown
      placement="bottom-start"
      trigger="manual"
      size="small"
      :show="ctxOpen"
      :x="ctxX"
      :y="ctxY"
      :options="ctxOptions"
      @select="onCtxSelect"
      @clickoutside="ctxOpen = false"
    />
  </div>
</template>

<style scoped>
.tree-pane { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.header {
  display: flex;
  align-items: center;
  padding: 6px 10px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  opacity: 0.7;
  border-bottom: 1px solid var(--n-border-color);
}
.body { flex: 1 1 auto; min-height: 0; padding: 6px; }
.body :deep(.n-tree-node-content) { font-size: 12px; }
</style>
