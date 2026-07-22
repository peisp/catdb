<script setup lang="ts">
// AgentComposer — multiline input + send/stop toggle, with @table mentions
// (§10.3). Typing "@" opens a table-name completion popover sourced from the
// session's current namespace (passed in via `tables`, already cached in the
// metadata store). Selecting a table turns it into a chip above the input and
// strips the "@xxx" text from the body; chips ride along as the `mentions`
// argument on send. Enter sends, Shift+Enter inserts a newline; while the menu
// is open Enter/Arrows/Esc drive the completion instead.
import { computed, nextTick, ref, watch } from 'vue'

const props = defineProps<{ busy: boolean; disabled?: boolean; tables: string[] }>()
const emit = defineEmits<{ (e: 'send', text: string, mentions: string[]): void; (e: 'stop'): void }>()

const text = ref('')
const mentions = ref<string[]>([])
const taRef = ref<HTMLTextAreaElement | null>(null)

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
  const chosen = new Set(mentions.value)
  return props.tables
    .filter((t) => !chosen.has(t) && (!q || t.toLowerCase().includes(q)))
    .slice(0, 50)
})

function refreshMention() {
  const el = taRef.value
  if (!el) { menuOpen.value = false; return }
  const pos = el.selectionStart ?? text.value.length
  const before = text.value.slice(0, pos)
  // '@' at start or after whitespace, followed by identifier chars up to caret.
  const m = /(^|\s)@([\p{L}\p{N}_$]*)$/u.exec(before)
  if (m) {
    atStart = pos - m[2].length - 1
    query.value = m[2]
    if (!menuOpen.value) { activeIndex.value = 0; debouncedQuery.value = m[2] }
    menuOpen.value = true
  } else {
    menuOpen.value = false
  }
}

function chooseTable(name: string) {
  if (!mentions.value.includes(name)) mentions.value.push(name)
  const el = taRef.value
  const pos = el?.selectionStart ?? text.value.length
  const before = atStart >= 0 ? text.value.slice(0, atStart) : text.value.slice(0, pos)
  const after = text.value.slice(pos)
  text.value = before + after
  menuOpen.value = false
  query.value = ''
  void nextTick(() => {
    const e = taRef.value
    if (e) { const c = before.length; e.focus(); e.setSelectionRange(c, c) }
  })
}

function removeMention(name: string) {
  mentions.value = mentions.value.filter((m) => m !== name)
}

function onKeydown(ev: KeyboardEvent) {
  if (menuOpen.value && filtered.value.length > 0) {
    if (ev.key === 'ArrowDown') { ev.preventDefault(); activeIndex.value = (activeIndex.value + 1) % filtered.value.length; return }
    if (ev.key === 'ArrowUp') { ev.preventDefault(); activeIndex.value = (activeIndex.value - 1 + filtered.value.length) % filtered.value.length; return }
    if (ev.key === 'Enter' && !ev.isComposing) { ev.preventDefault(); const t = filtered.value[activeIndex.value]; if (t) chooseTable(t); return }
    if (ev.key === 'Tab') { ev.preventDefault(); const t = filtered.value[activeIndex.value]; if (t) chooseTable(t); return }
  }
  if (menuOpen.value && ev.key === 'Escape') { ev.preventDefault(); menuOpen.value = false; return }
  if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing) {
    ev.preventDefault()
    submit()
  }
}

function submit() {
  const v = text.value.trim()
  if (!v || props.busy || props.disabled) return
  emit('send', v, [...mentions.value])
  text.value = ''
  mentions.value = []
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
    <div v-if="mentions.length" class="chips">
      <span v-for="m in mentions" :key="m" class="chip">
        @{{ m }}
        <button type="button" class="chip-x" :title="$t('common.delete')" @click="removeMention(m)">×</button>
      </span>
    </div>

    <div class="input-wrap">
      <div v-if="menuOpen" class="mention-menu">
        <div v-if="filtered.length === 0" class="mention-empty">{{ $t('agent.mention.empty') }}</div>
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
      />
    </div>

    <button
      type="button"
      class="send-btn"
      :class="{ stop: busy }"
      :disabled="disabled || (!busy && !text.trim())"
      @click="onButton"
    >
      {{ busy ? $t('agent.panel.stop') : $t('agent.panel.send') }}
    </button>
  </div>
</template>

<style scoped>
.composer {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
  border-top: 1px solid var(--catdb-separator);
  background: var(--catdb-surface-chrome);
}

/* Mention chips above the input (accent-soft, DESIGN.md). */
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-accent);
  background: var(--catdb-accent-soft);
  border-radius: var(--catdb-rounded-sm);
  padding: 1px 4px 1px 6px;
  white-space: nowrap;
}
.chip-x {
  border: none;
  background: transparent;
  color: var(--catdb-accent);
  font-size: 12px;
  line-height: 1;
  padding: 0 1px;
  cursor: default;
}
.chip-x:hover { color: var(--catdb-error); }

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
  width: 100%;
  resize: none;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  font-family: inherit;
  font-size: var(--catdb-fs-body);
  line-height: 1.4;
  padding: 6px 8px;
  outline: none;
  min-height: 44px;
  max-height: 160px;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.input:focus { border-color: var(--catdb-accent); }
.input:disabled { opacity: 0.5; }
.send-btn {
  align-self: flex-end;
  height: 24px;
  padding: 0 14px;
  border: none;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-accent);
  color: var(--catdb-text-on-accent);
  font: inherit;
  font-size: var(--catdb-fs-small);
  cursor: default;
  transition: background 130ms ease-out;
}
.send-btn:hover { background: var(--catdb-accent-pressed); }
.send-btn.stop { background: var(--catdb-error); }
.send-btn:disabled { opacity: 0.4; }
</style>
