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
import { computed, nextTick, onMounted, ref, watch } from 'vue'

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

// --- @mention completion state ---
const menuOpen = ref(false)
const query = ref('') // live token after '@' (drives caret tracking)
const debouncedQuery = ref('') // filter input, updated 150ms after query
const activeIndex = ref(0)
let atStart = -1 // index of the '@' in text.value that opened the menu
let debounceTimer: ReturnType<typeof setTimeout> | null = null

watch(query, (q) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => { debouncedQuery.value = q }, 150)
})

const filtered = computed(() => {
  const q = debouncedQuery.value.toLowerCase()
  return props.tables
    .filter((t) => !q || t.toLowerCase().includes(q))
    .slice(0, 50)
})

function refreshMention() {
  const el = taRef.value
  if (!el) { menuOpen.value = false; return }
  const pos = el.selectionStart ?? text.value.length
  const before = text.value.slice(0, pos)
  // '@' anywhere, followed by identifier chars up to the caret — mid-word
  // triggers too ("查@订"), matching extractMentions' send-time scan.
  const m = /@([\p{L}\p{N}_$]*)$/u.exec(before)
  if (m) {
    atStart = pos - m[1].length - 1
    query.value = m[1]
    if (!menuOpen.value) {
      activeIndex.value = 0
      debouncedQuery.value = m[1]
      if (props.tables.length === 0) emit('need-tables')
    }
    menuOpen.value = true
  } else {
    menuOpen.value = false
  }
}

function chooseTable(name: string) {
  // Complete the token in place: "@订" → "@订单表 ", staying in the sentence.
  const el = taRef.value
  const pos = el?.selectionStart ?? text.value.length
  const start = atStart >= 0 ? atStart : pos
  const before = text.value.slice(0, start)
  const after = text.value.slice(pos)
  const inserted = '@' + name + ' '
  text.value = before + inserted + after
  menuOpen.value = false
  query.value = ''
  void nextTick(() => {
    const e = taRef.value
    if (e) { const c = (before + inserted).length; e.focus(); e.setSelectionRange(c, c) }
  })
}

// Mentions are derived from the text at send time: every @token that names a
// known table (case-insensitive, canonical casing returned, deduped). Editing
// or deleting a mention in the text therefore just works.
function extractMentions(v: string): string[] {
  if (props.tables.length === 0) return []
  const byLower = new Map(props.tables.map((t) => [t.toLowerCase(), t]))
  const out: string[] = []
  for (const m of v.matchAll(/@([\p{L}\p{N}_$]+)/gu)) {
    const hit = byLower.get(m[1].toLowerCase())
    if (hit && !out.includes(hit)) out.push(hit)
  }
  return out
}

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
  if (menuOpen.value && filtered.value.length > 0) {
    if (ev.key === 'ArrowDown') { ev.preventDefault(); activeIndex.value = (activeIndex.value + 1) % filtered.value.length; return }
    if (ev.key === 'ArrowUp') { ev.preventDefault(); activeIndex.value = (activeIndex.value - 1 + filtered.value.length) % filtered.value.length; return }
    if (ev.key === 'Enter') { ev.preventDefault(); const t = filtered.value[activeIndex.value]; if (t) chooseTable(t); return }
    if (ev.key === 'Tab') { ev.preventDefault(); const t = filtered.value[activeIndex.value]; if (t) chooseTable(t); return }
  }
  if (menuOpen.value && ev.key === 'Escape') { ev.preventDefault(); menuOpen.value = false; return }
  if (ev.key === 'Enter' && !ev.shiftKey) {
    ev.preventDefault()
    submit()
  }
}

function submit() {
  const v = text.value.trim()
  if (!v || props.busy || props.disabled) return
  emit('send', v, extractMentions(v))
  text.value = ''
  menuOpen.value = false
}
function onButton() {
  if (props.busy) emit('stop')
  else submit()
}

// Clamp the active row when the filter list shrinks.
watch(filtered, (f) => { if (activeIndex.value >= f.length) activeIndex.value = 0 })
</script>

<template>
  <div class="composer">
    <div class="input-wrap">
      <div v-if="menuOpen" class="mention-menu">
        <div v-if="filtered.length === 0 && tablesLoading" class="mention-empty">{{ $t('agent.mention.loading') }}</div>
        <div v-else-if="filtered.length === 0" class="mention-empty">{{ $t('agent.mention.empty') }}</div>
        <button
          v-for="(t, i) in filtered"
          :key="t"
          type="button"
          class="mention-item"
          :class="{ active: i === activeIndex }"
          @mousedown.prevent="chooseTable(t)"
          @mousemove="activeIndex = i"
        >{{ t }}</button>
      </div>

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
        @blur="menuOpen = false"
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

/* Completion popover (menu panel style, DESIGN.md). */
.mention-menu {
  position: absolute;
  left: 0;
  right: 0;
  bottom: calc(100% + 4px);
  z-index: 20;
  max-height: 200px;
  overflow-y: auto;
  background: var(--catdb-surface-raised);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-md);
  box-shadow: var(--catdb-shadow-menu);
  padding: 4px;
}
.mention-empty {
  padding: 6px 8px;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
  text-align: center;
}
.mention-item {
  display: block;
  width: 100%;
  text-align: left;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  height: 24px;
  padding: 0 8px;
  border-radius: var(--catdb-rounded-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;
}
.mention-item.active { background: var(--catdb-accent); color: var(--catdb-text-on-accent); }

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
