// gridContextMenu — wires the native Wails context menu (registered in
// wailsbridge/contextmenu.go as "catdb-grid-cell") to the currently active
// DataGrid instance.
//
// Architecture:
//   1. DataGrid sets `style="--custom-contextmenu: catdb-grid-cell"` on its
//      wrapper → Wails opens the native menu on right-click.
//   2. ResultTable / TableBrowser call `setActiveGridContext({...})` whenever
//      their grid receives a `cell-context-menu` event, pushing their current
//      rows / column names / selection / tableName / pkColumns into a
//      module-level singleton.
//   3. `installGridContextMenuListener()` subscribes once (during app boot)
//      to `ctx:grid-*` events emitted by the Go menu handlers. The handler
//      reads the singleton and runs the matching format helper from
//      useTableSelection.
//
// Only ONE grid context can be active at a time — the latest right-click wins.
// Context menus are inherently modal-ish (clicking elsewhere dismisses), so
// a "stale" context is essentially impossible in practice.
import { useTableSelection, type SelectionRange } from '../composables/useTableSelection'
import { on } from './events'

const ctxSel = useTableSelection()
let ctxState = {
  rows: [] as any[][],
  columnNames: [] as string[],
  tableName: undefined as string | undefined,
  pkColumns: [] as string[],
}

export interface ActiveGridContext {
  rows: any[][]
  columnNames: string[]
  selection: SelectionRange | null
  tableName?: string
  pkColumns?: string[]
}

/** Called by ResultTable / TableBrowser on every cell-context-menu event. */
export function setActiveGridContext(p: ActiveGridContext): void {
  ctxSel.selection.value = p.selection
  ctxState = {
    rows: p.rows,
    columnNames: p.columnNames,
    tableName: p.tableName,
    pkColumns: p.pkColumns ?? [],
  }
}

async function copy(text: string): Promise<void> {
  if (!text) return
  try { await navigator.clipboard.writeText(text) } catch { /* clipboard denied */ }
}

let installed = false

/** Subscribe once to the Go-side context-menu click events. Call from app boot. */
export function installGridContextMenuListener(): void {
  if (installed) return
  installed = true
  on('ctx:grid-copy-tsv', () => {
    if (!ctxSel.hasSelection()) return
    copy(ctxSel.formatTSV(ctxState.rows, ctxState.columnNames, false))
  })
  on('ctx:grid-copy-insert', () => {
    if (!ctxSel.hasSelection() || !ctxState.tableName) return
    copy(ctxSel.formatInsert(ctxState.rows, ctxState.columnNames, ctxState.tableName))
  })
  on('ctx:grid-copy-update', () => {
    if (!ctxSel.hasSelection() || !ctxState.tableName) return
    copy(ctxSel.formatUpdate(
      ctxState.rows, ctxState.columnNames, ctxState.tableName, ctxState.pkColumns,
    ))
  })
  on('ctx:grid-copy-columns', () => {
    if (!ctxSel.hasSelection()) return
    copy(ctxSel.formatColumns(ctxState.columnNames))
  })
  on('ctx:grid-copy-data-plus-columns', () => {
    if (!ctxSel.hasSelection()) return
    copy(ctxSel.formatDataPlusColumns(ctxState.rows, ctxState.columnNames))
  })
}
