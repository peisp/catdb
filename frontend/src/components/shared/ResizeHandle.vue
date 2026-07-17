<script setup lang="ts">
// ResizeHandle — minimal drag affordance used by panels that own their own
// pointer-driven resize logic. Transparent until hovered or dragged; on
// activation it tints the bar and reveals a centered grip in the primary
// color.
//
// orientation:
//   horizontal — thin horizontal strip pinned to the top of its parent
//                (row-resize; drag is vertical). Default.
//   vertical   — thin vertical strip pinned to the right edge of its parent
//                (col-resize; drag is horizontal).
//
// The parent owns the pointer events and toggles `:active` while dragging so
// the grip stays visible after the cursor leaves the bar.
withDefaults(
  defineProps<{
    orientation?: 'horizontal' | 'vertical'
    active?: boolean
  }>(),
  { orientation: 'horizontal', active: false },
)
</script>

<template>
  <div
    class="resize-handle"
    :class="[`is-${orientation}`, { active }]"
  />
</template>

<style scoped>
.resize-handle {
  position: absolute;
  z-index: 10;
  touch-action: none;
  background: transparent;
  transition: background-color 0.2s ease;
}
.resize-handle.is-horizontal {
  top: 0;
  left: 0;
  right: 0;
  height: 4px;
  cursor: row-resize;
}
.resize-handle.is-vertical {
  top: 0;
  right: 0;
  bottom: 0;
  width: 4px;
  cursor: col-resize;
}
.resize-handle:hover,
.resize-handle.active {
  background-color: var(--catdb-accent-soft);
}
.resize-handle::after {
  content: '';
  position: absolute;
  border-radius: 1px;
  background: transparent;
  transition: background-color 0.2s ease;
}
.resize-handle.is-horizontal::after {
  top: 1px;
  left: 50%;
  transform: translateX(-50%);
  width: 32px;
  height: 2px;
}
.resize-handle.is-vertical::after {
  left: 1px;
  top: 50%;
  transform: translateY(-50%);
  width: 2px;
  height: 32px;
}
.resize-handle:hover::after,
.resize-handle.active::after {
  background: var(--catdb-accent);
}
</style>
