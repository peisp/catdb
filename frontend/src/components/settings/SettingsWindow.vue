<script setup lang="ts">
// SettingsWindow — root view of the standalone "设置" child window. Loaded when
// location.hash starts with #/settings (spawned via SystemService.OpenSettingsWindow).
//
// Layout mirrors ConnectionEditorWindow/ConnectionForm: a titlebar on top, then
// a two-column body — a left category rail + a right settings panel. Categories:
// 语言 (Language) and 关于 (About, incl. check-for-updates).
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { NButton, useMessage } from 'naive-ui'
import { settings as settingsApi } from '../../api'
import type { UpdateChannel } from '../../api/update'
import { i18n, setLocale, isSupportedLocale, t as tr } from '../../i18n'
import { useUpdatesStore } from '../../stores/updates'
import UpdateDialog from '../update/UpdateDialog.vue'
import AiSettingsPanel from './AiSettingsPanel.vue'

const message = useMessage()
const updates = useUpdatesStore()

const isMac = navigator.platform.includes('Mac')
const isWin = !isMac

const isMaximised = ref(false)
async function onWindowCtrl(cmd: 'min' | 'max' | 'close') {
  if (cmd === 'min') { await Window.Minimise(); return }
  if (cmd === 'close') { await Window.Close(); return }
  await Window.ToggleMaximise()
  isMaximised.value = await Window.IsMaximised()
}
function toggleMaximise() {
  void Window.ToggleMaximise()
}

type Category = 'language' | 'ai' | 'about'
const category = ref<Category>('language')

// The Go side re-points an already-open settings window at a new `?section=`
// via SetURL, which only changes the hash — no page reload — so we read the
// hash on mount and again on every hashchange (SystemService.OpenSettingsWindow).
function applyHashSection() {
  const m = /[?&]section=([a-z]+)/.exec(window.location.hash)
  const s = m?.[1]
  if (s === 'language' || s === 'ai' || s === 'about') category.value = s
  // Strip the query once applied so the next SetURL with the same section is
  // still a hash *change* (otherwise no hashchange fires and a re-open would
  // not re-select the category). replaceState fires no hashchange → no loop.
  if (m) history.replaceState(null, '', '#/settings')
}

// --- Language panel ---
// Fixed two options; labels stay in their native form (not translated).
const LOCALE_OPTIONS = [
  { code: 'en-US', label: 'English' },
  { code: 'zh-CN', label: '中文（简体）' },
]
const currentCode = computed(() => i18n.global.locale.value as string)
function onLocaleChange(e: Event) {
  const code = (e.target as HTMLSelectElement).value
  if (!isSupportedLocale(code)) return
  setLocale(code) // 本窗口立即切
  void settingsApi.setLocale(code) // 持久化 + 原生菜单 + 广播其他窗口
}

// --- About panel ---
const appVersion = (import.meta.env.VITE_APP_VERSION as string) || 'dev'
const checking = ref(false)
const isDevBuild = updates.currentVersion === 'dev'

// Update channel options — computed so labels re-resolve on language switch.
const CHANNEL_OPTIONS = computed(() => [
  { value: 'stable' as UpdateChannel, label: tr('settingsWindow.updateChannelStable') },
  { value: 'beta' as UpdateChannel, label: tr('settingsWindow.updateChannelBeta') },
])
function onChannelChange(e: Event) {
  const value = (e.target as HTMLSelectElement).value as UpdateChannel
  if (value !== 'stable' && value !== 'beta') return
  void updates.setChannel(value)
}

// Load the persisted channel so the select reflects the effective value.
onMounted(() => {
  void updates.loadChannel()
  applyHashSection()
  window.addEventListener('hashchange', applyHashSection)
})
onBeforeUnmount(() => {
  window.removeEventListener('hashchange', applyHashSection)
})
async function onCheckUpdate() {
  if (updates.currentVersion === 'dev') {
    message.info(tr('settingsWindow.devBuildNoUpdate'))
    return
  }
  if (checking.value) return
  checking.value = true
  try {
    const found = await updates.check(true)
    if (found) {
      updates.openDialog()
      return
    }
    if (updates.lastError) {
      message.error(tr('statusBar.checkUpdateFailed', { error: updates.lastError }))
      return
    }
    if (updates.skipped) {
      message.info(tr('statusBar.versionSkipped', { version: updates.latestVersion }))
      return
    }
    message.success(tr('statusBar.upToDate', { version: updates.currentVersion }))
  } finally {
    checking.value = false
  }
}
</script>

<template>
  <div class="root">
    <header class="titlebar" :class="{ win: isWin }" @dblclick="toggleMaximise">
      <span class="title">{{ $t('settingsWindow.title') }}</span>
      <!-- Windows frameless caption buttons -->
      <div v-if="isWin" class="window-controls">
        <button type="button" class="win-btn win-btn-min" :title="$t('connectionEditor.minimise')" @click="onWindowCtrl('min')">
          <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="0" y="4.5" width="10" height="1" fill="currentColor" /></svg>
        </button>
        <button type="button" class="win-btn win-btn-max" :title="isMaximised ? $t('connectionEditor.restore') : $t('connectionEditor.maximise')" @click="onWindowCtrl('max')">
          <svg v-if="isMaximised" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1.5" y="3.5" width="6" height="6" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
            <path d="M3.5 3.5V2A0.5 0.5 0 0 1 4 1.5h4A0.5 0.5 0 0 1 8.5 2v4a0.5 0.5 0 0 1-.5.5H7.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
          <svg v-else viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1" y="1" width="8" height="8" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
        </button>
        <button type="button" class="win-btn win-btn-close" :title="$t('common.close')" @click="onWindowCtrl('close')">
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    </header>
    <main class="body">
      <!-- Left rail: category picker. -->
      <aside class="cat-rail">
        <button
          type="button"
          class="rail-item"
          :class="{ active: category === 'language' }"
          @click="category = 'language'"
        >{{ $t('settingsWindow.categoryLanguage') }}</button>
        <button
          type="button"
          class="rail-item"
          :class="{ active: category === 'ai' }"
          @click="category = 'ai'"
        >{{ $t('settingsWindow.categoryAi') }}</button>
        <button
          type="button"
          class="rail-item"
          :class="{ active: category === 'about' }"
          @click="category = 'about'"
        >{{ $t('settingsWindow.categoryAbout') }}</button>
      </aside>

      <!-- Right panel: the active category's settings. -->
      <section class="panel">
        <template v-if="category === 'language'">
          <div class="field">
            <label class="field-label">{{ $t('settingsWindow.uiLanguage') }}</label>
            <select class="native-select" :value="currentCode" @change="onLocaleChange">
              <option v-for="o in LOCALE_OPTIONS" :key="o.code" :value="o.code">{{ o.label }}</option>
            </select>
          </div>
          <p class="hint">{{ $t('settingsWindow.languageHint') }}</p>
        </template>

        <template v-else-if="category === 'ai'">
          <AiSettingsPanel />
        </template>

        <template v-else-if="category === 'about'">
          <div class="about-head">
            <span class="app-name">catdb</span>
            <span class="app-version">{{ $t('settingsWindow.version', { version: appVersion }) }}</span>
          </div>
          <div class="field about-channel">
            <label class="field-label">{{ $t('settingsWindow.updateChannel') }}</label>
            <select
              class="native-select"
              :value="updates.channel"
              :disabled="isDevBuild"
              @change="onChannelChange"
            >
              <option v-for="o in CHANNEL_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </div>
          <div class="about-actions">
            <n-button size="small" :loading="checking" @click="onCheckUpdate">
              {{ $t('settingsWindow.checkForUpdates') }}
            </n-button>
          </div>
        </template>
      </section>
    </main>
    <!-- Mounted here so the "有新版本" download/install flow lives in this window. -->
    <UpdateDialog />
  </div>
</template>

<style scoped>
.root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  width: 100vw;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--n-color);
}
.titlebar {
  position: relative;
  flex: 0 0 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--catdb-fs-small);
  font-weight: 600;
  letter-spacing: 0.2px;
  opacity: 0.85;
  --wails-draggable: drag;
}
.titlebar.win .title {
  padding-left: 150px;
  padding-right: 150px;
}
.titlebar .window-controls {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 10;
  display: flex;
  flex-direction: row;
  align-items: stretch;
  height: 100%;
  -webkit-app-region: no-drag;
}
.titlebar .win-btn {
  --wails-draggable: no-drag;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  padding: 0;
  margin: 0;
  border: none;
  border-radius: 0;
  font: inherit;
  color: inherit;
  cursor: default;
  background: transparent;
  transition: background 80ms ease;
}
.titlebar .win-btn svg {
  width: 14px;
  height: 14px;
  opacity: 0.75;
}
.titlebar .win-btn:hover { background: var(--catdb-hover-fill); }
.titlebar .win-btn:active { background: var(--catdb-pressed-fill); }
.titlebar .win-btn-close:hover { background: var(--catdb-error); }
.titlebar .win-btn-close:hover svg { opacity: 1; }
.titlebar .win-btn-close:active { background: var(--catdb-error); }
.titlebar .win-btn-close:active svg { opacity: 1; }

.body {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: row;
  border-top: 1px solid var(--catdb-separator);
}

/* --- Category rail (left) --- */
.cat-rail {
  flex: 0 0 150px;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 12px 8px;
  border-right: 1px solid var(--catdb-separator);
}
.rail-item {
  display: flex;
  align-items: center;
  padding: 6px 10px;
  border: none;
  border-radius: var(--catdb-rounded-xs);
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: var(--catdb-fs-small);
  text-align: left;
  cursor: default;
  width: 100%;
  transition: background 80ms ease;
}
.rail-item:hover { background: var(--catdb-hover-fill); }
.rail-item.active {
  background: var(--catdb-accent-soft);
  font-weight: 600;
}

/* --- Settings panel (right) --- */
.panel {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: auto;
  padding: 18px 20px;
}
.field {
  display: flex;
  align-items: center;
  gap: 12px;
}
.field-label {
  font-size: var(--catdb-fs-small);
  opacity: 0.85;
}
.native-select {
  height: 28px;
  min-width: 180px;
  padding: 0 8px;
  font: inherit;
  font-size: var(--catdb-fs-body);
  color: inherit;
  background: var(--catdb-surface-content);
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  outline: none;
  box-sizing: border-box;
  transition: border-color 120ms ease, box-shadow 120ms ease;
}
.native-select:hover {
  border-color: var(--catdb-control-border);
}
.native-select:focus {
  border-color: var(--catdb-accent);
  box-shadow: var(--catdb-focus-ring);
}
.native-select:disabled {
  opacity: 0.5;
  cursor: default;
}
.hint {
  margin: 10px 0 0;
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
}

.about-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
}
.app-name {
  font-size: var(--catdb-fs-title);
  font-weight: 600;
}
.app-version {
  font-size: var(--catdb-fs-small);
  opacity: 0.6;
}
.about-channel {
  margin-top: 16px;
}
.about-actions {
  margin-top: 16px;
}
</style>
