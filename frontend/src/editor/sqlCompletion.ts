// editor/sqlCompletion — the SQL completion engine.
//
// A dbx-style engine that fully OWNS completion (registered via
// `autocompletion({ override })` — lang-sql only provides syntax highlighting).
// One source decides everything from the SqlContext (sqlContext.ts):
//
//   table position  → tables (auto-aliased `users u`) + views + CTEs + databases
//   `x.` qualifier  → columns of the alias/table/CTE, or tables of database x
//                     (other databases load their catalog on demand, async)
//   column position → in-scope columns (deduped, alias-qualified on conflict),
//                     `t.*` expansions, functions, keywords, snippets
//   INSERT (…)      → target-table columns minus the ones already listed
//   UPDATE … SET    → target-table columns minus the ones already assigned
//   JOIN … ON       → suggested join conditions (`o.user_id = u.id`) + columns
//   ORDER/GROUP BY  → SELECT-list aliases + columns
//   `col = `        → type-aware values (NOW() for dates, TRUE/FALSE for bools)
//   FROM tbl ▸      → a generated alias for tbl
//
// Ranking is dbx's model: fuzzy match score (exact > initials > prefix >
// substring > subsequence) + category boost (columns over tables over
// functions over keywords) + a per-session selection-history boost. Results
// use `filter: false`, so the order below IS the popup order.
import type { Completion, CompletionContext, CompletionResult, CompletionSource } from '@codemirror/autocomplete'
import { insertCompletionText, snippetCompletion } from '@codemirror/autocomplete'
import { StateField, type EditorState, type Extension } from '@codemirror/state'
import { EditorView, showTooltip, type Tooltip } from '@codemirror/view'
import { getSqlContext, ALIAS_BLOCKLIST, type RefTable, type SqlContext } from './sqlContext'

// ── Catalog input ────────────────────────────────────────────────────────────

export interface SchemaColumn {
  name: string
  type?: string
  pk?: boolean
  notNull?: boolean
  comment?: string
}
export interface SchemaTable {
  name: string
  kind?: string // 'table' | 'view'
  columns: SchemaColumn[]
}

/** Live view of the connection's metadata; closures read the latest state. */
export interface CompletionCatalog {
  databases(): string[]
  /**
   * Databases to offer as suggestions (object tree's schema filter applied).
   * Resolving an explicitly typed `db.` qualifier still uses databases(), so
   * a filtered-out database keeps working when named in full.
   */
  visibleDatabases?(): string[]
  currentDb(): string | undefined
  /** Tables of one database, or null if its snapshot isn't loaded yet. */
  tablesFor(db: string): SchemaTable[] | null
  /** Load a database's snapshot on demand (used for `otherdb.` completion). */
  ensureTables(db: string): Promise<SchemaTable[] | null>
}

/** The few user-visible strings, injected so this module stays i18n-free. */
export interface CompletionLabels {
  aliasFor(table: string): string
  nColumns(n: number): string
  joinCondition(): string
}

// ── Keywords ─────────────────────────────────────────────────────────────────

const KEYWORDS = [
  'SELECT', 'FROM', 'WHERE', 'JOIN', 'INNER JOIN', 'LEFT JOIN', 'RIGHT JOIN',
  'CROSS JOIN', 'ON', 'AS', 'AND', 'OR', 'NOT', 'NULL', 'IS', 'IN', 'BETWEEN',
  'LIKE', 'EXISTS', 'ORDER BY', 'GROUP BY', 'HAVING', 'LIMIT', 'OFFSET',
  'UNION', 'UNION ALL', 'DISTINCT', 'INSERT INTO', 'VALUES', 'UPDATE', 'SET',
  'DELETE FROM', 'CREATE TABLE', 'CREATE INDEX', 'CREATE VIEW', 'CREATE DATABASE',
  'ALTER TABLE', 'DROP TABLE', 'DROP INDEX', 'DROP VIEW', 'DROP DATABASE',
  'TRUNCATE TABLE', 'PRIMARY KEY', 'FOREIGN KEY', 'REFERENCES', 'UNIQUE',
  'DEFAULT', 'AUTO_INCREMENT', 'ENGINE', 'CHARSET', 'COLLATE', 'USE',
  'SHOW TABLES', 'SHOW DATABASES', 'SHOW CREATE TABLE', 'SHOW COLUMNS FROM',
  'SHOW INDEX FROM', 'SHOW PROCESSLIST', 'SHOW VARIABLES', 'SHOW STATUS',
  'DESCRIBE', 'EXPLAIN', 'CASE', 'WHEN', 'THEN', 'ELSE', 'END', 'WITH',
  'RECURSIVE', 'ASC', 'DESC', 'INTERVAL', 'ADD COLUMN', 'DROP COLUMN',
  'MODIFY COLUMN', 'CHANGE COLUMN', 'RENAME TO', 'IF NOT EXISTS', 'IF EXISTS',
  'START TRANSACTION', 'COMMIT', 'ROLLBACK', 'REPLACE INTO',
  'ON DUPLICATE KEY UPDATE', 'FOR UPDATE', 'PARTITION BY', 'OVER',
  'INT', 'BIGINT', 'SMALLINT', 'TINYINT', 'VARCHAR', 'CHAR', 'TEXT',
  'LONGTEXT', 'MEDIUMTEXT', 'DATETIME', 'TIMESTAMP', 'DATE', 'TIME', 'YEAR',
  'DECIMAL', 'FLOAT', 'DOUBLE', 'BOOLEAN', 'BLOB', 'LONGBLOB', 'JSON', 'ENUM',
  'BINARY', 'VARBINARY', 'UNSIGNED', 'NOT NULL', 'TRUE', 'FALSE',
]

const HIGH_FREQ = new Set([
  'SELECT', 'FROM', 'WHERE', 'JOIN', 'LEFT JOIN', 'ORDER BY', 'GROUP BY',
  'LIMIT', 'AND', 'OR', 'AS', 'ON', 'INSERT INTO', 'UPDATE', 'DELETE FROM',
  'SET', 'VALUES', 'DISTINCT', 'NOT', 'NULL', 'IN', 'LIKE', 'BETWEEN', 'HAVING',
])

/** Contextual follow-ups: after ORDER suggest BY first, etc. */
const PREFERRED_AFTER: Record<string, string[]> = {
  order: ['BY'],
  group: ['BY'],
  insert: ['INTO', 'IGNORE'],
  delete: ['FROM'],
  inner: ['JOIN'],
  left: ['JOIN', 'OUTER JOIN'],
  right: ['JOIN', 'OUTER JOIN'],
  cross: ['JOIN'],
  is: ['NULL', 'NOT NULL'],
  not: ['NULL', 'IN', 'EXISTS', 'LIKE', 'BETWEEN'],
  union: ['ALL', 'SELECT'],
  create: ['TABLE', 'INDEX', 'VIEW', 'DATABASE'],
  drop: ['TABLE', 'INDEX', 'VIEW', 'DATABASE'],
  alter: ['TABLE'],
  show: ['TABLES', 'DATABASES', 'CREATE TABLE', 'COLUMNS FROM', 'INDEX FROM', 'PROCESSLIST', 'VARIABLES', 'STATUS'],
  select: ['DISTINCT'],
}

// ── Functions ────────────────────────────────────────────────────────────────

const MYSQL_FUNCTIONS: Array<{ label: string; detail: string; info?: string }> = [
  { label: 'COUNT', detail: 'aggregate', info: 'COUNT(expr) — number of non-NULL rows' },
  { label: 'SUM', detail: 'aggregate', info: 'SUM(expr) — sum of expr' },
  { label: 'AVG', detail: 'aggregate', info: 'AVG(expr) — average of expr' },
  { label: 'MIN', detail: 'aggregate', info: 'MIN(expr)' },
  { label: 'MAX', detail: 'aggregate', info: 'MAX(expr)' },
  { label: 'GROUP_CONCAT', detail: 'aggregate', info: 'GROUP_CONCAT([DISTINCT] expr [ORDER BY …] [SEPARATOR str])' },
  { label: 'CONCAT', detail: 'string', info: 'CONCAT(str1, str2, …) — concatenate strings' },
  { label: 'CONCAT_WS', detail: 'string', info: 'CONCAT_WS(sep, str1, str2, …)' },
  { label: 'SUBSTRING', detail: 'string', info: 'SUBSTRING(str, pos[, len])' },
  { label: 'LENGTH', detail: 'string', info: 'LENGTH(str) — byte length' },
  { label: 'CHAR_LENGTH', detail: 'string', info: 'CHAR_LENGTH(str) — character length' },
  { label: 'TRIM', detail: 'string' },
  { label: 'LTRIM', detail: 'string' },
  { label: 'RTRIM', detail: 'string' },
  { label: 'LOWER', detail: 'string' },
  { label: 'UPPER', detail: 'string' },
  { label: 'REPLACE', detail: 'string', info: 'REPLACE(str, from, to)' },
  { label: 'LEFT', detail: 'string', info: 'LEFT(str, len)' },
  { label: 'RIGHT', detail: 'string', info: 'RIGHT(str, len)' },
  { label: 'LOCATE', detail: 'string', info: 'LOCATE(substr, str[, pos])' },
  { label: 'INSTR', detail: 'string', info: 'INSTR(str, substr)' },
  { label: 'LPAD', detail: 'string' },
  { label: 'RPAD', detail: 'string' },
  { label: 'FORMAT', detail: 'string', info: 'FORMAT(num, decimals)' },
  { label: 'ROUND', detail: 'numeric', info: 'ROUND(x[, d])' },
  { label: 'FLOOR', detail: 'numeric' },
  { label: 'CEIL', detail: 'numeric' },
  { label: 'ABS', detail: 'numeric' },
  { label: 'MOD', detail: 'numeric' },
  { label: 'POWER', detail: 'numeric' },
  { label: 'RAND', detail: 'numeric' },
  { label: 'GREATEST', detail: 'numeric' },
  { label: 'LEAST', detail: 'numeric' },
  { label: 'NOW', detail: 'datetime', info: 'NOW() — current DATETIME' },
  { label: 'CURDATE', detail: 'datetime' },
  { label: 'CURTIME', detail: 'datetime' },
  { label: 'CURRENT_TIMESTAMP', detail: 'datetime' },
  { label: 'UNIX_TIMESTAMP', detail: 'datetime', info: 'UNIX_TIMESTAMP([date])' },
  { label: 'FROM_UNIXTIME', detail: 'datetime', info: 'FROM_UNIXTIME(ts[, format])' },
  { label: 'DATE', detail: 'datetime' },
  { label: 'DATE_FORMAT', detail: 'datetime', info: 'DATE_FORMAT(date, format)' },
  { label: 'STR_TO_DATE', detail: 'datetime', info: 'STR_TO_DATE(str, format)' },
  { label: 'DATE_ADD', detail: 'datetime', info: 'DATE_ADD(date, INTERVAL n unit)' },
  { label: 'DATE_SUB', detail: 'datetime', info: 'DATE_SUB(date, INTERVAL n unit)' },
  { label: 'DATEDIFF', detail: 'datetime', info: 'DATEDIFF(date1, date2) — days between' },
  { label: 'TIMESTAMPDIFF', detail: 'datetime', info: 'TIMESTAMPDIFF(unit, dt1, dt2)' },
  { label: 'YEAR', detail: 'datetime' },
  { label: 'MONTH', detail: 'datetime' },
  { label: 'DAY', detail: 'datetime' },
  { label: 'HOUR', detail: 'datetime' },
  { label: 'MINUTE', detail: 'datetime' },
  { label: 'SECOND', detail: 'datetime' },
  { label: 'IFNULL', detail: 'control', info: 'IFNULL(expr, alt)' },
  { label: 'NULLIF', detail: 'control', info: 'NULLIF(a, b) — NULL if a=b' },
  { label: 'COALESCE', detail: 'control', info: 'COALESCE(a, b, …) — first non-NULL' },
  { label: 'IF', detail: 'control', info: 'IF(cond, a, b)' },
  { label: 'JSON_EXTRACT', detail: 'json' },
  { label: 'JSON_UNQUOTE', detail: 'json' },
  { label: 'JSON_OBJECT', detail: 'json' },
  { label: 'JSON_ARRAY', detail: 'json' },
  { label: 'CAST', detail: 'cast', info: 'CAST(expr AS type)' },
  { label: 'CONVERT', detail: 'cast', info: 'CONVERT(expr, type)' },
  { label: 'VERSION', detail: 'system' },
  { label: 'DATABASE', detail: 'system' },
  { label: 'USER', detail: 'system' },
  { label: 'LAST_INSERT_ID', detail: 'system' },
  { label: 'UUID', detail: 'system' },
]

export const FUNCTION_SIGNATURES = new Map<string, string[]>([
  ['COUNT', ['expr']],
  ['SUM', ['expr']],
  ['AVG', ['expr']],
  ['MIN', ['expr']],
  ['MAX', ['expr']],
  ['GROUP_CONCAT', ['expr', 'separator']],
  ['CONCAT', ['str', '…']],
  ['CONCAT_WS', ['separator', 'str', '…']],
  ['SUBSTRING', ['str', 'pos', 'len']],
  ['REPLACE', ['str', 'from', 'to']],
  ['LEFT', ['str', 'len']],
  ['RIGHT', ['str', 'len']],
  ['LOCATE', ['substr', 'str', 'pos']],
  ['INSTR', ['str', 'substr']],
  ['LPAD', ['str', 'len', 'pad']],
  ['RPAD', ['str', 'len', 'pad']],
  ['ROUND', ['x', 'decimals']],
  ['MOD', ['n', 'm']],
  ['POWER', ['base', 'exp']],
  ['FORMAT', ['num', 'decimals']],
  ['DATE_FORMAT', ['date', 'format']],
  ['STR_TO_DATE', ['str', 'format']],
  ['DATE_ADD', ['date', 'INTERVAL n unit']],
  ['DATE_SUB', ['date', 'INTERVAL n unit']],
  ['DATEDIFF', ['date1', 'date2']],
  ['TIMESTAMPDIFF', ['unit', 'dt1', 'dt2']],
  ['FROM_UNIXTIME', ['ts', 'format']],
  ['IFNULL', ['expr', 'alt']],
  ['NULLIF', ['a', 'b']],
  ['COALESCE', ['a', 'b', '…']],
  ['IF', ['cond', 'then', 'else']],
  ['CAST', ['expr AS type']],
  ['CONVERT', ['expr', 'type']],
])

const NO_ARG_FUNCTIONS = new Set([
  'NOW', 'CURDATE', 'CURTIME', 'CURRENT_TIMESTAMP', 'RAND', 'VERSION',
  'DATABASE', 'USER', 'LAST_INSERT_ID', 'UUID', 'UNIX_TIMESTAMP',
])

// ── Snippets ─────────────────────────────────────────────────────────────────

const SNIPPETS: readonly Completion[] = [
  snippetCompletion('SELECT ${columns} FROM ${table}${}', { label: 'select', detail: 'SELECT … FROM …', type: 'keyword' }),
  snippetCompletion('SELECT ${columns}\nFROM ${table}\nWHERE ${condition}${}', { label: 'selectw', detail: 'SELECT … FROM … WHERE', type: 'keyword' }),
  snippetCompletion('SELECT COUNT(*) FROM ${table}${}', { label: 'count', detail: 'SELECT COUNT(*) FROM …', type: 'keyword' }),
  snippetCompletion('INSERT INTO ${table} (${columns})\nVALUES (${values})${}', { label: 'insert', detail: 'INSERT INTO …', type: 'keyword' }),
  snippetCompletion('UPDATE ${table}\nSET ${col} = ${value}\nWHERE ${condition}${}', { label: 'update', detail: 'UPDATE … SET … WHERE', type: 'keyword' }),
  snippetCompletion('DELETE FROM ${table}\nWHERE ${condition}${}', { label: 'delete', detail: 'DELETE FROM … WHERE', type: 'keyword' }),
  snippetCompletion('JOIN ${table} ON ${left} = ${right}${}', { label: 'join', detail: 'JOIN … ON …', type: 'keyword' }),
  snippetCompletion('LEFT JOIN ${table} ON ${left} = ${right}${}', { label: 'leftjoin', detail: 'LEFT JOIN … ON …', type: 'keyword' }),
  snippetCompletion('WITH ${name} AS (\n  SELECT ${columns}\n  FROM ${table}\n)\nSELECT *\nFROM ${name}${}', { label: 'cte', detail: 'WITH … AS ( … )', type: 'keyword' }),
  snippetCompletion('GROUP BY ${columns}${}', { label: 'groupby', detail: 'GROUP BY …', type: 'keyword' }),
  snippetCompletion('ORDER BY ${columns} ${direction}${}', { label: 'orderby', detail: 'ORDER BY … ASC|DESC', type: 'keyword' }),
  snippetCompletion('CREATE TABLE ${name} (\n  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,\n  ${cols}\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4${}', { label: 'createtable', detail: 'CREATE TABLE …', type: 'keyword' }),
  snippetCompletion('CASE WHEN ${cond} THEN ${value} ELSE ${other} END${}', { label: 'case', detail: 'CASE WHEN … THEN … ELSE … END', type: 'keyword' }),
  snippetCompletion('EXISTS (\n  SELECT 1 FROM ${table} WHERE ${condition}\n)${}', { label: 'exists', detail: 'EXISTS ( SELECT 1 … )', type: 'keyword' }),
]

// ── Match scoring (dbx computeMatchScore tiers) ──────────────────────────────

function splitWords(s: string): string[] {
  return s
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .split(/[_\s]+/)
    .filter(Boolean)
    .map((w) => w.toLowerCase())
}

/** -1 = no match. Higher = better: exact > initials > prefix > substring > fuzzy. */
export function matchScore(label: string, prefix: string): number {
  if (!prefix) return 0
  const l = label.toLowerCase()
  const p = prefix.toLowerCase()
  if (l === p) return 3000 - label.length
  const words = splitWords(label)
  if (words.length > 1) {
    const initials = words.map((w) => w[0]).join('')
    if (initials === p) return 2800 - label.length
    if (initials.startsWith(p)) return 2400 - label.length
  }
  if (l.startsWith(p)) return 2000 - label.length
  const idx = l.indexOf(p)
  if (idx > 0) {
    const boundary = /[\s_.]/.test(l[idx - 1]) ? 100 : 0
    return 900 + boundary - label.length
  }
  // subsequence match
  let ti = 0
  let gaps = 0
  let first = -1
  for (let i = 0; i < l.length && ti < p.length; i++) {
    if (l[i] === p[ti]) {
      if (first < 0) first = i
      ti++
    } else if (ti > 0) gaps++
  }
  if (ti < p.length) return -1
  if (gaps < p.length) return 1200 - gaps * 10 - first * 2 - label.length
  return Math.max(1, 400 - gaps * 20 - label.length)
}

const TYPE_PRIORITY: Record<string, number> = {
  property: 180, // columns
  type: 160, // tables
  interface: 160, // views
  namespace: 120, // databases
  function: 90,
  variable: 60, // aliases
  keyword: 0,
  text: 0,
}

// ── Selection history (per session) ──────────────────────────────────────────

const pickHistory = new Map<string, number>()

function historyBoost(key: string): number {
  const n = pickHistory.get(key) ?? 0
  return Math.min(n * 80, 500)
}

/** Wrap a completion so accepting it feeds the ranking history. */
function withHistory(c: Completion): Completion {
  const key = `${c.type}:${c.label}`
  const orig = c.apply
  if (typeof orig === 'function') {
    return {
      ...c,
      apply: (view, completion, from, to) => {
        pickHistory.set(key, (pickHistory.get(key) ?? 0) + 1)
        orig(view, completion, from, to)
      },
    }
  }
  const text = typeof orig === 'string' ? orig : c.label
  return {
    ...c,
    // Kept alongside the wrapped function so tests/debugging can still see
    // the literal insert text.
    applyText: text,
    apply: (view: EditorView, _c: Completion, from: number, to: number) => {
      pickHistory.set(key, (pickHistory.get(key) ?? 0) + 1)
      view.dispatch(insertCompletionText(view.state, text, from, to))
    },
  } as Completion
}

// ── Identifier / alias helpers ───────────────────────────────────────────────

function needsQuote(name: string): boolean {
  return !/^[A-Za-z_][\w$]*$/.test(name)
}
function quoteName(name: string): string {
  return needsQuote(name) ? '`' + name + '`' : name
}

function aliasCandidates(name: string): string[] {
  const words = splitWords(name)
  const out: string[] = []
  if (words.length > 1) {
    const initials = words.map((w) => w[0]).join('')
    if (initials.length >= 2) out.push(initials.slice(0, 2))
    if (initials.length >= 3) out.push(initials.slice(0, 3))
  }
  const w0 = words[0] ?? ''
  out.push(w0.slice(0, 1), w0.slice(0, 2), w0.slice(0, 3))
  return out.filter(Boolean)
}

export function generateAlias(name: string, existing: Set<string>): string {
  for (const c of aliasCandidates(name)) {
    if (!ALIAS_BLOCKLIST.has(c) && !existing.has(c)) return c
  }
  const base = aliasCandidates(name)[0] ?? 't'
  for (let i = 2; i < 100; i++) {
    if (!existing.has(base + i)) return base + i
  }
  return base
}

// ── Item building ────────────────────────────────────────────────────────────

interface Scored {
  completion: Completion
  score: number
}

function push(items: Scored[], c: Completion, prefix: string, base: number) {
  const m = matchScore(c.label, prefix)
  if (m < 0) return
  const key = `${c.type}:${c.label}`
  items.push({
    completion: c,
    score: m + base + (TYPE_PRIORITY[c.type ?? ''] ?? 0) + historyBoost(key),
  })
}

function columnDetail(c: SchemaColumn, tableHint?: string): string | undefined {
  const parts: string[] = []
  if (c.type) parts.push(c.type)
  if (c.pk) parts.push('PK')
  else if (c.notNull) parts.push('NOT NULL')
  if (tableHint) parts.push(tableHint)
  return parts.join(' · ') || undefined
}

function columnCompletion(c: SchemaColumn, opts: { label?: string; apply?: string; tableHint?: string } = {}): Completion {
  return {
    label: opts.label ?? c.name,
    type: 'property',
    detail: columnDetail(c, opts.tableHint),
    info: c.comment || undefined,
    apply: opts.apply ?? quoteName(c.name),
  }
}

/** Resolve one referenced table to its columns via the catalog (or CTE list). */
function columnsForRef(ref: RefTable, catalog: CompletionCatalog): SchemaColumn[] | null {
  if (ref.columns) return ref.columns.map((n) => ({ name: n }))
  const db = ref.db ?? catalog.currentDb()
  if (!db) return null
  const tables = catalog.tablesFor(db)
  if (!tables) return null
  const t = tables.find((x) => x.name.toLowerCase() === ref.name.toLowerCase())
  return t ? t.columns : null
}

/** Distinct resolvable refs, keyed by db+name (a self-join counts once). */
function resolvedRefs(sc: SqlContext, catalog: CompletionCatalog): Array<{ ref: RefTable; cols: SchemaColumn[] }> {
  const seen = new Map<string, { ref: RefTable; cols: SchemaColumn[] }>()
  for (const ref of sc.refs) {
    const cols = columnsForRef(ref, catalog)
    if (!cols) continue
    const key = `${(ref.db ?? '').toLowerCase()}.${ref.name.toLowerCase()}`
    const prev = seen.get(key)
    // Prefer the entry that has an alias so qualified applies can use it.
    if (!prev || (!prev.ref.alias && ref.alias)) seen.set(key, { ref, cols })
  }
  return [...seen.values()]
}

function tableCompletion(t: SchemaTable, autoAlias: boolean, existingAliases: Set<string>): Completion {
  const name = quoteName(t.name)
  const apply = autoAlias ? `${name} ${generateAlias(t.name, existingAliases)}` : name
  return {
    label: t.name,
    type: t.kind === 'view' ? 'interface' : 'type',
    detail: t.kind === 'view' ? 'view' : undefined,
    apply,
  }
}

/** In-scope column items: dedupe across tables, qualify ambiguous names. */
function buildScopeColumns(items: Scored[], sc: SqlContext, catalog: CompletionCatalog, base: number) {
  const resolved = resolvedRefs(sc, catalog)
  if (!resolved.length) return
  const freq = new Map<string, number>()
  for (const { cols } of resolved) {
    for (const c of cols) freq.set(c.name, (freq.get(c.name) ?? 0) + 1)
  }
  const multi = resolved.length > 1
  for (const { ref, cols } of resolved) {
    const qual = ref.alias ?? ref.name
    for (const c of cols) {
      const ambiguous = (freq.get(c.name) ?? 0) > 1
      const keyBoost = c.pk || c.name === 'id' || c.name.endsWith('_id') ? 300 : 0
      if (ambiguous && multi) {
        push(items, columnCompletion(c, {
          label: `${qual}.${c.name}`,
          apply: `${quoteName(qual)}.${quoteName(c.name)}`,
          tableHint: ref.name,
        }), sc.prefix, base + keyBoost)
      } else {
        push(items, columnCompletion(c, { tableHint: multi ? ref.name : undefined }), sc.prefix, base + keyBoost)
      }
    }
  }
}

/** `alias.*` / `* → columns` expansion items for the SELECT list. */
function buildStarExpansions(items: Scored[], sc: SqlContext, catalog: CompletionCatalog, labels: CompletionLabels) {
  if (sc.prefix) return
  const resolved = resolvedRefs(sc, catalog)
  if (!resolved.length) return
  const multi = resolved.length > 1
  for (const { ref, cols } of resolved) {
    if (!cols.length) continue
    const qual = ref.alias ?? ref.name
    const expansion = cols
      .map((c) => (multi ? `${quoteName(qual)}.${quoteName(c.name)}` : quoteName(c.name)))
      .join(', ')
    const preview = expansion.length > 60 ? expansion.slice(0, 57) + '…' : expansion
    items.push({
      completion: {
        label: `${qual}.*`,
        type: 'text',
        detail: `${labels.nColumns(cols.length)}: ${preview}`,
        apply: expansion,
      },
      score: 1900,
    })
  }
}

/** Name-matching join conditions for the table just joined. */
function buildJoinConditions(items: Scored[], sc: SqlContext, catalog: CompletionCatalog, labels: CompletionLabels) {
  const resolved = resolvedRefs(sc, catalog)
  if (resolved.length < 2) return
  const target = resolved[resolved.length - 1]
  const targetQual = quoteName(target.ref.alias ?? target.ref.name)
  const conds: string[] = []
  const singular = (n: string) => n.toLowerCase().replace(/s$/, '')
  for (const other of resolved.slice(0, -1)) {
    const otherQual = quoteName(other.ref.alias ?? other.ref.name)
    const otherCols = new Set(other.cols.map((c) => c.name.toLowerCase()))
    for (const c of target.cols) {
      const n = c.name.toLowerCase()
      // fk-style: target.user_id = users.id
      if (n.endsWith('_id') && otherCols.has('id') && singular(other.ref.name).endsWith(n.slice(0, -3))) {
        conds.push(`${targetQual}.${quoteName(c.name)} = ${otherQual}.id`)
      }
    }
    for (const c of other.cols) {
      const n = c.name.toLowerCase()
      // reverse fk-style: target.id = other.target_id
      if (n.endsWith('_id') && singular(target.ref.name).endsWith(n.slice(0, -3))) {
        conds.push(`${targetQual}.id = ${otherQual}.${quoteName(c.name)}`)
      }
    }
    // same non-generic name on both sides
    for (const c of target.cols) {
      const n = c.name.toLowerCase()
      if (n !== 'id' && !n.endsWith('_id') && otherCols.has(n)) {
        conds.push(`${targetQual}.${quoteName(c.name)} = ${otherQual}.${quoteName(c.name)}`)
      }
    }
  }
  for (const [i, cond] of [...new Set(conds)].slice(0, 5).entries()) {
    push(items, { label: cond, type: 'text', detail: labels.joinCondition(), apply: cond }, sc.prefix, 2600 - i)
  }
}

/** Type-aware value suggestions after `col =`. */
function buildComparisonValues(items: Scored[], sc: SqlContext, catalog: CompletionCatalog) {
  if (!sc.comparison) return
  const colName = sc.comparison.column.split('.').pop()!.toLowerCase()
  let type = ''
  for (const { cols } of resolvedRefs(sc, catalog)) {
    const c = cols.find((x) => x.name.toLowerCase() === colName)
    if (c?.type) { type = c.type.toLowerCase(); break }
  }
  if (!type) return
  // Above the in-scope columns (2000+180) — after `col =` a concrete value is
  // the most likely pick.
  if (/date|time|year/.test(type)) {
    for (const [i, f] of ['NOW()', 'CURDATE()', 'CURRENT_TIMESTAMP'].entries()) {
      items.push({ completion: { label: f, type: 'function', apply: f }, score: 2700 - i })
    }
  } else if (/^(tinyint\(1\)|bool|boolean|bit)/.test(type)) {
    for (const [i, v] of ['TRUE', 'FALSE', '1', '0'].entries()) {
      items.push({ completion: { label: v, type: 'keyword', apply: v }, score: 2700 - i })
    }
  }
}

function buildKeywords(items: Scored[], sc: SqlContext) {
  const preferred = PREFERRED_AFTER[sc.lastWord]
  if (preferred) {
    for (const [i, kw] of preferred.entries()) {
      push(items, { label: kw, type: 'keyword', apply: kw + ' ' }, sc.prefix, 1800 - i * 10)
    }
  }
  for (const kw of KEYWORDS) {
    if (preferred?.includes(kw)) continue
    push(items, { label: kw, type: 'keyword', apply: kw + ' ' }, sc.prefix, HIGH_FREQ.has(kw) ? 100 : 0)
  }
}

const FUNCTION_ITEMS: readonly Completion[] = MYSQL_FUNCTIONS.map((f) => {
  if (NO_ARG_FUNCTIONS.has(f.label)) {
    return { label: f.label, type: 'function', detail: f.detail, info: f.info, apply: f.label + '()' }
  }
  const params = FUNCTION_SIGNATURES.get(f.label)
  const body = params
    ? `${f.label}(${params.filter((p) => p !== '…').map((p) => `\${${p}}`).join(', ')})\${}`
    : `${f.label}(\${})`
  return snippetCompletion(body, { label: f.label, type: 'function', detail: f.detail, info: f.info })
})

function buildFunctions(items: Scored[], sc: SqlContext) {
  for (const f of FUNCTION_ITEMS) push(items, f, sc.prefix, 0)
}

function buildSnippets(items: Scored[], sc: SqlContext) {
  if (!sc.prefix) return
  for (const s of SNIPPETS) {
    // exact label typed → the snippet should win over the bare keyword
    push(items, s, sc.prefix, s.label.toLowerCase() === sc.prefix.toLowerCase() ? 1500 : -50)
  }
}

function buildTables(items: Scored[], sc: SqlContext, catalog: CompletionCatalog, tables: SchemaTable[]) {
  const existing = new Set(
    sc.refs.map((r) => r.alias?.toLowerCase()).filter((a): a is string => !!a),
  )
  let n = 0
  for (const t of tables) {
    if (n >= 200) break
    if (matchScore(t.name, sc.prefix) < 0) continue
    push(items, tableCompletion(t, sc.autoAlias, existing), sc.prefix, 1000)
    n++
  }
  // CTE / subquery names are also valid FROM targets
  for (const ref of sc.refs) {
    if (!ref.columns) continue
    push(items, { label: ref.name, type: 'type', apply: quoteName(ref.name) }, sc.prefix, 1000)
  }
}

// System catalogs are noise in everyday completion — hidden until the user
// types a prefix that could be asking for them (e.g. "inf" → information_schema).
const SYSTEM_SCHEMAS = new Set(['information_schema', 'mysql', 'performance_schema', 'sys'])

function buildDatabases(items: Scored[], sc: SqlContext, catalog: CompletionCatalog, base = 0) {
  for (const db of catalog.visibleDatabases?.() ?? catalog.databases()) {
    if (!sc.prefix && SYSTEM_SCHEMAS.has(db.toLowerCase())) continue
    push(items, { label: db, type: 'namespace', apply: quoteName(db) }, sc.prefix, base)
  }
}

// ── Qualified completion (after `x.` / `db.table.`) ──────────────────────────

function buildQualified(
  sc: SqlContext,
  catalog: CompletionCatalog,
  labels: CompletionLabels,
): CompletionResult | Promise<CompletionResult | null> | null {
  const items: Scored[] = []
  const parts = sc.qualifier

  const columnsOf = (cols: SchemaColumn[]) => {
    for (const c of cols) push(items, columnCompletion(c), sc.prefix, 2000)
  }

  if (parts.length === 1) {
    const q = parts[0].toLowerCase()
    // 1. alias or referenced table
    const ref =
      sc.refs.find((r) => r.alias?.toLowerCase() === q) ??
      sc.refs.find((r) => r.name.toLowerCase() === q)
    if (ref) {
      const cols = columnsForRef(ref, catalog)
      if (cols) {
        columnsOf(cols)
        // `alias.*` expansion
        if (!sc.prefix && cols.length) {
          const expansion = cols.map((c) => quoteName(c.name)).join(', ')
          items.push({
            completion: {
              label: '*',
              type: 'text',
              detail: labels.nColumns(cols.length),
              apply: expansion,
            },
            score: 1900,
          })
        }
        return finish(items, sc)
      }
    }
    // 2. a table of the current db (not referenced yet)
    const cur = catalog.currentDb()
    if (cur) {
      const t = catalog.tablesFor(cur)?.find((x) => x.name.toLowerCase() === q)
      if (t) {
        columnsOf(t.columns)
        return finish(items, sc)
      }
    }
    // 3. a database → its tables (load on demand)
    const db = catalog.databases().find((d) => d.toLowerCase() === q)
    if (db) {
      const tables = catalog.tablesFor(db)
      if (tables) {
        buildTables(items, sc, catalog, tables)
        return finish(items, sc)
      }
      return catalog.ensureTables(db).then((loaded) => {
        if (!loaded) return null
        buildTables(items, sc, catalog, loaded)
        return finish(items, sc)
      })
    }
    return null
  }

  // db.table. → columns of that table
  if (parts.length >= 2) {
    const dbName = parts[parts.length - 2]
    const tblName = parts[parts.length - 1].toLowerCase()
    const db = catalog.databases().find((d) => d.toLowerCase() === dbName.toLowerCase())
    if (!db) return null
    const lookup = (tables: SchemaTable[] | null): CompletionResult | null => {
      const t = tables?.find((x) => x.name.toLowerCase() === tblName)
      if (!t) return null
      columnsOf(t.columns)
      return finish(items, sc)
    }
    const tables = catalog.tablesFor(db)
    if (tables) return lookup(tables)
    return catalog.ensureTables(db).then(lookup)
  }
  return null
}

// ── Assembly ─────────────────────────────────────────────────────────────────

const MAX_ITEMS = 300

function finish(items: Scored[], sc: SqlContext): CompletionResult | null {
  if (!items.length) return null
  items.sort((a, b) => b.score - a.score)
  const seen = new Set<string>()
  const options: Completion[] = []
  for (const { completion } of items) {
    const key = `${completion.type}:${completion.label}`
    if (seen.has(key)) continue
    seen.add(key)
    let c = withHistory(completion)
    // Reopen a quoted prefix's backtick on apply: `us → `users`
    if (sc.quoted && typeof completion.apply !== 'function') {
      const text = typeof completion.apply === 'string' ? completion.apply : completion.label
      const inner = text.replace(/`/g, '')
      c = withHistory({ ...completion, apply: '`' + inner + '`' })
    }
    options.push(c)
    if (options.length >= MAX_ITEMS) break
  }
  return { from: sc.from, options, filter: false }
}

/**
 * The completion source. Registered via `autocompletion({ override })`, so it
 * fully owns the popup: no other source contributes.
 */
export function createSqlCompletionSource(
  catalog: CompletionCatalog,
  labels: CompletionLabels,
): CompletionSource {
  const source = (
    ctx: CompletionContext,
    retried = false,
  ): CompletionResult | Promise<CompletionResult | null> | null => {
    const doc = ctx.state.doc.toString()
    const sc = getSqlContext(doc, ctx.pos)
    if (sc.suppressed) return null

    if (sc.qualifier.length) return buildQualified(sc, catalog, labels)

    // FROM/JOIN position with a selected database whose table list hasn't
    // been fetched yet: load it first, then re-run. Without this the popup
    // degrades to database names only until the snapshot happens to arrive.
    if (!retried && sc.clause === 'table') {
      const cur = catalog.currentDb()
      if (cur && catalog.tablesFor(cur) == null) {
        return catalog.ensureTables(cur).then(() => source(ctx, true))
      }
    }

    // Auto-open on empty prefix only where the next token is strongly implied.
    const autoOpen =
      sc.clause === 'table' || sc.clause === 'use' || sc.clause === 'insert-columns' ||
      sc.clause === 'set' || sc.clause === 'on' || !!sc.aliasSlot
    if (!ctx.explicit && !sc.prefix && !autoOpen) return null

    const items: Scored[] = []

    // alias suggestion in the FROM tbl ▸ slot
    if (sc.aliasSlot) {
      const existing = new Set(
        sc.refs.map((r) => r.alias?.toLowerCase()).filter((a): a is string => !!a),
      )
      const alias = generateAlias(sc.aliasSlot.table, existing)
      push(items, {
        label: alias,
        type: 'variable',
        detail: labels.aliasFor(sc.aliasSlot.table),
        apply: alias + ' ',
      }, sc.prefix, 1700)
    }

    switch (sc.clause) {
      case 'use':
        buildDatabases(items, sc, catalog, 1000)
        break
      case 'table': {
        const cur = catalog.currentDb()
        buildTables(items, sc, catalog, (cur && catalog.tablesFor(cur)) || [])
        buildDatabases(items, sc, catalog)
        break
      }
      case 'insert-columns': {
        const target = sc.insertTarget!
        const db = target.db ?? catalog.currentDb()
        const t = db ? catalog.tablesFor(db)?.find((x) => x.name.toLowerCase() === target.table.toLowerCase()) : null
        if (t) {
          const listed = new Set(target.listed.map((c) => c.toLowerCase()))
          for (const c of t.columns) {
            if (listed.has(c.name.toLowerCase())) continue
            push(items, columnCompletion(c), sc.prefix, 2000)
          }
        }
        break
      }
      case 'set': {
        const target = sc.updateTarget!
        const db = target.db ?? catalog.currentDb()
        const t = db ? catalog.tablesFor(db)?.find((x) => x.name.toLowerCase() === target.table.toLowerCase()) : null
        if (t && !sc.comparison) {
          const assigned = new Set(target.assigned.map((c) => c.toLowerCase()))
          for (const c of t.columns) {
            if (assigned.has(c.name.toLowerCase())) continue
            push(items, columnCompletion(c, { apply: quoteName(c.name) + ' = ' }), sc.prefix, 2000)
          }
        }
        if (sc.comparison) {
          buildComparisonValues(items, sc, catalog)
          buildFunctions(items, sc)
        }
        push(items, { label: 'WHERE', type: 'keyword', apply: 'WHERE ' }, sc.prefix, 100)
        break
      }
      case 'values':
        buildFunctions(items, sc)
        for (const kw of ['NULL', 'DEFAULT', 'TRUE', 'FALSE']) {
          push(items, { label: kw, type: 'keyword', apply: kw }, sc.prefix, 100)
        }
        break
      case 'on':
        buildJoinConditions(items, sc, catalog, labels)
        buildScopeColumns(items, sc, catalog, 1500)
        buildKeywords(items, sc)
        break
      case 'select-list':
        buildScopeColumns(items, sc, catalog, 2000)
        buildStarExpansions(items, sc, catalog, labels)
        buildComparisonValues(items, sc, catalog)
        buildFunctions(items, sc)
        buildKeywords(items, sc)
        buildSnippets(items, sc)
        break
      case 'order-group':
        for (const [i, a] of sc.selectAliases.entries()) {
          push(items, { label: a, type: 'variable', apply: quoteName(a) }, sc.prefix, 2200 - i)
        }
        buildScopeColumns(items, sc, catalog, 2000)
        buildKeywords(items, sc)
        break
      case 'column':
        buildScopeColumns(items, sc, catalog, 2000)
        buildComparisonValues(items, sc, catalog)
        buildFunctions(items, sc)
        buildKeywords(items, sc)
        buildSnippets(items, sc)
        break
      default:
        buildKeywords(items, sc)
        buildSnippets(items, sc)
        buildFunctions(items, sc)
        // columns still reachable in generic spots when the statement has refs
        buildScopeColumns(items, sc, catalog, 0)
        break
    }
    return finish(items, sc)
  }
  return (ctx: CompletionContext) => source(ctx)
}

// ── Function signature help ──────────────────────────────────────────────────

interface SigInfo { name: string; params: string[]; active: number }

function activeSignature(before: string): SigInfo | null {
  const pos = before.length
  let depth = 0
  let q: string | null = null
  let open = -1
  for (let i = pos - 1; i >= 0; i--) {
    const ch = before[i]
    if (q) { if (ch === q) q = null; continue }
    if (ch === "'" || ch === '"' || ch === '`') { q = ch; continue }
    if (ch === ')') depth++
    else if (ch === '(') {
      if (depth === 0) { open = i; break }
      depth--
    }
  }
  if (open < 0) return null
  const name = /([A-Za-z_][\w$]*)\s*$/.exec(before.slice(0, open))?.[1]
  if (!name) return null
  const params = FUNCTION_SIGNATURES.get(name.toUpperCase())
  if (!params) return null
  let active = 0
  let d = 0
  let qq: string | null = null
  for (let i = open + 1; i < pos; i++) {
    const ch = before[i]
    if (qq) { if (ch === qq) qq = null; continue }
    if (ch === "'" || ch === '"' || ch === '`') { qq = ch; continue }
    if (ch === '(') d++
    else if (ch === ')') d--
    else if (ch === ',' && d === 0) active++
  }
  return { name: name.toUpperCase(), params, active: Math.min(active, params.length - 1) }
}

function signatureTooltip(head: number, sig: SigInfo): Tooltip {
  return {
    pos: head,
    above: true,
    create() {
      const dom = document.createElement('div')
      dom.className = 'cm-sql-signature'
      dom.appendChild(document.createTextNode(sig.name + '('))
      sig.params.forEach((p, i) => {
        if (i) dom.appendChild(document.createTextNode(', '))
        const span = document.createElement('span')
        span.textContent = p
        if (i === sig.active) span.className = 'cm-sql-signature-active'
        dom.appendChild(span)
      })
      dom.appendChild(document.createTextNode(')'))
      return { dom }
    },
  }
}

function computeSignature(state: EditorState): Tooltip | null {
  const sel = state.selection.main
  if (!sel.empty) return null
  const before = state.doc.sliceString(Math.max(0, sel.head - 4000), sel.head)
  const sig = activeSignature(before)
  return sig ? signatureTooltip(sel.head, sig) : null
}

/** Editor extension: shows a parameter hint while inside a known function call. */
export const sqlSignatureHelp: Extension = StateField.define<Tooltip | null>({
  create(state) {
    return computeSignature(state)
  },
  update(value, tr) {
    if (!tr.docChanged && !tr.selection) return value
    return computeSignature(tr.state)
  },
  provide: (f) => showTooltip.from(f),
})
