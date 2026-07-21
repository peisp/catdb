<script setup lang="ts">
// AgentComposer — multiline input + send/stop toggle. Enter sends,
// Shift+Enter inserts a newline. Input is disabled while a turn is running;
// the button flips to Stop and aborts the loop.
import { ref } from 'vue'

const props = defineProps<{ busy: boolean; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'send', text: string): void; (e: 'stop'): void }>()

const text = ref('')

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing) {
    ev.preventDefault()
    submit()
  }
}
function submit() {
  const v = text.value.trim()
  if (!v || props.busy || props.disabled) return
  emit('send', v)
  text.value = ''
}
function onButton() {
  if (props.busy) emit('stop')
  else submit()
}
</script>

<template>
  <div class="composer">
    <textarea
      v-model="text"
      class="input mono"
      rows="2"
      :disabled="busy || disabled"
      :placeholder="$t('agent.panel.inputPlaceholder')"
      @keydown="onKeydown"
    />
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
