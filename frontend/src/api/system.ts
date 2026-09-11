// api/system — facade over SystemService bindings.
import { SystemService } from '../../bindings/catdb/internal/services'
import type {
  FileFilterDescriptor as BoundFileFilter,
  LaunchOutcome as BoundLaunchOutcome,
} from '../../bindings/catdb/internal/services/models'
import { on } from './events'

export type FileFilter = BoundFileFilter
export type LaunchOutcome = BoundLaunchOutcome

export function pickSaveFile(title: string, defaultName: string, filters: FileFilter[] = []): Promise<string> {
  return SystemService.PickSaveFile(title, defaultName, filters) as unknown as Promise<string>
}
export function pickOpenFile(title: string, filters: FileFilter[] = []): Promise<string> {
  return SystemService.PickOpenFile(title, filters) as unknown as Promise<string>
}
export function showInfo(title: string, message: string): Promise<void> {
  return SystemService.ShowInfo(title, message) as unknown as Promise<void>
}
export function showError(title: string, message: string): Promise<void> {
  return SystemService.ShowError(title, message) as unknown as Promise<void>
}
export function setDirtyTabs(count: number): Promise<void> {
  return SystemService.SetDirtyTabs(count) as unknown as Promise<void>
}
export function allowNextClose(): Promise<void> {
  return SystemService.AllowNextClose() as unknown as Promise<void>
}
export function openConnectionEditor(driver: string, connId = ''): Promise<void> {
  return SystemService.OpenConnectionEditor(driver, connId) as unknown as Promise<void>
}
export function broadcastConnectionSaved(connId: string): Promise<void> {
  return SystemService.BroadcastConnectionSaved(connId) as unknown as Promise<void>
}
export function openDatabaseEditor(connId: string, dbName = ''): Promise<void> {
  return SystemService.OpenDatabaseEditor(connId, dbName) as unknown as Promise<void>
}
export function openTransferDialog(): Promise<void> {
  return SystemService.OpenTransferDialog() as unknown as Promise<void>
}
export function openStructureSyncDialog(): Promise<void> {
  return SystemService.OpenStructureSyncDialog() as unknown as Promise<void>
}
export function openDataSyncDialog(): Promise<void> {
  return SystemService.OpenDataSyncDialog() as unknown as Promise<void>
}
export function broadcastDatabaseSaved(connId: string, dbName: string): Promise<void> {
  return SystemService.BroadcastDatabaseSaved(connId, dbName) as unknown as Promise<void>
}
export function openExternalURL(url: string): Promise<void> {
  return SystemService.OpenExternalURL(url) as unknown as Promise<void>
}
/**
 * Copy text to the system clipboard via the native (Go) side. The WebView's
 * navigator.clipboard is permission-gated — WKWebView rejects it with
 * NotAllowedError — so every copy action in the app goes through here.
 */
export function copyText(text: string): Promise<void> {
  return SystemService.CopyToClipboard(text) as unknown as Promise<void>
}
export function openSettingsWindow(section = ''): Promise<void> {
  return SystemService.OpenSettingsWindow(section) as unknown as Promise<void>
}
export type LocaleChangedPayload = { locale: string }
export function onLocaleChanged(cb: (p: LocaleChangedPayload) => void): () => void {
  return on<LocaleChangedPayload>('app:locale-changed', cb)
}

export type ThemeChangedPayload = { theme: string }
export function onThemeChanged(cb: (p: ThemeChangedPayload) => void): () => void {
  return on<ThemeChangedPayload>('app:theme-changed', cb)
}

export type ConnectionSavedPayload = { id: string }
export function onConnectionSaved(cb: (p: ConnectionSavedPayload) => void): () => void {
  return on<ConnectionSavedPayload>('connection:saved', cb)
}

/**
 * Drain the `-con` command line this process was started with. Call once after
 * the main window mounts: events emitted before a window is ready are dropped,
 * so the cold-start path pulls instead of being pushed. Resolves to an empty
 * outcome (no ids, no errors) when nothing is pending, which is the norm.
 */
export function consumePendingLaunch(): Promise<LaunchOutcome> {
  return SystemService.ConsumePendingLaunch() as unknown as Promise<LaunchOutcome>
}

/**
 * A second instance forwarded its `-con` command line to this one; the backend
 * has already located/created/opened the connections and reports the result.
 * Same payload shape as consumePendingLaunch's return value.
 */
export function onOpenConnection(cb: (p: LaunchOutcome) => void): () => void {
  return on<LaunchOutcome>('connection:open-request', cb)
}

export type PasswordRequestPayload = { requestId: string; name: string; user: string }
/**
 * A `-con` command line wants to connect but has no password anywhere, so the
 * backend is blocked waiting for one (docs/CLI_CON_DESIGN.md §6.2). Windows
 * shows a native dialog instead and never emits this.
 */
export function onPasswordRequest(cb: (p: PasswordRequestPayload) => void): () => void {
  return on<PasswordRequestPayload>('connection:password-request', cb)
}

/**
 * Answer a pending password request. Call it for EVERY way the modal ends —
 * confirm, cancel, close — because the backend is parked on it; ok=false means
 * "cancelled, no password".
 */
export function submitLaunchPassword(requestId: string, password: string, ok: boolean): Promise<void> {
  return SystemService.SubmitLaunchPassword(requestId, password, ok) as unknown as Promise<void>
}

export type DatabaseSavedPayload = { connId: string; db: string }
export function onDatabaseSaved(cb: (p: DatabaseSavedPayload) => void): () => void {
  return on<DatabaseSavedPayload>('database:saved', cb)
}

export type CloseBlockedPayload = { dirtyTabs: number }
export function onCloseBlocked(cb: (p: CloseBlockedPayload) => void): () => void {
  return on<CloseBlockedPayload>('window:close-blocked', cb)
}

export type MenuCommand =
  | 'menu:new-tab'
  | 'menu:close-tab'
  | 'menu:save-sql'
  | 'menu:open-sql'
  | 'menu:export-result'
  | 'menu:import'
  | 'menu:find'
  | 'menu:toggle-sidebar'
  | 'menu:run-query'
  | 'menu:run-selection'
  | 'menu:explain'
  | 'menu:cancel-query'
  | 'menu:open-docs'

export function onMenu(cmd: MenuCommand, cb: () => void): () => void {
  return on<null>(cmd, cb)
}

// Native "View ▸ Language" menu — each item Emits `menu:set-locale:<code>`.
// onSetLocale subscribes to every supported locale's event and reports the
// chosen code. Returns a combined unsubscribe.
const LOCALE_MENU_CODES = ['en-US', 'zh-CN'] as const
export function onSetLocale(cb: (locale: string) => void): () => void {
  const offs = LOCALE_MENU_CODES.map((code) => on<null>(`menu:set-locale:${code}`, () => cb(code)))
  return () => offs.forEach((off) => off())
}
