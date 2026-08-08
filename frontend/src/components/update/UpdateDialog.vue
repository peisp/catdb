<script setup lang="ts">
// UpdateDialog — sits at the AppShell root, visibility driven by the updates
// store. Shows the release notes (markdown) and offers the actions:
//   - 取消        : close dialog, no state change
//   - 跳过该版本  : persist a "ignore <latestVersion>" marker in app_settings
//   - 立即更新    : download the release asset (app keeps running)
//   - 重启并更新  : after the download lands, the user explicitly triggers the
//                   silent install + relaunch (the app quits at this point)
//
// During download/install the buttons swap out for a progress / status line.
// We never auto-close on success because the app is about to quit — the panel
// rendering "应用即将退出以完成更新" is the last thing the user sees.
import { computed, watch } from 'vue'
import { CAlert, CButton, CModal, CProgress, CSpin, CTag } from '../ui'
import MarkdownIt from 'markdown-it'
import { useUpdatesStore } from '../../stores/updates'
import { system as systemApi } from '../../api'
import { t } from '../../i18n'

const updates = useUpdatesStore()

// Known update error slugs Go can emit (see UpdateService.DownloadUpdate /
// RestartAndInstall).
const UPDATE_ERROR_CODES = ['fetch-failed', 'up-to-date', 'no-asset', 'download-failed', 'install-failed', 'no-download']

// Localized error line: friendly message from the Go error code, with the raw
// technical detail appended when present; falls back to the generic message.
const errorText = computed(() => {
  const code = updates.errorCode
  const friendly = code && UPDATE_ERROR_CODES.includes(code) ? t(`error.update.${code}`) : ''
  const detail = updates.lastError
  if (friendly && detail) return `${friendly}: ${detail}`
  return friendly || detail || t('update.updateFailed')
})

const md = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
})

const renderedNotes = computed(() => {
  if (!updates.releaseNotes) return '<p class="empty">No release notes.</p>'
  return md.render(updates.releaseNotes)
})

const isInstalling = computed(
  () => updates.phase === 'downloading' || updates.phase === 'installing',
)

const progressPercent = computed(() => {
  if (!updates.total) return 0
  return Math.min(100, Math.floor((updates.downloaded / updates.total) * 100))
})

const downloadedMB = computed(() => (updates.downloaded / 1024 / 1024).toFixed(1))
const totalMB = computed(() =>
  updates.total ? (updates.total / 1024 / 1024).toFixed(1) : '?',
)

const publishedAtPretty = computed(() => {
  if (!updates.publishedAt) return ''
  try {
    const d = new Date(updates.publishedAt)
    return d.toLocaleString()
  } catch {
    return updates.publishedAt
  }
})

const visible = computed({
  get: () => updates.dialogOpen,
  set: (v) => { updates.dialogOpen = v },
})

// If the user manually closes the dialog (esc / mask click) while idle, do
// nothing extra. If install is in progress, prevent closing.
watch(visible, (next, prev) => {
  if (prev && !next && isInstalling.value) {
    visible.value = true
  }
})

function onCancel() {
  if (isInstalling.value) return
  updates.closeDialog()
}

async function onSkip() {
  await updates.skipCurrent()
}

async function onInstall() {
  await updates.download()
}

async function onRestart() {
  await updates.restartAndInstall()
}

function openReleasePage(e: Event) {
  e.preventDefault()
  if (!updates.releaseUrl) return
  void systemApi.openExternalURL(updates.releaseUrl)
}

// Delegated handler for links inside the markdown-rendered release notes —
// anchors in the WebView don't reach the system browser on their own.
function onNotesClick(e: MouseEvent) {
  const target = e.target as HTMLElement | null
  if (!target) return
  const anchor = target.closest('a') as HTMLAnchorElement | null
  if (!anchor) return
  const href = anchor.getAttribute('href') || ''
  if (!href || href.startsWith('#')) return
  e.preventDefault()
  void systemApi.openExternalURL(href)
}
</script>

<template>
  <CModal
    v-model:show="visible"
    :width="560"
    :closable="!isInstalling"
  >
    <div class="modal-title">{{ $t('update.title') }}</div>

    <div class="meta">
      <div class="version-row">
        <span class="ver new">v{{ updates.latestVersion }}</span>
        <CTag v-if="updates.prerelease" kind="warning">Beta</CTag>
        <span class="ver from">{{ $t('update.currentVersion', { version: updates.currentVersion }) }}</span>
      </div>
      <div v-if="publishedAtPretty" class="published">{{ $t('update.publishedAt', { date: publishedAtPretty }) }}</div>
    </div>

    <div class="notes" v-html="renderedNotes" @click="onNotesClick" />

    <CAlert
      v-if="!updates.hasAsset && !isInstalling"
      kind="warning"
      class="no-asset"
    >
      {{ $t('update.noAsset') }}
    </CAlert>

    <div v-if="isInstalling || updates.phase === 'downloaded' || updates.phase === 'ready' || updates.phase === 'error'" class="install-status">
      <div v-if="updates.phase === 'downloading'" class="status-row">
        <div class="status-text">
          {{ $t('update.downloading', { name: updates.assetName, downloaded: downloadedMB, total: totalMB }) }}
        </div>
        <CProgress :percentage="progressPercent" />
      </div>
      <div v-else-if="updates.phase === 'downloaded'" class="status-row">
        <div class="status-text ready">{{ $t('update.downloadedReady') }}</div>
      </div>
      <div v-else-if="updates.phase === 'installing'" class="status-row">
        <div class="status-text">{{ $t('update.preparingInstall') }}</div>
        <CProgress indeterminate />
      </div>
      <div v-else-if="updates.phase === 'ready'" class="status-row">
        <div class="status-text ready">{{ $t('update.exitingToUpdate') }}</div>
      </div>
      <div v-else-if="updates.phase === 'error'" class="status-row">
        <CAlert kind="error">{{ errorText }}</CAlert>
      </div>
    </div>

    <div class="footer">
      <a
        v-if="updates.releaseUrl"
        class="open-link"
        :href="updates.releaseUrl"
        @click="openReleasePage"
      >
        {{ $t('update.viewOnGitHub') }} ↗
      </a>
      <span v-else />
      <div class="footer-actions">
        <CButton
          v-if="updates.phase !== 'ready'"
          :disabled="isInstalling"
          @click="onCancel"
        >
          {{ updates.phase === 'downloaded' ? $t('update.later') : $t('common.cancel') }}
        </CButton>
        <CButton
          v-if="updates.phase !== 'ready' && updates.phase !== 'downloaded'"
          :disabled="isInstalling"
          @click="onSkip"
        >
          {{ $t('update.skipVersion') }}
        </CButton>
        <CButton
          v-if="updates.phase === 'downloaded'"
          variant="primary"
          @click="onRestart"
        >
          {{ $t('update.restartAndInstall') }}
        </CButton>
        <CButton
          v-else-if="updates.phase !== 'ready'"
          variant="primary"
          :disabled="!updates.hasAsset || isInstalling"
          @click="onInstall"
        >
          <CSpin v-if="isInstalling" :size="12" />
          {{ $t('update.installNow') }}
        </CButton>
      </div>
    </div>
  </CModal>
</template>

<style scoped>
.modal-title {
  font-size: var(--catdb-fs-title);
  font-weight: 600;
  color: var(--catdb-text-primary);
  margin-bottom: 12px;
}
.meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
}
.version-row { display: flex; align-items: center; gap: 12px; }
.ver.new {
  font-size: var(--catdb-fs-body);
  font-weight: 600;
  color: var(--catdb-accent);
  background: var(--catdb-accent-soft);
  padding: 2px 8px;
  border-radius: var(--catdb-rounded-xs);
}
.ver.from {
  color: var(--catdb-text-secondary);
  font-size: var(--catdb-fs-small);
}
.published { font-size: var(--catdb-fs-small); opacity: 0.7; }

.notes {
  max-height: 280px;
  overflow-y: auto;
  padding: 10px 14px;
  border-radius: var(--catdb-rounded-md);
  background: var(--catdb-hover-fill);
  font-size: var(--catdb-fs-body);
  line-height: 1.55;
}
.notes :deep(h1),
.notes :deep(h2),
.notes :deep(h3) {
  font-size: var(--catdb-fs-title);
  margin: 8px 0 6px;
}
.notes :deep(p) { margin: 6px 0; }
.notes :deep(ul),
.notes :deep(ol) { padding-left: 22px; margin: 6px 0; }
.notes :deep(li) { margin: 2px 0; }
.notes :deep(code) {
  background: var(--catdb-pressed-fill);
  padding: 1px 4px;
  border-radius: var(--catdb-rounded-xs);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: var(--catdb-fs-mono);
}
.notes :deep(pre) {
  background: var(--catdb-pressed-fill);
  padding: 8px 10px;
  border-radius: var(--catdb-rounded-sm);
  overflow-x: auto;
}
.notes :deep(a) { color: var(--catdb-accent); text-decoration: none; }
.notes :deep(a:hover) { text-decoration: underline; }
.notes :deep(.empty) { opacity: 0.6; font-style: italic; }

.no-asset { margin-top: 10px; font-size: var(--catdb-fs-small); }

.install-status { margin-top: 14px; }
.status-row { display: flex; flex-direction: column; gap: 6px; }
.status-text {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
}
.status-text.ready {
  color: var(--catdb-success);
  font-weight: 600;
}

.footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 16px;
}
.footer-actions { display: flex; align-items: center; gap: 8px; }

.open-link {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
  text-decoration: none;
}
.open-link:hover { color: var(--catdb-accent); }
</style>
