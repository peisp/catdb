// useTableSelection — spreadsheet-style range selection + clipboard formatting
// for ResultTable / TableBrowser. Handles mousedown drag, Shift-click extend,
// and Cmd/Ctrl+C to copy the selected range as tab-separated values.
//
// Format helpers generate TSV, INSERT, UPDATE, column names, and data+columns.

import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

export interface SelectionRange {
  startRow: number
  startCol: number
  endRow: number
  endCol: number
}

function escapeSql(v: any): string {
  if (v == null) return 'NULL'
  if (typeof v === 'number') return String(v)
  if (typeof v === 'boolean') return v ? '1' : '0'
  const s = String(v)
  return "'" + s.replace(/\\/g, '\\\\').replace(/'/g, "\\'") + "'"
}

function renderValue(v: any): string {
  if (v == null) return 'NULL'
  if (typeof v === 'string') return v
  if (typeof v === 'number') return String(v)
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  if (typeof v === 'object') {
    if (v.__type__ === 'bytes') return `bytes(${v.length})`
    if (v.__type__ === 'bigint') return v.value
    try { return JSON.stringify(v) } catch { return String(v) }
  }
  return String(v)
}

export function useTableSelection() {
  const selection = ref<SelectionRange | null>(null)
  const selecting = ref(false)

  // ---- mouse handlers ----

  function startSelection(row: number, col: number) {
    selecting.value = true
    selection.value = { startRow: row, startCol: col, endRow: row, endCol: col }
  }

  function extendSelection(row: number, col: number) {
    if (!selecting.value || !selection.value) return
    selection.value.endRow = row
    selection.value.endCol = col
  }

  function endSelection() {
    selecting.value = false
  }

  function clearSelection() {
    selection.value = null
    selecting.value = false
  }

  const minRow = computed(() =>
    selection.value ? Math.min(selection.value.startRow, selection.value.endRow) : -1,
  )
  const maxRow = computed(() =>
    selection.value ? Math.max(selection.value.startRow, selection.value.endRow) : -1,
  )
  const minCol = computed(() =>
    selection.value ? Math.min(selection.value.startCol, selection.value.endCol) : -1,
  )
  const maxCol = computed(() =>
    selection.value ? Math.max(selection.value.startCol, selection.value.endCol) : -1,
  )

  function isSelected(row: number, col: number): boolean {
    if (!selection.value) return false
    // Row number column (col = -1): highlight if row is in selection range
    if (col === -1) return row >= minRow.value && row <= maxRow.value
    return row >= minRow.value && row <= maxRow.value && col >= minCol.value && col <= maxCol.value
  }

  function hasSelection(): boolean {
    return selection.value !== null
  }

  // ---- select a single cell ----
  function selectCell(row: number, col: number) {
    selection.value = { startRow: row, startCol: col, endRow: row, endCol: col }
  }

  /** Select all columns in a row (used when clicking the row number). Also
   *  sets selecting=true so the user can still drag to extend the range. */
  function selectRow(row: number, colCount: number) {
    selecting.value = true
    selection.value = { startRow: row, startCol: 0, endRow: row, endCol: colCount - 1 }
  }

  /** Clip a column index to >= 0 so row-number cells (col = -1) are excluded
   *  from formatted output. */
  function dataColStart(): number {
    return Math.max(0, minCol.value)
  }
  function dataColEnd(): number {
    return Math.max(0, maxCol.value)
  }

  // ---- format helpers ----

  function formatTSV(rows: any[][], columnNames: string[], includeHeader: boolean): string {
    const cs = dataColStart()
    const ce = dataColEnd()
    const parts: string[] = []
    if (includeHeader) {
      parts.push(columnNames.slice(cs, ce + 1).join('\t'))
    }
    for (let r = minRow.value; r <= maxRow.value; r++) {
      const row: string[] = []
      for (let c = cs; c <= ce; c++) {
        row.push(renderValue(rows[r]?.[c]))
      }
      parts.push(row.join('\t'))
    }
    return parts.join('\n')
  }

  function formatInsert(rows: any[][], columnNames: string[], table: string): string {
    const cs = dataColStart()
    const ce = dataColEnd()
    const cols = columnNames.slice(cs, ce + 1)
    const colList = cols.map((c) => '`' + c + '`').join(', ')
    const valueSets: string[] = []
    for (let r = minRow.value; r <= maxRow.value; r++) {
      const vals: string[] = []
      for (let c = cs; c <= ce; c++) {
        vals.push(escapeSql(rows[r]?.[c]))
      }
      valueSets.push('(' + vals.join(', ') + ')')
    }
    return `INSERT INTO ${table} (${colList}) VALUES\n${valueSets.join(',\n')};`
  }

  function formatUpdate(
    rows: any[][],
    columnNames: string[],
    table: string,
    pkColumns: string[],
  ): string {
    if (!pkColumns.length) return '-- No primary key — cannot generate UPDATE'
    const cs = dataColStart()
    const ce = dataColEnd()
    const parts: string[] = []
    for (let r = minRow.value; r <= maxRow.value; r++) {
      const setClauses: string[] = []
      const whereClauses: string[] = []
      for (let c = cs; c <= ce; c++) {
        const col = columnNames[c]
        const val = rows[r]?.[c]
        if (val == null) continue
        if (pkColumns.includes(col)) {
          whereClauses.push('`' + col + '` = ' + escapeSql(val))
        } else {
          setClauses.push('`' + col + '` = ' + escapeSql(val))
        }
      }
      // Include PK columns not in the selection by looking them up from rows
      for (const pk of pkColumns) {
        if (!columnNames.slice(cs, ce + 1).includes(pk)) {
          const pkIdx = columnNames.indexOf(pk)
          if (pkIdx >= 0) {
            whereClauses.push('`' + pk + '` = ' + escapeSql(rows[r]?.[pkIdx]))
          }
        }
      }
      if (!setClauses.length || !whereClauses.length) continue
      parts.push(`UPDATE ${table} SET ${setClauses.join(', ')} WHERE ${whereClauses.join(' AND ')};`)
    }
    return parts.join('\n')
  }

  function formatColumns(columnNames: string[]): string {
    const cs = dataColStart()
    const ce = dataColEnd()
    return columnNames.slice(cs, ce + 1).join('\t')
  }

  function formatDataPlusColumns(rows: any[][], columnNames: string[]): string {
    return formatColumns(columnNames) + '\n' + formatTSV(rows, columnNames, false)
  }

  return {
    selection,
    selecting,
    startSelection,
    extendSelection,
    endSelection,
    clearSelection,
    selectCell,
    selectRow,
    isSelected,
    hasSelection,
    minRow,
    maxRow,
    minCol,
    maxCol,
    formatTSV,
    formatInsert,
    formatUpdate,
    formatColumns,
    formatDataPlusColumns,
  }
}
