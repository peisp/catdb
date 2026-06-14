<script setup lang="ts">
// StatusBar — bottom strip (UI_SPEC.md §3). Shows live data drawn from the
// query store + connections store: active connection name, active tab's
// rows / elapsed / kind, and a build tag on the right. Cursor position
// requires deeper CodeMirror state hooks and is left for a later polish.
import { computed } from 'vue'
import { useConnectionsStore } from '../stores/connections'
import { useQueryStore } from '../stores/query'
import { useThemeStore } from '../stores/theme'

const conns = useConnectionsStore()
const tabs = useQueryStore()
const theme = useThemeStore()

const liveConn = computed(() => {
  // Reach the first live connection — for M4 the workspace only ever shows
  // tabs from the active connection, so this is a reasonable proxy.
  const id = Array.from(tabs.activeByConn ? Object.keys(tabs.activeByConn) : [])[0]
  if (!id) return null
  return conns.connections.find((c) => c.id === id) ?? null
})

const activeTab = computed(() => {
  if (!liveConn.value) return null
  return tabs.activeTab(liveConn.value.id) ?? null
})

const rowsLabel = computed(() => {
  const t = activeTab.value
  if (!t) return ''
  if (t.kind !== 'query') return ''
  if (!t.isResultSet) {
    if (t.execAffected !== null) return `${t.execAffected} affected`
    return ''
  }
  return `${t.rowsTotal} rows`
})

const elapsedLabel = computed(() => {
  const t = activeTab.value
  if (!t || t.elapsedMs <= 0) return ''
  return `${t.elapsedMs} ms`
})

const statusLabel = computed(() => {
  const t = activeTab.value
  if (!t) return 'Idle'
  switch (t.status) {
    case 'running': return 'Running…'
    case 'done': return t.truncated ? 'Done (truncated)' : 'Done'
    case 'error': return 'Error'
    case 'canceled': return 'Canceled'
    default: return 'Idle'
  }
})

const mode = computed(() => (theme.mode === 'dark' ? 'Dark' : 'Light'))
</script>

<template>
  <div class="bar">
    <span class="slot mono">{{ liveConn ? liveConn.name : 'No connection' }}</span>
    <span class="sep" />
    <span class="slot">{{ statusLabel }}</span>
    <span v-if="rowsLabel" class="sep" />
    <span v-if="rowsLabel" class="slot mono">{{ rowsLabel }}</span>
    <span v-if="elapsedLabel" class="sep" />
    <span v-if="elapsedLabel" class="slot mono">{{ elapsedLabel }}</span>
    <span class="grow" />
    <span class="slot mono">{{ mode }}</span>
    <span class="sep" />
    <span class="slot mono">catdb v0.1.0</span>
  </div>
</template>

<style scoped>
.bar {
  display: flex;
  align-items: center;
  height: 22px;
  font-size: 11px;
  padding: 0 10px;
  gap: 8px;
  opacity: 0.9;
}
.slot { white-space: nowrap; }
.sep { width: 1px; height: 12px; background: currentColor; opacity: 0.15; }
.grow { flex: 1 1 auto; }
</style>
