// useExport — native-export flow: caller picks format → SaveFile → stream
// export via TransferService. No Naive UI NModal or format dialog needed.
import { createDiscreteApi } from 'naive-ui'
import { Dialogs } from '@wailsio/runtime'
import { TransferFormat } from '../api/transfer'
import { exportQuery, exportTable, onProgress } from '../api/transfer'
import type { ExportOptions } from '../api/transfer'

export type ExportSource =
  | { kind: 'query'; connId: string; sql: string; defaultName?: string }
  | { kind: 'table'; connId: string; db: string; table: string; defaultName?: string }

export interface FormatOption {
  label: string
  format: TransferFormat
  ext: string
}

/** Available export formats — used by components to build dropdowns. */
export const exportFormats: FormatOption[] = [
  { label: 'CSV', format: TransferFormat.FormatCSV, ext: 'csv' },
  { label: 'Excel', format: TransferFormat.FormatXLSX, ext: 'xlsx' },
  { label: 'JSON', format: TransferFormat.FormatJSON, ext: 'json' },
  { label: 'SQL', format: TransferFormat.FormatSQL, ext: 'sql' },
]

const { message } = createDiscreteApi(['message'])

/**
 * Show SaveFile with the correct extension, then start exporting.
 * Progress / completion via toasts.
 */
export async function startExport(source: ExportSource, format: TransferFormat): Promise<void> {
  const chosen = exportFormats.find((f) => f.format === format)
  if (!chosen) return

  const defaultName = source.defaultName ?? 'export'

  const path = await Dialogs.SaveFile({
    Title: 'Export',
    Filename: `${defaultName}.${chosen.ext}`,
    Filters: [{ DisplayName: chosen.label, Pattern: `*.${chosen.ext}` }],
  })
  if (!path) return

  const opts: ExportOptions = {
    format,
    path,
    batchSize: 1000,
    includeHeader: format === TransferFormat.FormatCSV || format === TransferFormat.FormatXLSX,
    includeDDL: format === TransferFormat.FormatSQL,
    tableName: source.kind === 'table' ? source.table : '',
  }

  let done = false
  const unsub = onProgress((p) => {
    if (p.done) done = true
    if (p.error) message.error(`Export error: ${p.error}`)
  })

  try {
    const result =
      source.kind === 'table'
        ? await exportTable(source.connId, source.db, source.table, opts)
        : await exportQuery(source.connId, source.sql, opts)

    await new Promise((r) => setTimeout(r, 100))
    message.success(`Exported ${result.rowsTotal} rows → ${path}`)
  } catch (e) {
    if (!done) message.error(`Export failed: ${String(e)}`)
  } finally {
    unsub()
  }
}
