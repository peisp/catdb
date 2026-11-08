<script setup lang="ts">
// CSwitch — DESIGN.md「checkbox / radio / switch」自研规格:轨道选中 accent、
// 未选中 selection-unfocused 中性灰,白色旋钮。原生 input 承载 a11y 与焦点。
const model = defineModel<boolean>({ default: false })

interface Props {
  disabled?: boolean
}

withDefaults(defineProps<Props>(), { disabled: false })
</script>

<template>
  <label class="c-switch" :class="{ disabled }">
    <input v-model="model" type="checkbox" role="switch" :disabled="disabled" />
    <span class="track"><span class="knob" /></span>
    <span v-if="$slots.default" class="lbl"><slot /></span>
  </label>
</template>

<style scoped>
.c-switch {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  user-select: none;
  cursor: default;
}
.c-switch input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
}
.track {
  position: relative;
  width: 34px;
  height: 20px;
  flex: none;
  border-radius: var(--catdb-rounded-pill);
  background: var(--catdb-selection-unfocused);
  transition: background-color 130ms ease-out;
}
.knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #ffffff;
  /* 旋钮微投影是控件局部质感(同玻璃材质),不 token 化 */
  box-shadow: 0 1px 2.5px rgba(0, 0, 0, 0.25);
  transition: transform 130ms ease-out;
}
input:checked + .track {
  background: var(--catdb-accent);
}
input:checked + .track .knob {
  transform: translateX(14px);
}
input:focus-visible + .track {
  box-shadow: var(--catdb-focus-ring);
}
.lbl {
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
}
.c-switch.disabled {
  opacity: 0.4;
}
</style>
