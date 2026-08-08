<script setup lang="ts">
// CRadio — 配合 CRadioGroup(provide/inject 共享选中值)。
import { inject, type Ref } from 'vue'
import { C_RADIO_GROUP } from './radioGroup'

interface Props {
  value: string | number
  disabled?: boolean
}

const props = withDefaults(defineProps<Props>(), { disabled: false })

const group = inject<{ model: Ref<string | number | null>; name: string } | null>(C_RADIO_GROUP, null)

function onChange() {
  if (group) group.model.value = props.value
}
</script>

<template>
  <label class="c-radio" :class="{ disabled }">
    <input
      type="radio"
      :name="group?.name"
      :checked="group?.model.value === value"
      :disabled="disabled"
      @change="onChange"
    />
    <span class="dot" />
    <span v-if="$slots.default" class="lbl"><slot /></span>
  </label>
</template>

<style scoped>
.c-radio {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  user-select: none;
  cursor: default;
}
.c-radio input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
}
.dot {
  position: relative;
  width: 14px;
  height: 14px;
  flex: none;
  border-radius: 50%;
  background: var(--catdb-control-bg);
  box-shadow: inset 0 0 0 1px var(--catdb-control-border);
  transition: background-color 130ms ease-out;
}
input:checked + .dot {
  background: var(--catdb-accent);
  box-shadow: none;
}
input:checked + .dot::after {
  content: '';
  position: absolute;
  inset: 4.5px;
  border-radius: 50%;
  background: var(--catdb-text-on-accent);
}
input:focus-visible + .dot {
  box-shadow: inset 0 0 0 1px var(--catdb-control-border), var(--catdb-focus-ring);
}
input:checked:focus-visible + .dot {
  box-shadow: var(--catdb-focus-ring);
}
.lbl {
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
}
.c-radio.disabled {
  opacity: 0.4;
}
</style>
