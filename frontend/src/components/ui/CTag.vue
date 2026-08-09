<script setup lang="ts">
// CTag — 行内徽标(DESIGN.md:mini 字号、xs 圆角;语义色只染小面积,底用
// 8%~12% 透明度)。
interface Props {
  kind?: 'default' | 'accent' | 'success' | 'warning' | 'error'
}

withDefaults(defineProps<Props>(), { kind: 'default' })
</script>

<template>
  <span class="c-tag" :class="kind"><slot /></span>
</template>

<style scoped>
.c-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 18px;
  padding: 0 6px;
  border-radius: var(--catdb-rounded-xs);
  font-size: var(--catdb-fs-mini);
  line-height: 1;
  white-space: nowrap;
  background: var(--catdb-hover-fill);
  color: var(--catdb-text-secondary);
  user-select: none;
}
.c-tag.accent {
  background: var(--catdb-accent-soft);
  color: var(--catdb-accent);
}
.c-tag.success {
  background: color-mix(in srgb, var(--catdb-success) 12%, transparent);
  color: var(--catdb-success);
}
.c-tag.warning {
  background: color-mix(in srgb, var(--catdb-warning) 12%, transparent);
  color: var(--catdb-warning);
}
.c-tag.error {
  background: color-mix(in srgb, var(--catdb-error) 12%, transparent);
  color: var(--catdb-error);
}
</style>
