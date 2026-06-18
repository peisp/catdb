<script setup lang="ts">
// AlterSqlPanel — shared bottom-of-tab SQL preview.
//
// Hosts a read-only CodeMirror with the proposed ALTER TABLE statements, and
// exposes Copy / Apply / Reset actions. The parent owns the draft state and
// passes in the freshly-diffed statements via :statements. Applying just
// shells out to the parent (it knows the connId and how to refresh after).
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NButton, NEmpty, NFlex, NText, useDialog, useMessage } from 'naive-ui'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { sql, MySQL } from '@codemirror/lang-sql'
import { defaultHighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { useThemeStore } from '../../stores/theme'
import ResizeHandle from '../shared/ResizeHandle.vue'

const props = defineProps<{
  /** Generated SQL statements (already terminated with `;`). */
  statements: string[]
  /** Disable Apply when the parent knows the draft is incomplete/invalid. */
  applyDisabled?: boolean
  /** Loading flag from the parent — disables buttons while in-flight. */
  busy?: boolean
  /** Confirmation text shown in the Apply dialog. */
  applyConfirmTitle?: string
  applyConfirmContent?: string
}>()
const emit = defineEmits<{
  (e: 'apply'): void
  (e: 'reset'): void
}>()

const message = useMessage()
const dialog = useDialog()
const themeStore = useThemeStore()

const host = ref<HTMLDivElement | null>(null)
const view = ref<EditorView | null>(null)
const themeComp = new Compartment()

// ---- vertical resize handle ------------------------------------------------
const MIN_PANEL_H = 60
const MAX_PANEL_H = 400
const panelHeight = ref(0)
const dragging = ref(false)
let dragStartY = 0
let dragStartH = 0

onMounted(() => {
  panelHeight.value = Math.round(window.innerHeight * 0.15)
  panelHeight.value = Math.max(MIN_PANEL_H, Math.min(MAX_PANEL_H, panelHeight.value))
})

function onResizePointerDown(ev: PointerEvent) {
  if (ev.button !== 0) return
  dragging.value = true
  dragStartY = ev.clientY
  dragStartH = panelHeight.value
  const el = ev.currentTarget as HTMLDivElement
  el.setPointerCapture(ev.pointerId)
  document.body.style.cursor = 'row-resize'
  document.body.style.userSelect = 'none'
}

function onResizePointerMove(ev: PointerEvent) {
  if (!dragging.value) return
  const raw = dragStartH + (dragStartY - ev.clientY)
  panelHeight.value = Math.max(MIN_PANEL_H, Math.min(MAX_PANEL_H, raw))
}

function onResizePointerUp() {
  if (!dragging.value) return
  dragging.value = false
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
}

const joined = computed(() => props.statements.join('\n'))
const isEmpty = computed(() => props.statements.length === 0)

function init() {
  if (!host.value) return
  view.value = new EditorView({
    state: EditorState.create({
      doc: joined.value,
      extensions: [
        sql({ dialect: MySQL }),
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        EditorView.editable.of(false),
        EditorView.theme({
          '&': { height: '100%', fontSize: '12px' },
          '.cm-scroller': {
            fontFamily:
              'ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace',
            overflow: 'auto',
          },
        }),
        themeComp.of(themeStore.mode === 'dark' ? oneDark : []),
      ],
    }),
    parent: host.value,
  })
}

// The host div is wrapped in v-if (it disappears when there are no changes).
// Each time it remounts, the ref is set to a NEW element — we must tear down
// any prior EditorView before re-initializing, otherwise the new visible div
// has no editor while the orphaned, detached editor silently keeps receiving
// document updates (Copy works but nothing renders). See git log if puzzled.
watch(host, (el) => {
  if (view.value) {
    view.value.destroy()
    view.value = null
  }
  if (el) init()
})
watch(joined, (val) => {
  if (!view.value) return
  const cur = view.value.state.doc.toString()
  if (val !== cur) {
    view.value.dispatch({ changes: { from: 0, to: cur.length, insert: val } })
  }
})
watch(
  () => themeStore.mode,
  (mode) => {
    if (!view.value) return
    view.value.dispatch({
      effects: themeComp.reconfigure(mode === 'dark' ? oneDark : []),
    })
  },
)

onBeforeUnmount(() => view.value?.destroy())

async function onCopy() {
  if (isEmpty.value) return
  try {
    await navigator.clipboard.writeText(joined.value)
    message.success('已复制到剪贴板')
  } catch (e) {
    message.error(`复制失败: ${String(e)}`)
  }
}

function onApply() {
  if (isEmpty.value || props.applyDisabled || props.busy) return
  dialog.warning({
    title: props.applyConfirmTitle ?? '应用结构变更',
    content:
      props.applyConfirmContent ??
      `将执行 ${props.statements.length} 条 ALTER 语句，操作不可撤销。确定继续？`,
    positiveText: '执行',
    negativeText: '取消',
    onPositiveClick: () => emit('apply'),
  })
}

function onReset() {
  if (isEmpty.value || props.busy) return
  emit('reset')
}
</script>

<template>
  <section
    class="alter-panel"
    :style="{ height: panelHeight + 'px' }"
  >
    <ResizeHandle
      orientation="horizontal"
      :active="dragging"
      @pointerdown="onResizePointerDown"
      @pointermove="onResizePointerMove"
      @pointerup="onResizePointerUp"
      @pointercancel="onResizePointerUp"
    />
    <header class="alter-panel-head">
      <n-text depth="3" :class="{ 'has-changes': !isEmpty }">
        <template v-if="isEmpty">未检测到变更</template>
        <template v-else>{{ statements.length }} 条变更语句</template>
      </n-text>
      <n-flex :size="6">
        <n-button
          size="tiny"
          :disabled="isEmpty || busy"
          @click="onReset"
        >
          放弃修改
        </n-button>
        <n-button
          size="tiny"
          :disabled="isEmpty"
          @click="onCopy"
        >
          复制 SQL
        </n-button>
        <n-button
          size="tiny"
          type="primary"
          :disabled="isEmpty || applyDisabled || busy"
          :loading="busy"
          @click="onApply"
        >
          应用
        </n-button>
      </n-flex>
    </header>
    <div v-if="!isEmpty" ref="host" class="alter-panel-cm" />
    <div v-else class="alter-panel-empty">
      <n-empty size="small" description="编辑上方表格后，将在此处显示对应的 ALTER SQL" />
    </div>
  </section>
</template>

<style scoped>
.alter-panel {
  display: flex;
  flex-direction: column;
  flex: 0 0 auto;
  min-height: 0;
  position: relative;
  border-top: 1px solid var(--n-border-color);
  background: var(--n-card-color);
}
.alter-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 8px;
  font-size: 11px;
  border-top: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  border-bottom: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
}
.has-changes :deep(.n-text) {
  color: var(--n-warning-color);
}
.alter-panel-cm {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  user-select: text;
  -webkit-user-select: text;
}
.alter-panel-empty {
  padding: 12px;
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 60px;
}
</style>
