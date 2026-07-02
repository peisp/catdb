<script setup lang="ts">
// QueryWorkspace — per-connection tab container. Each tab is one of:
//   - query           (SQL editor + result table)
//   - table           (TableBrowser)
//   - structure       (TableStructure)
//   - tables-overview (TablesOverview)
//
// The strip itself lives in WorkspaceTabBar; this component renders the panes
// with lazy-mount + keep-alive-via-v-show (same semantics as the old n-tabs
// display-directive="show:lazy").
import { computed, onMounted, ref, watch } from 'vue'
import WorkspaceTabBar from './WorkspaceTabBar.vue'
import QueryTab from './QueryTab.vue'
import TableBrowser from './TableBrowser.vue'
import TableStructure from './TableStructure.vue'
import TablesOverview from './TablesOverview.vue'
import type { ConnectionProfile } from '../../api/connections'
import { useQueryStore } from '../../stores/query'

const props = defineProps<{
  connection: ConnectionProfile
  tabCommand?: { tabId: string; cmd: string; nonce: number } | null
}>()
const store = useQueryStore()

const tabs = computed(() => store.tabsForConn(props.connection.id))
const activeId = computed(() => store.activeTab(props.connection.id)?.id ?? '')

// Mount a pane on first activation, then keep it alive behind v-show so tab
// switches preserve editor/grid state. Stale ids after close are harmless —
// the v-for only iterates live tabs.
const mountedIds = ref(new Set<string>())
watch(
  activeId,
  (id) => {
    if (id) mountedIds.value.add(id)
  },
  { immediate: true },
)

function ensureTab() {
  // Per-connection pinned overview tab is always the first/default tab.
  store.ensureOverviewTab(props.connection.id)
}

onMounted(ensureTab)
watch(() => props.connection.id, ensureTab)
</script>

<template>
  <div class="ws">
    <WorkspaceTabBar :conn-id="connection.id" />
    <div class="ws-panes">
      <template v-for="t in tabs" :key="t.id">
        <div v-if="mountedIds.has(t.id)" v-show="t.id === activeId" class="ws-pane">
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
          <TableStructure
            v-else-if="t.kind === 'new-table' && t.db"
            :conn-id="t.connId"
            :db="t.db"
            :table="t.table ?? ''"
            mode="new"
            :tab-id="t.id"
          />
          <TablesOverview
            v-else-if="t.kind === 'tables-overview'"
            :conn-id="t.connId"
            :db="t.db ?? ''"
          />
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.ws {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
/* flex-basis 0 (not auto): pane height must come purely from grow
   distribution, never from intrinsic content height, or a tall result
   table would push the workspace out of the viewport. */
.ws-panes {
  display: flex;
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.ws-pane {
  display: flex;
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.ws-pane > * {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
}
</style>
