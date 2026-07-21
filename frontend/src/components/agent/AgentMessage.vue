<script setup lang="ts">
// AgentMessage — renders a user / assistant / system timeline entry.
//   user      : right-aligned plain-text bubble.
//   system    : centered muted notice line (namespace switch, interruption).
//   assistant : markdown throughout — while streaming the accumulated text is
//               re-rendered per rAF flush (markdown-it on chat-sized input is
//               sub-millisecond), with a trailing unclosed ```sql fence shown
//               as a pending highlighted block that upgrades in place to the
//               actionable AgentSqlBlock when the fence closes. Thinking is a
//               collapsible region; a max_iterations stop shows the "reply
//               继续" hint (§4.1/§10.4).
import { computed, ref } from 'vue'
import AppIcon from '../shared/AppIcon.vue'
import chevronDownIcon from '../../assets/icons/chevron-down.svg?raw'
import AgentSqlBlock from './AgentSqlBlock.vue'
import { segmentMarkdown } from './markdown'
import type { AssistantEntry, Entry } from './types'

const props = defineProps<{ entry: Entry }>()

const asAssistant = computed(() =>
  props.entry.kind === 'assistant' ? (props.entry as AssistantEntry) : null,
)
const segments = computed(() =>
  asAssistant.value
    ? segmentMarkdown(asAssistant.value.text, asAssistant.value.streaming)
    : [],
)
const thinkingOpen = ref(false)
</script>

<template>
  <!-- User -->
  <div v-if="entry.kind === 'user'" class="row user">
    <div class="bubble user-bubble">{{ entry.text }}</div>
  </div>

  <!-- System notice -->
  <div v-else-if="entry.kind === 'system'" class="row system">
    <span class="system-line">{{ entry.text }}</span>
  </div>

  <!-- Assistant -->
  <div v-else-if="entry.kind === 'assistant'" class="row assistant">
    <div class="assistant-body">
      <!-- Thinking (collapsible) -->
      <div v-if="entry.thinking" class="thinking">
        <button type="button" class="thinking-head" @click="thinkingOpen = !thinkingOpen">
          <AppIcon :src="chevronDownIcon" :size="11" class="caret" :class="{ open: thinkingOpen }" />
          {{ $t('agent.panel.thinking') }}
        </button>
        <pre v-if="thinkingOpen" class="thinking-body">{{ entry.thinking }}</pre>
      </div>

      <!-- Markdown + sql blocks, streaming and finalized alike. -->
      <template v-for="(seg, i) in segments" :key="i">
        <AgentSqlBlock v-if="seg.kind === 'sql'" :sql="seg.content" :pending="seg.open" />
        <!-- eslint-disable-next-line vue/no-v-html -->
        <div v-else-if="seg.html" class="md" v-html="seg.html" />
      </template>

      <!-- Iteration cap hint (§4.1). -->
      <div v-if="entry.stopReason === 'max_iterations'" class="max-iter">
        {{ $t('agent.panel.maxIterations') }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.row { display: flex; margin: 8px 0; }
.row.user { justify-content: flex-end; }
.row.assistant { justify-content: flex-start; }
.row.system { justify-content: center; }

.bubble {
  max-width: 85%;
  padding: 6px 10px;
  border-radius: var(--catdb-rounded-md);
  font-size: var(--catdb-fs-body);
  line-height: 1.45;
  white-space: pre-wrap;
  word-break: break-word;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.user-bubble {
  background: var(--catdb-accent-soft);
  color: var(--catdb-text-primary);
}

.system-line {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-tertiary);
  padding: 2px 8px;
  text-align: center;
}

.assistant-body {
  max-width: 100%;
  min-width: 0;
  font-size: var(--catdb-fs-body);
  line-height: 1.5;
  color: var(--catdb-text-primary);
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.thinking { margin-bottom: 6px; }
.thinking-head {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-secondary);
  cursor: default;
  padding: 0;
}
.caret { transition: transform 130ms ease-out; opacity: 0.6; }
.caret.open { transform: rotate(180deg); }
.thinking-body {
  margin: 4px 0 0;
  padding: 6px 8px;
  border-left: 2px solid var(--catdb-separator);
  font-size: var(--catdb-fs-mono-small);
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--catdb-text-secondary);
}

.max-iter {
  margin-top: 6px;
  padding: 6px 8px;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-accent-soft);
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
}

/* Markdown prose */
.md :deep(p) { margin: 0 0 6px; }
.md :deep(p:last-child) { margin-bottom: 0; }
.md :deep(ul), .md :deep(ol) { margin: 0 0 6px; padding-left: 18px; }
.md :deep(code) {
  font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  font-size: var(--catdb-fs-mono-small);
  background: var(--catdb-hover-fill);
  padding: 1px 4px;
  border-radius: var(--catdb-rounded-xs);
}
.md :deep(pre) {
  background: var(--catdb-surface-content);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  padding: 8px 10px;
  overflow-x: auto;
  margin: 6px 0;
}
.md :deep(pre code) { background: transparent; padding: 0; }
.md :deep(a) { color: var(--catdb-accent); }
.md :deep(table) { border-collapse: collapse; margin: 6px 0; }
.md :deep(th), .md :deep(td) {
  border: 1px solid var(--catdb-separator);
  padding: 3px 8px;
  font-size: var(--catdb-fs-small);
}
</style>
