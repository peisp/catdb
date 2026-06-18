<script setup lang="ts">
// QueryWorkspace — per-connection tab container. Each tab is one of:
//   - query           (SQL editor + result table)
//   - table           (TableBrowser)
//   - structure       (TableStructure)
//   - tables-overview (TablesOverview)
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { NTabPane, NTabs } from 'naive-ui'
import QueryTab from './QueryTab.vue'
import TableBrowser from './TableBrowser.vue'
import TableStructure from './TableStructure.vue'
import TablesOverview from './TablesOverview.vue'
import type { ConnectionProfile } from '../../api/connections'
import type { QueryTab as QueryTabInfo } from '../../stores/query'
import { useQueryStore } from '../../stores/query'
import { setActiveTabContext } from '../../api/tabContextMenu'

const props = defineProps<{
  connection: ConnectionProfile
  tabCommand?: { tabId: string; cmd: string; nonce: number } | null
}>()
const store = useQueryStore()

const tabs = computed(() => store.tabsForConn(props.connection.id))
const activeId = computed({
  get() {
    return store.activeTab(props.connection.id)?.id ?? ''
  },
  set(v: string) {
    store.setActive(props.connection.id, v)
  },
})

function ensureTab() {
  // Per-connection pinned overview tab is always the first/default tab.
  store.ensureOverviewTab(props.connection.id)
}

onMounted(ensureTab)
watch(() => props.connection.id, ensureTab)

function addTab() {
  const n = tabs.value.filter((t) => t.kind === 'query').length + 1
  store.addTab(props.connection.id, { title: `Query ${n}`, kind: 'query' })
}

async function closeTab(id: string) {
  await store.closeTab(id)
  // The pinned overview tab is always present, so we never end up with 0 tabs.
}

const tabsRef = ref<InstanceType<typeof NTabs> | null>(null)
const wsRef = ref<HTMLElement | null>(null)

// 当 active tab 变化时，如果 tab 在可视区外则自动滚到可视区
watch(activeId, () => {
  nextTick(() => {
    const el = tabsRef.value?.$el as HTMLElement | undefined
    if (!el) return
    const tab = el.querySelector('.n-tabs-tab--active') as HTMLElement | null
    if (tab) {
      tab.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    }
  })
})

// --- 原生右键菜单 ---
function openCtx(e: MouseEvent, tab: QueryTabInfo) {
  e.preventDefault()

  // 固定（pinned）的 tab 不展示右键菜单 —— 不可关闭。
  if (tab.pinned) {
    wsRef.value?.style.removeProperty('--custom-contextmenu')
    return
  }

  setActiveTabContext(tab.id, tab.connId)

  // 在「可关闭」tab 集合内判定位置（忽略固定 tab）
  const closable = store.tabsForConn(tab.connId).filter((t) => !t.pinned)
  const idx = closable.findIndex((t) => t.id === tab.id)
  let menuName = 'catdb-tab'
  if (closable.length <= 1) {
    menuName = 'catdb-tab-only'
  } else if (idx <= 0) {
    menuName = 'catdb-tab-first'
  } else if (idx >= closable.length - 1) {
    menuName = 'catdb-tab-last'
  }
  wsRef.value?.style.setProperty('--custom-contextmenu', menuName)
}
</script>

<template>
  <div ref="wsRef" class="ws">
    <n-tabs
      ref="tabsRef"
      v-model:value="activeId"
      type="card"
      closable
      addable
      size="small"
      tab-style="min-width: 80px;"
      pane-class="ws-pane"
      pane-wrapper-class="ws-pane-wrapper"
      @close="closeTab"
      @add="addTab"
    >
      <n-tab-pane
        v-for="t in tabs"
        :key="t.id"
        :name="t.id"
        :closable="!t.pinned"
        display-directive="show:lazy"
      >
        <template #tab>
          <span class="tab-label" @contextmenu.prevent="openCtx($event, t)">{{ t.title }}</span>
        </template>
        <QueryTab
          v-if="t.kind === 'query'"
          :tab-id="t.id"
          :driver="connection.driver"
          :command="tabCommand && tabCommand.tabId === t.id ? tabCommand : null"
        />
        <TableBrowser
          v-else-if="t.kind === 'table' && t.db && t.table"
          :conn-id="t.connId"
          :db="t.db"
          :table="t.table"
        />
        <TableStructure
          v-else-if="t.kind === 'structure' && t.db && t.table"
          :conn-id="t.connId"
          :db="t.db"
          :table="t.table"
        />
        <TablesOverview
          v-else-if="t.kind === 'tables-overview'"
          :conn-id="t.connId"
          :db="t.db ?? ''"
        />
      </n-tab-pane>
    </n-tabs>
  </div>
</template>

<style scoped>
/* The chain below uses `flex: 1 1 0` (basis: 0) — NOT `flex: 1 1 auto`.
   With basis: auto in a column flex container, the basis becomes the
   intrinsic content height; a tall result table would then propagate up
   through `.n-tabs-pane-wrapper` (which Naive UI ships without an
   explicit height) and push the tab body out of the viewport. Basis: 0
   means the slot's height is determined ENTIRELY by grow distribution
   against the definite parent height — content size has no influence. */
.ws { display: flex; flex-direction: column; height: 100%; min-width: 0; min-height: 0; overflow: hidden; }
.ws :deep(.n-tabs) {
  flex: 1 1 0;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.ws :deep(.n-tabs-tab-pad), .ws :deep(.n-tabs-tab) { padding-top: 4px; padding-bottom: 4px; }
.ws :deep(.n-tabs-tab) { padding-left: 8px; }
.ws :deep(.n-tabs-nav) { flex: 0 0 auto; padding: 6px;}
/* Pane wrapper is the actual culprit when broken — give it explicit
   flex: 1 1 0 so the wrapper has a definite height equal to (n-tabs height
   - nav height). With overflow: hidden anything taller inside is clipped. */
.ws :deep(.ws-pane-wrapper) {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  position: relative;
}
.ws :deep(.ws-pane) {
  display: flex;
  min-width: 0;
  min-height: 0;
  padding: 0;
  height: 100%;
  overflow: hidden;
}
.ws :deep(.ws-pane > *) { flex: 1 1 0; min-width: 0; min-height: 0; }
</style>
