<script setup lang="ts">
// AppIcon — single place that owns the lucide SVG set + their rendering.
// Icons are bundled as raw strings (currentColor stroke) so they inherit the
// surrounding text color and theme. Size/opacity are uniform here; consumers
// pick an icon by `name` and only override `size` when they need to.
import { computed } from 'vue'
import databaseZap from '../../assets/icons/database-zap.svg?raw'
import database from '../../assets/icons/database.svg?raw'
import table2 from '../../assets/icons/table-2.svg?raw'
import scanEye from '../../assets/icons/scan-eye.svg?raw'
import squareDashedKanban from '../../assets/icons/square-dashed-kanban.svg?raw'

const ICONS = {
  'database-zap': databaseZap,
  database,
  'table-2': table2,
  'scan-eye': scanEye,
  'square-dashed-kanban': squareDashedKanban,
} as const

export type AppIconName = keyof typeof ICONS

const props = withDefaults(defineProps<{ name: AppIconName; size?: number }>(), {
  size: 15,
})

const svg = computed(() => ICONS[props.name] ?? '')
const style = computed(() => ({ '--app-icon-size': `${props.size}px` }))
</script>

<template>
  <span class="app-icon" :style="style" aria-hidden="true" v-html="svg" />
</template>

<style scoped>
.app-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--app-icon-size);
  height: var(--app-icon-size);
  flex: 0 0 auto;
  opacity: 0.62;
}
.app-icon :deep(svg) {
  width: var(--app-icon-size);
  height: var(--app-icon-size);
  display: block;
}
</style>
