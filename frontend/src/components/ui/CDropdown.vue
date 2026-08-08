<script setup lang="ts">
// CDropdown — 菜单式下拉(对齐 n-dropdown 的 options/@select 用法子集)。
// #default 是触发器;菜单项 26px,hover 即 accent 实底白字(DESIGN.md:
// 菜单是唯一 hover 即蓝的地方)。type:'divider' 渲染分隔线。
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { positionPopup, type Placement } from './popup'

export interface DropdownOption {
  label?: string
  key?: string | number
  disabled?: boolean
  danger?: boolean
  type?: 'divider'
}

interface Props {
  options: DropdownOption[]
  placement?: Placement
}

const props = withDefaults(defineProps<Props>(), { placement: 'bottom-start' })

const emit = defineEmits<{
  select: [key: string | number]
  'update:show': [show: boolean]
}>()

const triggerRef = ref<HTMLElement | null>(null)
const menuRef = ref<HTMLElement | null>(null)
const visible = ref(false)
const style = ref<Record<string, string>>({})

async function open() {
  visible.value = true
  emit('update:show', true)
  await nextTick()
  const anchor = triggerRef.value?.getBoundingClientRect()
  const menu = menuRef.value
  if (!anchor || !menu) return
  const { left, top } = positionPopup(anchor, menu.getBoundingClientRect(), props.placement)
  style.value = { left: `${left}px`, top: `${top}px` }
  document.addEventListener('mousedown', onDocDown, true)
  document.addEventListener('keydown', onKey, true)
}

function close() {
  if (!visible.value) return
  visible.value = false
  emit('update:show', false)
  document.removeEventListener('mousedown', onDocDown, true)
  document.removeEventListener('keydown', onKey, true)
}

function toggle() {
  if (visible.value) close()
  else void open()
}

function onDocDown(e: MouseEvent) {
  const t = e.target as Node
  if (triggerRef.value?.contains(t) || menuRef.value?.contains(t)) return
  close()
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}

function pick(o: DropdownOption) {
  if (o.disabled || o.type === 'divider' || o.key === undefined) return
  close()
  emit('select', o.key)
}

onBeforeUnmount(close)
</script>

<template>
  <span ref="triggerRef" class="c-dd-trigger" @click="toggle">
    <slot />
  </span>
  <Teleport to="body">
    <div v-if="visible" ref="menuRef" class="c-dd" :style="style">
      <template v-for="(o, i) in options" :key="o.key ?? i">
        <div v-if="o.type === 'divider'" class="div" />
        <div
          v-else
          class="item"
          :class="{ disabled: o.disabled, danger: o.danger }"
          @click="pick(o)"
        >
          {{ o.label }}
        </div>
      </template>
    </div>
  </Teleport>
</template>

<style scoped>
.c-dd-trigger {
  display: inline-flex;
  min-width: 0;
}
.c-dd {
  position: fixed;
  z-index: 8000;
  min-width: 140px;
  max-height: 60vh;
  overflow-y: auto;
  padding: 5px;
  border-radius: var(--catdb-rounded-md);
  background: var(--catdb-surface-raised);
  box-shadow: var(--catdb-shadow-menu);
}
.item {
  display: flex;
  align-items: center;
  height: 26px;
  padding: 0 9px;
  border-radius: var(--catdb-rounded-sm);
  font-size: var(--catdb-fs-body);
  color: var(--catdb-text-primary);
  white-space: nowrap;
  user-select: none;
}
.item:hover:not(.disabled) {
  background: var(--catdb-accent);
  color: var(--catdb-text-on-accent);
}
.item.danger {
  color: var(--catdb-error);
}
.item.danger:hover:not(.disabled) {
  background: var(--catdb-error);
  color: var(--catdb-text-on-accent);
}
.item.disabled {
  opacity: 0.4;
}
.div {
  height: 1px;
  margin: 5px 9px;
  background: var(--catdb-separator);
}
</style>
