<script setup lang="ts">
// CModal — 模态对话框(scrim 遮罩 + raised 面 + lg 圆角 + shadow-modal;
// 阴影 token 自带描边环,不叠 border)。v-model:show 控制;ESC/遮罩点击可关。
import { onBeforeUnmount, watch } from 'vue'

interface Props {
  width?: number
  closable?: boolean
}

const props = withDefaults(defineProps<Props>(), { width: 420, closable: true })

const show = defineModel<boolean>('show', { default: false })

function close() {
  if (props.closable) show.value = false
}

function onBackdrop(e: MouseEvent) {
  if (e.target === e.currentTarget) close()
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}

watch(show, (v) => {
  if (v) document.addEventListener('keydown', onKey, true)
  else document.removeEventListener('keydown', onKey, true)
}, { immediate: true })

onBeforeUnmount(() => document.removeEventListener('keydown', onKey, true))
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="c-modal-backdrop" @mousedown="onBackdrop">
      <div
        class="c-modal"
        role="dialog"
        aria-modal="true"
        :style="{ width: width + 'px' }"
      >
        <slot />
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.c-modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 9500;
  background: var(--catdb-scrim);
  display: flex;
  align-items: center;
  justify-content: center;
  -webkit-app-region: no-drag;
  --wails-draggable: no-drag;
}
.c-modal {
  max-width: calc(100vw - 32px);
  max-height: calc(100vh - 64px);
  overflow: auto;
  background: var(--catdb-surface-raised);
  border-radius: var(--catdb-rounded-lg);
  box-shadow: var(--catdb-shadow-modal);
  padding: 16px 18px;
}
</style>
