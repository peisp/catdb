<script lang="ts">
// Input height bounds, shared with the panel's resize grip (§10.1).
export const INPUT_MIN_H = 64
export const INPUT_MAX_H = 220
</script>

<script setup lang="ts">
// AgentComposer — multiline input + send/stop toggle, with @table mentions
// (§10.3). Typing "@" opens a table-name completion popover sourced from the
// session's current namespace (passed in via `tables`, already cached in the
// metadata store). Selecting a table completes the token IN PLACE — "@订单表"
// stays part of the sentence ("关联查询 @a 和 @b") — and on send the text is
// scanned against the known table names to build the `mentions` argument.
// Enter sends, Shift+Enter inserts a newline; while the menu is open
// Enter/Arrows/Esc drive the completion instead.
//
// The circular send/stop button sits inside the input's bottom-right corner
// (§10.1): up arrow = send, square = stop. The textarea auto-grows with
// content up to INPUT_MAX_H then scrolls; `manualHeight` (owned by the panel,
// driven by the grip between the messages area and the dock) overrides the
// auto height when set.
import { nextTick, onMounted, ref, watch } from 'vue'
import MentionMenu from './MentionMenu.vue'
import { extractMentions, useMentionCompletion } from './mention'

const props = defineProps<{
  busy: boolean
  disabled?: boolean
  tables: string[]
  // The panel is lazily loading the namespace — the mention menu shows a
  // loading line instead of "no matches" (§10.2 lazy connect).
  tablesLoading?: boolean
  manualHeight?: number | null
}>()
const emit = defineEmits<{
  (e: 'send', text: string, mentions: string[]): void
  (e: 'stop'): void
  // Fired when the mention menu opens without a table list — the user gesture
  // that lazily connects and loads the namespace (§10.2).
  (e: 'need-tables'): void
}>()

const text = ref('')
const taRef = ref<HTMLTextAreaElement | null>(null)

// --- height management: auto-grow, overridden by the panel's manual height ---
function applyHeight() {
  const el = taRef.value
  if (!el) return
  if (props.manualHeight != null) {
    el.style.height = props.manualHeight + 'px'
    return
  }
  // Collapse first so scrollHeight reflects the content, not the old height.
  el.style.height = 'auto'
  el.style.height = Math.min(INPUT_MAX_H, Math.max(INPUT_MIN_H, el.scrollHeight)) + 'px'
}
watch(text, () => { void nextTick(applyHeight) })
watch(() => props.manualHeight, applyHeight)
onMounted(applyHeight)

// The panel's grip needs the rendered height as its drag starting point.
defineExpose({ currentHeight: () => taRef.value?.offsetHeight ?? INPUT_MIN_H })

// --- @mention completion (shared logic, ./mention.ts) ---
const { menuOpen, filtered, activeIndex, refreshMention, chooseTable, closeMenu, onMenuKeydown } =
  useMentionCompletion(text, taRef, () => props.tables, () => emit('need-tables'))

// --- IME composition guard ---
// `ev.isComposing` alone is not enough: WebKit fires the committing Enter's
// keydown AFTER compositionend, with isComposing already false — so the
// Enter that puts a Chinese candidate on screen would fall through and send
// the message. Track the composition lifecycle ourselves and swallow Enter
// inside a short post-commit window.
let composing = false
let composedAt = 0
function onCompositionStart() { composing = true }
function onCompositionEnd() { composing = false; composedAt = performance.now() }
function enterCommitsIme(ev: KeyboardEvent): boolean {
  return ev.isComposing || composing || performance.now() - composedAt < 100
}

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Enter' && enterCommitsIme(ev)) {
    // Commit-only: no send, no table pick, and (post-compositionend case) no
    // newline. Mid-composition the IME owns the default — leave it alone.
    if (!ev.isComposing) ev.preventDefault()
    return
  }
  if (onMenuKeydown(ev)) return
  if (ev.key === 'Enter' && !ev.shiftKey) {
    ev.preventDefault()
    submit()
  }
}

function submit() {
  const v = text.value.trim()
  if (!v || props.busy || props.disabled) return
  emit('send', v, extractMentions(v, props.tables))
  text.value = ''
  closeMenu()
}
function onButton() {
  if (props.busy) emit('stop')
  else submit()
}
</script>

<template>
  <div class="composer">
    <div class="input-wrap">
      <MentionMenu
        v-if="menuOpen"
        :items="filtered"
        :active-index="activeIndex"
        :loading="tablesLoading"
        @choose="chooseTable"
        @hover="(i) => (activeIndex = i)"
      />

      <textarea
        ref="taRef"
        v-model="text"
        class="input mono"
        rows="2"
        :disabled="busy || disabled"
        :placeholder="$t('agent.panel.inputPlaceholder')"
        @keydown="onKeydown"
        @input="refreshMention"
        @click="refreshMention"
        @blur="closeMenu"
        @compositionstart="onCompositionStart"
        @compositionend="onCompositionEnd"
      />

      <button
        type="button"
        class="round-btn"
        :class="{ stop: busy }"
        :disabled="disabled || (!busy && !text.trim())"
        :title="busy ? $t('agent.panel.stop') : $t('agent.panel.send')"
        @click="onButton"
      >
        <!-- up arrow = send -->
        <svg v-if="!busy" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
          <path d="M8 12.5v-9M4.2 7.3 8 3.5l3.8 3.8" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" fill="none" />
        </svg>
        <!-- square = stop -->
        <svg v-else viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
          <rect x="4.5" y="4.5" width="7" height="7" rx="1.5" fill="currentColor" />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.composer {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.input-wrap { position: relative; }

.input {
  display: block;
  width: 100%;
  resize: none;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-md);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  font-family: inherit;
  font-size: var(--catdb-fs-body);
  line-height: 1.4;
  /* Right padding keeps text clear of the round send button. */
  padding: 6px 36px 6px 8px;
  outline: none;
  min-height: 64px;
  overflow-y: auto;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.input:focus { border-color: var(--catdb-accent); }
.input:disabled { opacity: 0.5; }

/* Circular send (↑) / stop (■) button inside the bottom-right corner. */
.round-btn {
  position: absolute;
  right: 6px;
  bottom: 6px;
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 50%;
  background: var(--catdb-accent);
  color: var(--catdb-text-on-accent);
  cursor: default;
  transition: background 130ms ease-out;
}
.round-btn:hover { background: var(--catdb-accent-pressed); }
.round-btn:disabled { opacity: 0.4; }
</style>
