<script setup lang="ts">
// AgentSessionHeader — session chrome (AGENT_DESIGN.md §10.1):
//   [connection ⛁][db ▾][schema ▾]  [Ask|Agent]  [tokens]  [sessions ≡][＋]
// The connection is anchored at session creation (§10.2) so it is shown, not
// editable. db/schema switch the session namespace; the session list popover
// switches / renames / deletes sessions. Cumulative tokens are plain text
// (cost + watermark bar are a later milestone).
import { computed, ref } from 'vue'
import { NPopover } from 'naive-ui'
import AppIcon from '../shared/AppIcon.vue'
import databaseIcon from '../../assets/icons/database.svg?raw'
import plusIcon from '../../assets/icons/plus.svg?raw'
import historyIcon from '../../assets/icons/history.svg?raw'
import xIcon from '../../assets/icons/x.svg?raw'
import lockIcon from '../../assets/icons/lock.svg?raw'
import { t } from '../../i18n'
import type { AgentSession } from '../../api/agent'

const props = defineProps<{
  connectionName: string
  // Environment label of the anchored connection (闸 1): '' | dev | test |
  // staging | prod. Drives the read-only badge (AGENT_DESIGN §10.2).
  environment: string
  session: AgentSession | null
  sessions: AgentSession[]
  databases: string[]
  schemas: string[]
  schemasSupported: boolean
  currentDb: string
  currentSchema: string
  tokens: number
  mode: 'ask' | 'agent'
}>()

const emit = defineEmits<{
  (e: 'new-session'): void
  (e: 'select-session', id: string): void
  (e: 'rename-session', id: string): void
  (e: 'delete-session', id: string): void
  (e: 'change-db', db: string): void
  (e: 'change-schema', schema: string): void
  (e: 'change-mode', mode: 'ask' | 'agent'): void
}>()

const listOpen = ref(false)

// Environment badge: prod = red + lock (hard read-only), dev/test/staging =
// neutral tag, '' = gray "unmarked" nudge. Tier names reuse the connection
// form's localized labels so there is a single source of truth.
const envKind = computed<'prod' | 'other' | 'unmarked'>(() => {
  const e = props.environment
  if (e === 'prod') return 'prod'
  if (e === 'dev' || e === 'test' || e === 'staging') return 'other'
  return 'unmarked'
})
const envLabel = computed(() =>
  envKind.value === 'unmarked'
    ? t('agent.panel.env.unmarked')
    : t(`connection.form.environments.${props.environment}`),
)
const envTooltip = computed(() => {
  if (envKind.value === 'prod') return t('agent.panel.env.prodTooltip')
  if (envKind.value === 'unmarked') return t('agent.panel.env.unmarkedTooltip')
  return ''
})

function pickSession(id: string) {
  listOpen.value = false
  if (id !== props.session?.id) emit('select-session', id)
}
</script>

<template>
  <div class="header">
    <div class="row-1">
      <span class="conn" :title="connectionName">
        <AppIcon :src="databaseIcon" :size="13" />
        <span class="conn-name">{{ connectionName }}</span>
      </span>

      <span class="env-badge" :class="`env-${envKind}`" :title="envTooltip">
        <AppIcon v-if="envKind === 'prod'" :src="lockIcon" :size="11" />
        <span class="env-text">{{ envLabel }}</span>
      </span>

      <select
        class="ns-select"
        :value="currentDb"
        :disabled="!session || databases.length === 0"
        @change="emit('change-db', ($event.target as HTMLSelectElement).value)"
      >
        <option value="" disabled>{{ $t('agent.panel.selectDb') }}</option>
        <option v-for="d in databases" :key="d" :value="d">{{ d }}</option>
      </select>

      <select
        v-if="schemasSupported"
        class="ns-select"
        :value="currentSchema"
        :disabled="!session || schemas.length === 0"
        @change="emit('change-schema', ($event.target as HTMLSelectElement).value)"
      >
        <option value="" disabled>{{ $t('agent.panel.selectSchema') }}</option>
        <option v-for="s in schemas" :key="s" :value="s">{{ s }}</option>
      </select>

      <span class="spacer" />

      <n-popover v-model:show="listOpen" trigger="click" placement="bottom-end" :show-arrow="false" raw>
        <template #trigger>
          <button type="button" class="icon-btn" :title="$t('agent.panel.sessions')">
            <AppIcon :src="historyIcon" :size="15" />
          </button>
        </template>
        <div class="session-list">
          <div v-if="sessions.length === 0" class="session-empty">{{ $t('agent.panel.noSessions') }}</div>
          <div
            v-for="s in sessions"
            :key="s.id"
            class="session-item"
            :class="{ active: s.id === session?.id }"
          >
            <button type="button" class="session-title" @click="pickSession(s.id)">{{ s.title || $t('agent.panel.untitled') }}</button>
            <button type="button" class="session-op" :title="$t('common.rename')" @click.stop="emit('rename-session', s.id)">✎</button>
            <button type="button" class="session-op" :title="$t('common.delete')" @click.stop="emit('delete-session', s.id)">
              <AppIcon :src="xIcon" :size="11" />
            </button>
          </div>
        </div>
      </n-popover>

      <button type="button" class="icon-btn" :title="$t('agent.panel.newSession')" @click="emit('new-session')">
        <AppIcon :src="plusIcon" :size="15" />
      </button>
    </div>

    <div class="row-2">
      <div class="mode-seg">
        <button type="button" :class="{ active: mode === 'ask' }" @click="emit('change-mode', 'ask')">{{ $t('agent.panel.modeAsk') }}</button>
        <button type="button" :class="{ active: mode === 'agent' }" @click="emit('change-mode', 'agent')">{{ $t('agent.panel.modeAgent') }}</button>
      </div>
      <span class="spacer" />
      <span v-if="tokens > 0" class="tokens mono">{{ $t('agent.panel.tokens', { n: tokens }) }}</span>
    </div>
  </div>
</template>

<style scoped>
.header {
  flex: 0 0 auto;
  background: var(--catdb-surface-chrome);
  border-bottom: 1px solid var(--catdb-separator);
  padding: 5px 8px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.row-1, .row-2 { display: flex; align-items: center; gap: 6px; min-width: 0; }
.spacer { flex: 1 1 0; min-width: 0; }

.conn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 130px;
}
.conn-name {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Environment badge (闸 1). Small, semantic color, no shadow (DESIGN.md). */
.env-badge {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
  height: 16px;
  padding: 0 5px;
  border-radius: var(--catdb-rounded-sm);
  font-size: var(--catdb-fs-mini);
  line-height: 1;
  white-space: nowrap;
}
.env-badge .env-text { font-weight: 600; }
.env-prod {
  color: var(--catdb-error);
  background: color-mix(in srgb, var(--catdb-error) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--catdb-error) 32%, transparent);
}
.env-other {
  color: var(--catdb-text-secondary);
  background: var(--catdb-hover-fill);
}
.env-unmarked {
  color: var(--catdb-text-tertiary);
  background: var(--catdb-hover-fill);
}

.ns-select {
  height: 24px;
  max-width: 110px;
  font-size: var(--catdb-fs-small);
  padding: 1px 6px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  outline: none;
  cursor: default;
  font-family: inherit;
}
.ns-select:focus { border-color: var(--catdb-accent); }
.ns-select:disabled { opacity: 0.5; }

.icon-btn {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--catdb-rounded-sm);
  background: transparent;
  color: var(--catdb-text-primary);
  cursor: default;
  transition: background 130ms ease-out;
}
.icon-btn:hover { background: var(--catdb-hover-fill); }
.icon-btn:active { background: var(--catdb-pressed-fill); }

.mode-seg {
  display: inline-flex;
  background: var(--catdb-hover-fill);
  border-radius: var(--catdb-rounded-sm);
  padding: 1px;
}
.mode-seg button {
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
  height: 22px;
  padding: 0 12px;
  border-radius: var(--catdb-rounded-sm);
  cursor: default;
}
.mode-seg button.active {
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  box-shadow: 0 0 0 0.5px var(--catdb-separator);
}
.tokens { font-size: var(--catdb-fs-mini); color: var(--catdb-text-tertiary); }

/* Session list popover */
.session-list {
  min-width: 220px;
  max-width: 300px;
  max-height: 320px;
  overflow-y: auto;
  background: var(--catdb-surface-raised);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-md);
  box-shadow: var(--catdb-shadow-menu);
  padding: 4px;
}
.session-empty {
  padding: 8px;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
  text-align: center;
}
.session-item {
  display: flex;
  align-items: center;
  gap: 2px;
  border-radius: var(--catdb-rounded-sm);
  padding: 0 2px;
}
.session-item:hover { background: var(--catdb-hover-fill); }
.session-item.active { background: var(--catdb-accent-soft); }
.session-title {
  flex: 1 1 auto;
  min-width: 0;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  text-align: left;
  height: 26px;
  padding: 0 6px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;
}
.session-op {
  flex: 0 0 auto;
  width: 22px;
  height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--catdb-text-secondary);
  font: inherit;
  font-size: var(--catdb-fs-mini);
  border-radius: var(--catdb-rounded-sm);
  cursor: default;
}
.session-op:hover { background: var(--catdb-pressed-fill); color: var(--catdb-text-primary); }
</style>
