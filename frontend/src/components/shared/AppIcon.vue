<script setup lang="ts">
// AppIcon — renders a raw SVG string at a uniform size/opacity so it inherits
// the surrounding text color and theme. Consumers import their svg with `?raw`
// and pass it via `src`; adding an icon needs no change here.
import { computed } from 'vue'

const props = withDefaults(defineProps<{ src: string; size?: number }>(), {
  size: 15,
})

const style = computed(() => ({ '--app-icon-size': `${props.size}px` }))
</script>

<template>
  <span class="app-icon" :style="style" aria-hidden="true" v-html="props.src" />
</template>

<style scoped>
.app-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--app-icon-size);
  height: var(--app-icon-size);
  flex: 0 0 auto;
  opacity: 0.62;
}
.app-icon :deep(svg) {
  width: var(--app-icon-size);
  height: var(--app-icon-size);
  display: block;
}
</style>
