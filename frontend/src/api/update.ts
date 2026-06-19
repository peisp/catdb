// api/update — facade over UpdateService bindings + update:progress event.
//
// The Go UpdateService talks to GitHub Releases on demand, then emits
// `update:progress` events as it downloads/installs. The store layer is the
// only place that should listen for those — components subscribe through the
// store.
import { UpdateService } from '../../bindings/catdb/internal/services'
import { on } from './events'

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
}

export type UpdateProgressPhase = 'downloading' | 'installing' | 'ready' | 'error'

export type UpdateProgress = {
  phase: UpdateProgressPhase
  message?: string
  downloaded?: number
  total?: number
  path?: string
  error?: string
}

export function checkForUpdate(currentVersion: string): Promise<UpdateCheckResult> {
  return UpdateService.CheckForUpdate(currentVersion) as unknown as Promise<UpdateCheckResult>
}

export function getSkippedVersion(): Promise<string> {
  return UpdateService.GetSkippedVersion() as unknown as Promise<string>
}

export function skipVersion(version: string): Promise<void> {
  return UpdateService.SkipVersion(version) as unknown as Promise<void>
}

export function startInstall(currentVersion: string): Promise<void> {
  return UpdateService.StartInstall(currentVersion) as unknown as Promise<void>
}

export function onUpdateProgress(cb: (p: UpdateProgress) => void): () => void {
  return on<UpdateProgress>('update:progress', cb)
}
