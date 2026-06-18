<script setup lang="ts">
// ObjectTree — per-connection database/table/column tree. Lazy loads each
// level via MetadataService. Single-click a database node → open the
// tables overview tab. Double-click any non-leaf node → expand/collapse
// it. Right-click → action menu (M4 will replace this with the native
// Wails context menu).
import { computed, ref, watch } from 'vue'
import {
  NButton,
  NDropdown,
  NIcon,
  NScrollbar,
  NSpin,
  NTree,
  useMessage,
} from 'naive-ui'
import type { TreeOption } from 'naive-ui'
import type { ConnectionProfile } from '../../api/connections'
import { metadata as metaApi } from '../../api'
import { useMetadataStore } from '../../stores/metadata'
import { useConnectionsStore } from '../../stores/connections'

const props = defineProps<{ connection: ConnectionProfile }>()
const emit = defineEmits<{
  (e: 'open-data', payload: { db: string; table: string }): void
  (e: 'open-structure', payload: { db: string; table: string }): void
  (e: 'open-tables-overview', payload: { db: string }): void
}>()

const store = useMetadataStore()
const connStore = useConnectionsStore()
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

async function onLoad(node: TreeOption): Promise<boolean> {
  const meta = (node as any).extra as TreeMeta
  try {
    if (meta.kind === 'database') {
      // Show Tables + Views groups under the database.
      node.children = [
        mkNode('Tables', { kind: 'tableGroup', db: meta.db }),
        mkNode('Views', { kind: 'viewGroup', db: meta.db }),
      ]
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
  // 表/视图 → 打开数据浏览
  if (m.kind === 'table' || m.kind === 'view') {
    if (m.db && m.table) emit('open-data', { db: m.db, table: m.table })
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

// --- header actions: disconnect / reconnect / refresh ---

async function onDisconnect() {
  if (busy.value || !isLive.value) return
  busy.value = true
  try {
    await connStore.disconnect(props.connection.id)
    // Drop cached metadata so a future reconnect refetches cleanly.
    store.invalidate(props.connection.id)
    treeData.value = []
    expandedKeys.value = []
  } catch (e) {
    message.error(`断开失败: ${String(e)}`)
  } finally {
    busy.value = false
  }
}

async function onReconnect() {
  if (busy.value) return
  busy.value = true
  try {
    if (isLive.value) await connStore.disconnect(props.connection.id)
    await connStore.connect(props.connection.id)
    store.invalidate(props.connection.id)
    await loadRoot()
  } catch (e) {
    message.error(`重连失败: ${String(e)}`)
  } finally {
    busy.value = false
  }
}

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
</script>

<template>
  <div class="tree-pane">
    <div class="header">
      <span class="status-dot" :class="{ live: isLive }" />
      <span class="title">{{ connection.name }}</span>
      <span class="spacer" />
      <div class="actions">
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
        <n-button
          class="hbtn"
          size="tiny"
          quaternary
          :disabled="busy"
          :title="isLive ? '重新连接（先断开再连接）' : '连接'"
          @click="onReconnect"
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
            <path d="M7.5 9.5l-1 1a2.5 2.5 0 1 1-3.5-3.5l1-1" />
            <path d="M8.5 6.5l1-1a2.5 2.5 0 1 1 3.5 3.5l-1 1" />
            <line x1="6.5" y1="9.5" x2="9.5" y2="6.5" />
          </svg>
        </n-button>
        <n-button
          class="hbtn"
          size="tiny"
          quaternary
          :disabled="busy || !isLive"
          :title="isLive ? '断开连接' : '未连接'"
          @click="onDisconnect"
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
            <path d="M6.5 9l-1.5 1.5a2.5 2.5 0 0 1-3.5-3.5L3 5.5" />
            <path d="M9.5 7L11 5.5a2.5 2.5 0 0 1 3.5 3.5L13 10.5" />
            <line x1="2" y1="2" x2="14" y2="14" />
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
