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

  // refreshGroups is the lightweight counterpart of refreshAll — used by the
  // standalone connection-editor window so its form's group dropdown isn't
  // empty (it doesn't need the live connection list, only the groups).
  async function refreshGroups() {
    groups.value = (await connectionsApi.listGroups()) ?? []
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

  // saveGroup upserts a group and keeps the local list in sync. Used by both
  // the connection form (when picking from / creating in the dropdown) and
  // the sidebar's right-click 新建分组 / 重命名 actions.
  async function saveGroup(group: Partial<Group> & { name: string }): Promise<Group> {
    const saved = await connectionsApi.saveGroup(group as Group)
    const idx = groups.value.findIndex((g) => g.id === saved.id)
    if (idx >= 0) groups.value.splice(idx, 1, saved)
    else groups.value.push(saved)
    return saved
  }

  // removeGroup deletes a group (backend refuses non-empty groups with
  // ErrGroupNotEmpty — the sidebar suppresses the 删除 menu when not empty,
  // so this should normally succeed).
  async function removeGroup(id: string) {
    await connectionsApi.deleteGroup(id)
    groups.value = groups.value.filter((g) => g.id !== id)
  }

  // moveConnection reassigns a connection to a different group (or detaches
  // it when groupId is empty). Patches the local list optimistically after
  // the backend confirms — drag-and-drop targets should feel instantaneous.
  async function moveConnection(id: string, groupId: string) {
    await connectionsApi.moveConnection(id, groupId)
    const idx = connections.value.findIndex((c) => c.id === id)
    if (idx >= 0) {
      const c = connections.value[idx]
      // Mutate in place so any computed group buckets re-evaluate cleanly.
      connections.value.splice(idx, 1, { ...c, groupId: groupId || undefined } as ConnectionProfile)
    }
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
    refreshGroups,
    refreshAll,
    save,
    remove,
    test,
    saveGroup,
    removeGroup,
    moveConnection,
    connect,
    disconnect,
    isLive,
  }
})
