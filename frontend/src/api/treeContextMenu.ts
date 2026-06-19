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
import { useQueryStore } from '../stores/query'
import { on } from './events'

interface ActiveCtx {
  connId: string
  db?: string
  table?: string
  onRefreshColumns?: () => Promise<void> | void
  onRefreshTables?: () => Promise<void> | void
  onRefreshViews?: () => Promise<void> | void
  onRefreshDb?: () => Promise<void> | void
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
}
