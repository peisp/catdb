<script setup lang="ts">
// CSpin — 行内加载指示。size 为直径 px;show=false 时只渲染默认插槽内容
// (对齐 n-spin 包裹用法:<CSpin :show="loading"><内容/></CSpin>,无插槽则裸转圈)。
interface Props {
  size?: number
  show?: boolean
}

withDefaults(defineProps<Props>(), { size: 14, show: true })
</script>

<template>
  <div v-if="$slots.default" class="c-spin-wrap">
    <slot />
    <div v-if="show" class="veil">
      <span class="c-spin" :style="{ width: size + 'px', height: size + 'px' }" />
    </div>
  </div>
  <span
    v-else-if="show"
    class="c-spin"
    :style="{ width: size + 'px', height: size + 'px' }"
  />
</template>

<style scoped>
.c-spin {
  display: inline-block;
  flex: none;
  border: 1.5px solid var(--catdb-text-tertiary);
  border-top-color: var(--catdb-accent);
  border-radius: 50%;
  animation: c-spin-rotate 0.8s linear infinite;
}
.c-spin-wrap {
  position: relative;
  min-width: 0;
  min-height: 0;
}
.veil {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--catdb-surface-content) 55%, transparent);
}
@keyframes c-spin-rotate {
  to { transform: rotate(360deg); }
}
</style>
