<script setup lang="ts">
// CPopover — 点击触发的锚定浮层。#trigger 是锚点,默认插槽是面板内容;
// 外点/ESC 关闭。受控用 v-model:show,非受控内部托管。
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { positionPopup, type Placement } from './popup'

interface Props {
  placement?: Placement
}

const props = withDefaults(defineProps<Props>(), { placement: 'bottom-start' })

const show = defineModel<boolean>('show', { default: false })

const triggerRef = ref<HTMLElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
const style = ref<Record<string, string>>({})

async function reposition() {
  await nextTick()
  const anchor = triggerRef.value?.getBoundingClientRect()
  const panel = panelRef.value
  if (!anchor || !panel) return
  const { left, top } = positionPopup(anchor, panel.getBoundingClientRect(), props.placement)
  style.value = { left: `${left}px`, top: `${top}px` }
}

function toggle() {
  show.value = !show.value
}

function onDocDown(e: MouseEvent) {
  const t = e.target as Node
  if (triggerRef.value?.contains(t) || panelRef.value?.contains(t)) return
  show.value = false
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') show.value = false
}

watch(show, (v) => {
  if (v) {
    void reposition()
    document.addEventListener('mousedown', onDocDown, true)
    document.addEventListener('keydown', onKey, true)
  } else {
    document.removeEventListener('mousedown', onDocDown, true)
    document.removeEventListener('keydown', onKey, true)
  }
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocDown, true)
  document.removeEventListener('keydown', onKey, true)
})

defineExpose({ reposition })
</script>

<template>
  <span ref="triggerRef" class="c-pop-trigger" @click="toggle">
    <slot name="trigger" />
  </span>
  <Teleport to="body">
    <div v-if="show" ref="panelRef" class="c-pop" :style="style">
      <slot />
    </div>
  </Teleport>
</template>

<style scoped>
.c-pop-trigger {
  display: inline-flex;
  min-width: 0;
}
.c-pop {
  position: fixed;
  z-index: 8000;
  border-radius: var(--catdb-rounded-md);
  background: var(--catdb-surface-raised);
  box-shadow: var(--catdb-shadow-menu);
  padding: var(--catdb-space-md);
}
</style>
