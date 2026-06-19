// connectionContextMenu — wires the Wails native context menu for the
// connection sidebar (registered in wailsbridge/contextmenu.go as
// `catdb-connection`) to whichever connection row was right-clicked.
//
// Architecture mirrors `tableContextMenu.ts`:
//   1. ConnectionSidebar sets `style="--custom-contextmenu: catdb-connection"`
//      on its root element before the native menu opens.
//   2. The sidebar pushes the target connection into the singleton via
//      `setActiveConnectionContext(ctx)`.
//   3. `installConnectionContextMenuListener()` subscribes at app boot to
//      the `ctx:conn-*` events emitted by Go and acts on the singleton.
import { createDiscreteApi } from 'naive-ui'
import { Dialogs } from '@wailsio/runtime'
import { useConnectionsStore } from '../stores/connections'
import { on } from './events'

interface ActiveCtx {
  connId: string
  connName: string
}

let active: ActiveCtx | null = null

/** Called by ConnectionSidebar right before the native menu opens. */
export function setActiveConnectionContext(ctx: ActiveCtx): void {
  active = ctx
}

let installed = false

/** Subscribe once to the Go-side `ctx:conn-*` click events. Call from app boot. */
export function installConnectionContextMenuListener(): void {
  if (installed) return
  installed = true

  const { message } = createDiscreteApi(['message'])

  on('ctx:conn-connect', async () => {
    if (!active) return
    const store = useConnectionsStore()
    try {
      await store.connect(active.connId)
      message.success(`已连接 ${active.connName}`)
      // Notify the sidebar to emit 'select' so AppShell opens this connection.
      document.dispatchEvent(new CustomEvent('conn:select', { detail: active.connId }))
    } catch (e) {
      message.error(`连接失败: ${String(e)}`)
    }
  })

  on('ctx:conn-disconnect', async () => {
    if (!active) return
    const store = useConnectionsStore()
    try {
      await store.disconnect(active.connId)
      message.info(`已断开 ${active.connName}`)
    } catch (e) {
      message.error(String(e))
    }
  })

  on('ctx:conn-edit', () => {
    if (!active) return
    // Emit via a custom event so ConnectionSidebar can catch it.
    // We dispatch on the document so the component doesn't need a direct ref.
    document.dispatchEvent(new CustomEvent('conn:edit', { detail: active.connId }))
  })

  on('ctx:conn-delete', async () => {
    if (!active) return
    const ctx = active
    const btn = await Dialogs.Warning({
      Title: '删除连接',
      Message: `确定要删除 "${ctx.connName}" 吗？此操作不可撤销。`,
      Buttons: [
        { Label: '取消', IsCancel: true },
        { Label: '删除' },
      ],
    })
    if (btn !== '删除') return
    try {
      await useConnectionsStore().remove(ctx.connId)
      message.success('已删除')
    } catch (e) {
      message.error(String(e))
    }
  })
}
