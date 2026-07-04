// api/transfer — facade over TransferService bindings.
import { TransferService } from '../../bindings/catdb/internal/services'
import { TransferFormat } from '../../bindings/catdb/internal/services/models'
import type {
  ExportOptions as BoundExportOptions,
  ExportResult as BoundExportResult,
  ImportOptions as BoundImportOptions,
  ImportResult as BoundImportResult,
  DataTransferRequest as BoundTransferRequest,
  DataTransferResult as BoundTransferResult,
  TableTransferResult as BoundTableResult,
} from '../../bindings/catdb/internal/services/models'
import { on } from './events'

export type ExportOptions = BoundExportOptions
export type ExportResult = BoundExportResult
export type ImportOptions = BoundImportOptions
export type ImportResult = BoundImportResult
export type DataTransferRequest = BoundTransferRequest
export type DataTransferResult = BoundTransferResult
export type TableTransferResult = BoundTableResult
export { TransferFormat }

export type TransferProgress = {
  transferId: string
  rows: number
  done: boolean
  error?: string
}

export function startTransfer(req: DataTransferRequest, signal?: AbortSignal): Promise<DataTransferResult> {
  const p = TransferService.StartTransfer(req)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<DataTransferResult>
}

export function exportQuery(connId: string, sql: string, opts: ExportOptions, signal?: AbortSignal): Promise<ExportResult> {
  const p = TransferService.ExportQuery(connId, sql, opts)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<ExportResult>
}

export function exportTable(connId: string, db: string, table: string, opts: ExportOptions, signal?: AbortSignal, schema = ''): Promise<ExportResult> {
  const p = TransferService.ExportTable(connId, db, schema, table, opts)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<ExportResult>
}

export function importFile(connId: string, opts: ImportOptions, signal?: AbortSignal): Promise<ImportResult> {
  const p = TransferService.ImportFile(connId, opts)
  if (signal) {
    if (signal.aborted) p.cancel?.()
    else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  }
  return p as unknown as Promise<ImportResult>
}

export function onProgress(cb: (p: TransferProgress) => void): () => void {
  return on<TransferProgress>('transfer:progress', cb)
}
