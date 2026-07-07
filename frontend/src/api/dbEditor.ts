// dbEditor — helpers behind the "新建/编辑数据库" child window.
//
// The form itself lives in DatabaseEditorWindow.vue (a native Wails child
// window opened via SystemService.OpenDatabaseEditor, mirroring the
// new-connection flow). This module owns:
//
//   - charset / collation listing (cached per-connection)
//   - per-database options lookup (used in edit mode)
//   - CREATE / ALTER DATABASE DDL rendering
//
// All of it goes through MetadataService, which probes the driver's optional
// DatabaseEditor extension — drivers without it reject with the stable
// "database-editor-unsupported" slug and the window hides the option fields.
// The charset list is small and stable per server, so we cache it
// per-connection until the connection is invalidated.
import {
  buildAlterDatabase,
  buildCreateDatabase,
  getDatabaseOptions,
  listCharsets,
  type DatabaseOptions,
} from './metadata'

// ---- charset / collation metadata (per-connection cache) -------------------

export interface CharsetInfo {
  name: string
  defaultCollation: string
}

export interface CollationInfo {
  name: string
  charset: string
}

interface CharsetCacheEntry {
  charsets: CharsetInfo[]
  collations: CollationInfo[]
}

const charsetCache: Record<string, CharsetCacheEntry> = {}

export async function loadCharsetsAndCollations(connId: string): Promise<CharsetCacheEntry> {
  const cached = charsetCache[connId]
  if (cached) return cached

  const catalog = await listCharsets(connId)
  const entry: CharsetCacheEntry = {
    charsets: (catalog.charsets ?? []).map((c) => ({
      name: c.name,
      defaultCollation: c.defaultCollation ?? '',
    })),
    collations: (catalog.collations ?? []).map((c) => ({
      name: c.name,
      charset: c.charset ?? '',
    })),
  }
  charsetCache[connId] = entry
  return entry
}

export function invalidateCharsetCache(connId: string) {
  delete charsetCache[connId]
}

// ---- per-db info (charset/collation) -- read for edit mode -----------------

export interface DbInfo {
  charset: string
  collation: string
}

export async function loadDbInfo(connId: string, db: string): Promise<DbInfo | null> {
  try {
    const opts = await getDatabaseOptions(connId, db)
    return { charset: opts.charset ?? '', collation: opts.collation ?? '' }
  } catch {
    return null
  }
}

// ---- DDL rendering (driver-side) --------------------------------------------

export function buildCreateDb(connId: string, name: string, charset: string, collation: string): Promise<string> {
  return buildCreateDatabase(connId, name, { charset, collation } satisfies DatabaseOptions)
}

export function buildAlterDb(connId: string, name: string, charset: string, collation: string): Promise<string> {
  return buildAlterDatabase(connId, name, { charset, collation } satisfies DatabaseOptions)
}
