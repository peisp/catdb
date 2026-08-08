<script setup lang="ts">
// CEmpty — 空状态(DESIGN.md「空状态」:tertiary 图标 + 说明,垂直居中)。
interface Props {
  description?: string
}

defineProps<Props>()
</script>

<template>
  <div class="c-empty">
    <slot name="icon">
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <rect x="2" y="4" width="12" height="9" rx="1.5" />
        <path d="M2 7h12M5.5 2.5v3" />
      </svg>
    </slot>
    <div class="desc"><slot>{{ description }}</slot></div>
  </div>
</template>

<style scoped>
.c-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 20px;
  color: var(--catdb-text-tertiary);
}
.c-empty svg {
  width: 28px;
  height: 28px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.desc {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
}
</style>
