<script setup lang="ts">
// CTabs — 分段控件形态的 tab 轨(DESIGN.md segmented control:玻璃轨 +
// 选中段 content 底)。配合 CTabPane(name/tab),对齐 n-tabs 用法子集。
import { provide, reactive } from 'vue'
import { C_TABS, type TabPaneInfo, type TabsContext } from './tabs'

const model = defineModel<string | null>('value', { default: null })

const panes = reactive<TabPaneInfo[]>([])

const ctx: TabsContext = {
  active: model,
  register(info) {
    if (!panes.some((p) => p.name === info.name)) panes.push(info)
    if (model.value === null) model.value = info.name
  },
  unregister(name) {
    const i = panes.findIndex((p) => p.name === name)
    if (i >= 0) panes.splice(i, 1)
  },
}
provide(C_TABS, ctx)
</script>

<template>
  <div class="c-tabs">
    <div class="railbar">
    <div class="rail" role="tablist">
      <button
        v-for="p in panes"
        :key="p.name"
        type="button"
        class="seg"
        :class="{ on: p.name === model }"
        role="tab"
        :aria-selected="p.name === model"
        @click="model = p.name"
      >
        {{ p.tab }}
      </button>
    </div>
    </div>
    <div class="body">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.c-tabs {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}
/* railbar:轨道的通栏容器,默认只做居中;视图可 :deep 覆写成 view bar 带
   (chrome 底 + 底边 hairline,见 TableStructure)。 */
.railbar {
  display: flex;
  justify-content: center;
  flex: none;
  margin-bottom: var(--catdb-space-md);
}
.rail {
  display: inline-flex;
  align-items: center;
  gap: 1px;
  height: 26px;
  padding: 2px;
  border-radius: 7px;
  background: var(--catdb-hover-fill);
}
.seg {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 12px;
  border: none;
  border-radius: var(--catdb-rounded-sm);
  background: transparent;
  font-family: var(--catdb-font-family);
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
  user-select: none;
  cursor: default;
  transition: background-color 130ms ease-out;
}
.seg.on {
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  font-weight: 600;
  box-shadow: 0 0 0 0.5px var(--catdb-separator), 0 1px 2.5px rgba(0, 0, 0, 0.14);
}
.seg:focus-visible {
  outline: none;
  box-shadow: var(--catdb-focus-ring);
}
.body {
  min-height: 0;
  min-width: 0;
  flex: 1;
}
</style>
