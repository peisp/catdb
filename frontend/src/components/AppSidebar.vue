<script setup lang="ts">
// AppSidebar — left sidebar pane. When a connection is active it shows the
// connection list (top) + object tree (bottom) in a vertical split; otherwise
// just the connection list fills the pane.
import { NSplit } from 'naive-ui'
import ConnectionSidebar from './ConnectionSidebar.vue'
import ObjectTree from './ObjectTree.vue'
import type { ConnectionProfile, DriverInfo } from '../api/connections'

defineProps<{ activeConn: ConnectionProfile | null }>()

const emit = defineEmits<{
  (e: 'select', conn: ConnectionProfile): void
  (e: 'new', driver: DriverInfo): void
  (e: 'edit', conn: ConnectionProfile): void
  (e: 'openData', payload: { db: string; table: string }): void
  (e: 'openStructure', payload: { db: string; table: string }): void
}>()
</script>

<template>
  <aside class="sider">
    <n-split
      v-if="activeConn"
      direction="vertical"
      :max="0.7"
      :min="0.2"
      :default-size="0.4"
      class="sider-split"
    >
      <template #1>
        <ConnectionSidebar
          @select="(c) => emit('select', c)"
          @new="(d) => emit('new', d)"
          @edit="(c) => emit('edit', c)"
        />
      </template>
      <template #2>
        <ObjectTree
          :connection="activeConn"
          @open-data="(p) => emit('openData', p)"
          @open-structure="(p) => emit('openStructure', p)"
        />
      </template>
    </n-split>
    <ConnectionSidebar
      v-else
      @select="(c) => emit('select', c)"
      @new="(d) => emit('new', d)"
      @edit="(c) => emit('edit', c)"
    />
  </aside>
</template>

<style scoped>
.sider {
  flex: 0 0 280px;
  width: 280px;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  border-right: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  background: var(--n-color);
  display: flex;
  flex-direction: column;
}
.sider > * { flex: 1 1 0; min-width: 0; min-height: 0; }

.sider-split { height: 100%; min-height: 0; }
.sider-split :deep(.n-split-pane) { overflow: hidden; min-width: 0; min-height: 0; }
</style>
