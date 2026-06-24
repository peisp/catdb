// sidebarContextMenu — wires Wails native context menus for the connection
// sidebar's group-label and blank-area right-click targets (registered in
// wailsbridge/contextmenu.go as `catdb-sidebar-empty`, `catdb-sidebar-group`,
// `catdb-sidebar-group-nonempty`).
//
// Architecture mirrors connectionContextMenu.ts:
//   1. ConnectionSidebar sets `--custom-contextmenu` on the right-clicked
//      element before the native menu opens.
//   2. The sidebar pushes the target group identity into the singleton via
//      `setActiveGroupContext(ctx)` (or clears it for the blank-area menu).
//   3. `installSidebarContextMenuListener()` subscribes once at app boot to
//      the `ctx:sb-*` events and dispatches actions against the singleton.
//
// 新建分组 / 重命名 actions go through the in-app PromptOverlay (api/prompts)
// because Wails v3 ships no native text-input dialog; 删除 uses the native
// warning dialog.
import { createDiscreteApi } from 'naive-ui'
import { Dialogs } from '@wailsio/runtime'
import { t } from '../i18n'
import { useConnectionsStore } from '../stores/connections'
import { openTextPrompt } from './prompts'
import { on } from './events'

interface ActiveGroupCtx {
  groupId: string
  groupName: string
}

let activeGroup: ActiveGroupCtx | null = null

/** Called by ConnectionSidebar right before a group-label native menu opens. */
export function setActiveGroupContext(ctx: ActiveGroupCtx | null): void {
  activeGroup = ctx
}

let installed = false

/** Subscribe once to the Go-side `ctx:sb-*` click events. Call from app boot. */
export function installSidebarContextMenuListener(): void {
  if (installed) return
  installed = true

  const { message } = createDiscreteApi(['message'])

  on('ctx:sb-new-group', () => {
    // The sidebar renders an inline input row when it sees this event —
    // saving happens on blur/Enter from there, so this module's job is just
    // to signal "open the new-group input". Using a DOM custom event keeps
    // the module decoupled from the component (same pattern as conn:edit).
    document.dispatchEvent(new CustomEvent('sb:new-group'))
  })

  on('ctx:sb-group-rename', async () => {
    if (!activeGroup) return
    const ctx = activeGroup
    const store = useConnectionsStore()
    const name = await openTextPrompt({
      title: t('sidebar.group.rename.title'),
      label: t('sidebar.group.rename.label'),
      initial: ctx.groupName,
      okText: t('common.save'),
      validate: (v) => {
        if (!v) return t('sidebar.group.rename.empty')
        if (v === ctx.groupName) return null
        if (store.groups.some((g) => g.name === v)) return t('sidebar.group.rename.exists')
        return null
      },
    })
    if (!name || name === ctx.groupName) return
    try {
      await store.saveGroup({ id: ctx.groupId, name })
      message.success(t('common.renamed'))
    } catch (e) {
      message.error(t('common.renameFailed', { error: String(e) }))
    }
  })

  on('ctx:sb-group-delete', async () => {
    if (!activeGroup) return
    const ctx = activeGroup
    const store = useConnectionsStore()
    // Short-circuit non-empty deletes locally — the backend would refuse with
    // storage.ErrGroupNotEmpty anyway, but catching it here lets us show a
    // plain Chinese message instead of the raw Wails-wrapped error JSON.
    const memberCount = store.connections.filter((c) => c.groupId === ctx.groupId).length
    if (memberCount > 0) {
      await Dialogs.Warning({
        Title: t('sidebar.group.deleteBlocked.title'),
        Message: t('sidebar.group.deleteBlocked.message', { name: ctx.groupName, count: memberCount }),
        Buttons: [{ Label: t('sidebar.group.deleteBlocked.ok'), IsDefault: true }],
      })
      return
    }
    const deleteLabel = t('common.delete')
    const btn = await Dialogs.Warning({
      Title: t('sidebar.group.delete.title'),
      Message: t('sidebar.group.delete.confirm', { name: ctx.groupName }),
      Buttons: [
        { Label: t('common.cancel'), IsCancel: true },
        { Label: deleteLabel },
      ],
    })
    if (btn !== deleteLabel) return
    try {
      await store.removeGroup(ctx.groupId)
      message.success(t('common.deleted'))
    } catch (e) {
      message.error(t('common.deleteFailed', { error: String(e) }))
    }
  })
}
