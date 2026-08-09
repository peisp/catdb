<script setup lang="ts">
// CAlert — 行内提示条(DESIGN.md 语义色规则:图标染色 + 8%~12% 透明底,
// 不整块实底)。title 可选,正文走默认插槽。
interface Props {
  kind?: 'success' | 'warning' | 'error' | 'info'
  title?: string
}

withDefaults(defineProps<Props>(), { kind: 'info' })

const ICONS = {
  success: 'M2.5 8.5l3.5 3.5 7-8',
  error: 'M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13zM5.5 5.5l5 5M10.5 5.5l-5 5',
  warning: 'M8 2l6.5 11.5h-13zM8 6.5v3.5M8 12v.01',
  info: 'M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13zM8 7.5V11M8 5v.01',
} as const
</script>

<template>
  <div class="c-alert" :class="kind">
    <svg viewBox="0 0 16 16" aria-hidden="true"><path :d="ICONS[kind]" /></svg>
    <div class="body">
      <div v-if="title" class="title">{{ title }}</div>
      <div class="msg"><slot /></div>
    </div>
  </div>
</template>

<style scoped>
.c-alert {
  display: flex;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--catdb-rounded-sm);
  font-size: var(--catdb-fs-small);
  line-height: 1.5;
  color: var(--catdb-text-primary);
}
.c-alert svg {
  width: 14px;
  height: 14px;
  flex: none;
  margin-top: 2px;
  fill: none;
  stroke-width: 1.5;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.c-alert.info { background: var(--catdb-accent-soft); }
.c-alert.info svg { stroke: var(--catdb-accent); }
.c-alert.success { background: color-mix(in srgb, var(--catdb-success) 10%, transparent); }
.c-alert.success svg { stroke: var(--catdb-success); }
.c-alert.warning { background: color-mix(in srgb, var(--catdb-warning) 10%, transparent); }
.c-alert.warning svg { stroke: var(--catdb-warning); }
.c-alert.error { background: color-mix(in srgb, var(--catdb-error) 10%, transparent); }
.c-alert.error svg { stroke: var(--catdb-error); }
.title {
  font-weight: 600;
  margin-bottom: 2px;
}
.msg {
  user-select: text;
  -webkit-user-select: text;
}
.body { min-width: 0; }
</style>
