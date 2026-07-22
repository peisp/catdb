<script setup lang="ts">
// AgentHistoryView — full-panel session history (AGENT_DESIGN.md §10.2).
// Replaces the whole panel body while open: top bar (back / clear-all),
// search box, and the global session list sorted by last update, each row
// showing title + connection badge + updated time with rename/delete ops.
// Rename edits the title in place (Enter/blur commits, Esc cancels) — no
// prompt dialog.
import { computed, nextTick, ref } from 'vue'
import AppIcon from '../shared/AppIcon.vue'
import pencilIcon from '../../assets/icons/pencil.svg?raw'
import trashIcon from '../../assets/icons/trash.svg?raw'
import { t } from '../../i18n'
import type { AgentSession } from '../../api/agent'

const props = defineProps<{
  sessions: AgentSession[]
  // Connection name/environment per connId, for the badges.
  connsById: Record<string, { name: string; environment: string }>
  activeId: string
}>()

const emit = defineEmits<{
  (e: 'back'): void
  (e: 'select', id: string): void
  (e: 'rename', id: string, title: string): void
  (e: 'delete', id: string): void
  (e: 'clear'): void
}>()

const query = ref('')

// --- inline rename ---
const editingId = ref('')
const editText = ref('')
let editEl: HTMLInputElement | null = null
function setEditRef(el: unknown) {
  editEl = (el as HTMLInputElement | null) ?? null
}
function startEdit(s: AgentSession) {
  editingId.value = s.id
  editText.value = s.title || ''
  void nextTick(() => { editEl?.focus(); editEl?.select() })
}
function commitEdit() {
  const id = editingId.value
  if (!id) return
  editingId.value = ''
  const title = editText.value.trim()
  const s = props.sessions.find((x) => x.id === id)
  if (!title || !s || title === s.title) return // empty/unchanged = cancel
  emit('rename', id, title)
}
function onEditKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Enter' && !ev.isComposing) { ev.preventDefault(); commitEdit() }
  else if (ev.key === 'Escape') { ev.preventDefault(); editingId.value = '' }
}

// Sorted by updated time (newest first) and filtered by the search box, which
// matches session titles and connection names.
const items = computed(() => {
  const list = [...props.sessions].sort(
    (a, b) => new Date(b.updatedAt as string).getTime() - new Date(a.updatedAt as string).getTime(),
  )
  const q = query.value.trim().toLowerCase()
  if (!q) return list
  return list.filter((s) =>
    (s.title || '').toLowerCase().includes(q) ||
    (props.connsById[s.connId]?.name ?? '').toLowerCase().includes(q),
  )
})

function envKindOf(env: string): 'prod' | 'other' | 'unmarked' {
  if (env === 'prod') return 'prod'
  if (env === 'dev' || env === 'test' || env === 'staging') return 'other'
  return 'unmarked'
}
function connLabelOf(connId: string): string {
  return props.connsById[connId]?.name ?? t('agent.panel.connDeleted')
}

// Compact last-updated stamp: today → HH:mm, this year → MM-DD HH:mm,
// older → YYYY-MM-DD. Numeric, locale-neutral.
function fmtUpdated(v: unknown): string {
  const d = new Date(v as string)
  if (isNaN(d.getTime())) return ''
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (d.toDateString() === now.toDateString()) return hm
  const md = `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  if (d.getFullYear() === now.getFullYear()) return `${md} ${hm}`
  return `${d.getFullYear()}-${md}`
}
</script>

<template>
  <div class="history">
    <div class="bar">
      <button type="button" class="icon-btn" :title="$t('common.back')" @click="emit('back')">
        <!-- chevron-left -->
        <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
          <path d="M10 3.5 5.5 8 10 12.5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" fill="none" />
        </svg>
      </button>
      <span class="bar-title">{{ $t('agent.panel.historyTitle') }}</span>
      <span class="spacer" />
      <button type="button" class="clear-btn" :disabled="sessions.length === 0" @click="emit('clear')">
        {{ $t('agent.panel.clearAll') }}
      </button>
    </div>

    <input
      v-model="query"
      class="search"
      type="text"
      :placeholder="$t('agent.panel.searchPlaceholder')"
    />

    <div class="list">
      <div v-if="items.length === 0" class="empty">
        {{ sessions.length === 0 ? $t('agent.panel.noSessions') : $t('agent.panel.noMatches') }}
      </div>
      <div
        v-for="s in items"
        :key="s.id"
        class="item"
        :class="{ active: s.id === activeId }"
      >
        <!-- Inline rename replaces the row content while editing. -->
        <input
          v-if="editingId === s.id"
          :ref="setEditRef"
          v-model="editText"
          class="edit-input"
          type="text"
          @keydown="onEditKeydown"
          @blur="commitEdit"
          @click.stop
        />
        <template v-else>
          <button type="button" class="item-main" @click="emit('select', s.id)">
            <span class="item-row1">
              <span class="item-title">{{ s.title || $t('agent.panel.untitled') }}</span>
              <span class="item-time mono">{{ fmtUpdated(s.updatedAt) }}</span>
            </span>
            <span class="item-conn" :class="{ deleted: !connsById[s.connId] }">
              <span
                v-if="connsById[s.connId]"
                class="conn-dot"
                :class="`dot-${envKindOf(connsById[s.connId].environment)}`"
              />
              {{ connLabelOf(s.connId) }}
            </span>
          </button>
          <button type="button" class="item-op" :title="$t('common.rename')" @click.stop="startEdit(s)">
            <AppIcon :src="pencilIcon" :size="12" />
          </button>
          <button type="button" class="item-op" :title="$t('common.delete')" @click.stop="emit('delete', s.id)">
            <AppIcon :src="trashIcon" :size="12" />
          </button>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.history {
  flex: 1 1 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  background: var(--catdb-surface-chrome);
  border-bottom: 1px solid var(--catdb-separator);
}
.bar-title {
  font-size: var(--catdb-fs-small);
  font-weight: 600;
  color: var(--catdb-text-primary);
}
.spacer { flex: 1 1 0; min-width: 0; }

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

.clear-btn {
  height: 22px;
  padding: 0 8px;
  border: none;
  border-radius: var(--catdb-rounded-sm);
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-error);
  cursor: default;
}
.clear-btn:hover { background: color-mix(in srgb, var(--catdb-error) 10%, transparent); }
.clear-btn:disabled { opacity: 0.4; }

.search {
  flex: 0 0 auto;
  margin: 8px 8px 4px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  font: inherit;
  font-size: var(--catdb-fs-small);
  outline: none;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.search:focus { border-color: var(--catdb-accent); }

.list {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  padding: 4px 8px 8px;
}
.empty {
  padding: 20px 8px;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
  text-align: center;
}

.item {
  display: flex;
  align-items: center;
  gap: 2px;
  border-radius: var(--catdb-rounded-sm);
  padding: 0 2px;
}
.item:hover { background: var(--catdb-hover-fill); }
.item.active { background: var(--catdb-accent-soft); }
.item-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 1px;
  border: none;
  background: transparent;
  font: inherit;
  text-align: left;
  padding: 4px 6px;
  cursor: default;
}
.item-row1 {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}
.item-title {
  flex: 1 1 auto;
  min-width: 0;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.item-time {
  flex: 0 0 auto;
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-tertiary);
  white-space: nowrap;
}
.item-conn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.item-conn.deleted { font-style: italic; }
.conn-dot {
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  border-radius: 50%;
}
.dot-prod { background: var(--catdb-error); }
.dot-other { background: var(--catdb-success); }
.dot-unmarked { background: var(--catdb-text-tertiary); }

.item-op {
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
.item-op:hover { background: var(--catdb-pressed-fill); color: var(--catdb-text-primary); }

/* Inline rename input, sized to line up with the row's title. */
.edit-input {
  flex: 1 1 auto;
  min-width: 0;
  height: 26px;
  margin: 3px 2px;
  padding: 0 6px;
  border: 1px solid var(--catdb-accent);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  font: inherit;
  font-size: var(--catdb-fs-small);
  outline: none;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
</style>
