<script setup lang="ts">
// CProgress — 细进度条。percentage 0~100;indeterminate 时来回扫动。
interface Props {
  percentage?: number
  kind?: 'accent' | 'success' | 'error'
  indeterminate?: boolean
}

withDefaults(defineProps<Props>(), { percentage: 0, kind: 'accent', indeterminate: false })
</script>

<template>
  <div class="c-progress" :class="kind">
    <div
      class="bar"
      :class="{ indeterminate }"
      :style="indeterminate ? undefined : { width: Math.min(100, Math.max(0, percentage)) + '%' }"
    />
  </div>
</template>

<style scoped>
.c-progress {
  position: relative;
  height: 4px;
  border-radius: var(--catdb-rounded-pill);
  background: var(--catdb-pressed-fill);
  overflow: hidden;
}
.bar {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  border-radius: var(--catdb-rounded-pill);
  background: var(--catdb-accent);
  transition: width 130ms ease-out;
}
.c-progress.success .bar { background: var(--catdb-success); }
.c-progress.error .bar { background: var(--catdb-error); }
.bar.indeterminate {
  width: 40%;
  animation: c-progress-sweep 1.2s ease-in-out infinite;
}
@keyframes c-progress-sweep {
  0% { left: -40%; }
  100% { left: 100%; }
}
</style>
