<script setup lang="ts">
// CVirtualList — 定高行的窗口化列表,对齐 n-virtual-list 用法子集
// (items/item-size + 默认作用域插槽 {item, index})。
import { computed, ref } from 'vue'

interface Props {
  items: unknown[]
  itemSize: number
  overscan?: number
}

const props = withDefaults(defineProps<Props>(), { overscan: 6 })

const scroller = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewport = ref(0)

function onScroll() {
  const el = scroller.value
  if (!el) return
  scrollTop.value = el.scrollTop
  viewport.value = el.clientHeight
}

const range = computed(() => {
  const start = Math.max(0, Math.floor(scrollTop.value / props.itemSize) - props.overscan)
  const count = Math.ceil((viewport.value || 600) / props.itemSize) + props.overscan * 2
  const end = Math.min(props.items.length, start + count)
  return { start, end }
})

const visible = computed(() =>
  props.items.slice(range.value.start, range.value.end).map((item, i) => ({
    item,
    index: range.value.start + i,
  })),
)
</script>

<template>
  <div ref="scroller" class="c-vlist" @scroll.passive="onScroll">
    <div class="spacer" :style="{ height: items.length * itemSize + 'px' }">
      <div
        class="window"
        :style="{ transform: `translateY(${range.start * itemSize}px)` }"
      >
        <div
          v-for="v in visible"
          :key="v.index"
          :style="{ height: itemSize + 'px' }"
        >
          <slot :item="v.item" :index="v.index" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.c-vlist {
  overflow-y: auto;
  min-height: 0;
  height: 100%;
}
.spacer {
  position: relative;
}
.window {
  position: absolute;
  left: 0;
  right: 0;
  top: 0;
}
</style>
