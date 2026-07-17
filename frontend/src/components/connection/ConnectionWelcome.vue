<script setup lang="ts">
// ConnectionWelcome — empty-state for the main pane while no connection is
// active. Keeps the desktop-spec density (DESIGN.md) — no big hero card.
import { computed } from 'vue'
import { NButton, NSpace } from 'naive-ui'
import { useConnectionsStore } from '../../stores/connections'

const emit = defineEmits<{ (e: 'new'): void }>()
const store = useConnectionsStore()

const hasConnections = computed(() => store.connections.length > 0)
</script>

<template>
  <div class="welcome">
    <h2>catdb</h2>
    <p class="hint">
      <template v-if="hasConnections">
        {{ $t('connectionWelcome.hintHasConnections') }}
      </template>
      <template v-else>
        {{ $t('connectionWelcome.hintEmpty') }}
      </template>
    </p>
    <n-space :size="8">
      <n-button size="small" type="primary" @click="emit('new')">
        {{ $t('connectionWelcome.newConnection') }}
      </n-button>
    </n-space>
  </div>
</template>

<style scoped>
.welcome {
  padding: 12px 18px 10px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 520px;
}
.welcome h2 { margin: 0; font-size: var(--catdb-fs-title); font-weight: 600; }
.hint { font-size: var(--catdb-fs-small); opacity: 0.7; margin: 0; line-height: 1.5; }
</style>
