import { t } from '../i18n'

// alterPlan — the structure editor's draft model and UI helpers.
//
// The ALTER/CREATE diff engine that used to live here has moved to the Go
// backend (internal/core/schemadiff + Dialect.GenerateAlterTable); components
// call metadata.buildAlterPlan / buildCreateTable instead. What remains is
// pure editing state: draft shapes with stable client-side keys, MySQL
// native-type parsing for the three-field type editor, and the snapshot→draft
// converters.
import type {
  ColumnMeta,
  ForeignKeyInfo,
  IndexInfo,
  TableSummary,
} from '../api/metadata'

// ---- identifier quoting -----------------------------------------------------

/** MySQL identifier quoting — backticks, double any embedded backtick. */
export function quoteIdent(name: string): string {
  return '`' + String(name).replace(/`/g, '``') + '`'
}

/** Compose `db`.`table` form when db is non-empty. */
export function quoteTable(db: string, table: string): string {
  return db ? `${quoteIdent(db)}.${quoteIdent(table)}` : quoteIdent(table)
}

// ---- draft shapes ----------------------------------------------------------
//
// The structure editor keeps a parallel "draft" copy of the table summary
// while the user edits. Drafts carry stable client-side keys (_key) and a
// snapshot of the original name/position so the backend diff can detect
// rename and reorder.

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
      return { kind: 'length', supportsUnsigned, placeholder: t('structure.columns.ph.length'), paramsRequired: true }
    case 'CHAR':
    case 'BINARY':
      return { kind: 'length', supportsUnsigned, placeholder: t('structure.columns.ph.length'), paramsRequired: false }
    case 'TINYINT':
    case 'SMALLINT':
    case 'MEDIUMINT':
    case 'INT':
    case 'INTEGER':
    case 'BIGINT':
      return { kind: 'displayWidth', supportsUnsigned, placeholder: t('structure.columns.ph.width'), paramsRequired: false }
    case 'BIT':
      return { kind: 'displayWidth', supportsUnsigned, placeholder: t('structure.columns.ph.bits'), paramsRequired: false }
    case 'DECIMAL':
    case 'NUMERIC':
    case 'FLOAT':
    case 'DOUBLE':
    case 'REAL':
      return { kind: 'precisionScale', supportsUnsigned, placeholder: t('structure.columns.ph.precisionScale'), paramsRequired: false }
    case 'DATETIME':
    case 'TIMESTAMP':
    case 'TIME':
      return { kind: 'fractionalSeconds', supportsUnsigned, placeholder: t('structure.columns.ph.fractionalSeconds'), paramsRequired: false }
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
 * A function (not a const) so the group labels re-translate on locale switch —
 * call it from a `computed` in the component.
 */
export function baseTypeGroups(): { label: string; types: string[] }[] {
  return [
    {
      label: t('structure.typeGroups.string'),
      types: ['VARCHAR', 'CHAR', 'TEXT', 'TINYTEXT', 'MEDIUMTEXT', 'LONGTEXT'],
    },
    {
      label: t('structure.typeGroups.integer'),
      types: ['INT', 'BIGINT', 'TINYINT', 'SMALLINT', 'MEDIUMINT'],
    },
    {
      label: t('structure.typeGroups.decimal'),
      types: ['DECIMAL', 'FLOAT', 'DOUBLE'],
    },
    {
      label: t('structure.typeGroups.datetime'),
      types: ['DATETIME', 'TIMESTAMP', 'DATE', 'TIME', 'YEAR'],
    },
    {
      label: t('structure.typeGroups.binary'),
      types: ['BINARY', 'VARBINARY', 'BLOB', 'TINYBLOB', 'MEDIUMBLOB', 'LONGBLOB'],
    },
    {
      label: t('structure.typeGroups.other'),
      types: ['JSON', 'BIT', 'ENUM', 'SET', 'GEOMETRY'],
    },
  ]
}

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

// ---- draft → backend wire format -------------------------------------------
//
// The Go diff engine (internal/core/schemadiff) takes a schemadiff.Table.
// draftToWire reassembles each column's canonical native type from the split
// editor fields and strips the client-only keys.

export interface WireColumn {
  origName: string
  name: string
  nativeType: string
  nullable: boolean
  default?: string
  isPrimaryKey: boolean
  isAutoIncrement: boolean
  comment: string
}

export interface WireIndex {
  origName: string
  name: string
  columns: { name: string; order: string }[]
  unique: boolean
  primary: boolean
  type: string
  comment: string
}

export interface WireForeignKey {
  origName: string
  name: string
  columns: string[]
  referencedSchema: string
  referencedTable: string
  referencedColumns: string[]
  onUpdate: string
  onDelete: string
}

export interface WireTable {
  columns: WireColumn[]
  indexes: WireIndex[]
  foreignKeys: WireForeignKey[]
  comment: string
}

export function draftToWire(draft: StructureDraft): WireTable {
  return {
    columns: (draft.columns ?? []).map((c) => ({
      origName: c.origName,
      name: c.name,
      nativeType: buildNativeType(c),
      nullable: c.nullable,
      // undefined key is dropped by JSON serialization → Go nil (no DEFAULT).
      default: c.default,
      isPrimaryKey: c.isPrimaryKey,
      isAutoIncrement: c.isAutoIncrement,
      comment: c.comment ?? '',
    })),
    indexes: (draft.indexes ?? []).map((ix) => ({
      origName: ix.origName,
      name: ix.name,
      columns: (ix.columns ?? []).map((c) => ({ name: c.name, order: c.order ?? '' })),
      unique: ix.unique,
      primary: ix.primary,
      type: ix.type ?? '',
      comment: ix.comment ?? '',
    })),
    foreignKeys: (draft.foreignKeys ?? []).map((fk) => ({
      origName: fk.origName,
      name: fk.name,
      columns: [...fk.columns],
      referencedSchema: fk.referencedSchema ?? '',
      referencedTable: fk.referencedTable ?? '',
      referencedColumns: [...fk.referencedColumns],
      onUpdate: fk.onUpdate ?? '',
      onDelete: fk.onDelete ?? '',
    })),
    comment: draft.options?.comment ?? '',
  }
}

/** The per-tab statement bundle returned by metadata.buildAlterPlan. */
export interface AlterPlan {
  columns: string[]
  indexes: string[]
  foreignKeys: string[]
  options: string[]
  all: string[]
}

export function emptyAlterPlan(): AlterPlan {
  return { columns: [], indexes: [], foreignKeys: [], options: [], all: [] }
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
