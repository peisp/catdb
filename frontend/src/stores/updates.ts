// Updates store — owns the "is there a new release?" state and the install
// flow. Components only see the store; the GitHub API call + DMG / EXE
// download happens in Go via UpdateService.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { update as updateApi } from '../api'

export type UpdatePhase = 'idle' | 'downloading' | 'downloaded' | 'installing' | 'ready' | 'error'

const APP_VERSION = (import.meta.env.VITE_APP_VERSION as string) || 'dev'

// Auto-check cadence: once shortly after startup, then every 8 hours in the
// background. The persisted min-interval gate sits just under the timer
// period so a periodic firing is never skipped by its own previous run.
const STARTUP_DELAY_MS = 10_000
const CHECK_INTERVAL_MS = 8 * 60 * 60 * 1000
const MIN_RECHECK_MS = CHECK_INTERVAL_MS - 10 * 60 * 1000

export const useUpdatesStore = defineStore('updates', () => {
  const currentVersion = ref(APP_VERSION)
  const latestVersion = ref('')
  const releaseNotes = ref('')
  const releaseUrl = ref('')
  const publishedAt = ref('')
  const assetName = ref('')
  const hasAsset = ref(false)
  const available = ref(false)
  const skipped = ref(false)
  const lastCheckedAt = ref<number | null>(null)
  const lastError = ref('')

  // dialog visibility — UpdateDialog binds to this. Setting true brings the
  // dialog up; setting false closes it without affecting any other state.
  const dialogOpen = ref(false)

  // install progress
  const phase = ref<UpdatePhase>('idle')
  const downloaded = ref(0)
  const total = ref(0)
  // Locale-independent status/error slug from Go (e.g. 'fetch-failed'); the
  // UpdateDialog maps it to a localized message via error.update.*.
  const errorCode = ref('')

  let unsubscribe: (() => void) | null = null

  function attachProgress() {
    if (unsubscribe) return
    unsubscribe = updateApi.onUpdateProgress((p) => {
      phase.value = p.phase
      if (typeof p.downloaded === 'number') downloaded.value = p.downloaded
      if (typeof p.total === 'number') total.value = p.total
      if (p.code) errorCode.value = p.code
      if (p.error) lastError.value = p.error
    })
  }

  // force=true performs a real check unconditionally — used by the manual
  // "check for updates" click. The default (false) keeps the min-interval
  // throttle for the automatic checks (startup + 8h background timer).
  async function check(force = false): Promise<boolean> {
    // Development builds (VITE_APP_VERSION unset → "dev") should never
    // call the GitHub API — no rate limit to waste, no false badges.
    if (currentVersion.value === 'dev') return false

    // Min-interval gate: skip if the last successful check was recent.
    // The persisted value is an ISO timestamp (older builds stored a plain
    // YYYY-MM-DD — Date.parse still handles it; unparseable → treat as stale).
    // A manual click (force) bypasses this for a fresh result.
    if (!force) {
      try {
        const last = Date.parse(await updateApi.getLastCheckDate())
        if (!Number.isNaN(last) && Date.now() - last < MIN_RECHECK_MS) return false
      } catch {
        // Swallow — stale/missing setting is non-fatal; just proceed.
      }
    }

    lastError.value = ''
    try {
      const res = await updateApi.checkForUpdate(currentVersion.value)
      // Persist the check time after a successful check.
      await updateApi.setLastCheckDate(new Date().toISOString()).catch(() => {})
      latestVersion.value = res.latestVersion
      releaseNotes.value = res.releaseNotes
      releaseUrl.value = res.releaseUrl
      publishedAt.value = res.publishedAt
      assetName.value = res.assetName
      hasAsset.value = res.hasAsset
      available.value = res.available
      skipped.value = res.skipped
      lastCheckedAt.value = Date.now()
      return res.available
    } catch (e) {
      lastError.value = e instanceof Error ? e.message : String(e)
      return false
    }
  }

  async function skipCurrent(): Promise<void> {
    if (!latestVersion.value) return
    await updateApi.skipVersion(latestVersion.value)
    skipped.value = true
    available.value = false
    dialogOpen.value = false
  }

  // download fetches the release asset and stages it — the app keeps running.
  // Ends in phase 'downloaded'; the actual install waits for the user to
  // trigger restartAndInstall (重启并更新).
  async function download(): Promise<void> {
    attachProgress()
    phase.value = 'downloading'
    downloaded.value = 0
    total.value = 0
    errorCode.value = ''
    lastError.value = ''
    try {
      await updateApi.downloadUpdate(currentVersion.value)
    } catch (e) {
      phase.value = 'error'
      lastError.value = e instanceof Error ? e.message : String(e)
    }
  }

  // restartAndInstall hands the staged download to the platform installer
  // (silent) and quits the app — only call from an explicit user action.
  async function restartAndInstall(): Promise<void> {
    attachProgress()
    phase.value = 'installing'
    errorCode.value = ''
    lastError.value = ''
    try {
      await updateApi.restartAndInstall()
    } catch (e) {
      phase.value = 'error'
      lastError.value = e instanceof Error ? e.message : String(e)
    }
  }

  // startAutoCheck wires the automatic cadence: one check shortly after
  // startup (throttled by the persisted min-interval gate), then a background
  // re-check every 8 hours. Returns a cleanup function for the caller's
  // unmount hook. No-op in dev builds.
  function startAutoCheck(): () => void {
    if (currentVersion.value === 'dev') return () => {}
    const startupTimer = window.setTimeout(() => { void check() }, STARTUP_DELAY_MS)
    const intervalTimer = window.setInterval(() => { void check() }, CHECK_INTERVAL_MS)
    return () => {
      window.clearTimeout(startupTimer)
      window.clearInterval(intervalTimer)
    }
  }

  function openDialog() {
    dialogOpen.value = true
  }
  function closeDialog() {
    dialogOpen.value = false
  }

  // Convenience — true when the StatusBar badge dot should be visible.
  const hasBadge = computed(() => available.value && !skipped.value)

  return {
    currentVersion,
    latestVersion,
    releaseNotes,
    releaseUrl,
    publishedAt,
    assetName,
    hasAsset,
    available,
    skipped,
    lastCheckedAt,
    lastError,
    dialogOpen,
    phase,
    downloaded,
    total,
    errorCode,
    hasBadge,
    check,
    startAutoCheck,
    skipCurrent,
    download,
    restartAndInstall,
    openDialog,
    closeDialog,
  }
})
