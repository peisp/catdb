<script setup lang="ts">
// WorkspaceTabBar — flat desktop-style tab strip (DataGrip/dbx-like) that
// replaced n-tabs' nav. Owns tab activation, close (button / middle-click),
// drag-reorder, wheel scrolling with a hover-only overlay scrollbar, the
// native tab context menu, and the "+" new-query button. Panes are rendered
// by QueryWorkspace; this component only touches the query store.
import { computed, h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NDropdown, useThemeVars } from 'naive-ui'
import type { QueryTab as QueryTabInfo, TabKind } from '../../stores/query'
import { useQueryStore } from '../../stores/query'
import { setActiveTabContext } from '../../api/tabContextMenu'
import AppIcon from '../shared/AppIcon.vue'
import databaseIcon from '../../assets/icons/database.svg?raw'
import table2Icon from '../../assets/icons/table-2.svg?raw'
import squareDashedKanbanIcon from '../../assets/icons/square-dashed-kanban.svg?raw'
import tableOfContentsIcon from '../../assets/icons/table-of-contents.svg?raw'
import xIcon from '../../assets/icons/x.svg?raw'
import plusIcon from '../../assets/icons/plus.svg?raw'
import chevronDownIcon from '../../assets/icons/chevron-down.svg?raw'

// Tab icons mirror the object-tree node icons so a tab reads as the same
// object kind as the node that opened it. structure/new-table use the column
// (table-of-contents) glyph to read as "schema", distinct from data browse.
const TAB_ICONS: Record<TabKind, string> = {
  query: squareDashedKanbanIcon,
  table: table2Icon,
  structure: tableOfContentsIcon,
  'new-table': tableOfContentsIcon,
  'tables-overview': databaseIcon,
}

// Stored titles still carry an emoji prefix (used verbatim by dialogs); the
// AppIcon replaces it visually, so strip the leading glyph for display only.
const TITLE_EMOJI_RE = /^(?:📝|⊞|⚙|✚|📋)️?\s*/u
function tabTitle(title: string): string {
  return title.replace(TITLE_EMOJI_RE, '')
}

const props = defineProps<{ connId: string }>()
const store = useQueryStore()
const themeVars = useThemeVars()

const tabs = computed(() => store.tabsForConn(props.connId))
const activeId = computed(() => store.activeTab(props.connId)?.id ?? '')
const accentStyle = computed(() => ({ '--tab-accent': themeVars.value.primaryColor }))

const rootRef = ref<HTMLElement | null>(null)
const scrollRef = ref<HTMLElement | null>(null)

function addTab() {
  const n = tabs.value.filter((t) => t.kind === 'query').length + 1
  store.addTab(props.connId, { title: `Query ${n}`, kind: 'query' })
}

function close(t: QueryTabInfo) {
  if (!t.pinned) void store.closeTab(t.id)
}

// --- drag to reorder (drop-indicator style: commit on mouseup) ---
const drag = ref({ id: null as string | null, started: false, startX: 0, targetId: null as string | null, before: false })

function onTabMouseDown(e: MouseEvent, t: QueryTabInfo) {
  store.setActive(props.connId, t.id)
  if (t.pinned) return
  drag.value = { id: t.id, started: false, startX: e.clientX, targetId: null, before: false }
  window.addEventListener('mouseup', endDrag)
}

function onTabMouseMove(e: MouseEvent, t: QueryTabInfo) {
  const d = drag.value
  if (!d.id) return
  if (!d.started && Math.abs(e.clientX - d.startX) < 4) return
  d.started = true
  if (t.id === d.id || t.pinned) {
    d.targetId = null
    return
  }
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  d.targetId = t.id
  d.before = e.clientX < r.left + r.width / 2
}

function onTabMouseLeave(t: QueryTabInfo) {
  if (drag.value.targetId === t.id) drag.value.targetId = null
}

function endDrag() {
  const d = drag.value
  if (d.id && d.started && d.targetId) store.moveTab(d.id, d.targetId, d.before)
  drag.value = { id: null, started: false, startX: 0, targetId: null, before: false }
  window.removeEventListener('mouseup', endDrag)
}

function dropStyle(id: string) {
  const d = drag.value
  if (!d.started || d.targetId !== id) return undefined
  return { boxShadow: d.before ? 'inset 2px 0 0 0 var(--tab-accent)' : 'inset -2px 0 0 0 var(--tab-accent)' }
}

// --- overlay scrollbar (native one would claim layout height in the strip) ---
const thumb = ref({ show: false, left: 0, width: 100 })

function updateThumb() {
  const el = scrollRef.value
  if (!el) return
  thumb.value = {
    show: el.scrollWidth > el.clientWidth + 1,
    left: (el.scrollLeft / el.scrollWidth) * 100,
    width: (el.clientWidth / el.scrollWidth) * 100,
  }
}

function onWheel(e: WheelEvent) {
  const el = scrollRef.value
  if (!el || el.scrollWidth <= el.clientWidth) return
  if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
    el.scrollLeft += e.deltaY
    e.preventDefault()
  }
}

function onTrackPointerDown(e: PointerEvent) {
  const track = e.currentTarget as HTMLElement
  track.setPointerCapture(e.pointerId)
  const seek = (ev: PointerEvent) => {
    const el = scrollRef.value
    if (!el) return
    const r = track.getBoundingClientRect()
    el.scrollLeft = ((ev.clientX - r.left) / r.width) * el.scrollWidth - el.clientWidth / 2
  }
  seek(e)
  const up = () => {
    track.removeEventListener('pointermove', seek)
    track.removeEventListener('pointerup', up)
  }
  track.addEventListener('pointermove', seek)
  track.addEventListener('pointerup', up)
}

// --- overflow dropdown: lists tabs not (fully) visible in the scroll viewport ---
const hiddenTabs = ref<QueryTabInfo[]>([])

function onOverflowShow(show: boolean) {
  if (!show) return
  const el = scrollRef.value
  if (!el) {
    hiddenTabs.value = []
    return
  }
  const box = el.getBoundingClientRect()
  const hidden = new Set<string>()
  el.querySelectorAll<HTMLElement>('[data-tab-id]').forEach((n) => {
    const r = n.getBoundingClientRect()
    const visible = Math.min(r.right, box.right) - Math.max(r.left, box.left)
    if (visible < r.width - 1) hidden.add(n.dataset.tabId!)
  })
  hiddenTabs.value = tabs.value.filter((t) => hidden.has(t.id))
}

const overflowOptions = computed(() =>
  hiddenTabs.value.map((t) => ({
    key: t.id,
    label: tabTitle(t.title),
    icon: () => h(AppIcon, { src: TAB_ICONS[t.kind], size: 13 }),
  })),
)

function onOverflowSelect(key: string) {
  store.setActive(props.connId, key)
}

let ro: ResizeObserver | null = null
onMounted(() => {
  ro = new ResizeObserver(updateThumb)
  if (scrollRef.value) ro.observe(scrollRef.value)
  updateThumb()
})
onBeforeUnmount(() => {
  ro?.disconnect()
  window.removeEventListener('mouseup', endDrag)
})
watch(() => tabs.value.length, () => nextTick(updateThumb))

// Keep the active tab visible when activation comes from elsewhere (object
// tree opening a table, ctx-menu close, …).
watch(activeId, () => {
  nextTick(() => {
    scrollRef.value?.querySelector('[data-active]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    updateThumb()
  })
})

// --- 原生右键菜单 ---
function openCtx(t: QueryTabInfo) {
  // 固定（pinned）的 tab 不展示右键菜单 —— 不可关闭。
  if (t.pinned) {
    rootRef.value?.style.removeProperty('--custom-contextmenu')
    return
  }

  setActiveTabContext(t.id, t.connId)

  // 在「可关闭」tab 集合内判定位置（忽略固定 tab）
  const closable = store.tabsForConn(t.connId).filter((x) => !x.pinned)
  const idx = closable.findIndex((x) => x.id === t.id)
  let menuName = 'catdb-tab'
  if (closable.length <= 1) {
    menuName = 'catdb-tab-only'
  } else if (idx <= 0) {
    menuName = 'catdb-tab-first'
  } else if (idx >= closable.length - 1) {
    menuName = 'catdb-tab-last'
  }
  rootRef.value?.style.setProperty('--custom-contextmenu', menuName)
}
</script>

<template>
  <div ref="rootRef" class="tabbar" :style="accentStyle" role="tablist">
    <div class="tabbar-strip">
    <div ref="scrollRef" class="tabbar-scroll" @scroll="updateThumb" @wheel="onWheel" @dblclick.self="addTab">
      <div
        v-for="t in tabs"
        :key="t.id"
        class="tab"
        :class="{ active: t.id === activeId, pinned: t.pinned, dragging: drag.started && drag.id === t.id }"
        :style="dropStyle(t.id)"
        :data-tab-id="t.id"
        :data-active="t.id === activeId || undefined"
        role="tab"
        tabindex="0"
        :aria-selected="t.id === activeId"
        :title="tabTitle(t.title)"
        @mousedown.left="onTabMouseDown($event, t)"
        @mouseup.middle="close(t)"
        @mousemove="onTabMouseMove($event, t)"
        @mouseleave="onTabMouseLeave(t)"
        @keydown.enter.prevent="store.setActive(connId, t.id)"
        @keydown.space.prevent="store.setActive(connId, t.id)"
        @contextmenu.prevent="openCtx(t)"
      >
        <AppIcon :src="TAB_ICONS[t.kind]" :size="13" />
        <span class="tab-text">{{ tabTitle(t.title) }}</span>
        <span v-if="!t.pinned" class="tab-tail" :class="{ dirty: store.isQueryDirty(t) }">
          <span class="tab-dot" aria-hidden="true" />
          <button class="tab-close" :aria-label="$t('common.close')" @click.stop="close(t)" @mousedown.stop>
            <AppIcon :src="xIcon" :size="12" />
          </button>
        </span>
      </div>
      <div class="tab-fill" @dblclick="addTab" />
    </div>
    <div v-if="thumb.show" class="tab-scrollbar" @pointerdown="onTrackPointerDown">
      <div class="tab-scrollbar-thumb" :style="{ left: thumb.left + '%', width: thumb.width + '%' }" />
    </div>
    </div>
    <div class="tabbar-controls">
      <n-dropdown
        v-if="thumb.show"
        trigger="click"
        placement="bottom-end"
        :options="overflowOptions"
        @select="onOverflowSelect"
        @update:show="onOverflowShow"
      >
        <button class="tab-btn" :title="$t('tabBar.hiddenTabs')" :aria-label="$t('tabBar.hiddenTabs')">
          <AppIcon :src="chevronDownIcon" :size="13" />
        </button>
      </n-dropdown>
      <button class="tab-btn" :title="$t('tabBar.newQuery')" :aria-label="$t('tabBar.newQuery')" @click="addTab">
        <AppIcon :src="plusIcon" :size="13" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.tabbar {
  position: relative;
  display: flex;
  height: 32px;
  flex: 0 0 auto;
  min-width: 0;
  border-bottom: 1px solid var(--n-border-color);
  background: rgba(0, 0, 0, 0.04);
}
@media (prefers-color-scheme: dark) {
  .tabbar {
    background: rgba(255, 255, 255, 0.04);
  }
}

.tabbar-strip {
  position: relative;
  display: flex;
  flex: 1 1 0;
  min-width: 0;
}

.tabbar-scroll {
  display: flex;
  flex: 1 1 0;
  min-width: 0;
  align-items: stretch;
  overflow-x: auto;
  scrollbar-width: none;
}
.tabbar-scroll::-webkit-scrollbar {
  display: none;
}

.tab {
  position: relative;
  display: flex;
  align-items: center;
  gap: 5px;
  flex: 0 0 auto;
  min-width: 90px;
  max-width: 200px;
  padding: 0 6px 0 9px;
  font-size: 12px;
  white-space: nowrap;
  border-right: 1px solid var(--n-border-color);
  outline: none;
}
.tab.pinned {
  min-width: 0;
  padding-right: 10px;
}
.tab:not(.active) .tab-text {
  opacity: 0.68;
}
.tab:not(.active):hover .tab-text {
  opacity: 0.9;
}
.tab.active {
  background: var(--app-content-bg);
}
/* Accent underline on the active tab (dbx classic layout). */
.tab.active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 2px;
  background: var(--tab-accent);
}
.tab.dragging {
  opacity: 0.45;
}
.tab:focus-visible {
  box-shadow: inset 0 0 0 1px var(--tab-accent);
}

.tab-text {
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
  flex: 1 1 auto;
}

/* Tail slot: close button at rest; when the tab is dirty a dot takes its
   place until hover reveals the close button (VS Code semantics). */
.tab-tail {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  flex: 0 0 auto;
}
.tab-dot {
  display: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
  opacity: 0.55;
}
.tab-close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: inherit;
  opacity: 0.55;
}
.tab-close:hover {
  opacity: 1;
  background: rgba(127, 127, 127, 0.25);
}
.tab-tail.dirty .tab-dot {
  display: block;
}
.tab-tail.dirty .tab-close {
  display: none;
}
.tab:hover .tab-tail.dirty .tab-dot {
  display: none;
}
.tab:hover .tab-tail.dirty .tab-close {
  display: flex;
}

/* Fixed controls on the right: overflow-tab dropdown + new-query, never
   scroll with the strip. */
.tabbar-controls {
  display: flex;
  flex: 0 0 auto;
  align-items: stretch;
  border-left: 1px solid var(--n-border-color);
}
.tab-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  flex: 0 0 auto;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  opacity: 0.6;
}
.tab-btn:hover {
  opacity: 1;
  background: rgba(127, 127, 127, 0.15);
}

.tab-fill {
  flex: 1 1 24px;
  min-width: 24px;
}

/* Hover-only overlay scrollbar — the native bar would claim layout height. */
.tab-scrollbar {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 6px;
  z-index: 5;
  opacity: 0;
  pointer-events: none;
  touch-action: none;
  transition: opacity 120ms ease;
}
.tabbar-strip:hover .tab-scrollbar {
  opacity: 1;
  pointer-events: auto;
}
.tab-scrollbar-thumb {
  position: absolute;
  bottom: 1px;
  height: 3px;
  min-width: 20px;
  border-radius: 999px;
  background: rgba(127, 127, 127, 0.45);
}
.tab-scrollbar:hover .tab-scrollbar-thumb {
  background: rgba(127, 127, 127, 0.65);
}
</style>
