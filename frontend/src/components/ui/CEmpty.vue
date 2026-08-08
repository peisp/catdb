<script setup lang="ts">
// CEmpty — 空状态(DESIGN.md「空状态」:tertiary 图标 + 说明,垂直居中)。
interface Props {
  description?: string
}

defineProps<Props>()
</script>

<template>
  <!-- 图标由调用方通过 #icon 传静态资源(AppIcon + assets/icons),不自绘默认图 -->
  <div class="c-empty">
    <slot name="icon" />
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
.desc {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
}
</style>
