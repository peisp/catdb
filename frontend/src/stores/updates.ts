// Updates store — owns the "is there a new release?" state and the install
// flow. Components only see the store; the GitHub API call + DMG / EXE
// download happens in Go via UpdateService.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { update as updateApi } from '../api'

export type UpdatePhase = 'idle' | 'downloading' | 'installing' | 'ready' | 'error'

const APP_VERSION = (import.meta.env.VITE_APP_VERSION as string) || 'dev'

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

  /** Today's date as YYYY-MM-DD, used for the once-per-day gate. */
  function today(): string {
    const d = new Date()
    const y = d.getFullYear()
    const m = String(d.getMonth() + 1).padStart(2, '0')
    const day = String(d.getDate()).padStart(2, '0')
    return `${y}-${m}-${day}`
  }

  async function check(): Promise<boolean> {
    // Development builds (VITE_APP_VERSION unset → "dev") should never
    // call the GitHub API — no rate limit to waste, no false badges.
    if (currentVersion.value === 'dev') return false

    // Once-per-day gate: skip if we already checked today.
    try {
      const lastDate = await updateApi.getLastCheckDate()
      if (lastDate === today()) return false
    } catch {
      // Swallow — stale/missing setting is non-fatal; just proceed.
    }

    lastError.value = ''
    try {
      const res = await updateApi.checkForUpdate(currentVersion.value)
      // Persist today's date after a successful check.
      await updateApi.setLastCheckDate(today()).catch(() => {})
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

  async function install(): Promise<void> {
    attachProgress()
    phase.value = 'downloading'
    downloaded.value = 0
    total.value = 0
    errorCode.value = ''
    lastError.value = ''
    try {
      await updateApi.startInstall(currentVersion.value)
    } catch (e) {
      phase.value = 'error'
      lastError.value = e instanceof Error ? e.message : String(e)
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
    skipCurrent,
    install,
    openDialog,
    closeDialog,
  }
})
