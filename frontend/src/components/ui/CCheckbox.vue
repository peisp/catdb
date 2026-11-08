<script setup lang="ts">
// CCheckbox — DESIGN.md「checkbox / radio / switch」自研规格:14px、xs 圆角,
// 选中 accent 实底 + 白勾。原生 input 隐藏承载 a11y 与键盘焦点。
const model = defineModel<boolean>({ default: false })

interface Props {
  disabled?: boolean
}

withDefaults(defineProps<Props>(), { disabled: false })
</script>

<template>
  <label class="c-check" :class="{ disabled }">
    <input v-model="model" type="checkbox" :disabled="disabled" />
    <span class="box">
      <svg viewBox="0 0 14 14" aria-hidden="true">
        <path d="M3 7.5l2.5 2.5 5.5-6" />
      </svg>
    </span>
    <span v-if="$slots.default" class="lbl"><slot /></span>
  </label>
</template>

<style scoped>
.c-check {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  user-select: none;
  cursor: default;
}
.c-check input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
}
.box {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  flex: none;
  border-radius: var(--catdb-rounded-xs);
  background: var(--catdb-control-bg);
  box-shadow: inset 0 0 0 1px var(--catdb-control-border);
  transition: background-color 130ms ease-out;
}
.box svg {
  width: 10px;
  height: 10px;
  fill: none;
  stroke: var(--catdb-text-on-accent);
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  opacity: 0;
  transition: opacity 130ms ease-out;
}
input:checked + .box {
  background: var(--catdb-accent);
  box-shadow: none;
}
input:checked + .box svg {
  opacity: 1;
}
input:focus-visible + .box {
  box-shadow: inset 0 0 0 1px var(--catdb-control-border), var(--catdb-focus-ring);
}
input:checked:focus-visible + .box {
  box-shadow: var(--catdb-focus-ring);
}
.lbl {
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
}
.c-check.disabled {
  opacity: 0.4;
}
</style>
