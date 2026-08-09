<script setup lang="ts">
// StatusBar — bottom strip (DESIGN.md). Follows the workspace's active
// connection (passed down from AppShell) and its current database. Shows
// connection name, connection status, current database, server version/user,
// theme mode, and build tag.
import { computed, ref, watch } from 'vue'
import { CDropdown, useMessage } from '../ui'
import { connections as connectionsApi, system as systemApi } from '../../api'
import { useConnectionsStore } from '../../stores/connections'
import { useQueryStore } from '../../stores/query'
import { useThemeStore } from '../../stores/theme'
import { useUpdatesStore } from '../../stores/updates'
import { t } from '../../i18n'
import type { ConnectionProfile, ServerInfo } from '../../api/connections'
import type { ThemePreference } from '../../api/settings'

const props = defineProps<{ activeConn: ConnectionProfile | null }>()

const REPO_URL = 'https://github.com/peisp/catdb'

function openRepo() {
  void systemApi.openExternalURL(REPO_URL)
}

const conns = useConnectionsStore()
const queryStore = useQueryStore()
const theme = useThemeStore()
const updates = useUpdatesStore()
const message = useMessage()

const serverInfo = ref<ServerInfo | null>(null)
const checking = ref(false)

const isLive = computed(() => !!props.activeConn && conns.liveIds.has(props.activeConn.id))

// Current database: the active tab's db, falling back to the object tree's
// last-selected database for this connection.
const currentDb = computed(() => {
  const c = props.activeConn
  if (!c) return null
  return queryStore.activeTab(c.id)?.db || queryStore.selectedDb[c.id] || null
})

// Fetch server info when the live active connection changes — this covers
// switching connections and a selected connection going live/offline.
const liveConnId = computed(() => (isLive.value ? props.activeConn!.id : null))
watch(liveConnId, async (id) => {
  if (!id) { serverInfo.value = null; return }
  try {
    serverInfo.value = await connectionsApi.getServerInfo(id)
  } catch {
    // connection may not be fully live yet, that's fine
    serverInfo.value = null
  }
}, { immediate: true })

// 主题槽位:点击弹出 浅色/深色/跟随系统 三选一。跟随系统时额外标出当前生效模式。
const THEME_OPTIONS = computed(() => [
  { key: 'light', label: t('common.themeLight') },
  { key: 'dark', label: t('common.themeDark') },
  { key: 'system', label: t('common.themeSystem') },
])
const effectiveModeLabel = computed(() =>
  theme.mode === 'dark' ? t('statusBar.themeDark') : t('statusBar.themeLight'),
)
const themeLabel = computed(() =>
  theme.preference === 'system'
    ? t('statusBar.themeSystemWith', { mode: effectiveModeLabel.value })
    : effectiveModeLabel.value,
)
function onThemeSelect(key: string | number) {
  theme.setPreference(key as ThemePreference)
}

const appVersion = import.meta.env.VITE_APP_VERSION || 'dev'

// Click handler on the version slot: if an update is already known, open the
// dialog directly; otherwise trigger a fresh check and toast the outcome.
// The dialog ONLY opens when a genuinely newer version is found — otherwise
// we'd be showing "发现新版本" with the current version, which is the bug.
async function onVersionClick() {
  if (updates.hasBadge) {
    updates.openDialog()
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
      message.error(t('statusBar.checkUpdateFailed', { error: updates.lastError }))
      return
    }
    if (updates.skipped) {
      message.info(t('statusBar.versionSkipped', { version: updates.latestVersion }))
      return
    }
    message.success(t('statusBar.upToDate', { version: updates.currentVersion }))
  } finally {
    checking.value = false
  }
}

const versionTitle = computed(() => {
  if (updates.hasBadge) return t('statusBar.viewNewVersion', { version: updates.latestVersion })
  return t('statusBar.checkForUpdates')
})
</script>

<template>
  <div class="bar">
    <span class="slot mono">{{ activeConn ? activeConn.name : $t('statusBar.noConnection') }}</span>
    <span class="sep" />
    <span class="slot">{{ isLive ? $t('statusBar.connected') : $t('statusBar.disconnected') }}</span>
    <!-- 当前数据库槽位（暂时隐藏）
    <span v-if="currentDb" class="sep" />
    <span v-if="currentDb" class="slot mono">{{ currentDb }}</span>
    -->

    <span v-if="serverInfo" class="sep" />
    <span v-if="serverInfo" class="slot mono">{{ serverInfo.version }}</span>
    <span v-if="serverInfo" class="sep" />
    <span v-if="serverInfo" class="slot mono">{{ serverInfo.user }}</span>
    <span class="grow" />
    <CDropdown
      placement="top-end"
      :options="THEME_OPTIONS"
      @select="onThemeSelect"
    >
      <button type="button" class="theme-btn" :title="$t('statusBar.themeMenuTitle')">
        {{ themeLabel }}
      </button>
      <template #icon="{ option }">
        <span class="theme-check">{{ option.key === theme.preference ? '✓' : '' }}</span>
      </template>
    </CDropdown>
    <span class="sep" />
    <button
      type="button"
      class="icon-btn"
      :title="$t('statusBar.viewRepoOnGitHub')"
      @click="openRepo"
    >
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path
          fill="currentColor"
          d="M8 0C3.58 0 0 3.58 0 8a8 8 0 0 0 5.47 7.59c.4.07.55-.17.55-.38
             0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13
             -.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66
             .07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95
             0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82
             a7.5 7.5 0 0 1 2-.27c.68 0 1.36.09 2 .27
             1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15
             0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2
             0 .21.15.46.55.38A8.012 8.012 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
        />
      </svg>
    </button>
    <span class="sep" />
    <button
      type="button"
      class="version-btn"
      :class="{ 'has-update': updates.hasBadge, checking: checking }"
      :title="versionTitle"
      @click="onVersionClick"
    >
      <span class="mono">catdb v{{ appVersion }}</span>
      <span v-if="updates.hasBadge" class="badge" aria-hidden="true" />
    </button>
  </div>
</template>

<style scoped>
/* DESIGN.md statusbar 规格：高 24px，文字 small + text-secondary。 */
.bar {
  display: flex;
  align-items: center;
  height: var(--catdb-statusbar-height);
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
  background: var(--catdb-surface-chrome);
  padding: 0 18px;
  gap: 8px;
}
.slot { white-space: nowrap; }
.sep { width: 1px; height: 12px; background: currentColor; opacity: 0.15; }
.grow { flex: 1 1 auto; }

.theme-btn {
  display: inline-flex;
  align-items: center;
  height: 16px;
  padding: 0 5px;
  margin: 0;
  border: none;
  border-radius: var(--catdb-rounded-xs);
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: var(--catdb-fs-small);
  white-space: nowrap;
  cursor: default;
  transition: background 80ms ease;
}
.theme-btn:hover { background: var(--catdb-hover-fill); }
.theme-check {
  display: inline-block;
  width: 10px;
  text-align: center;
}

.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  padding: 0;
  margin: 0;
  border: none;
  background: transparent;
  color: var(--catdb-text-primary);
  cursor: default;
  opacity: 0.8;
  transition: opacity 100ms ease;
}
.icon-btn:hover { opacity: 1; }
.icon-btn svg { width: 12px; height: 12px; display: block; }

.version-btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 2px;
  margin: 0;
  border: none;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: default;
  white-space: nowrap;
  opacity: 0.9;
}
.version-btn:hover { opacity: 1; }
.version-btn.has-update { color: var(--catdb-warning); opacity: 1; }
.version-btn.checking { opacity: 0.6; }

.badge {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--catdb-warning);
  box-shadow: 0 0 0 1px var(--catdb-surface-chrome);
}
</style>
