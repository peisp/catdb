// stores/connections — single source of truth for connection profiles +
// groups + live state. Components read from here via getters and trigger
// mutations through the actions; they never call the api/ layer directly when
// reactive state is involved.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { connections as connectionsApi } from '../api'
import type {
  ConnectionDraft,
  ConnectionProfile,
  DriverInfo,
  Group,
} from '../api/connections'

export const useConnectionsStore = defineStore('connections', () => {
  const drivers = ref<DriverInfo[]>([])
  const connections = ref<ConnectionProfile[]>([])
  const groups = ref<Group[]>([])
  const liveIds = ref<Set<string>>(new Set())
  const loading = ref(false)

  const driverByName = computed(() => {
    const m = new Map<string, DriverInfo>()
    for (const d of drivers.value) m.set(d.name, d)
    return m
  })

  async function refreshDrivers() {
    drivers.value = await connectionsApi.listDrivers()
  }

  async function refreshAll() {
    loading.value = true
    try {
      const [conns, grps, ids, drvs] = await Promise.all([
        connectionsApi.listConnections(),
        connectionsApi.listGroups(),
        connectionsApi.connectedIds(),
        drivers.value.length === 0 ? connectionsApi.listDrivers() : Promise.resolve(drivers.value),
      ])
      connections.value = conns ?? []
      groups.value = grps ?? []
      drivers.value = drvs ?? []
      liveIds.value = new Set(ids ?? [])
    } finally {
      loading.value = false
    }
  }

  async function save(draft: ConnectionDraft): Promise<ConnectionProfile> {
    const saved = await connectionsApi.saveConnection(draft)
    const idx = connections.value.findIndex((c) => c.id === saved.id)
    if (idx >= 0) connections.value.splice(idx, 1, saved)
    else connections.value.push(saved)
    return saved
  }

  async function remove(id: string) {
    await connectionsApi.deleteConnection(id)
    connections.value = connections.value.filter((c) => c.id !== id)
    liveIds.value.delete(id)
  }

  async function test(draft: ConnectionDraft, signal?: AbortSignal) {
    return connectionsApi.testConnection(draft, signal)
  }

  async function connect(id: string) {
    await connectionsApi.connect(id)
    liveIds.value = new Set([...liveIds.value, id])
  }

  async function disconnect(id: string) {
    await connectionsApi.disconnect(id)
    const next = new Set(liveIds.value)
    next.delete(id)
    liveIds.value = next
  }

  function isLive(id: string): boolean {
    return liveIds.value.has(id)
  }

  return {
    drivers,
    connections,
    groups,
    liveIds,
    loading,
    driverByName,
    refreshDrivers,
    refreshAll,
    save,
    remove,
    test,
    connect,
    disconnect,
    isLive,
  }
})
