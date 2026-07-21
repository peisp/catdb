<script setup lang="ts">
// AgentToolCard — one tool step, start+end merged into a single card by callId
// (AGENT_DESIGN.md §10.4). Collapsed shows the tool name + summary; expanded
// reveals the JSON args and full result (available when rendering history).
// Error state tints the card red; a still-running step shows a spinner.
import { computed, ref } from 'vue'
import { NSpin } from 'naive-ui'
import AppIcon from '../shared/AppIcon.vue'
import chevronDownIcon from '../../assets/icons/chevron-down.svg?raw'
import scanEyeIcon from '../../assets/icons/scan-eye.svg?raw'
import type { ToolEntry } from './types'

const props = defineProps<{ entry: ToolEntry }>()
const expanded = ref(false)

const running = computed(() => props.entry.phase === 'start')
const hasDetail = computed(() => !!(props.entry.args || props.entry.result))

function prettyArgs(raw?: string): string {
  if (!raw) return ''
  try { return JSON.stringify(JSON.parse(raw), null, 2) } catch { return raw }
}
</script>

<template>
  <div class="tool-card" :class="{ error: entry.isError }">
    <button type="button" class="tool-head" :class="{ clickable: hasDetail }" @click="hasDetail && (expanded = !expanded)">
      <AppIcon v-if="!running" :src="scanEyeIcon" :size="13" class="tool-glyph" />
      <n-spin v-else :size="12" class="tool-spin" />
      <span class="tool-name mono">{{ entry.name }}</span>
      <span v-if="entry.summary" class="tool-summary">{{ entry.summary }}</span>
      <AppIcon
        v-if="hasDetail"
        :src="chevronDownIcon"
        :size="12"
        class="tool-caret"
        :class="{ open: expanded }"
      />
    </button>
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
.tool-caret {
  flex: 0 0 auto;
  transition: transform 130ms ease-out;
  opacity: 0.5;
}
.tool-caret.open { transform: rotate(180deg); }
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
