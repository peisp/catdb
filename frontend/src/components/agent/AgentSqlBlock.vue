<script setup lang="ts">
// AgentSqlBlock — a ```sql code block from an assistant answer, with the
// Ask-mode exit actions: copy / insert into the current editor / open in a new
// query tab. Highlighting is a lightweight keyword pass (no external lib).
//
// The three actions are provided by the panel via inject so this deeply-nested
// block never needs to know the session's connection — the panel wires them to
// the query store.
import { computed, inject } from 'vue'
import { useMessage } from 'naive-ui'
import { highlightSql } from './markdown'
import { AGENT_SQL_ACTIONS, type AgentSqlActions } from './sqlActions'
import { t } from '../../i18n'

// pending: the fence is still streaming in — show the highlighted code only,
// actions appear once the block is complete.
const props = defineProps<{ sql: string; pending?: boolean }>()
const message = useMessage()
const actions = inject<AgentSqlActions | null>(AGENT_SQL_ACTIONS, null)

const html = computed(() => highlightSql(props.sql))

async function onCopy() {
  try {
    await navigator.clipboard.writeText(props.sql)
    message.success(t('common.copied'))
  } catch {
    message.error(t('agent.panel.sql.copyFailed'))
  }
}
function onInsert() {
  if (actions?.insert(props.sql)) message.success(t('agent.panel.sql.inserted'))
  else message.warning(t('agent.panel.sql.noEditor'))
}
function onOpen() {
  actions?.openTab(props.sql)
}
</script>

<template>
  <div class="sql-block">
    <div v-if="!pending" class="sql-actions">
      <button type="button" class="sql-btn" :title="$t('common.copy')" @click="onCopy">{{ $t('common.copy') }}</button>
      <button type="button" class="sql-btn" :title="$t('agent.panel.sql.insert')" @click="onInsert">{{ $t('agent.panel.sql.insert') }}</button>
      <button type="button" class="sql-btn" :title="$t('agent.panel.sql.openTab')" @click="onOpen">{{ $t('agent.panel.sql.openTab') }}</button>
    </div>
    <pre class="sql-code mono"><code v-html="html" /></pre>
  </div>
</template>

<style scoped>
.sql-block {
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  overflow: hidden;
  margin: 8px 0;
  background: var(--catdb-surface-content);
}
.sql-actions {
  display: flex;
  gap: 2px;
  padding: 3px 4px;
  border-bottom: 1px solid var(--catdb-separator);
  background: var(--catdb-surface-chrome);
}
.sql-btn {
  border: none;
  background: transparent;
  color: var(--catdb-text-secondary);
  font: inherit;
  font-size: var(--catdb-fs-mini);
  height: 20px;
  padding: 0 8px;
  border-radius: var(--catdb-rounded-sm);
  cursor: default;
  transition: background 130ms ease-out;
}
.sql-btn:hover { background: var(--catdb-hover-fill); color: var(--catdb-text-primary); }
.sql-btn:active { background: var(--catdb-pressed-fill); }
.sql-code {
  margin: 0;
  padding: 8px 10px;
  overflow-x: auto;
  font-size: var(--catdb-fs-mono);
  line-height: 1.5;
  color: var(--catdb-text-primary);
  /* SQL text must be selectable/copyable (DESIGN.md). */
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.sql-code :deep(.kw) { color: var(--catdb-accent); font-weight: 600; }
</style>
