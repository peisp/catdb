<script setup lang="ts">
// NsDropdown — self-drawn replacement for a ctx-row native <select> whose
// options load lazily on open. A native select snapshots its options the
// moment the popup opens, and WKWebView neither honours a cancelled
// mousedown on native form controls nor reliably supports showPicker() —
// so an async-loading list needs an in-DOM panel that opens instantly with
// a loading row and refreshes in place. Panel styling follows DESIGN.md's
// self-drawn dropdown spec (menu surface, 24px items, hover = accent).
import { computed, onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{
  value: string
  options: string[]
  placeholder: string
  disabled?: boolean
  loading?: boolean
}>()
const emit = defineEmits<{
  (e: 'change', value: string): void
  // Fired on every popup open — the parent's lazy-load trigger.
  (e: 'open'): void
}>()

const open = ref(false)
const rootRef = ref<HTMLElement | null>(null)

// The saved value may not be in the (not-yet/partially loaded) list — pin it
// on top so it stays visible and selectable, like the old <select> did.
const items = computed(() =>
  props.value && !props.options.includes(props.value) ? [props.value, ...props.options] : props.options,
)

function toggle() {
  if (props.disabled) return
  open.value = !open.value
  if (open.value) emit('open')
}
function choose(v: string) {
  open.value = false
  if (v !== props.value) emit('change', v)
}
function onDocPointerDown(ev: PointerEvent) {
  if (!rootRef.value?.contains(ev.target as Node)) open.value = false
}
function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && open.value) {
    ev.stopPropagation()
    open.value = false
  }
}
watch(open, (o) => {
  if (o) document.addEventListener('pointerdown', onDocPointerDown, true)
  else document.removeEventListener('pointerdown', onDocPointerDown, true)
})
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocPointerDown, true))
</script>

<template>
  <div ref="rootRef" class="nsd" @keydown="onKeydown">
    <button
      type="button"
      class="nsd-trigger"
      :disabled="disabled"
      :title="value || placeholder"
      @click="toggle"
    >
      <span class="nsd-label" :class="{ placeholder: !value }">{{ value || placeholder }}</span>
      <svg class="nsd-caret" viewBox="0 0 8 8" width="8" height="8" aria-hidden="true">
        <path d="M1.5 3 4 5.5 6.5 3" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>

    <div v-if="open" class="nsd-menu">
      <div v-if="items.length === 0 && loading" class="nsd-empty">{{ $t('agent.panel.loadingDbs') }}</div>
      <div v-else-if="items.length === 0" class="nsd-empty">{{ $t('agent.panel.noDbs') }}</div>
      <button
        v-for="o in items"
        :key="o"
        type="button"
        class="nsd-item"
        @click="choose(o)"
      >
        <span class="nsd-check">{{ o === value ? '✓' : '' }}</span>
        <span class="nsd-item-label">{{ o }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.nsd { position: relative; }

/* Trigger mirrors the sibling .ns-select controls in the ctx-row. */
.nsd-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  max-width: 110px;
  font-size: var(--catdb-fs-small);
  font-family: inherit;
  padding: 1px 6px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  outline: none;
  cursor: default;
}
.nsd-trigger:focus { border-color: var(--catdb-accent); }
.nsd-trigger:disabled { opacity: 0.5; }
.nsd-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.nsd-label.placeholder { color: var(--catdb-text-tertiary); }
.nsd-caret { flex: 0 0 auto; opacity: 0.6; }

/* Menu panel per DESIGN.md: raised surface, md radius, menu shadow, 24px
   items, hover = solid accent. Opens upward — the ctx-row sits just above
   the composer near the panel bottom. */
.nsd-menu {
  position: absolute;
  left: 0;
  bottom: calc(100% + 4px);
  z-index: 20;
  min-width: 140px;
  max-width: 240px;
  max-height: 240px;
  overflow-y: auto;
  background: var(--catdb-surface-raised);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-md);
  box-shadow: var(--catdb-shadow-menu);
  padding: 4px;
}
.nsd-empty {
  padding: 6px 8px;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
  text-align: center;
  white-space: nowrap;
}
.nsd-item {
  display: flex;
  align-items: center;
  width: 100%;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  height: 24px;
  padding: 0 8px 0 4px;
  border-radius: var(--catdb-rounded-sm);
  cursor: default;
  text-align: left;
}
.nsd-item:hover {
  background: var(--catdb-accent);
  color: var(--catdb-text-on-accent);
}
.nsd-check {
  flex: 0 0 14px;
  font-size: 10px;
  text-align: center;
}
.nsd-item-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
