<script setup lang="ts">
// CToastHost — toast 渲染宿主,App.vue 根部挂一个。顶部居中堆叠,浮层规格
// (raised 面 + md 圆角 + shadow-menu),130ms 透明度过渡。
import { toasts, type ToastKind } from './toast'

const ICONS: Record<ToastKind, string> = {
  success: 'M2.5 8.5l3.5 3.5 7-8',
  error: 'M4.5 4.5l7 7M11.5 4.5l-7 7',
  warning: 'M8 3v6M8 11.5v.01',
  info: 'M8 7v5M8 4v.01',
}
</script>

<template>
  <Teleport to="body">
    <div class="toast-stack">
      <TransitionGroup name="toast">
        <div v-for="t in toasts" :key="t.id" class="toast" :class="t.kind">
          <svg viewBox="0 0 16 16" aria-hidden="true"><path :d="ICONS[t.kind]" /></svg>
          <span class="txt">{{ t.text }}</span>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-stack {
  position: fixed;
  top: 12px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 10000;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  pointer-events: none;
}
.toast {
  display: flex;
  align-items: center;
  gap: 7px;
  max-width: 480px;
  padding: 6px 12px;
  border-radius: var(--catdb-rounded-md);
  background: var(--catdb-surface-raised);
  box-shadow: var(--catdb-shadow-menu);
  pointer-events: auto;
}
.toast svg {
  width: 14px;
  height: 14px;
  flex: none;
  fill: none;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.toast.success svg { stroke: var(--catdb-success); }
.toast.error svg { stroke: var(--catdb-error); }
.toast.warning svg { stroke: var(--catdb-warning); }
.toast.info svg { stroke: var(--catdb-accent); }
.txt {
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
  line-height: 1.4;
  user-select: text;
  -webkit-user-select: text;
}
.toast-enter-active,
.toast-leave-active {
  transition: opacity 130ms ease-out;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
}
</style>
