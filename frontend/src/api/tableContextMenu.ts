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
import { confirm } from './dialogs'
import { t } from '../i18n'
import { quoteTable } from '../lib/alterPlan'
import { useQueryStore } from '../stores/query'
import { useMetadataStore } from '../stores/metadata'
import { runQuery } from './query'
import { on } from './events'
import { openTextPrompt } from './prompts'

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

/**
 * 重命名一张表 —— 弹出输入框、执行 RENAME TABLE、回指已开 tab 并失效元数据缓存。
 * 既被右键菜单 `ctx:tbl-rename` 调用，也被 TablesOverview 工具栏按钮直接调用。
 */
export async function renameTable(ctx: ActiveCtx): Promise<void> {
  const { message } = createDiscreteApi(['message'])
  const newName = await openTextPrompt({
    title: t('table.rename.title'),
    label: t('common.currentLabel', { name: `${ctx.db}.${ctx.table}` }),
    initial: ctx.table,
    okText: t('common.rename'),
    validate: (v) => {
      if (!v) return t('table.rename.empty')
      if (v === ctx.table) return t('common.sameName')
      if (/[`\s.]/.test(v)) return t('table.rename.invalidChars')
      return null
    },
  })
  if (newName === null) return
  try {
    await runQuery(
      ctx.connId,
      `RENAME TABLE ${quoteTable(ctx.db, ctx.table)} TO ${quoteTable(ctx.db, newName)}`,
    )
    // Re-point any open tabs at the new name so titles + future
    // openTableTab lookups stay consistent.
    const qs = useQueryStore()
    for (const t of qs.tabs) {
      if (t.connId !== ctx.connId || t.db !== ctx.db || t.table !== ctx.table) continue
      t.table = newName
      if (t.kind === 'table') t.title = `⊞ ${ctx.db}.${newName}`
      else if (t.kind === 'structure') t.title = `⚙ ${ctx.db}.${newName}`
    }
    const meta = useMetadataStore()
    meta.invalidateTables(ctx.connId, ctx.db)
    meta.invalidateColumns(ctx.connId, ctx.db, ctx.table)
    message.success(t('common.renamedTo', { name: newName }))
    await ctx.onAfterMutate?.()
  } catch (e) {
    message.error(t('common.renameFailed', { error: String(e) }))
  }
}

let installed = false

/** Subscribe once to the Go-side `ctx:tbl-*` click events. Call from app boot. */
export function installTableContextMenuListener(): void {
  if (installed) return
  installed = true

  const { message } = createDiscreteApi(['message'])

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

  on('ctx:tbl-rename', () => {
    if (!active) return
    void renameTable(active)
  })

  on('ctx:tbl-truncate', async () => {
    if (!active) return
    const ctx = active
    const choice = await confirm({
      title: t('table.truncate.title'),
      message: t('table.truncate.confirm', { name: `${ctx.db}.${ctx.table}` }),
      buttons: [
        { value: 'cancel', label: t('common.cancel'), isCancel: true },
        { value: 'truncate', label: t('table.truncate.ok') },
      ],
    })
    if (choice !== 'truncate') return
    try {
      await runQuery(ctx.connId, `TRUNCATE TABLE ${quoteTable(ctx.db, ctx.table)}`)
      message.success(t('table.truncate.success', { name: ctx.table }))
      await ctx.onAfterMutate?.()
    } catch (e) {
      message.error(t('table.truncate.error', { error: String(e) }))
    }
  })

  on('ctx:tbl-drop', async () => {
    if (!active) return
    const ctx = active
    const choice = await confirm({
      kind: 'error',
      title: t('table.drop.title'),
      message: t('table.drop.confirm', { name: `${ctx.db}.${ctx.table}` }),
      buttons: [
        { value: 'cancel', label: t('common.cancel'), isCancel: true },
        { value: 'drop', label: t('common.delete') },
      ],
    })
    if (choice !== 'drop') return
    try {
      await runQuery(ctx.connId, `DROP TABLE ${quoteTable(ctx.db, ctx.table)}`)
      message.success(t('table.drop.success', { name: ctx.table }))
      useMetadataStore().invalidateTables(ctx.connId, ctx.db)
      await ctx.onAfterMutate?.()
    } catch (e) {
      message.error(t('common.deleteFailed', { error: String(e) }))
    }
  })
}
