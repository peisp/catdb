<script setup lang="ts">
// CSplit — 两窗格分割,对齐 n-split 用法子集:direction、min/max(0~1 比例)、
// 具名插槽 #1/#2/#resize-trigger。size 为第一窗格占比,可 v-model:size。
import { ref } from 'vue'

interface Props {
  direction?: 'horizontal' | 'vertical'
  min?: number
  max?: number
  defaultSize?: number
}

const props = withDefaults(defineProps<Props>(), {
  direction: 'horizontal',
  min: 0.1,
  max: 0.9,
  defaultSize: 0.5,
})

const size = defineModel<number>('size', { default: undefined as unknown as number })
if (size.value === undefined) size.value = props.defaultSize

const rootRef = ref<HTMLElement | null>(null)
const dragging = ref(false)

function onDown(e: PointerEvent) {
  const root = rootRef.value
  if (!root) return
  dragging.value = true
  const rect = root.getBoundingClientRect()
  const move = (ev: PointerEvent) => {
    const ratio =
      props.direction === 'vertical'
        ? (ev.clientY - rect.top) / rect.height
        : (ev.clientX - rect.left) / rect.width
    size.value = Math.min(props.max, Math.max(props.min, ratio))
  }
  const up = () => {
    dragging.value = false
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', up)
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', up)
  e.preventDefault()
}
</script>

<template>
  <div ref="rootRef" class="c-split" :class="[direction, { dragging }]">
    <div class="pane one" :style="{ flexBasis: `calc(${(size * 100).toFixed(3)}% - 0.5px)` }">
      <slot name="1" />
    </div>
    <div class="trigger" @pointerdown="onDown">
      <slot name="resize-trigger">
        <div class="bar" />
      </slot>
    </div>
    <div class="pane two">
      <slot name="2" />
    </div>
  </div>
</template>

<style scoped>
.c-split {
  display: flex;
  min-width: 0;
  min-height: 0;
  width: 100%;
  height: 100%;
}
.c-split.vertical {
  flex-direction: column;
}
.pane {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.pane.one {
  flex-grow: 0;
  flex-shrink: 0;
}
.pane.two {
  flex: 1;
}
.trigger {
  position: relative;
  flex: none;
  z-index: 1;
}
.c-split.horizontal > .trigger {
  width: 1px;
  cursor: col-resize;
}
.c-split.vertical > .trigger {
  height: 1px;
  cursor: row-resize;
}
/* 扩大命中区,不占布局 */
.c-split.horizontal > .trigger::before {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  left: -3px;
  right: -3px;
}
.c-split.vertical > .trigger::before {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: -3px;
  bottom: -3px;
}
.bar {
  width: 100%;
  height: 100%;
  background: var(--catdb-separator);
  transition: background-color 130ms ease-out;
}
.trigger:hover .bar,
.c-split.dragging .bar {
  background: var(--catdb-accent);
}
</style>
