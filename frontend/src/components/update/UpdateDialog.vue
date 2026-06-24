<script setup lang="ts">
// UpdateDialog — sits at the AppShell root, visibility driven by the updates
// store. Shows the release notes (markdown) and offers three actions:
//   - 取消        : close dialog, no state change
//   - 跳过该版本  : persist a "ignore <latestVersion>" marker in app_settings
//   - 立即更新    : kick off the download → install flow
//
// During install the buttons swap out for a progress / status line. We never
// auto-close on success because the app is about to quit — the panel rendering
// "应用即将退出以完成更新" is the last thing the user sees.
import { computed, watch } from 'vue'
import { NModal, NButton, NProgress, NSpace, NAlert } from 'naive-ui'
import MarkdownIt from 'markdown-it'
import { useUpdatesStore } from '../../stores/updates'
import { system as systemApi } from '../../api'
import { t } from '../../i18n'

const updates = useUpdatesStore()

// Known update error slugs Go can emit (see UpdateService.StartInstall).
const UPDATE_ERROR_CODES = ['fetch-failed', 'up-to-date', 'no-asset', 'download-failed', 'install-failed']

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
  await updates.install()
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
  <n-modal
    v-model:show="visible"
    preset="card"
    :mask-closable="!isInstalling"
    :close-on-esc="!isInstalling"
    :closable="!isInstalling"
    :style="{ width: '560px' }"
    :title="$t('update.title')"
  >
    <div class="meta">
      <div class="version-row">
        <span class="ver new">v{{ updates.latestVersion }}</span>
        <span class="ver from">{{ $t('update.currentVersion', { version: updates.currentVersion }) }}</span>
      </div>
      <div v-if="publishedAtPretty" class="published">{{ $t('update.publishedAt', { date: publishedAtPretty }) }}</div>
    </div>

    <div class="notes" v-html="renderedNotes" @click="onNotesClick" />

    <n-alert
      v-if="!updates.hasAsset && !isInstalling"
      type="warning"
      :show-icon="false"
      class="no-asset"
    >
      {{ $t('update.noAsset') }}
    </n-alert>

    <div v-if="isInstalling || updates.phase === 'ready' || updates.phase === 'error'" class="install-status">
      <div v-if="updates.phase === 'downloading'" class="status-row">
        <div class="status-text">
          {{ $t('update.downloading', { name: updates.assetName, downloaded: downloadedMB, total: totalMB }) }}
        </div>
        <n-progress
          type="line"
          :percentage="progressPercent"
          :show-indicator="true"
          :height="6"
        />
      </div>
      <div v-else-if="updates.phase === 'installing'" class="status-row">
        <div class="status-text">{{ $t('update.preparingInstall') }}</div>
        <n-progress type="line" :percentage="100" :show-indicator="false" :height="6" status="info" />
      </div>
      <div v-else-if="updates.phase === 'ready'" class="status-row">
        <div class="status-text ready">{{ $t('update.exitingToUpdate') }}</div>
      </div>
      <div v-else-if="updates.phase === 'error'" class="status-row">
        <n-alert type="error" :show-icon="false">{{ errorText }}</n-alert>
      </div>
    </div>

    <template #footer>
      <n-space justify="space-between" align="center">
        <a
          v-if="updates.releaseUrl"
          class="open-link"
          :href="updates.releaseUrl"
          @click="openReleasePage"
        >
          {{ $t('update.viewOnGitHub') }} ↗
        </a>
        <span v-else />
        <n-space>
          <n-button
            v-if="updates.phase !== 'ready'"
            quaternary
            :disabled="isInstalling"
            @click="onCancel"
          >
            {{ $t('common.cancel') }}
          </n-button>
          <n-button
            v-if="updates.phase !== 'ready'"
            quaternary
            :disabled="isInstalling"
            @click="onSkip"
          >
            {{ $t('update.skipVersion') }}
          </n-button>
          <n-button
            v-if="updates.phase !== 'ready'"
            type="primary"
            :disabled="!updates.hasAsset || isInstalling"
            :loading="isInstalling"
            @click="onInstall"
          >
            {{ $t('update.installNow') }}
          </n-button>
        </n-space>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  font-size: 12px;
  color: var(--text-subtle, #888);
}
.version-row { display: flex; align-items: center; gap: 12px; }
.ver.new {
  font-size: 14px;
  font-weight: 600;
  color: var(--n-text-color, #222);
  background: rgba(10, 132, 255, 0.12);
  padding: 2px 8px;
  border-radius: 4px;
}
.ver.from {
  color: var(--text-subtle, #888);
  font-size: 12px;
}
.published { font-size: 12px; opacity: 0.7; }

.notes {
  max-height: 280px;
  overflow-y: auto;
  padding: 10px 14px;
  border-radius: 6px;
  background: rgba(127, 127, 127, 0.06);
  font-size: 13px;
  line-height: 1.55;
}
.notes :deep(h1),
.notes :deep(h2),
.notes :deep(h3) {
  font-size: 14px;
  margin: 8px 0 6px;
}
.notes :deep(p) { margin: 6px 0; }
.notes :deep(ul),
.notes :deep(ol) { padding-left: 22px; margin: 6px 0; }
.notes :deep(li) { margin: 2px 0; }
.notes :deep(code) {
  background: rgba(127, 127, 127, 0.12);
  padding: 1px 4px;
  border-radius: 3px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}
.notes :deep(pre) {
  background: rgba(127, 127, 127, 0.12);
  padding: 8px 10px;
  border-radius: 4px;
  overflow-x: auto;
}
.notes :deep(a) { color: #0a84ff; text-decoration: none; }
.notes :deep(a:hover) { text-decoration: underline; }
.notes :deep(.empty) { opacity: 0.6; font-style: italic; }

.no-asset { margin-top: 10px; font-size: 12px; }

.install-status { margin-top: 14px; }
.status-row { display: flex; flex-direction: column; gap: 6px; }
.status-text {
  font-size: 12px;
  color: var(--text-subtle, #888);
}
.status-text.ready {
  color: #16a34a;
  font-weight: 500;
}

.open-link {
  font-size: 12px;
  color: var(--text-subtle, #888);
  text-decoration: none;
}
.open-link:hover { color: #0a84ff; }
</style>
