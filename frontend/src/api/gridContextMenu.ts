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
import { createDiscreteApi } from 'naive-ui'
import { useTableSelection, type SelectionRange } from '../composables/useTableSelection'
import { on, emit } from './events'

const ctxSel = useTableSelection()
let ctxState = {
  rows: [] as any[][],
  columnNames: [] as string[],
  tableName: undefined as string | undefined,
  pkColumns: [] as string[],
  connId: undefined as string | undefined,
  db: undefined as string | undefined,
  table: undefined as string | undefined,
}

export interface ActiveGridContext {
  rows: any[][]
  columnNames: string[]
  selection: SelectionRange | null
  tableName?: string
  pkColumns?: string[]
  connId?: string
  db?: string
  table?: string
}

/** Called by ResultTable / TableBrowser on every cell-context-menu event. */
export function setActiveGridContext(p: ActiveGridContext): void {
  ctxSel.selection.value = p.selection
  ctxState = {
    rows: p.rows,
    columnNames: p.columnNames,
    tableName: p.tableName,
    pkColumns: p.pkColumns ?? [],
    connId: p.connId,
    db: p.db,
    table: p.table,
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

  // ---- 设置为NULL ----

  on('ctx:grid-set-null', async () => {
    const { rows, columnNames, pkColumns, connId, db, table } = ctxState
    const sel = ctxSel.selection.value
    const { message } = createDiscreteApi(['message'])

    // Can't edit from SQL results (no connId/db/table context)
    if (!sel || !connId || !db || !table || !rows.length) return

    // Table has no primary key → can't build UPDATE statements
    if (!pkColumns.length) return

    const minR = Math.min(sel.startRow, sel.endRow)
    const maxR = Math.max(sel.startRow, sel.endRow)
    const minC = Math.max(0, Math.min(sel.startCol, sel.endCol))
    const maxC = Math.max(0, Math.max(sel.startCol, sel.endCol))

    // Check if any selected column is a primary-key column
    for (let c = minC; c <= maxC; c++) {
      if (pkColumns.includes(columnNames[c])) {
        message.warning('主键不能设置为NULL')
        return
      }
    }

    // Collect selected non-PK column indices
    const selectedColIndices: number[] = []
    for (let c = minC; c <= maxC; c++) {
      if (!pkColumns.includes(columnNames[c])) {
        selectedColIndices.push(c)
      }
    }
    if (!selectedColIndices.length) return

    // Build list of cell changes for the active TableBrowser to queue
    interface CellChange { row: number; col: number; oldValue: any; columnName: string }
    const changes: CellChange[] = []
    for (let r = minR; r <= maxR; r++) {
      for (const c of selectedColIndices) {
        changes.push({
          row: r,
          col: c,
          oldValue: rows[r]?.[c],
          columnName: columnNames[c],
        })
      }
    }

    emit('ctx:grid-set-null-queue', changes)
  })
}
