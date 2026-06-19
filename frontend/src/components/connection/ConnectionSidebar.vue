<script setup lang="ts">
// ConnectionSidebar — grouped list of saved connections with native
// right-click context menu (connect / disconnect / edit / delete).
//
// New-connection action lives on the AppShell's top-drag region; the form
// hosts its own driver-type picker, so the sidebar header stays purely a
// list label.
import { computed, onMounted, ref } from 'vue'
import {
  NScrollbar,
  NSpin,
  useMessage,
} from 'naive-ui'
import type { ConnectionProfile } from '../../api/connections'
import { useConnectionsStore } from '../../stores/connections'
import { setActiveConnectionContext } from '../../api/connectionContextMenu'

const emit = defineEmits<{
  (e: 'select', conn: ConnectionProfile): void
  (e: 'edit', conn: ConnectionProfile): void
}>()

const store = useConnectionsStore()
const message = useMessage()

// Windows frameless: no title bar offset, content starts at the very top.
const isWin = !navigator.platform.includes('Mac')

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

const sidebarRef = ref<HTMLElement | null>(null)

function onCtx(ev: MouseEvent, conn: ConnectionProfile) {
  ev.preventDefault()
  // Set native context menu + push connection identity before menu opens.
  sidebarRef.value?.style.setProperty('--custom-contextmenu', 'catdb-connection')
  setActiveConnectionContext({ connId: conn.id, connName: conn.name })
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

onMounted(() => {
  void store.refreshAll()
  // Listen for edit events from the native context menu handler.
  document.addEventListener('conn:edit', ((e: CustomEvent<string>) => {
    const conn = store.connections.find((c) => c.id === e.detail)
    if (conn) emit('edit', conn)
  }) as EventListener)
  // Listen for connect-then-select events from the native context menu.
  document.addEventListener('conn:select', ((e: CustomEvent<string>) => {
    const conn = store.connections.find((c) => c.id === e.detail)
    if (conn) emit('select', conn)
  }) as EventListener)
})
</script>

<template>
  <div ref="sidebarRef" class="sidebar" :class="{ win: isWin }">
    <div class="header">
      <span class="title">Connections</span>
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
              @contextmenu.prevent="onCtx($event, c)"
          >
            <span class="dot" :class="{ live: store.isLive(c.id) }" />
            <span class="row-name">{{ c.name }}</span>
            <span class="row-driver mono">{{ c.driver }}</span>
          </div>
        </div>
      </n-spin>
    </n-scrollbar>
  </div>
</template>

<style scoped>
.sidebar { display: flex; flex-direction: column; height: 100%; }
.header {
  display: flex;
  align-items: center;
  padding: 6px 10px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  opacity: 0.7;
  border-bottom: var(--n-border-color, rgba(127,127,127,0.2));
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

/* Windows frameless: no top padding on header so content starts flush. */
.sidebar.win .header { padding-top: 18px; }
</style>
