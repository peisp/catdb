<script setup lang="ts">
// CInputNumber — 数字输入 + macOS 风格右侧步进器。对齐 n-input-number 的
// v-model:value(number|null)/min/max/step 用法子集。
import { ref, watch } from 'vue'

interface Props {
  min?: number
  max?: number
  step?: number
  size?: 'medium' | 'small'
  disabled?: boolean
  placeholder?: string
}

const props = withDefaults(defineProps<Props>(), {
  min: Number.MIN_SAFE_INTEGER,
  max: Number.MAX_SAFE_INTEGER,
  step: 1,
  size: 'medium',
  disabled: false,
  placeholder: '',
})

const model = defineModel<number | null>('value', { default: null })

const text = ref(model.value === null ? '' : String(model.value))

watch(model, (v) => {
  const cur = text.value.trim() === '' ? null : Number(text.value)
  if (v !== cur) text.value = v === null ? '' : String(v)
})

function clamp(n: number) {
  return Math.min(props.max, Math.max(props.min, n))
}

function commit() {
  const t = text.value.trim()
  if (t === '') {
    model.value = null
    return
  }
  const n = Number(t)
  if (Number.isFinite(n)) {
    const c = clamp(n)
    model.value = c
    text.value = String(c)
  } else {
    text.value = model.value === null ? '' : String(model.value)
  }
}

function bump(dir: 1 | -1) {
  if (props.disabled) return
  const base = model.value ?? 0
  const c = clamp(base + dir * props.step)
  model.value = c
  text.value = String(c)
}
</script>

<template>
  <div class="c-number" :class="[size, { disabled }]">
    <input
      v-model="text"
      :disabled="disabled"
      :placeholder="placeholder"
      inputmode="decimal"
      @blur="commit"
      @keydown.enter.prevent="commit"
      @keydown.up.prevent="bump(1)"
      @keydown.down.prevent="bump(-1)"
    />
    <span class="steps">
      <button type="button" tabindex="-1" :disabled="disabled" @click="bump(1)">
        <svg viewBox="0 0 16 16"><path d="M4.5 9.5L8 6l3.5 3.5" /></svg>
      </button>
      <button type="button" tabindex="-1" :disabled="disabled" @click="bump(-1)">
        <svg viewBox="0 0 16 16"><path d="M4.5 6.5L8 10l3.5-3.5" /></svg>
      </button>
    </span>
  </div>
</template>

<style scoped>
.c-number {
  display: flex;
  align-items: stretch;
  height: var(--catdb-control-height-medium);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  box-shadow: inset 0 0 0 1px var(--catdb-control-border);
  transition: box-shadow 130ms ease-out;
  min-width: 0;
}
.c-number:focus-within {
  box-shadow: inset 0 0 0 1px var(--catdb-accent), var(--catdb-focus-ring);
}
.c-number.small {
  height: var(--catdb-control-height);
}
.c-number input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  padding: 0 8px;
  background: transparent;
  color: var(--catdb-text-primary);
  font-family: var(--catdb-font-family);
  font-size: var(--catdb-fs-body);
  font-variant-numeric: tabular-nums;
  user-select: text;
  -webkit-user-select: text;
}
.c-number.small input {
  font-size: var(--catdb-fs-small);
  padding: 0 7px;
}
.c-number input::placeholder {
  color: var(--catdb-text-tertiary);
}
.steps {
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding-right: 3px;
}
.steps button {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 10px;
  border: none;
  padding: 0;
  background: transparent;
  border-radius: var(--catdb-rounded-xs);
  color: var(--catdb-text-secondary);
}
.steps button:hover:not(:disabled) {
  background: var(--catdb-hover-fill);
}
.steps button:active:not(:disabled) {
  background: var(--catdb-pressed-fill);
}
.steps svg {
  width: 9px;
  height: 9px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.c-number.disabled {
  opacity: 0.4;
}
</style>
