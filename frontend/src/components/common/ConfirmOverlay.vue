<script setup lang="ts">
// ConfirmOverlay — in-app confirm modal used on Windows, where Wails v3's
// native message dialog can't render multiple custom-labeled buttons (Win32
// MessageBoxW collapses them to a single "OK"). Driven by api/confirms.ts;
// api/dialogs.ts routes confirm() here on Windows and keeps native on macOS.
//
// Visual goal per UI_SPEC §5: compact, hairline border, small radius, ESC
// closes, buttons right-aligned. Windows convention puts the primary button on
// the left, so buttons render in reverse of the (cancel-first) input order.
import { computed, nextTick, ref, watch } from 'vue'
import { NButton } from 'naive-ui'
import { currentConfirm, resolveCurrentConfirm } from '../../api/confirms'

const panelRef = ref<HTMLElement | null>(null)

const visible = computed(() => currentConfirm.value !== null)
const opts = computed(() => currentConfirm.value?.opts ?? null)

// Buttons arrive cancel-first, primary-last (matching the native NSAlert order
// on macOS). Windows shows the primary action on the left, so we reverse.
const displayButtons = computed(() => (opts.value ? [...opts.value.buttons].reverse() : []))
const cancelButton = computed(() => opts.value?.buttons.find((b) => b.isCancel) ?? null)
// Enter only fires an EXPLICIT default button — never a fallback — so it can't
// trigger a destructive action (discard/delete) that wasn't marked default.
const defaultButton = computed(() => opts.value?.buttons.find((b) => b.isDefault) ?? null)

function buttonType(b: { isCancel?: boolean; isDefault?: boolean }) {
  if (b.isCancel) return 'default' as const
  if (b.isDefault) return 'primary' as const
  if (opts.value?.kind === 'error') return 'error' as const
  return 'default' as const
}

watch(currentConfirm, (c) => {
  if (!c) return
  void nextTick(() => panelRef.value?.focus())
})

function pick(value: string) {
  resolveCurrentConfirm(value)
}

function onEnter() {
  if (defaultButton.value) resolveCurrentConfirm(defaultButton.value.value)
}

function onCancel() {
  resolveCurrentConfirm(cancelButton.value?.value ?? null)
}

function onBackdropClick(e: MouseEvent) {
  if (e.target === e.currentTarget) onCancel()
}
</script>

<template>
  <div v-if="visible" class="confirm-backdrop" @mousedown="onBackdropClick">
    <div
      ref="panelRef"
      class="confirm-panel"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      @keydown.enter.prevent="onEnter"
      @keydown.esc.prevent="onCancel"
    >
      <div class="title">{{ opts?.title }}</div>
      <div class="message">{{ opts?.message }}</div>
      <div class="actions">
        <n-button
          v-for="b in displayButtons"
          :key="b.value"
          size="small"
          :type="buttonType(b)"
          @click="pick(b.value)"
        >
          {{ b.label }}
        </n-button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.confirm-backdrop {
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

.confirm-panel {
  width: 380px;
  max-width: calc(100vw - 32px);
  background: var(--n-color, #fff);
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 6px;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.18);
  padding: 14px 16px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  outline: none;
}

.title {
  font-size: 13px;
  font-weight: 600;
  opacity: 0.9;
}

.message {
  font-size: 12px;
  opacity: 0.75;
  line-height: 1.5;
  white-space: pre-wrap;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 6px;
}
</style>
