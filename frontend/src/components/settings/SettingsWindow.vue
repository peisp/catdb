<script setup lang="ts">
// SettingsWindow — root view of the standalone "设置" child window. Loaded when
// location.hash starts with #/settings (spawned via SystemService.OpenSettingsWindow).
//
// Layout mirrors ConnectionEditorWindow/ConnectionForm: a titlebar on top, then
// a two-column body — a left category rail + a right settings panel. Categories:
// 语言 (Language) and 关于 (About, incl. check-for-updates).
import { computed, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { NButton, useMessage } from 'naive-ui'
import { settings as settingsApi } from '../../api'
import { i18n, setLocale, isSupportedLocale, t as tr } from '../../i18n'
import { useUpdatesStore } from '../../stores/updates'
import UpdateDialog from '../update/UpdateDialog.vue'

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

type Category = 'language' | 'about'
const category = ref<Category>('language')

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

        <template v-else-if="category === 'about'">
          <div class="about-head">
            <span class="app-name">catdb</span>
            <span class="app-version">{{ $t('settingsWindow.version', { version: appVersion }) }}</span>
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
  font-size: 12px;
  font-weight: 500;
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
.titlebar .win-btn:hover { background: rgba(127, 127, 127, 0.15); }
.titlebar .win-btn:active { background: rgba(127, 127, 127, 0.25); }
.titlebar .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
.titlebar .win-btn-close:hover svg { opacity: 1; }
.titlebar .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
.titlebar .win-btn-close:active svg { opacity: 1; }
@media (prefers-color-scheme: dark) {
  .titlebar .win-btn:hover { background: rgba(255, 255, 255, 0.1); }
  .titlebar .win-btn:active { background: rgba(255, 255, 255, 0.16); }
  .titlebar .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
  .titlebar .win-btn-close:hover svg { opacity: 1; }
  .titlebar .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
}

.body {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: row;
  border-top: 1px solid var(--n-border-color, rgba(127,127,127,0.15));
}

/* --- Category rail (left) --- */
.cat-rail {
  flex: 0 0 150px;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 12px 8px;
  border-right: 1px solid var(--n-border-color, rgba(127,127,127,0.15));
}
.rail-item {
  display: flex;
  align-items: center;
  padding: 6px 10px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 12px;
  text-align: left;
  cursor: default;
  width: 100%;
  transition: background 80ms ease;
}
.rail-item:hover { background: rgba(127, 127, 127, 0.1); }
.rail-item.active {
  background: rgba(24, 160, 88, 0.12);
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
  font-size: 12px;
  opacity: 0.85;
}
.native-select {
  height: 28px;
  min-width: 180px;
  padding: 0 8px;
  font: inherit;
  font-size: 13px;
  color: inherit;
  background: var(--n-color, transparent);
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.3));
  border-radius: 3px;
  outline: none;
  box-sizing: border-box;
  transition: border-color 120ms ease, box-shadow 120ms ease;
}
.native-select:hover {
  border-color: var(--n-border-color-hover, rgba(127, 127, 127, 0.5));
}
.native-select:focus {
  border-color: var(--n-border-color-focus, #18a058);
  box-shadow: 0 0 0 2px rgba(24, 160, 88, 0.18);
}
.hint {
  margin: 10px 0 0;
  font-size: 12px;
  opacity: 0.55;
}

.about-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
}
.app-name {
  font-size: 16px;
  font-weight: 700;
}
.app-version {
  font-size: 12px;
  opacity: 0.6;
}
.about-actions {
  margin-top: 16px;
}
</style>
