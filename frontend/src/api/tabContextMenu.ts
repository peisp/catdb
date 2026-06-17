// tabContextMenu — wires the Wails native context menu (registered in
// wailsbridge/contextmenu.go as "catdb-tab" variants) to the tab store.
//
// Architecture:
//   1. QueryWorkspace sets `style="--custom-contextmenu: catdb-tab"` on its
//      wrapper div on right-click → Wails opens the native menu.
//   2. QueryWorkspace calls `setActiveTabContext(tabId, connId)` so the
//      event listeners below know which tab to act on.
//   3. `installTabContextMenuListener()` subscribes once (during app boot)
//      to `ctx:tab-*` events emitted by the Go menu handlers.
import { useQueryStore } from '../stores/query'
import { on } from './events'

let activeTabId: string | null = null
let activeConnId: string | null = null
let installed = false

/** Called by QueryWorkspace before the native menu opens. */
export function setActiveTabContext(tabId: string, connId: string): void {
  activeTabId = tabId
  activeConnId = connId
}

/** Subscribe once to the Go-side tab context-menu click events. Call from app boot. */
export function installTabContextMenuListener(): void {
  if (installed) return
  installed = true

  on('ctx:tab-close', async () => {
    const store = useQueryStore()
    if (!activeTabId || !activeConnId) return
    const id = activeTabId
    const connId = activeConnId
    clearContext()
    await store.closeTab(id)
    // 自动新建 tab 避免编辑面板空白
    const remaining = store.tabsForConn(connId)
    if (!remaining.length) {
      store.addTab(connId, { title: 'Query 1', kind: 'query' })
    }
  })

  on('ctx:tab-close-others', async () => {
    const store = useQueryStore()
    if (!activeTabId) return
    const id = activeTabId
    clearContext()
    await store.closeOthers(id)
  })

  on('ctx:tab-close-left', async () => {
    const store = useQueryStore()
    if (!activeTabId) return
    const id = activeTabId
    clearContext()
    await store.closeLeft(id)
  })

  on('ctx:tab-close-right', async () => {
    const store = useQueryStore()
    if (!activeTabId) return
    const id = activeTabId
    clearContext()
    await store.closeRight(id)
  })
}

function clearContext() {
  activeTabId = null
  activeConnId = null
}
