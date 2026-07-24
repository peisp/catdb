<script setup lang="ts">
// AgentSessionHeader — slim session chrome (AGENT_DESIGN.md §10.1):
//   [session title]        [watermark][tokens][cost] [compact][history][＋]
// Connection/namespace context and the Ask|Agent / model switches live in the
// composer dock; the history button swaps the whole panel body for the
// full-page session-history view (AgentHistoryView).
import { computed } from 'vue'
import AppIcon from '../shared/AppIcon.vue'
import plusIcon from '../../assets/icons/plus.svg?raw'
import historyIcon from '../../assets/icons/history.svg?raw'
import compressIcon from '../../assets/icons/compress.svg?raw'
import scanEyeIcon from '../../assets/icons/scan-eye.svg?raw'
import { t } from '../../i18n'
import type { AgentSession } from '../../api/agent'

const props = defineProps<{
  session: AgentSession | null
  tokens: number
  // Context watermark 0~1 (§9); undefined when the model's context window is
  // unknown (openai-compat custom model) → the bar hides.
  watermark?: number
  // Estimated cumulative cost, pre-formatted "$0.0123"; null when the session's
  // model has no pricing configured → only tokens show.
  cost?: string | null
  compacting: boolean
  // Dev builds only: shows the Trace-window button (internal/agenttrace).
  traceEnabled?: boolean
}>()

const emit = defineEmits<{
  (e: 'new-session'): void
  (e: 'open-history'): void
  (e: 'compact'): void
  (e: 'open-trace'): void
}>()

// Watermark bar: hidden without a window size; >0.7 flips to the warning color
// (compaction threshold, §9). Clamp defensively.
const showWatermark = computed(() => props.watermark != null && props.watermark > 0)
const watermarkPct = computed(() => Math.min(100, Math.max(0, (props.watermark ?? 0) * 100)))
const watermarkWarn = computed(() => (props.watermark ?? 0) > 0.7)
const watermarkTip = computed(() => t('agent.panel.watermarkTooltip', { pct: Math.round(watermarkPct.value) }))
</script>

<template>
  <div class="header">
    <span class="title" :title="session?.title || $t('agent.panel.untitled')">
      {{ session?.title || $t('agent.panel.untitled') }}
    </span>

    <span class="spacer" />

    <span
      v-if="showWatermark"
      class="watermark"
      :class="{ warn: watermarkWarn }"
      :title="watermarkTip"
    >
      <span class="watermark-fill" :style="{ width: watermarkPct + '%' }" />
    </span>

    <span v-if="tokens > 0" class="tokens mono">{{ $t('agent.panel.tokens', { n: tokens }) }}</span>
    <span v-if="tokens > 0 && cost" class="cost mono">{{ cost }}</span>

    <button
      v-if="traceEnabled"
      type="button"
      class="icon-btn"
      :title="$t('agentTrace.openButton')"
      @click="emit('open-trace')"
    >
      <AppIcon :src="scanEyeIcon" :size="14" />
    </button>

    <button
      type="button"
      class="icon-btn compact-btn"
      :disabled="!session || compacting"
      :title="$t('agent.compact.button')"
      @click="emit('compact')"
    >
      <AppIcon :src="compressIcon" :size="14" />
    </button>

    <button type="button" class="icon-btn" :title="$t('agent.panel.sessions')" @click="emit('open-history')">
      <AppIcon :src="historyIcon" :size="15" />
    </button>

    <button type="button" class="icon-btn" :title="$t('agent.panel.newSession')" @click="emit('new-session')">
      <AppIcon :src="plusIcon" :size="15" />
    </button>
  </div>
</template>

<style scoped>
.header {
  flex: 0 0 36px;
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  background: var(--catdb-surface-chrome);
  border-bottom: 1px solid var(--catdb-separator);
  padding: 5px 8px;
}
.spacer { flex: 1 1 0; min-width: 0; }

.title {
  min-width: 0;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

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

.tokens { font-size: var(--catdb-fs-mini); color: var(--catdb-text-tertiary); }
.cost { font-size: var(--catdb-fs-mini); color: var(--catdb-text-secondary); }

/* Context watermark bar (§9). Thin fill; warns past the compaction threshold. */
.watermark {
  flex: 0 0 auto;
  width: 44px;
  height: 5px;
  border-radius: var(--catdb-rounded-pill);
  background: var(--catdb-hover-fill);
  overflow: hidden;
}
.watermark-fill {
  display: block;
  height: 100%;
  border-radius: var(--catdb-rounded-pill);
  background: var(--catdb-accent);
  transition: width 200ms ease-out;
}
.watermark.warn .watermark-fill { background: var(--catdb-warning); }

.compact-btn:disabled { opacity: 0.4; }
</style>
