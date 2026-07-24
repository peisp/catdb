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
import { computed, nextTick, ref, watch } from 'vue'
import AppIcon from '../shared/AppIcon.vue'
import chevronDownIcon from '../../assets/icons/chevron-down.svg?raw'
import pencilIcon from '../../assets/icons/pencil.svg?raw'
import AgentSqlBlock from './AgentSqlBlock.vue'
import { segmentMarkdown } from './markdown'
import type { AssistantEntry, Entry } from './types'

// editable: user entries only — shows the hover edit affordance; resending
// deletes everything after this message (edit-and-resend, handled by the panel).
const props = defineProps<{ entry: Entry; editable?: boolean }>()
const emit = defineEmits<{ (e: 'resend', text: string): void }>()

// --- edit-and-resend (user entries) ---
const editing = ref(false)
const draft = ref('')
const editRef = ref<HTMLTextAreaElement | null>(null)
function startEdit() {
  if (props.entry.kind !== 'user') return
  draft.value = props.entry.text
  editing.value = true
  void nextTick(() => {
    const el = editRef.value
    if (el) {
      el.focus()
      el.setSelectionRange(el.value.length, el.value.length)
    }
  })
}
function cancelEdit() {
  editing.value = false
}
function submitEdit() {
  const text = draft.value.trim()
  if (!text) return
  editing.value = false
  emit('resend', text)
}

const asAssistant = computed(() =>
  props.entry.kind === 'assistant' ? (props.entry as AssistantEntry) : null,
)
const segments = computed(() =>
  asAssistant.value
    ? segmentMarkdown(asAssistant.value.text, asAssistant.value.streaming)
    : [],
)

// Thinking region (§10.4): auto-expanded WHILE the model is thinking (before
// any answer text arrives), clipped to the last few lines with auto-scroll;
// auto-collapses once thinking ends. A manual click overrides the automatics
// from then on.
const thinkingToggled = ref<boolean | null>(null)
const thinkingActive = computed(() => {
  const e = asAssistant.value
  return !!e && e.streaming && !e.text
})
const thinkingOpen = computed(() => thinkingToggled.value ?? thinkingActive.value)
function toggleThinking() {
  thinkingToggled.value = !thinkingOpen.value
}
const thinkingRef = ref<HTMLElement | null>(null)
watch(() => asAssistant.value?.thinking, () => {
  if (!thinkingActive.value) return
  void nextTick(() => {
    const el = thinkingRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
})

// User bubble: @mentions stay inline in the sentence (§10.3) and get
// highlighted by splitting the text around each "@name" occurrence.
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
const userSegs = computed<{ mention: boolean; text: string }[] | null>(() => {
  if (props.entry.kind !== 'user') return null
  const text = props.entry.text ?? ''
  const mentions = props.entry.mentions ?? []
  if (mentions.length === 0) return null
  const names = [...mentions].sort((a, b) => b.length - a.length).map(escapeRegExp)
  const re = new RegExp('@(?:' + names.join('|') + ')', 'gu')
  const segs: { mention: boolean; text: string }[] = []
  let last = 0
  for (const m of text.matchAll(re)) {
    const i = m.index ?? 0
    if (i > last) segs.push({ mention: false, text: text.slice(last, i) })
    segs.push({ mention: true, text: m[0] })
    last = i + m[0].length
  }
  if (segs.length === 0) return null
  if (last < text.length) segs.push({ mention: false, text: text.slice(last) })
  return segs
})
</script>

<template>
  <!-- User -->
  <div v-if="entry.kind === 'user'" class="row user">
    <template v-if="!editing">
      <button
        v-if="editable"
        type="button"
        class="edit-btn"
        :title="$t('agent.panel.edit.button')"
        @click="startEdit"
      >
        <AppIcon :src="pencilIcon" :size="12" />
      </button>
      <div class="bubble user-bubble">
        <template v-if="userSegs">
          <template v-for="(sg, i) in userSegs" :key="i">
            <span v-if="sg.mention" class="inline-mention">{{ sg.text }}</span>
            <template v-else>{{ sg.text }}</template>
          </template>
        </template>
        <template v-else>{{ entry.text }}</template>
      </div>
    </template>
    <div v-else class="edit-box">
      <textarea
        ref="editRef"
        v-model="draft"
        class="edit-input"
        rows="3"
        @keydown.esc.prevent="cancelEdit"
        @keydown.meta.enter.prevent="submitEdit"
        @keydown.ctrl.enter.prevent="submitEdit"
      />
      <div class="edit-foot">
        <span class="edit-hint">{{ $t('agent.panel.edit.hint') }}</span>
        <button type="button" class="edit-action" @click="cancelEdit">{{ $t('common.cancel') }}</button>
        <button type="button" class="edit-action primary" :disabled="!draft.trim()" @click="submitEdit">
          {{ $t('agent.panel.edit.resend') }}
        </button>
      </div>
    </div>
  </div>

  <!-- System notice -->
  <div v-else-if="entry.kind === 'system'" class="row system">
    <span class="system-line">{{ entry.text }}</span>
  </div>

  <!-- Context-compacted notice line (§9) -->
  <div v-else-if="entry.kind === 'compacted'" class="row system">
    <span class="system-line">{{ entry.count != null ? $t('agent.compact.line', { n: entry.count }) : $t('agent.compact.lineGeneric') }}</span>
  </div>

  <!-- Assistant -->
  <div v-else-if="entry.kind === 'assistant'" class="row assistant">
    <div class="assistant-body">
      <!-- Thinking (collapsible) -->
      <div v-if="entry.thinking" class="thinking">
        <button type="button" class="thinking-head" @click="toggleThinking">
          <AppIcon :src="chevronDownIcon" :size="11" class="caret" :class="{ open: thinkingOpen }" />
          {{ $t('agent.panel.thinking') }}
        </button>
        <pre v-if="thinkingOpen" ref="thinkingRef" class="thinking-body" :class="{ live: thinkingActive }">{{ entry.thinking }}</pre>
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
      <!-- Session token-budget hit (§4.1 / §9). -->
      <div v-else-if="entry.stopReason === 'token_budget'" class="max-iter">
        {{ $t('agent.panel.tokenBudget') }}
      </div>

      <!-- Delivery-contract warning (§6/§8): answer delivered but unvalidated. -->
      <div v-if="entry.deliveryWarning" class="delivery-warn">
        {{ $t('agent.panel.deliveryWarning') }}
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
/* Inline @table mention inside the user bubble (§10.3). */
.inline-mention {
  color: var(--catdb-accent);
  font-weight: 600;
}

/* Edit-and-resend: pencil appears on row hover, left of the bubble. */
.edit-btn {
  flex: 0 0 auto;
  align-self: center;
  width: 22px;
  height: 22px;
  margin-right: 4px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--catdb-rounded-sm);
  background: transparent;
  color: var(--catdb-text-tertiary);
  cursor: default;
  opacity: 0;
  transition: opacity 130ms ease-out, background 130ms ease-out;
}
.row.user:hover .edit-btn { opacity: 1; }
.edit-btn:hover { background: var(--catdb-hover-fill); color: var(--catdb-text-primary); }
.edit-btn:active { background: var(--catdb-pressed-fill); }

.edit-box {
  flex: 1 1 auto;
  min-width: 0;
  max-width: 85%;
  border: 1px solid var(--catdb-accent);
  border-radius: var(--catdb-rounded-md);
  background: var(--catdb-surface-content);
  overflow: hidden;
}
.edit-input {
  display: block;
  width: 100%;
  border: none;
  outline: none;
  resize: vertical;
  background: transparent;
  padding: 6px 10px;
  font: inherit;
  font-size: var(--catdb-fs-body);
  line-height: 1.45;
  color: var(--catdb-text-primary);
}
.edit-foot {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 6px;
  border-top: 1px solid var(--catdb-separator);
  background: var(--catdb-surface-chrome);
}
.edit-hint {
  flex: 1 1 auto;
  min-width: 0;
  font-size: var(--catdb-fs-micro);
  color: var(--catdb-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.edit-action {
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
.edit-action:hover { background: var(--catdb-hover-fill); color: var(--catdb-text-primary); }
.edit-action:active { background: var(--catdb-pressed-fill); }
.edit-action.primary { color: var(--catdb-accent); font-weight: 600; }
.edit-action:disabled { opacity: 0.4; }

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
/* Collapsed points right (>), expanded points down (v). */
.caret { transition: transform 130ms ease-out; opacity: 0.6; transform: rotate(-90deg); }
.caret.open { transform: rotate(0deg); }
.thinking-body {
  margin: 4px 0 0;
  padding: 6px 8px;
  border-left: 2px solid var(--catdb-separator);
  font-size: var(--catdb-fs-mono-small);
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--catdb-text-secondary);
}
/* While the model is thinking: show only the newest ~6 lines, auto-scrolled. */
.thinking-body.live {
  max-height: 120px;
  overflow-y: auto;
}

.max-iter {
  margin-top: 6px;
  padding: 6px 8px;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-accent-soft);
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
}

.delivery-warn {
  margin-top: 6px;
  padding: 6px 8px;
  border-radius: var(--catdb-rounded-sm);
  background: color-mix(in srgb, var(--catdb-warning) 12%, transparent);
  font-size: var(--catdb-fs-small);
  color: var(--catdb-warning);
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
