// useTableSelection — grid selection state + clipboard formatting for
// ResultTable / TableBrowser. Selection is a single rectangular range
// (DataGrid disables VTable's Ctrl+click multi-range select so what's
// highlighted is exactly what copies).
//
// Format helpers generate TSV, INSERT, UPDATE, column names, and data+columns.

import { ref } from 'vue'

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

/** 单元格含 Tab/换行/引号时按电子表格惯例加双引号包裹，避免粘贴时行列错位。 */
function tsvCell(v: any): string {
  const s = renderValue(v)
  if (/[\t\n\r"]/.test(s)) return '"' + s.replace(/"/g, '""') + '"'
  return s
}

/** 解析 TSV 文本为二维数组（tsvCell 的逆操作）。带引号状态机：
 *  字段以 `"` 开头视为引号包裹，内部 `""` 还原为 `"`，包裹内的 Tab/换行
 *  属于字段内容而不是分隔符。 */
export function parseTSV(text: string): string[][] {
  const s = text.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
  const rows: string[][] = []
  let row: string[] = []
  let field = ''
  let inQuotes = false
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]
    if (inQuotes) {
      if (ch === '"') {
        if (s[i + 1] === '"') { field += '"'; i++ }
        else inQuotes = false
      } else {
        field += ch
      }
    } else if (ch === '"' && field === '') {
      inQuotes = true
    } else if (ch === '\t') {
      row.push(field)
      field = ''
    } else if (ch === '\n') {
      row.push(field)
      rows.push(row)
      row = []
      field = ''
    } else {
      field += ch
    }
  }
  row.push(field)
  rows.push(row)
  // 去掉末尾换行符产生的空行
  if (rows.length > 1 && rows[rows.length - 1].length === 1 && rows[rows.length - 1][0] === '') {
    rows.pop()
  }
  return rows
}

export function useTableSelection() {
  const selection = ref<SelectionRange | null>(null)

  /** 归一化（r0<=r1, c0<=c1）；无选区返回 null。 */
  function block(): { r0: number; r1: number; c0: number; c1: number } | null {
    const r = selection.value
    if (!r) return null
    return {
      r0: Math.min(r.startRow, r.endRow),
      r1: Math.max(r.startRow, r.endRow),
      c0: Math.min(r.startCol, r.endCol),
      c1: Math.max(r.startCol, r.endCol),
    }
  }

  function hasSelection(): boolean {
    return selection.value !== null
  }

  function isSelected(row: number, col: number): boolean {
    const b = block()
    if (!b) return false
    return row >= b.r0 && row <= b.r1 && col >= b.c0 && col <= b.c1
  }

  function selectCell(row: number, col: number) {
    selection.value = { startRow: row, startCol: col, endRow: row, endCol: col }
  }

  // ---- format helpers ----

  function formatTSV(rows: any[][]): string {
    const b = block()
    if (!b) return ''
    const lines: string[] = []
    for (let r = b.r0; r <= b.r1; r++) {
      const line: string[] = []
      for (let c = b.c0; c <= b.c1; c++) line.push(tsvCell(rows[r]?.[c]))
      lines.push(line.join('\t'))
    }
    return lines.join('\n')
  }

  function formatInsert(rows: any[][], columnNames: string[], table: string): string {
    const b = block()
    if (!b) return ''
    const colList = columnNames.slice(b.c0, b.c1 + 1).map((c) => '`' + c + '`').join(', ')
    const valueSets: string[] = []
    for (let r = b.r0; r <= b.r1; r++) {
      const vals: string[] = []
      for (let c = b.c0; c <= b.c1; c++) vals.push(escapeSql(rows[r]?.[c]))
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
    const b = block()
    if (!b) return ''
    const selected = columnNames.slice(b.c0, b.c1 + 1)
    const parts: string[] = []
    for (let r = b.r0; r <= b.r1; r++) {
      const setClauses: string[] = []
      const whereClauses: string[] = []
      for (let c = b.c0; c <= b.c1; c++) {
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
        if (!selected.includes(pk)) {
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
    const b = block()
    if (!b) return ''
    return columnNames.slice(b.c0, b.c1 + 1).join('\t')
  }

  function formatDataPlusColumns(rows: any[][], columnNames: string[]): string {
    return formatColumns(columnNames) + '\n' + formatTSV(rows)
  }

  return {
    selection,
    selectCell,
    isSelected,
    hasSelection,
    formatTSV,
    formatInsert,
    formatUpdate,
    formatColumns,
    formatDataPlusColumns,
  }
}
