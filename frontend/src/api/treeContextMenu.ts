// treeContextMenu — wires the Wails native context menus for the object tree
// (registered in wailsbridge/contextmenu.go as `catdb-tree-*`) to ObjectTree's
// per-node callbacks.
//
// Table-level actions (Open / Edit / Truncate / Drop) are handled by
// `tableContextMenu.ts` via the shared `ctx:tbl-*` events; this module owns
// only the tree-specific events:
//
//   ctx:tree-new-table       — 直接调 queryStore.openNewTableTab(connId, db)
//   ctx:tree-refresh-cols    — 调 active.onRefreshColumns()
//   ctx:tree-refresh-tables  — 调 active.onRefreshTables()
//   ctx:tree-refresh-views   — 调 active.onRefreshViews()
//   ctx:tree-refresh-db      — 调 active.onRefreshDb()
//
// 「新建表」不经过组件树 emit —— 直接进 queryStore，符合「同一 connection 一个固定
// overview tab」的总体设计。其余 refresh 动作需要 ObjectTree 自身的 n-tree
// 状态（找到节点、重置 children、重新 onLoad），所以通过 callback 反弹回去。
import { createDiscreteApi } from 'naive-ui'
import { Dialogs } from '@wailsio/runtime'
import { useQueryStore } from '../stores/query'
import { on } from './events'
import { system as systemApi } from '.'
import { savedQuery as savedQueryApi } from '.'
import { openTextPrompt } from './prompts'

interface ActiveCtx {
  connId: string
  db?: string
  table?: string
  // for 「查询」 group / leaf
  queryId?: string
  queryName?: string
  querySql?: string
  onRefreshColumns?: () => Promise<void> | void
  onRefreshTables?: () => Promise<void> | void
  onRefreshViews?: () => Promise<void> | void
  onRefreshDb?: () => Promise<void> | void
  onRefreshQueries?: () => Promise<void> | void
}

let active: ActiveCtx | null = null

/** Called by ObjectTree right before the native menu opens. */
export function setActiveTreeContext(ctx: ActiveCtx): void {
  active = ctx
}

let installed = false

/** Subscribe once to the Go-side `ctx:tree-*` click events. Call from app boot. */
export function installTreeContextMenuListener(): void {
  if (installed) return
  installed = true

  const { message } = createDiscreteApi(['message'])

  // 「查询」 group: 新建查询 → open a blank query tab anchored to the db.
  on('ctx:tree-new-query', () => {
    if (!active || !active.db) return
    useQueryStore().addTab(active.connId, { kind: 'query', db: active.db, title: '查询' })
  })

  on('ctx:tree-refresh-queries', async () => {
    await active?.onRefreshQueries?.()
  })

  // saved-query leaf: 打开 / 重命名 / 删除.
  on('ctx:query-open', () => {
    if (!active || !active.queryId) return
    useQueryStore().openSavedQuery(active.connId, {
      id: active.queryId,
      name: active.queryName ?? '查询',
      sqlText: active.querySql ?? '',
      dbName: active.db ?? '',
    })
  })

  on('ctx:query-rename', async () => {
    if (!active || !active.queryId) return
    const ctx = active
    const newName = await openTextPrompt({
      title: '重命名查询',
      label: `当前: ${ctx.queryName ?? ''}`,
      initial: ctx.queryName ?? '',
      okText: '重命名',
      validate: (v) => (v ? (v === ctx.queryName ? '与原名相同' : null) : '名称不能为空'),
    })
    if (newName === null) return
    try {
      await savedQueryApi.save({
        id: ctx.queryId,
        connId: ctx.connId,
        dbName: ctx.db ?? '',
        name: newName,
        sqlText: ctx.querySql ?? '',
      })
      // Re-title any open tab bound to this saved query.
      const qs = useQueryStore()
      for (const t of qs.tabs) {
        if (t.savedQueryId === ctx.queryId) t.title = `📝 ${newName}`
      }
      message.success(`已重命名为 ${newName}`)
      await ctx.onRefreshQueries?.()
    } catch (e) {
      message.error(`重命名失败: ${String(e)}`)
    }
  })

  on('ctx:query-delete', async () => {
    if (!active || !active.queryId) return
    const ctx = active
    const btn = await Dialogs.Warning({
      Title: '删除查询',
      Message: `确定要删除查询「${ctx.queryName ?? ''}」吗？`,
      Buttons: [
        { Label: '取消', IsCancel: true },
        { Label: '删除' },
      ],
    })
    if (btn !== '删除') return
    try {
      await savedQueryApi.del(ctx.queryId!)
      // Detach any open tab so a later 保存 creates a fresh entry instead of
      // updating the now-deleted row.
      const qs = useQueryStore()
      for (const t of qs.tabs) {
        if (t.savedQueryId === ctx.queryId) t.savedQueryId = undefined
      }
      message.success('已删除')
      await ctx.onRefreshQueries?.()
    } catch (e) {
      message.error(`删除失败: ${String(e)}`)
    }
  })

  on('ctx:tree-new-table', () => {
    if (!active || !active.db) return
    useQueryStore().openNewTableTab(active.connId, active.db)
  })

  on('ctx:tree-refresh-cols', async () => {
    await active?.onRefreshColumns?.()
  })

  on('ctx:tree-refresh-tables', async () => {
    await active?.onRefreshTables?.()
  })

  on('ctx:tree-refresh-views', async () => {
    await active?.onRefreshViews?.()
  })

  on('ctx:tree-refresh-db', async () => {
    await active?.onRefreshDb?.()
  })

  // 「新建数据库」/「编辑数据库」 open the editor as a Wails native child
  // window (see SystemService.OpenDatabaseEditor). The window broadcasts
  // `database:saved` on success — ObjectTree subscribes to that event and
  // refreshes its tree; we don't need to hook a callback here.
  on('ctx:tree-db-new', () => {
    if (!active) return
    void systemApi.openDatabaseEditor(active.connId, '')
  })

  on('ctx:tree-db-edit', () => {
    if (!active || !active.db) return
    void systemApi.openDatabaseEditor(active.connId, active.db)
  })
}
