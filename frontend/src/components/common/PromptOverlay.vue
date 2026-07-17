<script setup lang="ts">
// PromptOverlay — minimal in-app text-input prompt, driven by api/prompts.ts.
//
// Why this exists: Wails v3 only exposes file/info/warn/error dialogs, no
// generic text-input prompt. Rather than open a second native window for a
// single string (rename, etc.), we render a small modal-style overlay.
//
// Visual goal per DESIGN.md: desktop-like, hairline border, small radius, no
// flashy animation. The backdrop is a thin dim layer; the panel is sized
// only as wide as a name field needs.
import { computed, nextTick, ref, watch } from 'vue'
import { NButton, NInput } from 'naive-ui'
import { currentPrompt, resolveCurrentPrompt } from '../../api/prompts'

const value = ref('')
const errorMsg = ref<string | null>(null)
const inputRef = ref<InstanceType<typeof NInput> | null>(null)

const visible = computed(() => currentPrompt.value !== null)

watch(currentPrompt, (p) => {
  if (!p) return
  value.value = p.initial
  errorMsg.value = null
  void nextTick(() => {
    inputRef.value?.focus()
    inputRef.value?.select()
  })
})

watch(value, () => { errorMsg.value = null })

function onConfirm() {
  const p = currentPrompt.value
  if (!p) return
  const v = value.value.trim()
  if (p.validate) {
    const msg = p.validate(v)
    if (msg) { errorMsg.value = msg; return }
  }
  resolveCurrentPrompt(v)
}

function onCancel() {
  resolveCurrentPrompt(null)
}

// Esc on the input fires NInput's @keydown; backdrop click also cancels.
function onBackdropClick(e: MouseEvent) {
  if (e.target === e.currentTarget) onCancel()
}
</script>

<template>
  <div v-if="visible" class="prompt-backdrop" @mousedown="onBackdropClick">
    <div class="prompt-panel" role="dialog" aria-modal="true">
      <div class="title">{{ currentPrompt?.title }}</div>
      <div v-if="currentPrompt?.label" class="label">{{ currentPrompt.label }}</div>
      <n-input
        ref="inputRef"
        v-model:value="value"
        size="small"
        :status="errorMsg ? 'error' : undefined"
        @keydown.enter.prevent="onConfirm"
        @keydown.esc.prevent="onCancel"
      />
      <div v-if="errorMsg" class="err">{{ errorMsg }}</div>
      <div class="actions">
        <n-button size="small" @click="onCancel">{{ currentPrompt?.cancelText ?? $t('common.cancel') }}</n-button>
        <n-button size="small" type="primary" @click="onConfirm">{{ currentPrompt?.okText ?? $t('common.ok') }}</n-button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.prompt-backdrop {
  position: fixed;
  inset: 0;
  z-index: 9999;
  background: rgba(0, 0, 0, 0.25);
  display: flex;
  align-items: center;
  justify-content: center;
  -webkit-app-region: no-drag;
  --wails-draggable: no-drag;
}

.prompt-panel {
  width: 360px;
  max-width: calc(100vw - 32px);
  background: var(--n-color, #fff);
  border: 1px solid var(--n-border-color, rgba(127,127,127,0.25));
  border-radius: 6px;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.18);
  padding: 14px 16px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.title {
  font-size: 13px;
  font-weight: 600;
  opacity: 0.9;
}

.label {
  font-size: 12px;
  opacity: 0.7;
}

.err {
  font-size: 11px;
  color: #d03050;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 6px;
}
</style>
