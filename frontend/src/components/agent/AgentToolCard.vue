<script setup lang="ts">
// AgentToolCard — one tool step, start+end merged into a single card by callId
// (AGENT_DESIGN.md §10.4). Collapsed shows the tool name + summary; expanded
// reveals the JSON args and full result (available when rendering history).
// Error state tints the card red; a still-running step shows a spinner.
import { computed, ref } from 'vue'
import { CSpin, useMessage } from '../ui'
import AppIcon from '../shared/AppIcon.vue'
import chevronDownIcon from '../../assets/icons/chevron-down.svg?raw'
import scanEyeIcon from '../../assets/icons/scan-eye.svg?raw'
import copyIcon from '../../assets/icons/copy.svg?raw'
import { copyText } from '../../api/system'
import { t } from '../../i18n'
import type { ToolEntry } from './types'

const props = defineProps<{ entry: ToolEntry }>()
const expanded = ref(false)
const message = useMessage()

const running = computed(() => props.entry.phase === 'start')
const hasDetail = computed(() => !!(props.entry.args || props.entry.result))

// run_sql quick action: lift the sql arg out of the args JSON for one-click copy.
const sqlArg = computed(() => {
  if (props.entry.name !== 'run_sql' || !props.entry.args) return ''
  try {
    const a = JSON.parse(props.entry.args) as { sql?: unknown }
    return typeof a.sql === 'string' ? a.sql : ''
  } catch { return '' }
})

async function onCopySql() {
  try {
    await copyText(sqlArg.value)
    message.success(t('common.copied'))
  } catch {
    message.error(t('agent.panel.sql.copyFailed'))
  }
}

function prettyArgs(raw?: string): string {
  if (!raw) return ''
  try { return JSON.stringify(JSON.parse(raw), null, 2) } catch { return raw }
}
</script>

<template>
  <div class="tool-card" :class="{ error: entry.isError }">
    <button type="button" class="tool-head" :class="{ clickable: hasDetail }" @click="hasDetail && (expanded = !expanded)">
      <AppIcon v-if="!running" :src="scanEyeIcon" :size="13" class="tool-glyph" />
      <CSpin v-else :size="12" class="tool-spin" />
      <span class="tool-name mono">{{ entry.name }}</span>
      <span v-if="entry.summary" class="tool-summary">{{ entry.summary }}</span>
      <span
        v-if="sqlArg"
        class="tool-copy"
        role="button"
        :title="$t('agent.panel.tool.copySql')"
        @click.stop="onCopySql"
      >
        <AppIcon :src="copyIcon" :size="12" />
      </span>
      <AppIcon
        v-if="hasDetail"
        :src="chevronDownIcon"
        :size="12"
        class="tool-caret"
        :class="{ open: expanded }"
      />
    </button>
    <div v-if="entry.resultEphemeral" class="tool-ephemeral">{{ $t('agent.panel.tool.resultEphemeral') }}</div>
    <div v-if="expanded && hasDetail" class="tool-detail">
      <template v-if="entry.args">
        <div class="detail-label">{{ $t('agent.panel.tool.args') }}</div>
        <pre class="mono detail-pre">{{ prettyArgs(entry.args) }}</pre>
      </template>
      <template v-if="entry.result">
        <div class="detail-label">{{ $t('agent.panel.tool.result') }}</div>
        <pre class="mono detail-pre">{{ entry.result }}</pre>
      </template>
    </div>
  </div>
</template>

<style scoped>
.tool-card {
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  margin: 4px 0;
  overflow: hidden;
}
.tool-card.error { border-color: var(--catdb-error); }
.tool-head {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: none;
  background: transparent;
  padding: 5px 8px;
  font: inherit;
  color: var(--catdb-text-primary);
  cursor: default;
  text-align: left;
}
.tool-head.clickable { cursor: default; }
.tool-head.clickable:hover { background: var(--catdb-hover-fill); }
.tool-glyph, .tool-spin { flex: 0 0 auto; }
.tool-name { font-size: var(--catdb-fs-small); flex: 0 0 auto; }
.tool-card.error .tool-name { color: var(--catdb-error); }
.tool-summary {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1 1 auto;
  min-width: 0;
}
/* run_sql quick copy — quiet by default, lights up on hover. */
.tool-copy {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: var(--catdb-rounded-sm);
  color: var(--catdb-text-tertiary);
  transition: background 130ms ease-out;
}
.tool-copy:hover { background: var(--catdb-hover-fill); color: var(--catdb-text-primary); }
.tool-copy:active { background: var(--catdb-pressed-fill); }

/* History-restored run_sql: the inline table was event-only (§7) — hint line. */
.tool-ephemeral {
  padding: 0 8px 5px 27px;
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-tertiary);
}

/* Collapsed points right (>), expanded points down (v). */
.tool-caret {
  flex: 0 0 auto;
  transition: transform 130ms ease-out;
  opacity: 0.5;
  transform: rotate(-90deg);
}
.tool-caret.open { transform: rotate(0deg); }
.tool-detail {
  border-top: 1px solid var(--catdb-separator);
  padding: 6px 8px;
  background: var(--catdb-surface-chrome);
}
.detail-label {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-secondary);
  margin: 4px 0 2px;
}
.detail-pre {
  margin: 0;
  font-size: var(--catdb-fs-mono-small);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 220px;
  overflow: auto;
  color: var(--catdb-text-primary);
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
</style>
