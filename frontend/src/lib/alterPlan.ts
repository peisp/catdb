// alterPlan — diff (original table summary) vs. (user-edited draft) and emit
// MySQL ALTER TABLE statements. Pure TypeScript: no Vue, no IPC. The structure
// editor calls this on every edit to refresh the SQL preview panel; the same
// statements drive the Apply button.
//
// Rationale (per ARCHITECTURE design note): MySQL is the only driver in MVP,
// so generating in the front-end gives instant feedback with zero IPC cost.
// When a second driver lands, factor BuildAlterTable into Dialect on the Go
// side and call it from here instead.
//
// MySQL quirks baked in:
//   - identifiers quoted with backticks; embedded backticks doubled
//   - changing an index = DROP + ADD (no ALTER INDEX … in MySQL)
//   - same for foreign keys
//   - column position uses AFTER `prev` (or FIRST) on ADD/MODIFY/CHANGE
//   - PRIMARY KEY is its own DROP/ADD pair, not an index name
import type {
  ColumnMeta,
  ForeignKeyInfo,
  IndexInfo,
  TableSummary,
} from '../api/metadata'

// ---- identifier / literal quoting -----------------------------------------

/** MySQL identifier quoting — backticks, double any embedded backtick. */
export function quoteIdent(name: string): string {
  return '`' + String(name).replace(/`/g, '``') + '`'
}

/** MySQL string-literal quoting — single quotes, escape \ and ', plus \n/\r/\t. */
export function quoteString(s: string): string {
  return (
    "'" +
    String(s)
      .replace(/\\/g, '\\\\')
      .replace(/'/g, "''")
      .replace(/\n/g, '\\n')
      .replace(/\r/g, '\\r')
      .replace(/\t/g, '\\t') +
    "'"
  )
}

/** Compose `db`.`table` form when db is non-empty. */
export function quoteTable(db: string, table: string): string {
  return db ? `${quoteIdent(db)}.${quoteIdent(table)}` : quoteIdent(table)
}

// Tokens that we pass through unquoted when found as a DEFAULT value.
// Anything else that isn't a pure number gets wrapped in '…' as a string lit.
const DEFAULT_KEYWORDS = new Set([
  'NULL',
  'CURRENT_TIMESTAMP',
  'CURRENT_DATE',
  'CURRENT_TIME',
  'NOW()',
  'UUID()',
  'TRUE',
  'FALSE',
])

/** Format a DEFAULT expression. Returns the right-hand side of `DEFAULT …`. */
export function formatDefaultExpr(raw: string): string {
  const trimmed = raw.trim()
  if (trimmed === '') return "''"
  const upper = trimmed.toUpperCase()
  if (DEFAULT_KEYWORDS.has(upper)) return upper
  // Functional defaults like (CURRENT_TIMESTAMP) or (UUID()) — keep verbatim.
  if (trimmed.startsWith('(') && trimmed.endsWith(')')) return trimmed
  // Pure numeric literal (incl. negative, decimal, scientific).
  if (/^-?\d+(\.\d+)?(e-?\d+)?$/i.test(trimmed)) return trimmed
  return quoteString(trimmed)
}

// ---- draft shapes ----------------------------------------------------------
//
// The structure editor keeps a parallel "draft" copy of the table summary
// while the user edits. Drafts carry stable client-side keys (_key) and a
// snapshot of the original name/position so we can detect rename and reorder.

export interface ColumnDraft {
  _key: string
  /** Original column name as loaded; empty string for newly-added rows. */
  origName: string
  /** Original ORDINAL_POSITION (0-based); -1 for newly-added rows. */
  origPos: number

  name: string
  /**
   * Base SQL type, uppercased, no params or modifiers — e.g. "VARCHAR", "INT",
   * "DECIMAL". UNSIGNED is *not* part of this string (kept as a separate flag),
   * so the type select doesn't have to list "INT" / "INT UNSIGNED" / "BIGINT" /
   * "BIGINT UNSIGNED" as different entries.
   */
  baseType: string
  /**
   * Parameters inside the parens, as a free-form string. Meaning depends on
   * the base type:
   *   VARCHAR/CHAR/...     → length          ("255")
   *   DECIMAL/NUMERIC      → precision,scale ("10,2")
   *   FLOAT/DOUBLE         → precision[,scale] (rarely used)
   *   DATETIME/TIMESTAMP/TIME → fractional-seconds precision ("6")
   *   TINYINT/.../BIGINT/BIT → display width (legacy/optional)
   *   ENUM/SET             → value list (`'a','b'`)
   *   TEXT/BLOB/JSON/DATE  → empty (no params accepted)
   * Empty string means "no params" — the parens are omitted in the emitted
   * DDL.
   */
  typeParams: string
  /** UNSIGNED modifier; only meaningful on numeric base types. */
  unsigned: boolean
  nullable: boolean
  /** undefined = no DEFAULT clause; '' = DEFAULT ''; 'NULL' = DEFAULT NULL. */
  default?: string
  isPrimaryKey: boolean
  isAutoIncrement: boolean
  comment: string
}

// ---- native-type parsing / formatting -------------------------------------
//
// The backend returns information_schema.COLUMN_TYPE verbatim (e.g.
// "varchar(255)", "int(10) unsigned", "decimal(10,2)", "datetime(6)",
// "enum('a','b')"). We split that into baseType + typeParams + unsigned so
// the editor's three fields are independent, then reassemble on the way out.

export interface ParsedNativeType {
  baseType: string
  typeParams: string
  unsigned: boolean
}

/** Parse a MySQL COLUMN_TYPE string into base/params/unsigned. */
export function parseNativeType(raw: string): ParsedNativeType {
  let s = (raw ?? '').trim()
  if (!s) return { baseType: '', typeParams: '', unsigned: false }
  // Strip ZEROFILL first (it's rare but MySQL appends it after UNSIGNED).
  s = s.replace(/\s+ZEROFILL\b/gi, '')
  let unsigned = false
  if (/\s+UNSIGNED\b/i.test(s)) {
    unsigned = true
    s = s.replace(/\s+UNSIGNED\b/gi, '')
  }
  s = s.trim()
  // Greedy match on the LAST `(...)` so types whose params themselves contain
  // parens (none in MySQL today, but defensive) still split cleanly. We
  // anchor the closing paren to end-of-string after the unsigned strip.
  const m = s.match(/^([^()]+?)\s*\((.+)\)\s*$/)
  if (m) {
    return {
      baseType: m[1].trim().toUpperCase(),
      typeParams: m[2].trim(),
      unsigned,
    }
  }
  return { baseType: s.toUpperCase(), typeParams: '', unsigned }
}

/** Reassemble the canonical native-type string from the split fields. */
export function buildNativeType(c: {
  baseType: string
  typeParams: string
  unsigned: boolean
}): string {
  const base = (c.baseType || 'VARCHAR').toUpperCase()
  let s = base
  if (c.typeParams && c.typeParams.trim() !== '') {
    s += `(${c.typeParams.trim()})`
  }
  if (c.unsigned && baseTypeSupportsUnsigned(base)) {
    s += ' UNSIGNED'
  }
  return s
}

/**
 * Canonicalize a native-type string for equality comparison: uppercase, drop
 * whitespace inside parens, normalize the UNSIGNED suffix. So that drafts
 * built from split fields ("DECIMAL(10,2)") compare equal to backend strings
 * the server happens to return as "decimal(10, 2)" or "decimal(10,2) unsigned".
 */
export function normNativeType(s: string): string {
  if (!s) return ''
  const p = parseNativeType(s)
  return buildNativeType(p)
}

/** Whether a base type accepts the UNSIGNED modifier (numeric types only). */
export function baseTypeSupportsUnsigned(base: string): boolean {
  switch ((base || '').toUpperCase()) {
    case 'TINYINT':
    case 'SMALLINT':
    case 'MEDIUMINT':
    case 'INT':
    case 'INTEGER':
    case 'BIGINT':
    case 'DECIMAL':
    case 'NUMERIC':
    case 'FLOAT':
    case 'DOUBLE':
    case 'REAL':
      return true
    default:
      return false
  }
}

/**
 * Categorize the params field for a base type. The UI uses `kind` to pick a
 * placeholder + whether to disable the input, and `supportsUnsigned` to show
 * the UNSIGNED toggle.
 */
export type TypeParamKind =
  | 'length' // VARCHAR(255), CHAR(64), VARBINARY(64), BINARY(16)
  | 'displayWidth' // INT(11), BIT(8) — legacy display width
  | 'precisionScale' // DECIMAL(10,2), NUMERIC(10,2), FLOAT(10,2), DOUBLE(10,2)
  | 'fractionalSeconds' // DATETIME(6), TIMESTAMP(3), TIME(6)
  | 'enumValues' // ENUM('a','b'), SET('a','b')
  | 'none' // TEXT/BLOB/JSON/DATE/YEAR/GEOMETRY etc.

export interface TypeFormat {
  kind: TypeParamKind
  supportsUnsigned: boolean
  placeholder: string
  /** Whether typeParams is required for the type to be valid (e.g. VARCHAR). */
  paramsRequired: boolean
}

export function typeFormatFor(base: string): TypeFormat {
  const supportsUnsigned = baseTypeSupportsUnsigned(base)
  switch ((base || '').toUpperCase()) {
    case 'VARCHAR':
    case 'VARBINARY':
      return { kind: 'length', supportsUnsigned, placeholder: '长度', paramsRequired: true }
    case 'CHAR':
    case 'BINARY':
      return { kind: 'length', supportsUnsigned, placeholder: '长度', paramsRequired: false }
    case 'TINYINT':
    case 'SMALLINT':
    case 'MEDIUMINT':
    case 'INT':
    case 'INTEGER':
    case 'BIGINT':
      return { kind: 'displayWidth', supportsUnsigned, placeholder: '宽度', paramsRequired: false }
    case 'BIT':
      return { kind: 'displayWidth', supportsUnsigned, placeholder: '位数', paramsRequired: false }
    case 'DECIMAL':
    case 'NUMERIC':
    case 'FLOAT':
    case 'DOUBLE':
    case 'REAL':
      return { kind: 'precisionScale', supportsUnsigned, placeholder: '精度,小数', paramsRequired: false }
    case 'DATETIME':
    case 'TIMESTAMP':
    case 'TIME':
      return { kind: 'fractionalSeconds', supportsUnsigned, placeholder: '秒精度', paramsRequired: false }
    case 'ENUM':
    case 'SET':
      return { kind: 'enumValues', supportsUnsigned, placeholder: "'a','b'", paramsRequired: true }
    default:
      return { kind: 'none', supportsUnsigned, placeholder: '—', paramsRequired: false }
  }
}

/**
 * Grouped catalog of base types for the type-select dropdown. Order matters:
 * the first option of the first group is what newly-created columns default to.
 */
export const BASE_TYPE_GROUPS: { label: string; types: string[] }[] = [
  {
    label: '字符串',
    types: ['VARCHAR', 'CHAR', 'TEXT', 'TINYTEXT', 'MEDIUMTEXT', 'LONGTEXT'],
  },
  {
    label: '整数',
    types: ['INT', 'BIGINT', 'TINYINT', 'SMALLINT', 'MEDIUMINT'],
  },
  {
    label: '小数',
    types: ['DECIMAL', 'FLOAT', 'DOUBLE'],
  },
  {
    label: '日期时间',
    types: ['DATETIME', 'TIMESTAMP', 'DATE', 'TIME', 'YEAR'],
  },
  {
    label: '二进制',
    types: ['BINARY', 'VARBINARY', 'BLOB', 'TINYBLOB', 'MEDIUMBLOB', 'LONGBLOB'],
  },
  {
    label: '其他',
    types: ['JSON', 'BIT', 'ENUM', 'SET', 'GEOMETRY'],
  },
]

/**
 * One column inside an index draft. `order` is "ASC" / "DESC" / "" — empty
 * means the user hasn't picked a direction, which in MySQL maps to "NONE"
 * (omits the sort modifier so the engine picks the default, typically ASC).
 */
export interface IndexColumnDraft {
  name: string
  order: string
}

export interface IndexDraft {
  _key: string
  origName: string

  name: string
  columns: IndexColumnDraft[]
  unique: boolean
  /** PRIMARY indexes are handled by the PK pipeline, not here. */
  primary: boolean
  /** BTREE / HASH / FULLTEXT — empty string falls back to default BTREE. */
  type: string
  /** Index COMMENT clause; empty string = no clause. */
  comment: string
}

export interface ForeignKeyDraft {
  _key: string
  origName: string

  name: string
  columns: string[]
  referencedSchema: string
  referencedTable: string
  referencedColumns: string[]
  onUpdate: string
  onDelete: string
}

export interface TableOptionsDraft {
  comment: string
}

export interface StructureDraft {
  columns: ColumnDraft[]
  indexes: IndexDraft[]
  foreignKeys: ForeignKeyDraft[]
  options: TableOptionsDraft
}

// ---- snapshot → draft -----------------------------------------------------

let _keySeq = 0
const nextKey = () => `k${++_keySeq}`

export function columnToDraft(c: ColumnMeta, pos: number): ColumnDraft {
  const parsed = parseNativeType(c.nativeType ?? '')
  return {
    _key: nextKey(),
    origName: c.name,
    origPos: pos,
    name: c.name,
    baseType: parsed.baseType,
    typeParams: parsed.typeParams,
    unsigned: parsed.unsigned,
    nullable: !!c.nullable,
    default: c.default == null ? undefined : c.default,
    isPrimaryKey: !!c.isPrimaryKey,
    isAutoIncrement: !!c.isAutoIncrement,
    comment: c.comment ?? '',
  }
}

export function indexToDraft(ix: IndexInfo): IndexDraft {
  return {
    _key: nextKey(),
    origName: ix.name,
    name: ix.name,
    columns: (ix.columns ?? []).map((c) => ({
      name: c.name,
      order: (c.order ?? '').toUpperCase(),
    })),
    unique: !!ix.unique,
    primary: !!ix.primary,
    type: ix.type ?? '',
    comment: ix.comment ?? '',
  }
}

export function foreignKeyToDraft(fk: ForeignKeyInfo): ForeignKeyDraft {
  return {
    _key: nextKey(),
    origName: fk.name,
    name: fk.name,
    columns: [...(fk.columns ?? [])],
    referencedSchema: fk.referencedSchema ?? '',
    referencedTable: fk.referencedTable ?? '',
    referencedColumns: [...(fk.referencedColumns ?? [])],
    onUpdate: fk.onUpdate ?? '',
    onDelete: fk.onDelete ?? '',
  }
}

export function summaryToDraft(s: TableSummary, comment: string): StructureDraft {
  return {
    columns: (s.columns ?? []).map((c, i) => columnToDraft(c, i)),
    indexes: (s.indexes ?? []).map(indexToDraft),
    foreignKeys: (s.foreignKeys ?? []).map(foreignKeyToDraft),
    options: { comment },
  }
}

export function emptyColumnDraft(): ColumnDraft {
  return {
    _key: nextKey(),
    origName: '',
    origPos: -1,
    name: '',
    baseType: 'VARCHAR',
    typeParams: '255',
    unsigned: false,
    nullable: true,
    default: undefined,
    isPrimaryKey: false,
    isAutoIncrement: false,
    comment: '',
  }
}

export function emptyIndexDraft(): IndexDraft {
  return {
    _key: nextKey(),
    origName: '',
    name: '',
    columns: [],
    unique: false,
    primary: false,
    type: '',
    comment: '',
  }
}

export function emptyForeignKeyDraft(): ForeignKeyDraft {
  return {
    _key: nextKey(),
    origName: '',
    name: '',
    columns: [],
    referencedSchema: '',
    referencedTable: '',
    referencedColumns: [],
    onUpdate: '',
    onDelete: '',
  }
}

// ---- column definition formatting -----------------------------------------

/**
 * Format a column definition fragment (everything after the column name, used
 * verbatim in ADD / MODIFY / CHANGE). Skips PRIMARY KEY — that's emitted as a
 * separate constraint.
 */
export function columnDefBody(c: ColumnDraft): string {
  const parts: string[] = []
  parts.push(buildNativeType(c) || 'VARCHAR(255)')
  parts.push(c.nullable ? 'NULL' : 'NOT NULL')
  if (c.default !== undefined) {
    parts.push(`DEFAULT ${formatDefaultExpr(c.default)}`)
  }
  if (c.isAutoIncrement) parts.push('AUTO_INCREMENT')
  if (c.comment) parts.push(`COMMENT ${quoteString(c.comment)}`)
  return parts.join(' ')
}

function fullColumnDef(c: ColumnDraft): string {
  return `${quoteIdent(c.name)} ${columnDefBody(c)}`
}

// ---- column diff -----------------------------------------------------------

function columnDefBodiesEqual(a: ColumnDraft, b: ColumnMeta): boolean {
  // Compare every non-name, non-position attribute that ends up in the DDL.
  // We compare the *built* native type so cosmetic param whitespace differences
  // (e.g. "decimal(10, 2)" vs "decimal(10,2)") in user input don't generate a
  // bogus MODIFY when the column hasn't really changed.
  if (normNativeType(buildNativeType(a)) !== normNativeType(b.nativeType ?? '')) return false
  if (!!a.nullable !== !!b.nullable) return false
  const ad = a.default ?? null
  const bd = b.default ?? null
  if (ad !== bd) return false
  if (!!a.isAutoIncrement !== !!b.isAutoIncrement) return false
  if ((a.comment ?? '') !== (b.comment ?? '')) return false
  return true
}

interface ColumnDiff {
  /** Statements that mutate columns (ADD / DROP / CHANGE / MODIFY). */
  columnStmts: string[]
  /** PRIMARY KEY DROP / ADD pair (separate so callers can group). */
  pkStmts: string[]
}

export function diffColumns(
  orig: ColumnMeta[],
  draft: ColumnDraft[],
  fq: string,
): ColumnDiff {
  const origByName = new Map<string, { col: ColumnMeta; pos: number }>()
  orig.forEach((c, i) => origByName.set(c.name, { col: c, pos: i }))

  const draftByOrigName = new Map<string, ColumnDraft>()
  for (const c of draft) {
    if (c.origName) draftByOrigName.set(c.origName, c)
  }

  const stmts: string[] = []

  // ---- DROP -----------------------------------------------------------------
  // A column is dropped when its original name is not claimed by any draft
  // row (neither as origName nor as the new name of a renamed row).
  for (const c of orig) {
    if (!draftByOrigName.has(c.name)) {
      stmts.push(`ALTER TABLE ${fq} DROP COLUMN ${quoteIdent(c.name)};`)
    }
  }

  // Build the post-drop "surviving" order list so we can emit accurate
  // AFTER clauses. Surviving = column in draft whose origName matches some
  // remaining original column.
  const survivingDraftIdx: number[] = []
  draft.forEach((c, i) => {
    if (c.origName && origByName.has(c.origName)) survivingDraftIdx.push(i)
  })
  // Build the original order *restricted* to surviving names so we can tell
  // whether a survivor's previous-column changed.
  const survivingOrigOrder: string[] = orig
    .filter((c) => draftByOrigName.has(c.name))
    .map((c) => c.name)

  // ---- ADD / CHANGE / MODIFY -----------------------------------------------
  // Walk the draft in its final order; that order also tells us each column's
  // "previous column" for the AFTER clause.
  let prevName: string | null = null
  for (let i = 0; i < draft.length; i++) {
    const d = draft[i]
    const trimmedName = d.name.trim()
    if (!trimmedName) {
      // Skip rows with blank names — the user hasn't finished typing.
      // We DO update prevName: it remains the previous non-blank name so a
      // later non-blank row's AFTER clause doesn't latch onto a blank id.
      continue
    }

    const positional = positionalClause(prevName)

    if (!d.origName) {
      // Brand-new column.
      stmts.push(
        `ALTER TABLE ${fq} ADD COLUMN ${fullColumnDef(d)}${positional};`,
      )
    } else {
      const origEntry = origByName.get(d.origName)
      if (!origEntry) {
        // origName was set but doesn't match anything (shouldn't normally
        // happen — defensive). Treat as new.
        stmts.push(
          `ALTER TABLE ${fq} ADD COLUMN ${fullColumnDef(d)}${positional};`,
        )
      } else {
        const renamed = d.origName !== trimmedName
        const bodyChanged = !columnDefBodiesEqual(d, origEntry.col)
        const moved = positionChanged(
          d.origName,
          trimmedName,
          survivingDraftIdx,
          survivingOrigOrder,
          draft,
        )
        if (renamed) {
          // CHANGE handles both rename and any def change at once.
          stmts.push(
            `ALTER TABLE ${fq} CHANGE COLUMN ${quoteIdent(d.origName)} ${fullColumnDef(d)}${moved ? positional : ''};`,
          )
        } else if (bodyChanged || moved) {
          stmts.push(
            `ALTER TABLE ${fq} MODIFY COLUMN ${fullColumnDef(d)}${moved ? positional : ''};`,
          )
        }
      }
    }
    prevName = trimmedName
  }

  // ---- PRIMARY KEY ----------------------------------------------------------
  const origPK = orig.filter((c) => c.isPrimaryKey).map((c) => c.name)
  const draftPK = draft
    .filter((c) => c.isPrimaryKey && c.name.trim() !== '')
    .map((c) => c.name.trim())
  const pkStmts: string[] = []
  if (!arraysEqual(origPK, draftPK)) {
    if (origPK.length > 0) {
      pkStmts.push(`ALTER TABLE ${fq} DROP PRIMARY KEY;`)
    }
    if (draftPK.length > 0) {
      pkStmts.push(
        `ALTER TABLE ${fq} ADD PRIMARY KEY (${draftPK.map(quoteIdent).join(', ')});`,
      )
    }
  }

  return { columnStmts: stmts, pkStmts }
}

/** Compose the positional clause (`FIRST` / `AFTER \`prev\``) for a draft column. */
function positionalClause(prevName: string | null): string {
  if (prevName === null) return ' FIRST'
  return ` AFTER ${quoteIdent(prevName)}`
}

/**
 * Whether `name` moved relative to the surviving-only original order. We only
 * emit AFTER on MODIFY when this returns true — unmoved columns don't need a
 * positional clause.
 */
function positionChanged(
  origName: string,
  newName: string,
  survivingDraftIdx: number[],
  survivingOrigOrder: string[],
  draft: ColumnDraft[],
): boolean {
  const finalIdx = survivingDraftIdx.findIndex((i) => draft[i].origName === origName)
  const origIdx = survivingOrigOrder.indexOf(origName)
  if (finalIdx < 0 || origIdx < 0) return false
  // Previous column in surviving-final-order:
  const prevFinal =
    finalIdx === 0 ? null : draft[survivingDraftIdx[finalIdx - 1]].origName
  // Previous column in surviving-original-order:
  const prevOrig = origIdx === 0 ? null : survivingOrigOrder[origIdx - 1]
  return prevFinal !== prevOrig
}

function arraysEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
  return true
}

// ---- index diff -----------------------------------------------------------

function indexFromDraft(d: IndexDraft, fq: string): string {
  // Filter out blank column rows the user added but didn't fill in.
  const cols = (d.columns ?? []).filter((c) => c.name.trim() !== '')
  if (!d.name.trim() || cols.length === 0) return ''
  const colSpec = cols
    .map((c) => {
      const dir = (c.order ?? '').toUpperCase()
      const suffix = dir === 'ASC' || dir === 'DESC' ? ` ${dir}` : ''
      return `${quoteIdent(c.name.trim())}${suffix}`
    })
    .join(', ')
  const kw = d.unique ? 'UNIQUE INDEX' : 'INDEX'
  const using = d.type && d.type.toUpperCase() !== 'BTREE' ? ` USING ${d.type.toUpperCase()}` : ''
  const comment = d.comment && d.comment.trim() !== '' ? ` COMMENT ${quoteString(d.comment)}` : ''
  return `ALTER TABLE ${fq} ADD ${kw} ${quoteIdent(d.name.trim())} (${colSpec})${using}${comment};`
}

function indexColumnsEqual(a: IndexColumnDraft[], b: { name: string; order?: string }[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    if (a[i].name !== b[i].name) return false
    const ao = (a[i].order ?? '').toUpperCase()
    const bo = (b[i].order ?? '').toUpperCase()
    if (ao !== bo) return false
  }
  return true
}

function indexesEqual(a: IndexDraft, b: IndexInfo): boolean {
  if (a.name !== b.name) return false
  if (!!a.unique !== !!b.unique) return false
  if ((a.type ?? '').toUpperCase() !== (b.type ?? '').toUpperCase()) return false
  if ((a.comment ?? '') !== (b.comment ?? '')) return false
  if (!indexColumnsEqual(a.columns, b.columns ?? [])) return false
  return true
}

export function diffIndexes(
  orig: IndexInfo[],
  draft: IndexDraft[],
  fq: string,
): string[] {
  // PRIMARY is handled in diffColumns' PK pipeline — filter it out here.
  const origNonPK = orig.filter((ix) => !ix.primary)
  const draftNonPK = draft.filter((ix) => !ix.primary)

  const origByName = new Map<string, IndexInfo>()
  origNonPK.forEach((ix) => origByName.set(ix.name, ix))
  const draftByOrigName = new Map<string, IndexDraft>()
  draftNonPK.forEach((d) => {
    if (d.origName) draftByOrigName.set(d.origName, d)
  })

  const drops: string[] = []
  const adds: string[] = []

  // DROP: original indexes whose name is no longer claimed by any draft row.
  for (const ix of origNonPK) {
    if (!draftByOrigName.has(ix.name)) {
      drops.push(`ALTER TABLE ${fq} DROP INDEX ${quoteIdent(ix.name)};`)
    }
  }

  // ADD: every draft row whose definition differs from its original counterpart.
  for (const d of draftNonPK) {
    const filledCols = (d.columns ?? []).filter((c) => c.name.trim() !== '')
    if (!d.name.trim() || filledCols.length === 0) continue
    if (!d.origName) {
      // brand-new index
      const stmt = indexFromDraft(d, fq)
      if (stmt) adds.push(stmt)
      continue
    }
    const orig = origByName.get(d.origName)
    if (!orig) continue
    // If anything changed (name/cols/unique/type), DROP + ADD.
    if (!indexesEqual(d, orig)) {
      drops.push(`ALTER TABLE ${fq} DROP INDEX ${quoteIdent(d.origName)};`)
      const stmt = indexFromDraft(d, fq)
      if (stmt) adds.push(stmt)
    }
  }
  // Group: drops first (so we can re-add with the same name), then adds.
  return [...drops, ...adds]
}

// ---- foreign-key diff -----------------------------------------------------

function fkFromDraft(d: ForeignKeyDraft, fq: string): string {
  if (!d.name.trim() || d.columns.length === 0 || !d.referencedTable.trim() || d.referencedColumns.length === 0) {
    return ''
  }
  const cols = d.columns.map(quoteIdent).join(', ')
  const refCols = d.referencedColumns.map(quoteIdent).join(', ')
  const refTable = quoteTable(d.referencedSchema, d.referencedTable)
  let stmt = `ALTER TABLE ${fq} ADD CONSTRAINT ${quoteIdent(d.name.trim())} FOREIGN KEY (${cols}) REFERENCES ${refTable} (${refCols})`
  if (d.onUpdate && d.onUpdate.toUpperCase() !== 'RESTRICT') {
    stmt += ` ON UPDATE ${d.onUpdate.toUpperCase()}`
  }
  if (d.onDelete && d.onDelete.toUpperCase() !== 'RESTRICT') {
    stmt += ` ON DELETE ${d.onDelete.toUpperCase()}`
  }
  return stmt + ';'
}

function fkEqual(a: ForeignKeyDraft, b: ForeignKeyInfo): boolean {
  if (a.name !== b.name) return false
  if (!arraysEqual(a.columns, b.columns ?? [])) return false
  if ((a.referencedSchema ?? '') !== (b.referencedSchema ?? '')) return false
  if ((a.referencedTable ?? '') !== (b.referencedTable ?? '')) return false
  if (!arraysEqual(a.referencedColumns, b.referencedColumns ?? [])) return false
  // MySQL reports RESTRICT as the absence of an ON UPDATE/DELETE clause; treat
  // empty and RESTRICT the same.
  const norm = (s: string | undefined) => {
    const u = (s ?? '').toUpperCase()
    return u === '' ? 'RESTRICT' : u
  }
  if (norm(a.onUpdate) !== norm(b.onUpdate)) return false
  if (norm(a.onDelete) !== norm(b.onDelete)) return false
  return true
}

export function diffForeignKeys(
  orig: ForeignKeyInfo[],
  draft: ForeignKeyDraft[],
  fq: string,
): string[] {
  const origByName = new Map<string, ForeignKeyInfo>()
  orig.forEach((fk) => origByName.set(fk.name, fk))
  const draftByOrigName = new Map<string, ForeignKeyDraft>()
  draft.forEach((d) => {
    if (d.origName) draftByOrigName.set(d.origName, d)
  })

  const drops: string[] = []
  const adds: string[] = []

  for (const fk of orig) {
    if (!draftByOrigName.has(fk.name)) {
      drops.push(`ALTER TABLE ${fq} DROP FOREIGN KEY ${quoteIdent(fk.name)};`)
    }
  }
  for (const d of draft) {
    if (!d.name.trim()) continue
    if (!d.origName) {
      const stmt = fkFromDraft(d, fq)
      if (stmt) adds.push(stmt)
      continue
    }
    const orig = origByName.get(d.origName)
    if (!orig) continue
    if (!fkEqual(d, orig)) {
      drops.push(`ALTER TABLE ${fq} DROP FOREIGN KEY ${quoteIdent(d.origName)};`)
      const stmt = fkFromDraft(d, fq)
      if (stmt) adds.push(stmt)
    }
  }
  return [...drops, ...adds]
}

// ---- table comment / options diff -----------------------------------------

export function diffOptions(
  origComment: string,
  draft: TableOptionsDraft,
  fq: string,
): string[] {
  const stmts: string[] = []
  if ((origComment ?? '') !== (draft.comment ?? '')) {
    stmts.push(`ALTER TABLE ${fq} COMMENT = ${quoteString(draft.comment ?? '')};`)
  }
  return stmts
}

// ---- DDL parsing (read-only) ----------------------------------------------

/**
 * Extract the table COMMENT from a MySQL `SHOW CREATE TABLE` output. Returns
 * '' when no comment clause is present. Handles the doubled-single-quote
 * escape MySQL emits inside the COMMENT='…' literal.
 *
 * We deliberately match only on the trailing table-options portion (after the
 * last closing paren) so a COMMENT='…' on a column definition can't be picked
 * up by mistake.
 */
export function parseTableCommentFromDDL(ddl: string): string {
  if (!ddl) return ''
  const tail = ddl.slice(ddl.lastIndexOf(')'))
  const m = tail.match(/\bCOMMENT\s*=\s*'((?:[^']|'')*)'/)
  if (!m) return ''
  return m[1].replace(/''/g, "'")
}

// ---- top-level: build all alter statements grouped by tab -----------------

export interface AlterPlan {
  /** Column-tab statements: DROP/ADD/MODIFY/CHANGE plus PRIMARY KEY pair. */
  columns: string[]
  /** Index-tab statements (excluding PK). */
  indexes: string[]
  /** Foreign-key-tab statements. */
  foreignKeys: string[]
  /** Options-tab statements (currently table comment only). */
  options: string[]
  /** Concatenation in safe-execution order. */
  all: string[]
}

export interface BuildAlterPlanInput {
  db: string
  table: string
  origSummary: TableSummary
  origComment: string
  draft: StructureDraft
}

export function buildAlterPlan({
  db,
  table,
  origSummary,
  origComment,
  draft,
}: BuildAlterPlanInput): AlterPlan {
  const fq = quoteTable(db, table)
  const colDiff = diffColumns(origSummary.columns ?? [], draft.columns, fq)
  const indexes = diffIndexes(origSummary.indexes ?? [], draft.indexes, fq)
  const foreignKeys = diffForeignKeys(origSummary.foreignKeys ?? [], draft.foreignKeys, fq)
  const options = diffOptions(origComment, draft.options, fq)

  // Column tab shows column-edits + PK changes.
  const columnsTab = [...colDiff.columnStmts, ...colDiff.pkStmts]
  // Execution order for "Apply all": columns + PK first (so indexes/FKs can
  // reference the new shape), then indexes, then FKs, then options.
  const all = [
    ...colDiff.columnStmts,
    ...colDiff.pkStmts,
    ...indexes,
    ...foreignKeys,
    ...options,
  ]
  return { columns: columnsTab, indexes, foreignKeys, options, all }
}
