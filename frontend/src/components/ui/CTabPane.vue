<script setup lang="ts">
// CTabPane — 配合 CTabs。内容惰性渲染(选中才挂载,切走即卸载)。
import { computed, inject, onBeforeUnmount } from 'vue'
import { C_TABS, type TabsContext } from './tabs'

interface Props {
  name: string
  tab: string
}

const props = defineProps<Props>()

const ctx = inject<TabsContext | null>(C_TABS, null)
ctx?.register({ name: props.name, tab: props.tab })
onBeforeUnmount(() => ctx?.unregister(props.name))

const active = computed(() => ctx?.active.value === props.name)
</script>

<template>
  <div v-if="active" class="c-tab-pane" role="tabpanel">
    <slot />
  </div>
</template>

<style scoped>
.c-tab-pane {
  min-width: 0;
  min-height: 0;
}
</style>
