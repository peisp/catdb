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
import { CButton, CInput } from '../ui'
import { currentPrompt, resolveCurrentPrompt } from '../../api/prompts'

const value = ref('')
const errorMsg = ref<string | null>(null)
const inputRef = ref<InstanceType<typeof CInput> | null>(null)

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

// Esc on the input fires the inner input's @keydown; backdrop click also cancels.
function onBackdropClick(e: MouseEvent) {
  if (e.target === e.currentTarget) onCancel()
}
</script>

<template>
  <div v-if="visible" class="prompt-backdrop" @mousedown="onBackdropClick">
    <div class="prompt-panel" role="dialog" aria-modal="true">
      <div class="title">{{ currentPrompt?.title }}</div>
      <div v-if="currentPrompt?.label" class="label">{{ currentPrompt.label }}</div>
      <CInput
        ref="inputRef"
        v-model="value"
        size="small"
        :error="!!errorMsg"
        @keydown.enter.prevent="onConfirm"
        @keydown.esc.prevent="onCancel"
      />
      <div v-if="errorMsg" class="err">{{ errorMsg }}</div>
      <div class="actions">
        <CButton size="small" @click="onCancel">{{ currentPrompt?.cancelText ?? $t('common.cancel') }}</CButton>
        <CButton size="small" variant="primary" @click="onConfirm">{{ currentPrompt?.okText ?? $t('common.ok') }}</CButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.prompt-backdrop {
  position: fixed;
  inset: 0;
  z-index: 9999;
  background: var(--catdb-scrim);
  display: flex;
  align-items: center;
  justify-content: center;
  -webkit-app-region: no-drag;
  --wails-draggable: no-drag;
}

.prompt-panel {
  width: 360px;
  max-width: calc(100vw - 32px);
  background: var(--catdb-surface-raised);
  /* shadow token 第一段自带 0.5px 描边环,不再叠 separator 描边(DESIGN.md) */
  border-radius: var(--catdb-rounded-lg);
  box-shadow: var(--catdb-shadow-modal);
  padding: 14px 16px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.title {
  font-size: var(--catdb-fs-body);
  font-weight: 600;
  opacity: 0.9;
}

.label {
  font-size: var(--catdb-fs-small);
  opacity: 0.7;
}

.err {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-error);
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 6px;
}
</style>
