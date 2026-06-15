<script setup lang="ts">
// StatusBar — bottom strip (UI_SPEC.md §3). Only reacts to connection state
// changes (connect/disconnect/switch). Shows connection name, connection
// status, server version/user, theme mode, and build tag.
import { computed, ref, watch } from 'vue'
import { connections as connectionsApi } from '../api'
import { useConnectionsStore } from '../stores/connections'
import { useThemeStore } from '../stores/theme'
import type { ServerInfo } from '../api/connections'

const conns = useConnectionsStore()
const theme = useThemeStore()

const serverInfo = ref<ServerInfo | null>(null)

const liveConn = computed(() => {
  // Reach the first live connection — for M4 the workspace only ever shows
  // tabs from the active connection, so this is a reasonable proxy.
  const connId = conns.liveIds.values().next().value
  if (!connId) return null
  return conns.connections.find((c) => c.id === connId) ?? null
})

// Fetch server info when the active connection changes.
watch(liveConn, async (conn) => {
  if (!conn) { serverInfo.value = null; return }
  try {
    serverInfo.value = await connectionsApi.getServerInfo(conn.id)
  } catch {
    // connection may not be fully live yet, that's fine
    serverInfo.value = null
  }
}, { immediate: true })

const mode = computed(() => (theme.mode === 'dark' ? 'Dark' : 'Light'))
</script>

<template>
  <div class="bar">
    <span class="slot mono">{{ liveConn ? liveConn.name : 'No connection' }}</span>
    <span class="sep" />
    <span class="slot">{{ liveConn ? 'Connected' : 'Disconnected' }}</span>
    <span v-if="serverInfo" class="sep" />
    <span v-if="serverInfo" class="slot mono">{{ serverInfo.version }}</span>
    <span v-if="serverInfo" class="sep" />
    <span v-if="serverInfo" class="slot mono">{{ serverInfo.user }}</span>
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
  padding: 0 18px;
  gap: 8px;
  opacity: 0.9;
}
.slot { white-space: nowrap; }
.sep { width: 1px; height: 12px; background: currentColor; opacity: 0.15; }
.grow { flex: 1 1 auto; }
</style>
