// api/dialect — access to the driver's UIDialect descriptor.
//
// The descriptor is the driver's declarative UI contract (identifier quoting,
// editor dialect, type system, completion catalogs) shipped by
// ConnectionService.ListDrivers. Components never hard-code per-database
// knowledge; they resolve the descriptor here and read from it. When the
// driver can't be resolved (yet), genericUIDialect() is a safe ANSI fallback.
import type {
  UIDialect as BoundUIDialect,
  UITypeFormat as BoundUITypeFormat,
  UIFunction as BoundUIFunction,
  UISnippet as BoundUISnippet,
  UITypeGroup as BoundUITypeGroup,
  UIAutoIncrement as BoundUIAutoIncrement,
} from '../../bindings/catdb/internal/dbdriver/models'
import { listDrivers, getConnection } from './connections'

export type UIDialect = BoundUIDialect
export type UITypeFormat = BoundUITypeFormat
export type UIFunction = BoundUIFunction
export type UISnippet = BoundUISnippet
export type UITypeGroup = BoundUITypeGroup
export type UIAutoIncrement = BoundUIAutoIncrement

// ---- generic fallback -------------------------------------------------------

const GENERIC: UIDialect = {
  editorDialect: 'standard',
  identQuote: '"',
  systemSchemas: [],
  keywords: [],
  functions: [],
  snippets: [],
  typeGroups: [],
  typeFormats: {},
  defaultColumnType: 'VARCHAR',
  defaultColumnParams: '255',
  hasUnsigned: false,
  autoIncrement: { supported: false, baseTypes: [], maxPerTable: 0 },
  primaryKeyForcesNotNull: false,
  indexTypes: [],
} as unknown as UIDialect

/** ANSI-flavored fallback used until the driver's descriptor resolves. */
export function genericUIDialect(): UIDialect {
  return GENERIC
}

// ---- resolution (cached) ----------------------------------------------------

let byDriver: Map<string, UIDialect> | null = null
let driversLoading: Promise<void> | null = null
const driverOfConn = new Map<string, string>()

async function ensureDrivers(): Promise<void> {
  if (byDriver) return
  driversLoading ??= listDrivers().then((list) => {
    byDriver = new Map()
    for (const d of list ?? []) byDriver.set(d.name, d.ui)
  }).finally(() => { driversLoading = null })
  await driversLoading
}

/** The UIDialect of a registered driver, by driver name. */
export async function uiDialectForDriver(driver: string): Promise<UIDialect> {
  await ensureDrivers()
  return byDriver?.get(driver) ?? GENERIC
}

/** The UIDialect behind a saved connection. */
export async function uiDialectForConnection(connId: string): Promise<UIDialect> {
  let driver = driverOfConn.get(connId)
  if (!driver) {
    try {
      driver = (await getConnection(connId)).driver
      driverOfConn.set(connId, driver)
    } catch {
      return GENERIC
    }
  }
  return uiDialectForDriver(driver)
}

// ---- descriptor-driven helpers ----------------------------------------------

/** What the tree's top-level container is (UIDialect.NamespaceTerm) — drives
 *  UI copy (i18n keys nested under `.database` / `.schema`) and node icons. */
export type NamespaceTerm = 'database' | 'schema'

/** The dialect's namespace term, defaulting to 'database' when undeclared. */
export function namespaceTermOf(d: UIDialect | undefined | null): NamespaceTerm {
  return d?.namespaceTerm === 'schema' ? 'schema' : 'database'
}

/** Quote an identifier with the dialect's quote character (doubling embedded ones). */
export function quoteIdentWith(d: UIDialect, name: string): string {
  const q = d.identQuote || '"'
  return q + String(name).split(q).join(q + q) + q
}

/** Compose db.table (or just table) with the dialect's quoting. */
export function quoteTableWith(d: UIDialect, db: string, table: string): string {
  return db ? `${quoteIdentWith(d, db)}.${quoteIdentWith(d, table)}` : quoteIdentWith(d, table)
}

/** The params-field behavior for a base type ({kind:'none'} when undeclared). */
export function typeFormatOf(d: UIDialect, baseType: string): UITypeFormat {
  const f = d.typeFormats?.[(baseType || '').toUpperCase()]
  return (f ?? { kind: 'none' }) as UITypeFormat
}

/** Whether the dialect allows the auto-increment flag on this base type. */
export function autoIncrementAllowed(d: UIDialect, baseType: string): boolean {
  const ai = d.autoIncrement
  if (!ai?.supported) return false
  const types = ai.baseTypes ?? []
  return types.length === 0 || types.includes((baseType || '').toUpperCase())
}
