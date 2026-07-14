// api/update — facade over UpdateService bindings + update:progress event.
//
// The Go UpdateService talks to GitHub Releases on demand, then emits
// `update:progress` events as it downloads/installs. The store layer is the
// only place that should listen for those — components subscribe through the
// store.
import { UpdateService } from '../../bindings/catdb/internal/services'
import { on } from './events'

export type UpdateChannel = 'stable' | 'beta'

export type UpdateCheckResult = {
  available: boolean
  latestVersion: string
  currentVersion: string
  releaseNotes: string
  releaseUrl: string
  publishedAt: string
  assetName: string
  hasAsset: boolean
  skipped: boolean
  prerelease: boolean
}

export type UpdateProgressPhase = 'downloading' | 'downloaded' | 'installing' | 'ready' | 'error'

export type UpdateProgress = {
  phase: UpdateProgressPhase
  // Stable, locale-independent slug for error/status (e.g. "fetch-failed").
  // The store maps it to a localized message; see error.update.* in i18n.
  code?: string
  downloaded?: number
  total?: number
  path?: string
  error?: string
}

export function checkForUpdate(currentVersion: string): Promise<UpdateCheckResult> {
  return UpdateService.CheckForUpdate(currentVersion) as unknown as Promise<UpdateCheckResult>
}

export function getChannel(currentVersion: string): Promise<UpdateChannel> {
  return UpdateService.GetChannel(currentVersion) as unknown as Promise<UpdateChannel>
}

export function setChannel(channel: UpdateChannel): Promise<void> {
  return UpdateService.SetChannel(channel) as unknown as Promise<void>
}

export function getSkippedVersion(): Promise<string> {
  return UpdateService.GetSkippedVersion() as unknown as Promise<string>
}

export function skipVersion(version: string): Promise<void> {
  return UpdateService.SkipVersion(version) as unknown as Promise<void>
}

export function getLastCheckDate(): Promise<string> {
  return UpdateService.GetLastCheckDate() as unknown as Promise<string>
}

export function setLastCheckDate(date: string): Promise<void> {
  return UpdateService.SetLastCheckDate(date) as unknown as Promise<void>
}

export function downloadUpdate(currentVersion: string): Promise<void> {
  return UpdateService.DownloadUpdate(currentVersion) as unknown as Promise<void>
}

export function restartAndInstall(): Promise<void> {
  return UpdateService.RestartAndInstall() as unknown as Promise<void>
}

export function onUpdateProgress(cb: (p: UpdateProgress) => void): () => void {
  return on<UpdateProgress>('update:progress', cb)
}
