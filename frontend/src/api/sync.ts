// api/sync — facade over SyncService bindings (structure & data sync).
import { SyncService } from '../../bindings/catdb/internal/services'
import type {
  SchemaCompareRequest as BoundCompareRequest,
  SchemaCompareResult as BoundCompareResult,
  SchemaObjectDiff as BoundObjectDiff,
  SchemaSyncExecRequest as BoundExecRequest,
  SchemaSyncExecResult as BoundExecResult,
  DataCompareRequest as BoundDataCompareRequest,
  DataCompareResult as BoundDataCompareResult,
  DataTableDiff as BoundDataTableDiff,
  DataSyncExecRequest as BoundDataExecRequest,
  DataSyncExecResult as BoundDataExecResult,
} from '../../bindings/catdb/internal/services/models'
import { on } from './events'

export type SchemaCompareRequest = BoundCompareRequest
export type SchemaCompareResult = BoundCompareResult
export type SchemaObjectDiff = BoundObjectDiff
export type SchemaSyncExecRequest = BoundExecRequest
export type SchemaSyncExecResult = BoundExecResult

export type SyncProgress = {
  syncId: string
  index: number
  total: number
  error?: string
  done: boolean
}

export function compareSchemas(req: SchemaCompareRequest, signal?: AbortSignal): Promise<SchemaCompareResult> {
  const p = SyncService.CompareSchemas(req)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<SchemaCompareResult>
}

export function executeSchemaSync(req: SchemaSyncExecRequest, signal?: AbortSignal): Promise<SchemaSyncExecResult> {
  const p = SyncService.ExecuteSchemaSync(req)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<SchemaSyncExecResult>
}

/** Subscribe to sync progress events. Returns the unsubscribe function. */
export function onSyncProgress(cb: (p: SyncProgress) => void): () => void {
  return on<SyncProgress>('sync:progress', cb)
}

// ---- data sync ---------------------------------------------------------------

export type DataCompareRequest = BoundDataCompareRequest
export type DataCompareResult = BoundDataCompareResult
export type DataTableDiff = BoundDataTableDiff
export type DataSyncExecRequest = BoundDataExecRequest
export type DataSyncExecResult = BoundDataExecResult

export type DataSyncProgress = {
  syncId: string
  table: string
  inserts: number
  updates: number
  deletes: number
  scannedSource: number
  scannedTarget: number
  done: boolean
}

export function compareData(req: DataCompareRequest, signal?: AbortSignal): Promise<DataCompareResult> {
  const p = SyncService.CompareData(req)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<DataCompareResult>
}

export function executeDataSync(req: DataSyncExecRequest, signal?: AbortSignal): Promise<DataSyncExecResult> {
  const p = SyncService.ExecuteDataSync(req)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<DataSyncExecResult>
}

/** Subscribe to data-sync progress events. Returns the unsubscribe function. */
export function onDataSyncProgress(cb: (p: DataSyncProgress) => void): () => void {
  return on<DataSyncProgress>('sync:data-progress', cb)
}
