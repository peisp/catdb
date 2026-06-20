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
      title: '重命名分组',
      label: '分组名称',
      initial: ctx.groupName,
      okText: '保存',
      validate: (v) => {
        if (!v) return '请输入分组名称'
        if (v === ctx.groupName) return null
        if (store.groups.some((g) => g.name === v)) return '分组名称已存在'
        return null
      },
    })
    if (!name || name === ctx.groupName) return
    try {
      await store.saveGroup({ id: ctx.groupId, name })
      message.success('已重命名')
    } catch (e) {
      message.error(`重命名失败: ${String(e)}`)
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
        Title: '无法删除分组',
        Message: `分组 "${ctx.groupName}" 还有 ${memberCount} 个连接，请先移动或删除后再试。`,
        Buttons: [{ Label: '知道了', IsDefault: true }],
      })
      return
    }
    const btn = await Dialogs.Warning({
      Title: '删除分组',
      Message: `确定要删除分组 "${ctx.groupName}" 吗？`,
      Buttons: [
        { Label: '取消', IsCancel: true },
        { Label: '删除' },
      ],
    })
    if (btn !== '删除') return
    try {
      await store.removeGroup(ctx.groupId)
      message.success('已删除')
    } catch (e) {
      message.error(`删除失败: ${String(e)}`)
    }
  })
}
