<script setup lang="ts">
// ConnectionSidebar — grouped list of saved connections with native
// right-click context menus:
//   * row             → connect / disconnect / edit / delete (catdb-connection)
//   * group label     → 新建分组 / 重命名 / 删除 (catdb-sidebar-group)
//   * blank area      → 新建分组 (catdb-sidebar-empty)
//
// 新建分组 opens an inline draft input at the bottom of the group list
// (see beginNewGroup / commitNewGroup) — no PromptOverlay roundtrip.
//
// Connections can be drag-and-dropped between groups. The drop target is the
// group's body region (including the row list); dropping into 未分组 detaches
// the connection (group_id NULL).
import { computed, nextTick, onMounted, ref } from 'vue'
import {
  NScrollbar,
  NSpin,
  useMessage,
} from 'naive-ui'
import type { ConnectionProfile } from '../../api/connections'
import { useConnectionsStore } from '../../stores/connections'
import { setActiveConnectionContext } from '../../api/connectionContextMenu'
import { setActiveGroupContext } from '../../api/sidebarContextMenu'
import { t } from '../../i18n'
import AppIcon from '../shared/AppIcon.vue'
import databaseZapIcon from '../../assets/icons/database-zap.svg?raw'

const emit = defineEmits<{
  (e: 'select', conn: ConnectionProfile): void
  (e: 'edit', conn: ConnectionProfile): void
}>()

const store = useConnectionsStore()
const message = useMessage()

// Windows frameless: no title bar offset, content starts at the very top.
const isWin = !navigator.platform.includes('Mac')

const UNGROUPED = '__ungrouped__'

const grouped = computed(() => {
  const byGroup = new Map<string, ConnectionProfile[]>()
  byGroup.set(UNGROUPED, [])
  for (const g of store.groups) byGroup.set(g.id, [])
  for (const c of store.connections) {
    const key = c.groupId && byGroup.has(c.groupId) ? c.groupId : UNGROUPED
    byGroup.get(key)!.push(c)
  }
  return Array.from(byGroup.entries()).map(([id, items]) => ({
    id,
    label: id === UNGROUPED ? t('connectionSidebar.ungrouped') : store.groups.find((g) => g.id === id)?.name ?? id,
    items,
  }))
})

const sidebarRef = ref<HTMLElement | null>(null)

// --- native context menus -------------------------------------------------
// Wails v3 reads `--custom-contextmenu` (via getComputedStyle) from the
// element under the cursor in its own window-level contextmenu listener.
// We set the property as a static inline style on each element (root for
// blank area, group-label for groups, .row for connections). The JS handler
// only pushes the identity into the singleton — it must NOT call
// stopPropagation, otherwise the event never reaches Wails' handler on window.
// A flag prevents the root handler from clearing the context when a child
// element's handler already fired during bubbling.

let childHandledContext = false

function onRowCtx(ev: MouseEvent, conn: ConnectionProfile) {
  ev.preventDefault()
  childHandledContext = true
  setActiveConnectionContext({ connId: conn.id, connName: conn.name })
}

function onGroupCtx(ev: MouseEvent, group: { id: string; label: string; items: ConnectionProfile[] }) {
  ev.preventDefault()
  childHandledContext = true
  // 未分组 is a synthetic bucket — only "新建分组" makes sense for it.
  if (group.id === UNGROUPED) {
    setActiveGroupContext(null)
    return
  }
  setActiveGroupContext({ groupId: group.id, groupName: group.label })
}

function onBlankCtx() {
  if (childHandledContext) {
    // A child (group-label or row) already handled this event — don't
    // override the context it set.
    childHandledContext = false
    return
  }
  setActiveGroupContext(null)
}

// --- inline new-group input ----------------------------------------------
// Right-click → 新建分组 dispatches `sb:new-group`; the sidebar renders a
// pseudo-group row at the end of the list with an autofocused input. The
// commit policy matches Finder/Navicat:
//   * Enter or blur with non-empty text → save the group
//   * Esc or blur with empty text       → cancel silently (no save)
// Saving while another save is in flight is debounced via `savingNewGroup`
// to avoid double-create when blur fires right after Enter.

const newGroupName = ref<string | null>(null)
const newGroupInputRef = ref<HTMLInputElement | null>(null)
const savingNewGroup = ref(false)

function beginNewGroup() {
  if (newGroupName.value !== null) {
    // Already editing — re-focus the existing input instead of stacking
    // multiple draft rows.
    void nextTick(() => newGroupInputRef.value?.focus())
    return
  }
  newGroupName.value = ''
  void nextTick(() => {
    newGroupInputRef.value?.focus()
  })
}

async function commitNewGroup() {
  if (newGroupName.value === null) return
  if (savingNewGroup.value) return
  const name = newGroupName.value.trim()
  if (!name) {
    // Empty → drop the draft silently, matching the user's brief
    // ("没有就不保存"). No toast — the disappearing input is feedback enough.
    newGroupName.value = null
    return
  }
  if (store.groups.some((g) => g.name === name)) {
    // Name collisions are quiet: keep the input open and surface a toast so
    // the user can correct without losing what they typed.
    message.warning(t('connectionSidebar.groupExists', { name }))
    void nextTick(() => newGroupInputRef.value?.focus())
    return
  }
  savingNewGroup.value = true
  try {
    await store.saveGroup({ name })
    newGroupName.value = null
  } catch (e) {
    message.error(t('connectionSidebar.createFailed', { error: String(e) }))
  } finally {
    savingNewGroup.value = false
  }
}

function cancelNewGroup() {
  newGroupName.value = null
}

// --- drag-and-drop --------------------------------------------------------
// Source: connection row. Target: group element (header + body). dragOverId
// drives the highlight ring on the group currently under the cursor.

const draggingId = ref<string | null>(null)
const dragOverId = ref<string | null>(null)

function onDragStart(ev: DragEvent, conn: ConnectionProfile) {
  draggingId.value = conn.id
  if (ev.dataTransfer) {
    // text/plain payload makes the drag robust across browser quirks; the
    // identity we actually trust is draggingId (set above), since text/plain
    // could theoretically be hijacked.
    ev.dataTransfer.setData('text/plain', conn.id)
    ev.dataTransfer.effectAllowed = 'move'
  }
}

function onDragEnd() {
  draggingId.value = null
  dragOverId.value = null
}

function onDragOver(ev: DragEvent, groupId: string) {
  if (!draggingId.value) return
  // Suppressing default is what makes the element a valid drop target.
  ev.preventDefault()
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move'
  dragOverId.value = groupId
}

function onDragLeave(_ev: DragEvent, groupId: string) {
  // Only clear the highlight if we're leaving the very element we're tracking.
  // Children firing dragleave shouldn't kill the parent's highlight.
  if (dragOverId.value === groupId) dragOverId.value = null
}

async function onDrop(ev: DragEvent, groupId: string) {
  ev.preventDefault()
  const id = draggingId.value
  draggingId.value = null
  dragOverId.value = null
  if (!id) return
  const target = groupId === UNGROUPED ? '' : groupId
  const conn = store.connections.find((c) => c.id === id)
  if (!conn) return
  const currentGroup = conn.groupId || ''
  if (currentGroup === target) return
  try {
    await store.moveConnection(id, target)
  } catch (e) {
    message.error(t('connectionSidebar.moveFailed', { error: String(e) }))
  }
}

// --- row actions ----------------------------------------------------------

async function onDoubleClick(conn: ConnectionProfile) {
  if (store.isLive(conn.id)) {
    emit('select', conn)
    return
  }
  try {
    await store.connect(conn.id)
    emit('select', conn)
  } catch (e) {
    message.error(t('common.connectFailed', { error: String(e) }))
  }
}

onMounted(() => {
  void store.refreshAll()
  // Listen for edit events from the native context menu handler.
  document.addEventListener('conn:edit', ((e: CustomEvent<string>) => {
    const conn = store.connections.find((c) => c.id === e.detail)
    if (conn) emit('edit', conn)
  }) as EventListener)
  // Listen for connect-then-select events from the native context menu.
  document.addEventListener('conn:select', ((e: CustomEvent<string>) => {
    const conn = store.connections.find((c) => c.id === e.detail)
    if (conn) emit('select', conn)
  }) as EventListener)
  // The sidebar's 新建分组 context-menu item dispatches `sb:new-group` instead
  // of opening an in-app prompt — we render an inline draft row instead.
  document.addEventListener('sb:new-group', beginNewGroup as EventListener)
})
</script>

<template>
  <!-- Root captures the blank-area right-click so any spot of the sidebar not
       covered by a row or group label still surfaces the "新建分组" menu. -->
  <!-- Root carries --custom-contextmenu for blank-area right-clicks. Wails
       reads computed style from the target element up through ancestors, so
       child elements (group-label, .row) override this with their own inline
       value. -->
  <div
    ref="sidebarRef"
    class="sidebar"
    :class="{ win: isWin }"
    style="--custom-contextmenu: catdb-sidebar-empty"
    @contextmenu.prevent="onBlankCtx"
  >
    <div class="header">
      <span class="title">{{ $t('connectionSidebar.title') }}</span>
    </div>
    <n-scrollbar class="list">
      <n-spin :show="store.loading">
        <div
          v-for="g in grouped"
          :key="g.id"
          class="group"
          :class="{ 'drag-over': dragOverId === g.id }"
          :style="{ '--custom-contextmenu': g.id === UNGROUPED ? 'catdb-sidebar-empty' : 'catdb-sidebar-group' }"
          @contextmenu="onGroupCtx($event, g)"
          @dragover="onDragOver($event, g.id)"
          @dragleave="onDragLeave($event, g.id)"
          @drop="onDrop($event, g.id)"
        >
          <div class="group-label">{{ g.label }}</div>
          <div v-if="g.items.length === 0" class="group-empty">{{ $t('connectionSidebar.empty') }}</div>
          <div
              v-for="c in g.items"
              :key="c.id"
              class="row clickable"
              :class="{ dragging: draggingId === c.id }"
              draggable="true"
              style="--custom-contextmenu: catdb-connection"
              @dragstart="onDragStart($event, c)"
              @dragend="onDragEnd"
              @dblclick="onDoubleClick(c)"
              @contextmenu="onRowCtx($event, c)"
          >
            <span class="dot" :class="{ live: store.isLive(c.id) }" />
            <AppIcon :src="databaseZapIcon" />
            <span class="row-name">{{ c.name }}</span>
            <span class="row-driver mono">{{ c.driver }}</span>
          </div>
        </div>

        <!-- Inline new-group draft row. Sits at the bottom of the list so
             it appears where a freshly created group will land (groups
             sort by sort_order, name — appending visually matches that). -->
        <div v-if="newGroupName !== null" class="group new-group-draft">
          <div class="group-label new-group-label">
            <input
              ref="newGroupInputRef"
              v-model="newGroupName"
              type="text"
              class="new-group-input"
              :placeholder="$t('connectionSidebar.groupNamePlaceholder')"
              autocomplete="off"
              spellcheck="false"
              @keydown.enter.prevent="commitNewGroup"
              @keydown.esc.prevent="cancelNewGroup"
              @blur="commitNewGroup"
            />
          </div>
        </div>
      </n-spin>
    </n-scrollbar>
  </div>
</template>

<style scoped>
.sidebar { display: flex; flex-direction: column; height: 100%; }
.header {
  --wails-draggable: drag;
  display: flex;
  align-items: center;
  padding: 6px 10px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  opacity: 0.7;
}
.title { font-size: 11px; }
.list { flex: 1 1 auto; }
.group {
  padding: 4px 0;
  position: relative;
  /* Inset hairline used as the drag-over indicator. Keep this transparent
     so the layout never shifts when the ring appears. */
  box-shadow: inset 0 0 0 1px transparent;
  border-radius: 4px;
  transition: background 80ms ease, box-shadow 80ms ease;
}
.group.drag-over {
  background: rgba(24, 160, 88, 0.08);
  box-shadow: inset 0 0 0 1px rgba(24, 160, 88, 0.45);
}
.group-label {
  font-size: 11px;
  padding: 4px 10px 2px;
  opacity: 0.55;
  cursor: default;
}
.group-empty {
  padding: 2px 10px 6px 22px;
  font-size: 11px;
  opacity: 0.4;
}

/* Inline new-group draft row — sized to occupy the same vertical band as a
   group label so the appearance doesn't shift the rest of the list. The
   input is borderless to read as part of the sidebar; a subtle baseline
   underline marks it as editable. */
.new-group-label { padding: 2px 8px; opacity: 1; }
.new-group-input {
  width: 100%;
  padding: 2px 4px;
  font: inherit;
  font-size: 11px;
  color: inherit;
  background: transparent;
  border: none;
  border-bottom: 1px solid var(--n-border-color-focus, #18a058);
  outline: none;
}
.new-group-input::placeholder { opacity: 0.4; }
.row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px 4px 14px;
  font-size: 12px;
  height: 24px;
  cursor: default;
}
.row:hover { background: var(--n-color-target); }
.row.dragging { opacity: 0.4; }
.row-name { flex: 1 1 auto; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row-driver { font-size: 10px; opacity: 0.5; }
.dot {
  width: 6px; height: 6px; border-radius: 3px;
  background: rgba(127,127,127,0.4);
  flex: 0 0 auto;
}
.dot.live { background: #18a058; }

/* Windows frameless: no top padding on header so content starts flush. */
.sidebar.win .header { padding-top: 18px; }
</style>
