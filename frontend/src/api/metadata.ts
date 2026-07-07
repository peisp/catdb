// api/metadata — facade over MetadataService bindings.
import { MetadataService } from '../../bindings/catdb/internal/services'
import type {
  AutocompleteSnapshot as BoundSnap,
  BrowseResult as BoundBrowse,
  TableSummary as BoundSummary,
} from '../../bindings/catdb/internal/services/models'
import {
  LogicalType,
} from '../../bindings/catdb/internal/dbdriver/models'
import type {
  ColumnMeta as BoundColumn,
  ForeignKeyInfo as BoundFK,
  IndexColumn as BoundIndexColumn,
  IndexInfo as BoundIndex,
  RoutineInfo as BoundRoutine,
  TableInfo as BoundTable,
  ViewInfo as BoundView,
} from '../../bindings/catdb/internal/dbdriver/models'

export type TableInfo = BoundTable
export type ViewInfo = BoundView
export type ColumnMeta = BoundColumn
export type IndexColumn = BoundIndexColumn
export type IndexInfo = BoundIndex
export type ForeignKeyInfo = BoundFK
export type RoutineInfo = BoundRoutine
export type TableSummary = BoundSummary
export type BrowseResult = BoundBrowse
export type AutocompleteSnapshot = BoundSnap
export { LogicalType }

export function listDatabases(connId: string): Promise<string[]> {
  return MetadataService.ListDatabases(connId) as unknown as Promise<string[]>
}
export function listSchemas(connId: string, db: string): Promise<string[]> {
  return MetadataService.ListSchemas(connId, db) as unknown as Promise<string[]>
}
export function listTables(connId: string, db: string, schema = ''): Promise<TableInfo[]> {
  return MetadataService.ListTables(connId, db, schema) as unknown as Promise<TableInfo[]>
}
export function listViews(connId: string, db: string, schema = ''): Promise<ViewInfo[]> {
  return MetadataService.ListViews(connId, db, schema) as unknown as Promise<ViewInfo[]>
}
export function listColumns(connId: string, db: string, table: string, schema = ''): Promise<ColumnMeta[]> {
  return MetadataService.ListColumns(connId, db, schema, table) as unknown as Promise<ColumnMeta[]>
}
export function listIndexes(connId: string, db: string, table: string, schema = ''): Promise<IndexInfo[]> {
  return MetadataService.ListIndexes(connId, db, schema, table) as unknown as Promise<IndexInfo[]>
}
export function listForeignKeys(connId: string, db: string, table: string, schema = ''): Promise<ForeignKeyInfo[]> {
  return MetadataService.ListForeignKeys(connId, db, schema, table) as unknown as Promise<ForeignKeyInfo[]>
}
export function listRoutines(connId: string, db: string, schema = ''): Promise<RoutineInfo[]> {
  return MetadataService.ListRoutines(connId, db, schema) as unknown as Promise<RoutineInfo[]>
}
export function getCreateTable(connId: string, db: string, table: string, schema = ''): Promise<string> {
  return MetadataService.GetCreateTable(connId, db, schema, table) as unknown as Promise<string>
}
export function getTableSummary(connId: string, db: string, table: string, schema = ''): Promise<TableSummary> {
  return MetadataService.GetTableSummary(connId, db, schema, table) as unknown as Promise<TableSummary>
}
export function getTableComment(connId: string, db: string, table: string, schema = ''): Promise<string> {
  return MetadataService.GetTableComment(connId, db, schema, table) as unknown as Promise<string>
}
// Database editor (optional per driver — rejects with
// "database-editor-unsupported" when the driver has no support).
export interface CharsetCatalog {
  charsets: { name: string; defaultCollation?: string }[]
  collations: { name: string; charset?: string }[]
}
export interface DatabaseOptions {
  charset?: string
  collation?: string
}
export function listCharsets(connId: string): Promise<CharsetCatalog> {
  return MetadataService.ListCharsets(connId) as unknown as Promise<CharsetCatalog>
}
export function getDatabaseOptions(connId: string, db: string): Promise<DatabaseOptions> {
  return MetadataService.GetDatabaseOptions(connId, db) as unknown as Promise<DatabaseOptions>
}
export function buildCreateDatabase(connId: string, name: string, opts: DatabaseOptions): Promise<string> {
  return MetadataService.BuildCreateDatabase(connId, name, opts as never) as unknown as Promise<string>
}
export function buildAlterDatabase(connId: string, name: string, opts: DatabaseOptions): Promise<string> {
  return MetadataService.BuildAlterDatabase(connId, name, opts as never) as unknown as Promise<string>
}
export function autocompleteFor(connId: string, db: string, schema = ''): Promise<AutocompleteSnapshot> {
  return MetadataService.AutocompleteFor(connId, db, schema) as unknown as Promise<AutocompleteSnapshot>
}
export function countTableRows(
  connId: string, db: string, table: string,
  whereClause = '', schema = '',
): Promise<number> {
  return MetadataService.CountTableRows(connId, db, schema, table, whereClause) as unknown as Promise<number>
}
// buildAlterPlan / buildCreateTable — backend diff engine (schemadiff +
// Dialect). `draft` is the wire shape produced by lib/alterPlan draftToWire.
export interface AlterPlanStatements {
  columns: string[]
  indexes: string[]
  foreignKeys: string[]
  options: string[]
  all: string[]
}
export function buildAlterPlan(
  connId: string, db: string, table: string,
  orig: TableSummary, origComment: string, draft: unknown,
  schema = '',
): Promise<AlterPlanStatements> {
  return MetadataService.BuildAlterPlan(connId, {
    db, schema, table, orig, origComment, draft,
  } as never) as unknown as Promise<AlterPlanStatements>
}
export function buildCreateTable(
  connId: string, db: string, table: string, draft: unknown, schema = '',
): Promise<string> {
  return MetadataService.BuildCreateTable(connId, {
    db, schema, table, draft,
  } as never) as unknown as Promise<string>
}
export function browseTable(
  connId: string, db: string, table: string,
  limit: number, offset: number,
  orderBy?: string, orderDir?: string,
  whereClause?: string, orderByClause?: string,
  schema = '',
): Promise<BrowseResult> {
  return MetadataService.BrowseTable(
    connId, db, schema, table, orderBy ?? '', orderDir ?? '',
    limit, offset,
    whereClause ?? '', orderByClause ?? '',
  ) as unknown as Promise<BrowseResult>
}
