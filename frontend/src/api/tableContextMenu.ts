// tableContextMenu — wires the Wails native context menus that act on a
// single table (registered in wailsbridge/contextmenu.go as
// `catdb-tables-overview` and `catdb-tree-table`) to whichever component
// last surfaced one (TablesOverview row or ObjectTree table node).
//
// Architecture mirrors `tabContextMenu.ts`:
//   1. The caller (TablesOverview / ObjectTree) sets `--custom-contextmenu` on
//      a wrapper before the native menu opens.
//   2. The caller pushes the target table into the singleton via
//      `setActiveTableContext({connId, db, table, onAfterMutate})`.
//   3. `installTableContextMenuListener()` subscribes once (at app boot) to
//      the `ctx:tbl-*` events emitted by Go and acts on the singleton.
//
// 删除 / 清空 走客户端确认对话框 + 真实 SQL，刷新由调用方注入的
// `onAfterMutate` 回调触发（重新拉取表列表 + 清理元数据缓存）。
import { createDiscreteApi } from 'naive-ui'
import { quoteTable } from '../lib/alterPlan'
import { useQueryStore } from '../stores/query'
import { useMetadataStore } from '../stores/metadata'
import { runQuery } from './query'
import { on } from './events'

interface ActiveCtx {
  connId: string
  db: string
  table: string
  /** 调用方在右键时注入，删除/清空成功后调用以刷新本地视图。 */
  onAfterMutate?: () => Promise<void> | void
}

let active: ActiveCtx | null = null

/** Called by TablesOverview / ObjectTree right before the native menu opens. */
export function setActiveTableContext(ctx: ActiveCtx): void {
  active = ctx
}

let installed = false

/** Subscribe once to the Go-side `ctx:tbl-*` click events. Call from app boot. */
export function installTableContextMenuListener(): void {
  if (installed) return
  installed = true

  const { dialog, message } = createDiscreteApi(['dialog', 'message'])

  on('ctx:tbl-open', () => {
    if (!active) return
    const { connId, db, table } = active
    useQueryStore().openTableTab(connId, db, table, 'table')
  })

  on('ctx:tbl-edit', () => {
    if (!active) return
    const { connId, db, table } = active
    useQueryStore().openTableTab(connId, db, table, 'structure')
  })

  on('ctx:tbl-truncate', () => {
    if (!active) return
    const ctx = active
    dialog.warning({
      title: '清空表',
      content: `确定要清空 ${ctx.db}.${ctx.table} 吗？所有数据将被删除，操作不可撤销。`,
      positiveText: '清空',
      negativeText: '取消',
      onPositiveClick: async () => {
        try {
          await runQuery(ctx.connId, `TRUNCATE TABLE ${quoteTable(ctx.db, ctx.table)}`)
          message.success(`已清空 ${ctx.table}`)
          await ctx.onAfterMutate?.()
        } catch (e) {
          message.error(`清空失败: ${String(e)}`)
        }
      },
    })
  })

  on('ctx:tbl-drop', () => {
    if (!active) return
    const ctx = active
    dialog.error({
      title: '删除表',
      content: `确定要删除 ${ctx.db}.${ctx.table} 吗？表结构与数据都将被删除，操作不可撤销。`,
      positiveText: '删除',
      negativeText: '取消',
      onPositiveClick: async () => {
        try {
          await runQuery(ctx.connId, `DROP TABLE ${quoteTable(ctx.db, ctx.table)}`)
          message.success(`已删除 ${ctx.table}`)
          useMetadataStore().invalidateTables(ctx.connId, ctx.db)
          await ctx.onAfterMutate?.()
        } catch (e) {
          message.error(`删除失败: ${String(e)}`)
        }
      },
    })
  })
}
