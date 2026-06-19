// stores/metadata — per-connection metadata cache.
//
// Used by ObjectTree (lazy-load DB → tables → columns), by TableStructure,
// and by the autocomplete CompletionSource (which calls snapshotFor).
//
// We cache:
//   - databases[connId]      — string list, refreshed on demand
//   - tables[connId][db]     — TableInfo[] (also a flag for "loaded")
//   - columns[connId][db][table] — ColumnMeta[]
//   - snapshot[connId][db]   — full autocomplete map for CodeMirror
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { metadata as metaApi } from '../api'
import type {
  AutocompleteSnapshot,
  ColumnMeta,
  TableInfo,
} from '../api/metadata'

type DbName = string
type TableName = string

export const useMetadataStore = defineStore('metadata', () => {
  const databases = ref<Record<string, string[]>>({})
  const tables = ref<Record<string, Record<DbName, TableInfo[]>>>({})
  const columns = ref<Record<string, Record<DbName, Record<TableName, ColumnMeta[]>>>>({})
  const snapshots = ref<Record<string, Record<DbName, AutocompleteSnapshot>>>({})

  async function ensureDatabases(connId: string, force = false): Promise<string[]> {
    if (!force && databases.value[connId]) return databases.value[connId]
    const list = await metaApi.listDatabases(connId)
    databases.value = { ...databases.value, [connId]: list ?? [] }
    return list ?? []
  }

  async function ensureTables(connId: string, db: string, force = false): Promise<TableInfo[]> {
    if (!force && tables.value[connId]?.[db]) return tables.value[connId][db]
    const list = await metaApi.listTables(connId, db)
    const byConn = { ...(tables.value[connId] ?? {}), [db]: list ?? [] }
    tables.value = { ...tables.value, [connId]: byConn }
    return list ?? []
  }

  async function ensureColumns(connId: string, db: string, table: string, force = false): Promise<ColumnMeta[]> {
    if (!force && columns.value[connId]?.[db]?.[table]) {
      return columns.value[connId][db][table]
    }
    const list = await metaApi.listColumns(connId, db, table)
    const byDb = { ...(columns.value[connId]?.[db] ?? {}), [table]: list ?? [] }
    const byConn = { ...(columns.value[connId] ?? {}), [db]: byDb }
    columns.value = { ...columns.value, [connId]: byConn }
    return list ?? []
  }

  async function ensureSnapshot(connId: string, db: string, force = false): Promise<AutocompleteSnapshot> {
    if (!force && snapshots.value[connId]?.[db]) {
      return snapshots.value[connId][db]
    }
    const snap = await metaApi.autocompleteFor(connId, db)
    const byConn = { ...(snapshots.value[connId] ?? {}), [db]: snap }
    snapshots.value = { ...snapshots.value, [connId]: byConn }
    return snap
  }

  function invalidate(connId: string) {
    delete databases.value[connId]
    delete tables.value[connId]
    delete columns.value[connId]
    delete snapshots.value[connId]
  }

  /** Drop the cached table list for one db so the next ensureTables() refetches. */
  function invalidateTables(connId: string, db: string) {
    const byConn = tables.value[connId]
    if (byConn && byConn[db]) {
      const next = { ...byConn }
      delete next[db]
      tables.value = { ...tables.value, [connId]: next }
    }
    // The autocomplete snapshot mirrors the table list — drop it too.
    const snapByConn = snapshots.value[connId]
    if (snapByConn && snapByConn[db]) {
      const next = { ...snapByConn }
      delete next[db]
      snapshots.value = { ...snapshots.value, [connId]: next }
    }
  }

  function snapshotFor(connId: string, db: string): AutocompleteSnapshot | undefined {
    return snapshots.value[connId]?.[db]
  }

  const totalCached = computed(() => Object.keys(databases.value).length)

  return {
    databases,
    tables,
    columns,
    snapshots,
    totalCached,
    ensureDatabases,
    ensureTables,
    ensureColumns,
    ensureSnapshot,
    invalidate,
    invalidateTables,
    snapshotFor,
  }
})
