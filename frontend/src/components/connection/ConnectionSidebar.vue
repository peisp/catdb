<script setup lang="ts">
// ConnectionSidebar — grouped list of saved connections + entry point for
// "new connection". The native context menu (right-click on a node) lands
// with M4; for now we use Naive UI's Dropdown for actions.
import { computed, onMounted, ref } from 'vue'
import {
  NButton,
  NDropdown,
  NIcon,
  NScrollbar,
  NSpin,
  useDialog,
  useMessage,
} from 'naive-ui'
import type { ConnectionProfile, DriverInfo } from '../../api/connections'
import { useConnectionsStore } from '../../stores/connections'

const emit = defineEmits<{
  (e: 'select', conn: ConnectionProfile): void
  (e: 'new', driver: DriverInfo): void
  (e: 'edit', conn: ConnectionProfile): void
}>()

const store = useConnectionsStore()
const message = useMessage()
const dialog = useDialog()

onMounted(() => {
  void store.refreshAll()
})

const grouped = computed(() => {
  const byGroup = new Map<string, ConnectionProfile[]>()
  byGroup.set('__ungrouped__', [])
  for (const g of store.groups) byGroup.set(g.id, [])
  for (const c of store.connections) {
    const key = c.groupId && byGroup.has(c.groupId) ? c.groupId : '__ungrouped__'
    byGroup.get(key)!.push(c)
  }
  return Array.from(byGroup.entries()).map(([id, items]) => ({
    id,
    label: id === '__ungrouped__' ? '未分组' : store.groups.find((g) => g.id === id)?.name ?? id,
    items,
  }))
})

const ctxNode = ref<ConnectionProfile | null>(null)
const ctxX = ref(0)
const ctxY = ref(0)
const ctxOpen = ref(false)
const ctxOptions = [
  { label: '打开连接', key: 'connect' },
  { label: '断开连接', key: 'disconnect' },
  { label: '编辑', key: 'edit' },
  { type: 'divider' as const, key: 'd' },
  { label: '删除', key: 'delete' },
]

function onCtx(ev: MouseEvent, conn: ConnectionProfile) {
  ev.preventDefault()
  ctxNode.value = conn
  ctxX.value = ev.clientX
  ctxY.value = ev.clientY
  ctxOpen.value = false
  // Force re-position by toggling on next tick.
  requestAnimationFrame(() => (ctxOpen.value = true))
}

async function onCtxSelect(key: string) {
  ctxOpen.value = false
  const node = ctxNode.value
  if (!node) return
  switch (key) {
    case 'connect':
      try {
        await store.connect(node.id)
        message.success(`已连接 ${node.name}`)
        emit('select', node)
      } catch (e) {
        message.error(`连接失败: ${String(e)}`)
      }
      break
    case 'disconnect':
      try {
        await store.disconnect(node.id)
        message.info(`已断开 ${node.name}`)
      } catch (e) {
        message.error(String(e))
      }
      break
    case 'edit':
      emit('edit', node)
      break
    case 'delete':
      dialog.warning({
        title: '删除连接',
        content: `确定要删除 "${node.name}" 吗？此操作不可撤销。`,
        positiveText: '删除',
        negativeText: '取消',
        onPositiveClick: async () => {
          try {
            await store.remove(node.id)
            message.success('已删除')
          } catch (e) {
            message.error(String(e))
          }
        },
      })
      break
  }
}

const driverMenuOpen = ref(false)
const driverOptions = computed(() =>
  store.drivers.map((d) => ({ label: d.name, key: d.name })),
)
function onNewDriver(key: string) {
  driverMenuOpen.value = false
  const d = store.drivers.find((dd) => dd.name === key)
  if (d) emit('new', d)
}

async function onDoubleClick(conn: ConnectionProfile) {
  if (store.isLive(conn.id)) {
    emit('select', conn)
    return
  }
  try {
    await store.connect(conn.id)
    emit('select', conn)
  } catch (e) {
    message.error(`连接失败: ${String(e)}`)
  }
}
</script>

<template>
  <div class="sidebar">
    <div class="header">
      <span class="title">Connections</span>
      <n-dropdown
        :options="driverOptions"
        :show="driverMenuOpen"
        @select="onNewDriver"
        @clickoutside="driverMenuOpen = false"
        size="small"
      >
        <n-button
          size="tiny"
          quaternary
          @click="driverMenuOpen = !driverMenuOpen"
          :disabled="!store.drivers.length"
        >
          +
        </n-button>
      </n-dropdown>
    </div>
    <n-scrollbar class="list">
      <n-spin :show="store.loading">
        <div v-for="g in grouped" :key="g.id" class="group">
          <div class="group-label">{{ g.label }}</div>
          <div v-if="g.items.length === 0" class="group-empty">空</div>
          <div
            v-for="c in g.items"
            :key="c.id"
            class="row clickable"
            @dblclick="onDoubleClick(c)"
            @contextmenu="onCtx($event, c)"
          >
            <span class="dot" :class="{ live: store.isLive(c.id) }" />
            <span class="row-name">{{ c.name }}</span>
            <span class="row-driver mono">{{ c.driver }}</span>
          </div>
        </div>
      </n-spin>
    </n-scrollbar>
    <n-dropdown
      placement="bottom-start"
      trigger="manual"
      :show="ctxOpen"
      :x="ctxX"
      :y="ctxY"
      :options="ctxOptions"
      @select="onCtxSelect"
      @clickoutside="ctxOpen = false"
      size="small"
    />
  </div>
</template>

<style scoped>
.sidebar { display: flex; flex-direction: column; height: 100%; }
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  opacity: 0.7;
  border-bottom: 1px solid var(--n-border-color);
}
.title { font-size: 11px; }
.list { flex: 1 1 auto; }
.group { padding: 4px 0; }
.group-label {
  font-size: 11px;
  padding: 4px 10px 2px;
  opacity: 0.55;
}
.group-empty {
  padding: 2px 10px 6px 22px;
  font-size: 11px;
  opacity: 0.4;
}
.row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px 4px 14px;
  font-size: 12px;
  height: 24px;
  cursor: default;
}
.row:hover { background: var(--n-color-target); }
.row-name { flex: 1 1 auto; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row-driver { font-size: 10px; opacity: 0.5; }
.dot {
  width: 6px; height: 6px; border-radius: 3px;
  background: rgba(127,127,127,0.4);
  flex: 0 0 auto;
}
.dot.live { background: #18a058; }
</style>
