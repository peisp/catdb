// api/system — facade over SystemService bindings.
import { SystemService } from '../../bindings/catdb/internal/services'
import type { FileFilterDescriptor as BoundFileFilter } from '../../bindings/catdb/internal/services/models'
import { on } from './events'

export type FileFilter = BoundFileFilter

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
export function openExternalURL(url: string): Promise<void> {
  return SystemService.OpenExternalURL(url) as unknown as Promise<void>
}

export type ConnectionSavedPayload = { id: string }
export function onConnectionSaved(cb: (p: ConnectionSavedPayload) => void): () => void {
  return on<ConnectionSavedPayload>('connection:saved', cb)
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
