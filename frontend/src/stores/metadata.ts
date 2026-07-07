// stores/metadata — per-connection metadata cache.
//
// Used by ObjectTree (lazy-load DB → [schema →] tables → columns), by
// TableStructure, and by the autocomplete CompletionSource (snapshotFor).
//
// We cache:
//   - databases[connId]        — string list, refreshed on demand
//   - schemas[connId][db]      — string list (schema-ful databases only)
//   - tables[connId][ns]       — TableInfo[] (also a flag for "loaded")
//   - columns[connId][ns][table] — ColumnMeta[]
//   - snapshot[connId][ns]     — full autocomplete map for CodeMirror
//
// `ns` is the namespace key: the database name, or db+schema for databases
// with a schema level (see nsKey). All schema params default to '' so the
// schema-less (MySQL) call sites stay unchanged.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { metadata as metaApi } from '../api'
import type {
  AutocompleteSnapshot,
  ColumnMeta,
  TableInfo,
} from '../api/metadata'

type NsName = string
type TableName = string

/** Namespace cache key — db alone, or db+schema (NUL-joined, collision-free). */
function nsKey(db: string, schema = ''): NsName {
  return schema ? `${db}\u0000${schema}` : db
}

export const useMetadataStore = defineStore('metadata', () => {
  const databases = ref<Record<string, string[]>>({})
  const schemas = ref<Record<string, Record<string, string[]>>>({})
  const tables = ref<Record<string, Record<NsName, TableInfo[]>>>({})
  const columns = ref<Record<string, Record<NsName, Record<TableName, ColumnMeta[]>>>>({})
  const snapshots = ref<Record<string, Record<NsName, AutocompleteSnapshot>>>({})

  async function ensureDatabases(connId: string, force = false): Promise<string[]> {
    if (!force && databases.value[connId]) return databases.value[connId]
    const list = await metaApi.listDatabases(connId)
    databases.value = { ...databases.value, [connId]: list ?? [] }
    return list ?? []
  }

  /** Schemas under db — only meaningful when Capabilities.schemas is true. */
  async function ensureSchemas(connId: string, db: string, force = false): Promise<string[]> {
    if (!force && schemas.value[connId]?.[db]) return schemas.value[connId][db]
    const list = await metaApi.listSchemas(connId, db)
    const byConn = { ...(schemas.value[connId] ?? {}), [db]: list ?? [] }
    schemas.value = { ...schemas.value, [connId]: byConn }
    return list ?? []
  }

  async function ensureTables(connId: string, db: string, force = false, schema = ''): Promise<TableInfo[]> {
    const ns = nsKey(db, schema)
    if (!force && tables.value[connId]?.[ns]) return tables.value[connId][ns]
    const list = await metaApi.listTables(connId, db, schema)
    const byConn = { ...(tables.value[connId] ?? {}), [ns]: list ?? [] }
    tables.value = { ...tables.value, [connId]: byConn }
    return list ?? []
  }

  async function ensureColumns(connId: string, db: string, table: string, force = false, schema = ''): Promise<ColumnMeta[]> {
    const ns = nsKey(db, schema)
    if (!force && columns.value[connId]?.[ns]?.[table]) {
      return columns.value[connId][ns][table]
    }
    const list = await metaApi.listColumns(connId, db, table, schema)
    const byNs = { ...(columns.value[connId]?.[ns] ?? {}), [table]: list ?? [] }
    const byConn = { ...(columns.value[connId] ?? {}), [ns]: byNs }
    columns.value = { ...columns.value, [connId]: byConn }
    return list ?? []
  }

  async function ensureSnapshot(connId: string, db: string, force = false, schema = ''): Promise<AutocompleteSnapshot> {
    const ns = nsKey(db, schema)
    if (!force && snapshots.value[connId]?.[ns]) {
      return snapshots.value[connId][ns]
    }
    const snap = await metaApi.autocompleteFor(connId, db, schema)
    const byConn = { ...(snapshots.value[connId] ?? {}), [ns]: snap }
    snapshots.value = { ...snapshots.value, [connId]: byConn }
    return snap
  }

  function invalidate(connId: string) {
    delete databases.value[connId]
    delete schemas.value[connId]
    delete tables.value[connId]
    delete columns.value[connId]
    delete snapshots.value[connId]
  }

  /** Drop the cached column list for a single table (after rename/drop). */
  function invalidateColumns(connId: string, db: string, table: string, schema = '') {
    const ns = nsKey(db, schema)
    const byNs = columns.value[connId]?.[ns]
    if (!byNs || !(table in byNs)) return
    const next = { ...byNs }
    delete next[table]
    const byConn = { ...(columns.value[connId] ?? {}), [ns]: next }
    columns.value = { ...columns.value, [connId]: byConn }
  }

  /** Drop the cached table list for one namespace so the next ensureTables() refetches. */
  function invalidateTables(connId: string, db: string, schema = '') {
    const ns = nsKey(db, schema)
    const byConn = tables.value[connId]
    if (byConn && byConn[ns]) {
      const next = { ...byConn }
      delete next[ns]
      tables.value = { ...tables.value, [connId]: next }
    }
    // The autocomplete snapshot mirrors the table list — drop it too.
    const snapByConn = snapshots.value[connId]
    if (snapByConn && snapByConn[ns]) {
      const next = { ...snapByConn }
      delete next[ns]
      snapshots.value = { ...snapshots.value, [connId]: next }
    }
  }

  function snapshotFor(connId: string, db: string, schema = ''): AutocompleteSnapshot | undefined {
    return snapshots.value[connId]?.[nsKey(db, schema)]
  }

  const totalCached = computed(() => Object.keys(databases.value).length)

  return {
    databases,
    schemas,
    tables,
    columns,
    snapshots,
    totalCached,
    ensureDatabases,
    ensureSchemas,
    ensureTables,
    ensureColumns,
    ensureSnapshot,
    invalidate,
    invalidateTables,
    invalidateColumns,
    snapshotFor,
  }
})
