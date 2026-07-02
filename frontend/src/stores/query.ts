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
import { savedQuery as savedQueryApi } from '../api'
import { emit as emitEvent } from '../api/events'
import { openTextPrompt } from '../api/prompts'
import { confirmCloseUnsaved } from '../api/dialogs'
import { t as tr } from '../i18n' // aliased: `t` is the per-tab local var throughout this store
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
 *   - 'new-table': structure editor in "create" mode (db known, table tbd)
 *   - 'tables-overview': all tables in a database/schema
 */
export type TabKind = 'query' | 'table' | 'structure' | 'new-table' | 'tables-overview'

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

  // Pinned tabs are non-closable and always sort first. Used for the
  // per-connection "database overview" tab — there is at most one
  // pinned tab per connection.
  pinned?: boolean

  // For 'table' / 'structure' kinds, the object reference.
  db?: string
  table?: string

  // For 'query' kind: the saved_query id this tab is bound to, if it was
  // opened from / saved into the object tree's 「查询」 group. Undefined for
  // ad-hoc query tabs that have never been saved.
  savedQueryId?: string
  // Baseline SQL as last persisted/loaded — the tab is "dirty" when `sql`
  // diverges from this. '' for a fresh blank tab, so any typed SQL counts.
  savedSql?: string

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

function freshTab(connId: string, opts?: { kind?: TabKind; title?: string; db?: string; table?: string; pinned?: boolean }): QueryTab {
  return {
    id: nextTabId(),
    connId,
    kind: opts?.kind ?? 'query',
    title: opts?.title ?? 'Query',
    pinned: opts?.pinned ?? false,
    db: opts?.db,
    table: opts?.table,
    sql: '',
    savedSql: '',
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

  // Tracks which database the object tree has most recently selected, keyed by
  // connection id. New query tabs initialize their schema-selector from this
  // value (rather than defaulting to the first database alphabetically).
  const selectedDb = ref<Record<string, string | null>>({})

  // Schema filter from the sidebar object tree — null means "show all".
  // Query tabs filter their schema dropdown by this.
  const schemaFilter = ref<Record<string, string[] | null>>({})

  function setSchemaFilter(connId: string, schemas: string[] | null) {
    schemaFilter.value = { ...schemaFilter.value, [connId]: schemas }
  }

  function setSelectedDb(connId: string, db: string | null) {
    selectedDb.value = { ...selectedDb.value, [connId]: db }
  }

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

  function addTab(connId: string, opts?: { sql?: string; title?: string; kind?: TabKind; db?: string; table?: string; savedQueryId?: string }): QueryTab {
    const t = freshTab(connId, { kind: opts?.kind, title: opts?.title, db: opts?.db, table: opts?.table })
    if (opts?.sql) t.sql = opts.sql
    if (opts?.savedQueryId) t.savedQueryId = opts.savedQueryId
    tabs.value.push(t)
    setActive(connId, t.id)
    return t
  }

  /**
   * Open a saved query from the object tree. Reuses an already-open tab bound
   * to the same saved_query id; otherwise opens a fresh query tab seeded with
   * the stored SQL and bound to its id (so 保存 overwrites it in place).
   */
  function openSavedQuery(connId: string, sq: { id: string; name: string; sqlText: string; dbName: string }): QueryTab {
    const existing = tabs.value.find((t) => t.connId === connId && t.kind === 'query' && t.savedQueryId === sq.id)
    if (existing) {
      setActive(connId, existing.id)
      return existing
    }
    const t = addTab(connId, {
      kind: 'query',
      sql: sq.sqlText,
      db: sq.dbName,
      title: `📝 ${sq.name}`,
      savedQueryId: sq.id,
    })
    t.savedSql = sq.sqlText
    return t
  }

  /** A query tab is dirty when its SQL diverges from the last saved baseline. */
  function isQueryDirty(t: QueryTab): boolean {
    if (t.kind !== 'query') return false
    return (t.sql ?? '').trim() !== (t.savedSql ?? '').trim()
  }

  /**
   * Persist a query tab's SQL into the saved-query store. Bound tabs overwrite
   * in place (keeping their name); first-time saves prompt for a name. Returns
   * true on success, false if the user cancels the name prompt; throws on a
   * backend error so callers can surface it.
   */
  async function saveTabQuery(tabId: string): Promise<boolean> {
    const t = getTab(tabId)
    if (!t || t.kind !== 'query') return false
    if (!t.sql.trim()) return false
    const db = t.db ?? ''
    if (t.savedQueryId) {
      await savedQueryApi.save({
        id: t.savedQueryId,
        connId: t.connId,
        dbName: db,
        name: t.title.replace(/^📝\s*/, ''),
        sqlText: t.sql,
      })
      t.savedSql = t.sql
      void emitEvent('saved-query:changed', { connId: t.connId, db })
      return true
    }
    const name = await openTextPrompt({
      title: tr('queryStore.saveTitle'),
      label: db ? tr('queryStore.saveToDb', { db }) : tr('queryStore.saveTitle'),
      initial: '',
      okText: tr('common.save'),
      validate: (v) => (v ? null : tr('common.nameEmpty')),
    })
    if (name === null) return false
    const saved = await savedQueryApi.save({ connId: t.connId, dbName: db, name, sqlText: t.sql })
    t.savedQueryId = saved.id
    t.db = db
    t.title = `📝 ${name}`
    t.savedSql = t.sql
    void emitEvent('saved-query:changed', { connId: t.connId, db })
    return true
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

  /**
   * Open a "new table" structure-editor tab anchored to `db`. The table name
   * is decided by the user inside the tab; we don't reuse existing tabs here
   * because each click should give a fresh blank draft.
   */
  function openNewTableTab(connId: string, db: string): QueryTab {
    return addTab(connId, {
      kind: 'new-table',
      db,
      title: tr('queryStore.newTableTitle', { db }),
    })
  }

  /**
   * After a CREATE TABLE succeeds, promote the new-table tab to a regular
   * structure tab so subsequent edits behave like editing an existing table.
   */
  function promoteNewTableTab(tabId: string, table: string) {
    const t = getTab(tabId)
    if (!t || t.kind !== 'new-table') return
    t.kind = 'structure'
    t.table = table
    t.title = `⚙ ${t.db}.${table}`
  }

  /**
   * Ensure the pinned "database overview" tab exists for a connection. Inserts
   * a new pinned tab at the front of the connection's tab list if absent.
   * Does NOT change the active tab unless none is currently active.
   */
  function ensureOverviewTab(connId: string, db?: string): QueryTab {
    const existing = tabs.value.find(
      (t) => t.connId === connId && t.kind === 'tables-overview' && t.pinned,
    )
    if (existing) {
      if (db && existing.db !== db) {
        existing.db = db
        existing.title = `📋 ${db}`
      }
      return existing
    }
    const t = freshTab(connId, {
      kind: 'tables-overview',
      db,
      title: db ? `📋 ${db}` : `📋 ${tr('tablesOverview.title')}`,
      pinned: true,
    })
    // Splice in at the front of this connection's run of tabs so it sorts first.
    const firstIdx = tabs.value.findIndex((x) => x.connId === connId)
    if (firstIdx === -1) tabs.value.push(t)
    else tabs.value.splice(firstIdx, 0, t)
    if (!activeByConn.value[connId]) setActive(connId, t.id)
    return t
  }

  /**
   * Click a database in the object tree → focus the pinned overview tab and
   * point it at this db. Always exactly one overview tab per connection.
   */
  function openTablesOverviewTab(connId: string, db: string): QueryTab {
    const t = ensureOverviewTab(connId, db)
    if (t.db !== db) {
      t.db = db
      t.title = `📋 ${db}`
    }
    setActive(connId, t.id)
    return t
  }

  async function closeTab(id: string) {
    const t = getTab(id)
    if (!t || t.pinned) return
    // Guard against losing unsaved/edited SQL.
    if (isQueryDirty(t)) {
      const choice = await confirmCloseUnsaved(t.title)
      if (choice === 'cancel') return
      if (choice === 'save') {
        try {
          if (!(await saveTabQuery(id))) return // name prompt canceled → keep tab
        } catch {
          return // save failed → keep tab open so the SQL isn't lost
        }
      }
    }
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
    const ids = tabsForConn(connId).filter((t) => !t.pinned).map((t) => t.id)
    for (const id of ids) {
      await closeTab(id)
    }
  }

  async function closeOthers(id: string) {
    const t = getTab(id)
    if (!t) return
    const connTabs = tabsForConn(t.connId)
    for (const tab of connTabs) {
      if (tab.id !== id && !tab.pinned) {
        await closeTab(tab.id)
      }
    }
  }

  async function closeLeft(id: string) {
    const t = getTab(id)
    if (!t) return
    const connTabs = tabsForConn(t.connId)
    for (const tab of connTabs) {
      if (tab.id === id) break
      if (!tab.pinned) await closeTab(tab.id)
    }
  }

  async function closeRight(id: string) {
    const t = getTab(id)
    if (!t) return
    const connTabs = tabsForConn(t.connId)
    const idx = connTabs.findIndex((x) => x.id === id)
    if (idx === -1) return
    for (let i = idx + 1; i < connTabs.length; i++) {
      if (!connTabs[i].pinned) await closeTab(connTabs[i].id)
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
    openSavedQuery,
    isQueryDirty,
    saveTabQuery,
    openTableTab,
    openNewTableTab,
    promoteNewTableTab,
    openTablesOverviewTab,
    ensureOverviewTab,
    closeTab,
    closeAllForConn,
    closeOthers,
    closeLeft,
    closeRight,
    runActive,
    fetchMore,
    cancel,
    explain,
    loadCapabilities,
    selectedDb,
    setSelectedDb,
    schemaFilter,
    setSchemaFilter,
  }
})

function formatError(e: any): string {
  if (!e) return 'unknown error'
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  try { return JSON.stringify(e) } catch { return String(e) }
}
