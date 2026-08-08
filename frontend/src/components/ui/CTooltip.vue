<script setup lang="ts">
// CTooltip — hover 延迟出现的提示。默认插槽是触发内容,text/内容插槽二选一。
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { positionPopup, type Placement } from './popup'

interface Props {
  text?: string
  placement?: Placement
  delay?: number
  disabled?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  placement: 'top',
  delay: 450,
  disabled: false,
})

const triggerRef = ref<HTMLElement | null>(null)
const tipRef = ref<HTMLElement | null>(null)
const visible = ref(false)
const style = ref<Record<string, string>>({})
let timer = 0

function show() {
  if (props.disabled) return
  window.clearTimeout(timer)
  timer = window.setTimeout(async () => {
    if (!triggerRef.value) return
    visible.value = true
    await nextTick()
    const anchor = triggerRef.value.getBoundingClientRect()
    const tip = tipRef.value
    if (!tip) return
    const { left, top } = positionPopup(anchor, tip.getBoundingClientRect(), props.placement)
    style.value = { left: `${left}px`, top: `${top}px` }
  }, props.delay)
}

function hide() {
  window.clearTimeout(timer)
  visible.value = false
}

onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <span ref="triggerRef" class="c-tip-trigger" @mouseenter="show" @mouseleave="hide" @mousedown="hide">
    <slot />
  </span>
  <Teleport to="body">
    <div v-if="visible" ref="tipRef" class="c-tip" :style="style">
      <slot name="content">{{ text }}</slot>
    </div>
  </Teleport>
</template>

<style scoped>
.c-tip-trigger {
  display: inline-flex;
  min-width: 0;
}
.c-tip {
  position: fixed;
  z-index: 9000;
  max-width: 320px;
  padding: 3px 8px;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-raised);
  box-shadow: var(--catdb-shadow-menu);
  color: var(--catdb-text-primary);
  font-size: var(--catdb-fs-small);
  line-height: 1.4;
  pointer-events: none;
  user-select: none;
}
</style>
