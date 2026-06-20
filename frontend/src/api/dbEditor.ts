// dbEditor — helpers behind the "新建/编辑数据库" child window.
//
// The form itself lives in DatabaseEditorWindow.vue (a native Wails child
// window opened via SystemService.OpenDatabaseEditor, mirroring the
// new-connection flow). This module owns:
//
//   - charset / collation listing (cached per-connection)
//   - per-database charset/collation lookup (used in edit mode)
//   - CREATE / ALTER DATABASE DDL builders
//
// All MySQL work goes through runQuery against `information_schema`; no new
// Service method needed. The charset list is small and stable per server,
// so we cache it per-connection until the connection is invalidated.
import { quoteIdent } from '../lib/alterPlan'
import { runQuery } from './query'

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

  const csRes = await runQuery(
    connId,
    `SELECT CHARACTER_SET_NAME, DEFAULT_COLLATE_NAME
       FROM information_schema.CHARACTER_SETS
       ORDER BY CHARACTER_SET_NAME`,
  )
  const charsets: CharsetInfo[] = (csRes.rows ?? []).map((r) => ({
    name: String(r[0] ?? ''),
    defaultCollation: String(r[1] ?? ''),
  }))

  const coRes = await runQuery(
    connId,
    `SELECT COLLATION_NAME, CHARACTER_SET_NAME
       FROM information_schema.COLLATIONS
       WHERE COLLATION_NAME IS NOT NULL
       ORDER BY CHARACTER_SET_NAME, COLLATION_NAME`,
  )
  const collations: CollationInfo[] = (coRes.rows ?? []).map((r) => ({
    name: String(r[0] ?? ''),
    charset: String(r[1] ?? ''),
  }))

  const entry: CharsetCacheEntry = { charsets, collations }
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

/** SQL-escape a string literal value (single quotes doubled, backslash kept). */
function escapeStringLiteral(s: string): string {
  return s.replace(/\\/g, '\\\\').replace(/'/g, "''")
}

export async function loadDbInfo(connId: string, db: string): Promise<DbInfo | null> {
  const res = await runQuery(
    connId,
    `SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME
       FROM information_schema.SCHEMATA
       WHERE SCHEMA_NAME = '${escapeStringLiteral(db)}'`,
  )
  if (!res.rows || res.rows.length === 0) return null
  const r = res.rows[0]
  return { charset: String(r[0] ?? ''), collation: String(r[1] ?? '') }
}

// ---- DDL builders ----------------------------------------------------------
//
// MySQL accepts unquoted bareword for CHARACTER SET / COLLATE names. Since
// these are picked from a fixed server-provided list, not user free-text,
// emitting them bare is safe — and matches what `SHOW CREATE DATABASE`
// returns.

export function buildCreateDb(name: string, charset: string, collation: string): string {
  const parts: string[] = [`CREATE DATABASE ${quoteIdent(name)}`]
  if (charset) parts.push(`CHARACTER SET = ${charset}`)
  if (collation) parts.push(`COLLATE = ${collation}`)
  return parts.join(' ')
}

export function buildAlterDb(name: string, charset: string, collation: string): string {
  const parts: string[] = [`ALTER DATABASE ${quoteIdent(name)}`]
  if (charset) parts.push(`CHARACTER SET = ${charset}`)
  if (collation) parts.push(`COLLATE = ${collation}`)
  return parts.join(' ')
}
