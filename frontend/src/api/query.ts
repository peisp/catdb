// api/query — façade for QueryService bindings.
//
// Cancellation: every method returns a Promise wrapping a CancellablePromise
// from the bindings. Pass an AbortSignal and we hook cancel() so the Go ctx
// gets cancelled when the front-end calls .abort().
import { QueryService } from '../../bindings/catdb/internal/services'
import type {
  QueryBatchResult as BoundQueryBatchResult,
  QueryOptions as BoundQueryOptions,
  QueryRunResult as BoundQueryRunResult,
} from '../../bindings/catdb/internal/services/models'
import type { Capabilities as BoundCapabilities } from '../../bindings/catdb/internal/dbdriver/models'

export type QueryRunResult = BoundQueryRunResult
export type QueryBatchResult = BoundQueryBatchResult
export type QueryOptions = BoundQueryOptions
export type Capabilities = BoundCapabilities

function bindSignal<T>(p: PromiseLike<T> & { cancel?: () => void }, signal?: AbortSignal): Promise<T> {
  if (!signal) return Promise.resolve(p as PromiseLike<T>)
  if (signal.aborted) p.cancel?.()
  else signal.addEventListener('abort', () => p.cancel?.(), { once: true })
  return Promise.resolve(p as PromiseLike<T>)
}

export function runQuery(
  connId: string,
  sql: string,
  opts: Partial<QueryOptions> = {},
  signal?: AbortSignal,
): Promise<QueryRunResult> {
  const p = QueryService.RunQuery(connId, sql, opts as QueryOptions)
  return bindSignal(p, signal)
}

export function fetchMore(handle: string, batch = 500, signal?: AbortSignal): Promise<QueryBatchResult> {
  const p = QueryService.FetchMore(handle, batch)
  return bindSignal(p, signal)
}

export function closeHandle(handle: string): Promise<void> {
  return QueryService.Close(handle)
}

export function explain(connId: string, sql: string, signal?: AbortSignal): Promise<QueryRunResult> {
  const p = QueryService.Explain(connId, sql)
  return bindSignal(p, signal)
}

export function capabilitiesFor(driverName: string): Promise<Capabilities> {
  return QueryService.CapabilitiesFor(driverName)
}
