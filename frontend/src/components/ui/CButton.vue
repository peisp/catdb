<script setup lang="ts">
// CButton — DESIGN.md「按钮」规格的自研实现(button-toolbar 图标钮形态简单,
// 各视图按规范自绘,不在此组件内)。danger-solid 仅限确认对话框的破坏性动作。
interface Props {
  variant?: 'primary' | 'standard' | 'danger' | 'danger-solid'
  size?: 'medium' | 'small' | 'mini'
  disabled?: boolean
  type?: 'button' | 'submit'
}

withDefaults(defineProps<Props>(), {
  variant: 'standard',
  size: 'medium',
  disabled: false,
  type: 'button',
})
</script>

<template>
  <button class="c-btn" :class="[variant, size]" :type="type" :disabled="disabled">
    <slot />
  </button>
</template>

<style scoped>
.c-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: var(--catdb-control-height-medium);
  padding: 0 12px;
  border: none;
  border-radius: var(--catdb-rounded-sm);
  font-family: var(--catdb-font-family);
  font-size: var(--catdb-fs-body);
  font-weight: 400;
  color: var(--catdb-text-primary);
  background-color: var(--catdb-control-bg);
  box-shadow: var(--catdb-control-shadow);
  white-space: nowrap;
  user-select: none;
  cursor: default;
  transition: background-color 130ms ease-out, opacity 130ms ease-out;
}
.c-btn:active:not(:disabled) {
  background-image: linear-gradient(var(--catdb-pressed-fill), var(--catdb-pressed-fill));
}
.c-btn:focus-visible {
  outline: none;
  box-shadow: var(--catdb-control-shadow), var(--catdb-focus-ring);
}

.c-btn.primary {
  color: var(--catdb-text-on-accent);
  background-color: var(--catdb-accent);
  box-shadow: var(--catdb-control-shadow-primary);
}
.c-btn.primary:active:not(:disabled) {
  background-color: var(--catdb-accent-pressed);
  background-image: none;
}
.c-btn.primary:focus-visible {
  box-shadow: var(--catdb-control-shadow-primary), var(--catdb-focus-ring);
}

.c-btn.danger {
  color: var(--catdb-error);
}

.c-btn.danger-solid {
  color: var(--catdb-text-on-accent);
  background-color: var(--catdb-error);
  box-shadow: var(--catdb-control-shadow-primary);
}
.c-btn.danger-solid:focus-visible {
  box-shadow: var(--catdb-control-shadow-primary), var(--catdb-focus-ring);
}

.c-btn.small {
  height: var(--catdb-control-height);
  padding: 0 9px;
  font-size: var(--catdb-fs-small);
}
.c-btn.mini {
  height: var(--catdb-control-height-mini);
  padding: 0 7px;
  font-size: var(--catdb-fs-small);
}

.c-btn:disabled {
  opacity: 0.4;
}
</style>
