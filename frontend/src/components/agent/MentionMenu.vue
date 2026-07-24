<script setup lang="ts">
// MentionMenu — the @table completion popover (§10.3), shared by the composer
// and the edit-resend box. Positioned above the input; the parent wrapper must
// be position:relative. mousedown.prevent keeps the textarea focused on pick.
defineProps<{ items: string[]; activeIndex: number; loading?: boolean }>()
defineEmits<{ (e: 'choose', name: string): void; (e: 'hover', i: number): void }>()
</script>

<template>
  <div class="mention-menu">
    <div v-if="items.length === 0 && loading" class="mention-empty">{{ $t('agent.mention.loading') }}</div>
    <div v-else-if="items.length === 0" class="mention-empty">{{ $t('agent.mention.empty') }}</div>
    <button
      v-for="(t, i) in items"
      :key="t"
      type="button"
      class="mention-item"
      :class="{ active: i === activeIndex }"
      @mousedown.prevent="$emit('choose', t)"
      @mousemove="$emit('hover', i)"
    >{{ t }}</button>
  </div>
</template>

<style scoped>
/* Completion popover (menu panel style, DESIGN.md). */
.mention-menu {
  position: absolute;
  left: 0;
  right: 0;
  bottom: calc(100% + 4px);
  z-index: 20;
  max-height: 200px;
  overflow-y: auto;
  background: var(--catdb-surface-raised);
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-md);
  box-shadow: var(--catdb-shadow-menu);
  padding: 4px;
}
.mention-empty {
  padding: 6px 8px;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
  text-align: center;
}
.mention-item {
  display: block;
  width: 100%;
  text-align: left;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  height: 24px;
  padding: 0 8px;
  border-radius: var(--catdb-rounded-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;
}
.mention-item.active { background: var(--catdb-accent); color: var(--catdb-text-on-accent); }
</style>
