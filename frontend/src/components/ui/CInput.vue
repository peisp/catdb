<script setup lang="ts">
// CInput — DESIGN.md「输入控件」的自研实现。原生 input 属性(placeholder/
// type/maxlength/autofocus…)与事件透传到内部 <input>;前后缀图标用 slot。
import { ref } from 'vue'

defineOptions({ inheritAttrs: false })

const model = defineModel<string>({ default: '' })

const inputEl = ref<HTMLInputElement | null>(null)

defineExpose({
  focus: () => inputEl.value?.focus(),
  select: () => inputEl.value?.select(),
})

interface Props {
  size?: 'medium' | 'small'
  error?: boolean
  disabled?: boolean
}

withDefaults(defineProps<Props>(), {
  size: 'medium',
  error: false,
  disabled: false,
})
</script>

<template>
  <div class="c-input" :class="[size, { error, disabled }]">
    <slot name="prefix" />
    <input ref="inputEl" v-model="model" v-bind="$attrs" :disabled="disabled" />
    <slot name="suffix" />
  </div>
</template>

<style scoped>
.c-input {
  display: flex;
  align-items: center;
  gap: 6px;
  height: var(--catdb-control-height-medium);
  padding: 0 9px;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  box-shadow: inset 0 0 0 1px var(--catdb-control-border);
  transition: box-shadow 130ms ease-out;
}
.c-input:focus-within {
  box-shadow: inset 0 0 0 1px var(--catdb-accent), var(--catdb-focus-ring);
}
.c-input.error,
.c-input.error:focus-within {
  box-shadow: inset 0 0 0 1px var(--catdb-error);
}

.c-input input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  padding: 0;
  background: transparent;
  color: var(--catdb-text-primary);
  font-family: var(--catdb-font-family);
  font-size: var(--catdb-fs-body);
  user-select: text;
  -webkit-user-select: text;
}
.c-input input::placeholder {
  color: var(--catdb-text-tertiary);
}

.c-input.small {
  height: var(--catdb-control-height);
  padding: 0 7px;
  border-radius: var(--catdb-rounded-sm);
}
.c-input.small input {
  font-size: var(--catdb-fs-small);
}

.c-input.disabled {
  opacity: 0.4;
}
</style>
