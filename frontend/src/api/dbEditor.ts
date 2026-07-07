// dbEditor — helpers behind the "新建/编辑数据库" child window.
//
// The form itself lives in DatabaseEditorWindow.vue (a native Wails child
// window opened via SystemService.OpenDatabaseEditor, mirroring the
// new-connection flow). This module owns:
//
//   - the driver-described option-field catalog (cached per-connection)
//   - per-database options lookup (used in edit mode)
//   - CREATE / ALTER DATABASE DDL rendering
//
// All of it goes through MetadataService, which probes the driver's optional
// DatabaseEditor extension — drivers without it reject with the stable
// "database-editor-unsupported" slug. The field catalog is small and stable
// per server, so we cache it per-connection.
import {
  buildAlterDatabase,
  buildCreateDatabase,
  getDatabaseOptions,
  listDatabaseOptionFields,
  type DatabaseOptionField,
  type DatabaseOptionValues,
} from './metadata'

export type { DatabaseOptionField, DatabaseOptionValues }

// ---- option-field catalog (per-connection cache) ----------------------------

const fieldCache: Record<string, DatabaseOptionField[]> = {}

export async function loadDbOptionFields(connId: string): Promise<DatabaseOptionField[]> {
  const cached = fieldCache[connId]
  if (cached) return cached
  const fields = (await listDatabaseOptionFields(connId)) ?? []
  fieldCache[connId] = fields
  return fields
}

export function invalidateDbOptionFieldsCache(connId: string) {
  delete fieldCache[connId]
}

// ---- per-db option values — read for edit mode ------------------------------

export async function loadDbInfo(connId: string, db: string): Promise<DatabaseOptionValues | null> {
  try {
    return (await getDatabaseOptions(connId, db)) ?? {}
  } catch {
    return null
  }
}

// ---- DDL rendering (driver-side) --------------------------------------------

export function buildCreateDb(connId: string, name: string, opts: DatabaseOptionValues): Promise<string> {
  return buildCreateDatabase(connId, name, opts)
}

/** opts must contain only the changed options. */
export function buildAlterDb(connId: string, name: string, opts: DatabaseOptionValues): Promise<string> {
  return buildAlterDatabase(connId, name, opts)
}
