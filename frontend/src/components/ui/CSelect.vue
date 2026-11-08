<script setup lang="ts">
// CSelect — macOS 形态 select(双箭头胶囊 + 菜单面板),对齐 n-select 的
// options/v-model:value/filterable/clearable 用法子集。
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { positionPopup } from './popup'

export interface SelectOption {
  label: string
  value: string | number
  disabled?: boolean
}

interface Props {
  options: SelectOption[]
  size?: 'medium' | 'small'
  filterable?: boolean
  clearable?: boolean
  disabled?: boolean
  placeholder?: string
}

const props = withDefaults(defineProps<Props>(), {
  size: 'medium',
  filterable: false,
  clearable: false,
  disabled: false,
  placeholder: '',
})

const model = defineModel<string | number | null>('value', { default: null })

const triggerRef = ref<HTMLElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
const filterRef = ref<HTMLInputElement | null>(null)
const open = ref(false)
const filter = ref('')
const style = ref<Record<string, string>>({})

const selectedLabel = computed(
  () => props.options.find((o) => o.value === model.value)?.label ?? '',
)

const shown = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return props.options
  return props.options.filter((o) => o.label.toLowerCase().includes(q))
})

async function show() {
  if (props.disabled) return
  open.value = true
  filter.value = ''
  await nextTick()
  const anchor = triggerRef.value?.getBoundingClientRect()
  const panel = panelRef.value
  if (!anchor || !panel) return
  const { left, top } = positionPopup(anchor, panel.getBoundingClientRect(), 'bottom-start', 4)
  style.value = { left: `${left}px`, top: `${top}px`, minWidth: `${anchor.width}px` }
  filterRef.value?.focus()
  document.addEventListener('mousedown', onDocDown, true)
  document.addEventListener('keydown', onKey, true)
}

function close() {
  if (!open.value) return
  open.value = false
  document.removeEventListener('mousedown', onDocDown, true)
  document.removeEventListener('keydown', onKey, true)
}

function onDocDown(e: MouseEvent) {
  const t = e.target as Node
  if (triggerRef.value?.contains(t) || panelRef.value?.contains(t)) return
  close()
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}

function pick(o: SelectOption) {
  if (o.disabled) return
  model.value = o.value
  close()
}

function clear(e: MouseEvent) {
  e.stopPropagation()
  model.value = null
}

onBeforeUnmount(close)
</script>

<template>
  <div
    ref="triggerRef"
    class="c-select"
    :class="[size, { disabled, open }]"
    :tabindex="disabled ? -1 : 0"
    @click="open ? close() : show()"
    @keydown.enter.prevent="open ? close() : show()"
  >
    <span class="val" :class="{ ph: !selectedLabel }">{{ selectedLabel || placeholder }}</span>
    <span
      v-if="clearable && model !== null && model !== ''"
      class="clear"
      @click="clear"
    >
      <svg viewBox="0 0 16 16"><path d="M4.5 4.5l7 7M11.5 4.5l-7 7" /></svg>
    </span>
    <span class="chev">
      <svg viewBox="0 0 16 16"><path d="M4.5 6.2L8 3l3.5 3.2M4.5 9.8L8 13l3.5-3.2" /></svg>
    </span>
  </div>
  <Teleport to="body">
    <div v-if="open" ref="panelRef" class="c-select-panel" :style="style">
      <div v-if="filterable" class="filter">
        <input ref="filterRef" v-model="filter" @keydown.esc.prevent="close" />
      </div>
      <div class="list">
        <div
          v-for="o in shown"
          :key="o.value"
          class="opt"
          :class="{ on: o.value === model, disabled: o.disabled }"
          @click="pick(o)"
        >
          <span class="check">
            <svg v-if="o.value === model" viewBox="0 0 16 16"><path d="M3 8.5l3 3 7-7.5" /></svg>
          </span>
          {{ o.label }}
        </div>
        <div v-if="shown.length === 0" class="none">—</div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.c-select {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  height: var(--catdb-control-height-medium);
  padding: 0 24px 0 9px;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-control-bg);
  box-shadow: var(--catdb-control-shadow);
  user-select: none;
  cursor: default;
  min-width: 0;
}
.c-select:focus-visible {
  outline: none;
  box-shadow: var(--catdb-control-shadow), var(--catdb-focus-ring);
}
.c-select.small {
  height: var(--catdb-control-height);
  padding-left: 7px;
}
.val {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
}
.c-select.small .val {
  font-size: var(--catdb-fs-small);
}
.val.ph {
  color: var(--catdb-text-tertiary);
}
.chev {
  position: absolute;
  right: 4px;
  top: 50%;
  transform: translateY(-50%);
  width: 16px;
  height: 16px;
  border-radius: var(--catdb-rounded-xs);
  background: var(--catdb-accent);
  display: flex;
  align-items: center;
  justify-content: center;
}
.c-select.small .chev {
  width: 14px;
  height: 14px;
}
.chev svg {
  width: 10px;
  height: 10px;
  fill: none;
  stroke: var(--catdb-text-on-accent);
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.clear {
  display: flex;
  align-items: center;
  color: var(--catdb-text-tertiary);
}
.clear svg {
  width: 10px;
  height: 10px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.8;
  stroke-linecap: round;
}
.c-select.disabled {
  opacity: 0.4;
}

.c-select-panel {
  position: fixed;
  z-index: 8000;
  max-height: 40vh;
  display: flex;
  flex-direction: column;
  padding: 5px;
  border-radius: var(--catdb-rounded-md);
  background: var(--catdb-surface-raised);
  box-shadow: var(--catdb-shadow-menu);
}
.filter {
  padding: 2px 2px 6px;
}
.filter input {
  width: 100%;
  height: var(--catdb-control-height);
  padding: 0 7px;
  border: none;
  outline: none;
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  box-shadow: inset 0 0 0 1px var(--catdb-control-border);
  color: var(--catdb-text-primary);
  font-size: var(--catdb-fs-small);
  font-family: var(--catdb-font-family);
}
.filter input:focus {
  box-shadow: inset 0 0 0 1px var(--catdb-accent);
}
.list {
  overflow-y: auto;
  min-width: 0;
}
.opt {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 26px;
  padding: 0 8px 0 2px;
  border-radius: var(--catdb-rounded-sm);
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
  white-space: nowrap;
  user-select: none;
}
.opt:hover:not(.disabled) {
  background: var(--catdb-accent);
  color: var(--catdb-text-on-accent);
}
.opt.disabled {
  opacity: 0.4;
}
.check {
  width: 16px;
  flex: none;
  display: flex;
  justify-content: center;
}
.check svg {
  width: 10px;
  height: 10px;
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.none {
  padding: 6px 8px;
  color: var(--catdb-text-tertiary);
  font-size: var(--catdb-fs-small);
}
</style>
