// stores/query — owns the multi-tab query workspace state.
//
// One QueryTab per editor tab. Tabs are scoped to a *connection*; switching
// the active connection in the sidebar swaps which set of tabs is visible.
// Per-tab state holds the SQL text, current run handle, columns + rows
// buffer, status, and the AbortController of any in-flight call.
//
// Cancellation rule (ARCHITECTURE.md §4.2): when the user presses Cancel
// we abort the controller; that triggers .cancel() on the bound promise,
// which Wails routes to Go ctx, which routes to driver.QueryContext.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { query as queryApi } from '../api'
import type {
  Capabilities,
  QueryBatchResult,
  QueryOptions,
  QueryRunResult,
} from '../api/query'

export type QueryStatus = 'idle' | 'running' | 'done' | 'error' | 'canceled'

/**
 * Tab kinds:
 *   - 'query': SQL editor + result table
 *   - 'table': data browser for `db.table`
 *   - 'structure': structure viewer for `db.table`
 *   - 'tables-overview': all tables in a database/schema
 */
export type TabKind = 'query' | 'table' | 'structure' | 'tables-overview'

let tabSeq = 0
function nextTabId(): string {
  tabSeq += 1
  return 'tab-' + tabSeq
}

export type QueryColumn = QueryRunResult['columns'][number]

export interface QueryTab {
  id: string
  connId: string
  title: string
  kind: TabKind
  sql: string

  // For 'table' / 'structure' kinds, the object reference.
  db?: string
  table?: string

  // result state (used by 'query' kind only)
  handle: string | null
  columns: QueryColumn[]
  rows: any[][]
  rowsTotal: number
  done: boolean
  truncated: boolean
  isResultSet: boolean
  elapsedMs: number
  execAffected: number | null
  execLastInsertId: number | null

  status: QueryStatus
  errorMessage: string

  // in-flight controller; null when idle
  controller: AbortController | null
  fetching: boolean
}

function freshTab(connId: string, opts?: { kind?: TabKind; title?: string; db?: string; table?: string }): QueryTab {
  return {
    id: nextTabId(),
    connId,
    kind: opts?.kind ?? 'query',
    title: opts?.title ?? 'Query',
    db: opts?.db,
    table: opts?.table,
    sql: '',
    handle: null,
    columns: [],
    rows: [],
    rowsTotal: 0,
    done: false,
    truncated: false,
    isResultSet: false,
    elapsedMs: 0,
    execAffected: null,
    execLastInsertId: null,
    status: 'idle',
    errorMessage: '',
    controller: null,
    fetching: false,
  }
}

export const useQueryStore = defineStore('query', () => {
  // tabs keyed by id; ordered list maintained separately for tab strip order
  const tabs = ref<QueryTab[]>([])
  // active tab per connection
  const activeByConn = ref<Record<string, string>>({})
  // capabilities cache keyed by driver name
  const capsByDriver = ref<Record<string, Capabilities>>({})

  function tabsForConn(connId: string): QueryTab[] {
    return tabs.value.filter((t) => t.connId === connId)
  }
  function getTab(id: string): QueryTab | undefined {
    return tabs.value.find((t) => t.id === id)
  }
  function activeTab(connId: string): QueryTab | undefined {
    const id = activeByConn.value[connId]
    return id ? getTab(id) : undefined
  }
  function setActive(connId: string, id: string) {
    activeByConn.value = { ...activeByConn.value, [connId]: id }
  }

  function addTab(connId: string, opts?: { sql?: string; title?: string; kind?: TabKind; db?: string; table?: string }): QueryTab {
    const t = freshTab(connId, { kind: opts?.kind, title: opts?.title, db: opts?.db, table: opts?.table })
    if (opts?.sql) t.sql = opts.sql
    tabs.value.push(t)
    setActive(connId, t.id)
    return t
  }

  function openTableTab(connId: string, db: string, table: string, kind: 'table' | 'structure' = 'table'): QueryTab {
    const titlePrefix = kind === 'structure' ? '⚙' : '⊞'
    const existing = tabs.value.find(
      (t) => t.connId === connId && t.kind === kind && t.db === db && t.table === table,
    )
    if (existing) {
      setActive(connId, existing.id)
      return existing
    }
    return addTab(connId, {
      kind,
      db,
      table,
      title: `${titlePrefix} ${db}.${table}`,
    })
  }

  function openTablesOverviewTab(connId: string, db: string): QueryTab {
    const existing = tabs.value.find(
      (t) => t.connId === connId && t.kind === 'tables-overview' && t.db === db,
    )
    if (existing) {
      setActive(connId, existing.id)
      return existing
    }
    return addTab(connId, {
      kind: 'tables-overview',
      db,
      title: `📋 ${db}`,
    })
  }

  async function closeTab(id: string) {
    const t = getTab(id)
    if (!t) return
    t.controller?.abort()
    if (t.handle) {
      try { await queryApi.closeHandle(t.handle) } catch { /* idempotent */ }
    }
    const connId = t.connId
    tabs.value = tabs.value.filter((x) => x.id !== id)
    if (activeByConn.value[connId] === id) {
      const remaining = tabsForConn(connId)
      if (remaining.length) setActive(connId, remaining[remaining.length - 1].id)
      else delete activeByConn.value[connId]
    }
  }

  async function closeAllForConn(connId: string) {
    const ids = tabsForConn(connId).map((t) => t.id)
    for (const id of ids) {
      await closeTab(id)
    }
  }

  function resetResult(t: QueryTab) {
    t.handle = null
    t.columns = []
    t.rows = []
    t.rowsTotal = 0
    t.done = false
    t.truncated = false
    t.isResultSet = false
    t.elapsedMs = 0
    t.execAffected = null
    t.execLastInsertId = null
    t.errorMessage = ''
  }

  function applyRun(t: QueryTab, res: QueryRunResult) {
    t.columns = (res.columns ?? []) as QueryColumn[]
    t.rows = (res.rows ?? []) as any[][]
    t.rowsTotal = res.rowsTotal ?? 0
    t.done = !!res.done
    t.truncated = !!res.truncated
    t.elapsedMs = Number(res.elapsedMs ?? 0)
    t.isResultSet = !!res.isResultSet
    t.handle = res.handle ?? null
    if (res.execResult) {
      t.execAffected = res.execResult.rowsAffected ?? 0
      t.execLastInsertId = res.execResult.lastInsertId ?? 0
    }
    t.status = 'done'
  }

  function applyBatch(t: QueryTab, b: QueryBatchResult) {
    if (b.rows?.length) {
      t.rows = t.rows.concat(b.rows)
    }
    t.rowsTotal = b.rowsTotal ?? t.rowsTotal
    if (b.done) {
      t.done = true
      t.handle = null
    }
    if (b.truncated) t.truncated = true
  }

  async function runActive(tabId: string, options: Partial<QueryOptions> = {}) {
    const t = getTab(tabId)
    if (!t) return
    if (t.status === 'running') return
    if (t.handle) {
      try { await queryApi.closeHandle(t.handle) } catch { /* ignore */ }
      t.handle = null
    }
    resetResult(t)
    t.status = 'running'
    const ctrl = new AbortController()
    t.controller = ctrl
    try {
      const res = await queryApi.runQuery(t.connId, t.sql, options, ctrl.signal)
      if (ctrl.signal.aborted) {
        t.status = 'canceled'
        t.errorMessage = 'canceled by user'
        return
      }
      applyRun(t, res)
    } catch (e: any) {
      if (ctrl.signal.aborted) {
        t.status = 'canceled'
        t.errorMessage = 'canceled by user'
      } else {
        t.status = 'error'
        t.errorMessage = formatError(e)
      }
    } finally {
      t.controller = null
    }
  }

  async function fetchMore(tabId: string, batch = 500): Promise<boolean> {
    const t = getTab(tabId)
    if (!t || !t.handle || t.done || t.fetching) return false
    t.fetching = true
    const ctrl = new AbortController()
    t.controller = ctrl
    try {
      const res = await queryApi.fetchMore(t.handle, batch, ctrl.signal)
      applyBatch(t, res)
      return !t.done
    } catch (e: any) {
      if (!ctrl.signal.aborted) {
        t.status = 'error'
        t.errorMessage = formatError(e)
      }
      return false
    } finally {
      t.controller = null
      t.fetching = false
    }
  }

  async function cancel(tabId: string) {
    const t = getTab(tabId)
    if (!t || !t.controller) return
    t.controller.abort()
  }

  async function explain(tabId: string, options: Partial<QueryOptions> = {}) {
    const t = getTab(tabId)
    if (!t) return
    if (t.status === 'running') return
    resetResult(t)
    t.status = 'running'
    const ctrl = new AbortController()
    t.controller = ctrl
    try {
      const res = await queryApi.explain(t.connId, t.sql, options, ctrl.signal)
      applyRun(t, res)
    } catch (e: any) {
      if (ctrl.signal.aborted) {
        t.status = 'canceled'
        t.errorMessage = 'canceled by user'
      } else {
        t.status = 'error'
        t.errorMessage = formatError(e)
      }
    } finally {
      t.controller = null
    }
  }

  async function loadCapabilities(driver: string): Promise<Capabilities> {
    const cached = capsByDriver.value[driver]
    if (cached) return cached
    const caps = await queryApi.capabilitiesFor(driver)
    capsByDriver.value = { ...capsByDriver.value, [driver]: caps }
    return caps
  }

  const totalTabs = computed(() => tabs.value.length)

  return {
    tabs,
    activeByConn,
    capsByDriver,
    totalTabs,
    tabsForConn,
    getTab,
    activeTab,
    setActive,
    addTab,
    openTableTab,
    openTablesOverviewTab,
    closeTab,
    closeAllForConn,
    runActive,
    fetchMore,
    cancel,
    explain,
    loadCapabilities,
  }
})

function formatError(e: any): string {
  if (!e) return 'unknown error'
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  try { return JSON.stringify(e) } catch { return String(e) }
}
