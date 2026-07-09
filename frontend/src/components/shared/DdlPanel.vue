<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { NSpin, useMessage } from 'naive-ui'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { sql } from '@codemirror/lang-sql'
import { syntaxHighlighting, defaultHighlightStyle } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { useThemeStore } from '../../stores/theme'
import { genericUIDialect, type UIDialect } from '../../api/dialect'
import { cmSqlDialect } from '../../editor/cmDialect'
import { t } from '../../i18n'
import ResizeHandle from '../shared/ResizeHandle.vue'

const props = withDefaults(defineProps<{
  ddl: string
  loading?: boolean
  table?: string | null
  variant?: 'panel' | 'tab'
  active?: boolean
  width?: number
  /** Driver UI descriptor — picks the syntax-highlighting SQL dialect. */
  dialect?: UIDialect
}>(), {
  loading: false,
  table: null,
  variant: 'panel',
  active: false,
  width: 360,
  dialect: undefined,
})

const emit = defineEmits<{
  close: []
}>()

const message = useMessage()
const themeStore = useThemeStore()

// ---- CodeMirror ----
const ddlHost = ref<HTMLDivElement | null>(null)
const ddlView = ref<EditorView | null>(null)
const ddlThemeComp = new Compartment()

function initDdlEditor() {
  if (!ddlHost.value) return
  ddlView.value = new EditorView({
    state: EditorState.create({
      doc: props.ddl,
      extensions: [
        sql({ dialect: cmSqlDialect((props.dialect ?? genericUIDialect()).editorDialect) }),
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
        EditorView.lineWrapping,
        EditorView.theme({
          '&': { height: '100%', fontSize: '12px' },
          '.cm-scroller': {
            fontFamily:
              'ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace',
            overflow: 'auto',
          },
        }),
        ddlThemeComp.of(themeStore.mode === 'dark' ? oneDark : []),
      ],
    }),
    parent: ddlHost.value,
  })
}

watch(ddlHost, (el) => {
  if (el && !ddlView.value) {
    initDdlEditor()
  } else if (!el && ddlView.value) {
    ddlView.value.destroy()
    ddlView.value = null
  }
})

watch(() => props.ddl, (val) => {
  if (!ddlView.value) return
  const cur = ddlView.value.state.doc.toString()
  if (val !== cur) {
    ddlView.value.dispatch({
      changes: { from: 0, to: cur.length, insert: val ?? '' },
    })
  }
})

watch(() => themeStore.mode, (mode) => {
  if (!ddlView.value) return
  ddlView.value.dispatch({
    effects: ddlThemeComp.reconfigure(mode === 'dark' ? oneDark : []),
  })
})

onBeforeUnmount(() => ddlView.value?.destroy())

// ---- Copy ----
async function copyDdl() {
  if (!props.ddl) return
  try {
    await navigator.clipboard.writeText(props.ddl)
    message.success(t('tablesOverview.ddlCopied'))
  } catch (e) {
    message.error(t('common.copyFailed', { error: String(e) }))
  }
}

// ---- Resize (panel only) ----
const MIN_PANEL_W = 240
const MAX_PANEL_W = 640
const panelWidth = ref(props.width)
const resizing = ref(false)
let dragStartX = 0
let dragStartW = 0

function onResizePointerDown(ev: PointerEvent) {
  if (ev.button !== 0) return
  resizing.value = true
  dragStartX = ev.clientX
  dragStartW = panelWidth.value
  ;(ev.currentTarget as HTMLElement).setPointerCapture(ev.pointerId)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onResizePointerMove(ev: PointerEvent) {
  if (!resizing.value) return
  const raw = dragStartW + (dragStartX - ev.clientX)
  panelWidth.value = Math.max(MIN_PANEL_W, Math.min(MAX_PANEL_W, raw))
}

function onResizePointerUp() {
  if (!resizing.value) return
  resizing.value = false
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
}
</script>

<template>
  <!-- Panel variant (side-panel with header, resize, copy) -->
  <aside
    v-if="variant === 'panel' && active"
    class="ddl-panel"
    :style="{ width: panelWidth + 'px', flexBasis: panelWidth + 'px' }"
  >
    <ResizeHandle
      orientation="vertical"
      class="ddl-resize"
      :active="resizing"
      @pointerdown="onResizePointerDown"
      @pointermove="onResizePointerMove"
      @pointerup="onResizePointerUp"
      @pointercancel="onResizePointerUp"
    />
    <div class="ddl-head">
      <span class="ddl-title mono">{{ table || $t('tablesOverview.ddlTitle') }}</span>
      <div class="ddl-head-actions">
        <button v-if="ddl" class="ddl-btn" :title="$t('common.copy')" @click="copyDdl">{{ $t('common.copy') }}</button>
        <button class="ddl-close" :title="$t('common.close')" @click="emit('close')">&times;</button>
      </div>
    </div>
    <n-spin :show="loading" class="ddl-body">
      <div v-if="!table && variant === 'panel'" class="ddl-empty mute">{{ $t('tablesOverview.ddlSelectHint') }}</div>
      <div v-else ref="ddlHost" class="ddl-host" />
    </n-spin>
  </aside>

  <!-- Tab variant (just the editor, no panel chrome) -->
  <div v-if="variant === 'tab'" class="ddl-tab">
    <n-spin :show="loading" class="ddl-body">
      <div ref="ddlHost" class="ddl-host" />
    </n-spin>
  </div>
</template>

<style scoped>
/* ---- panel variant ---- */
.ddl-panel {
  position: relative;
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-left: 1px solid var(--n-border-color);
  background: var(--n-color);
}
.ddl-panel > .ddl-resize.is-vertical { right: auto; left: 0; }
.ddl-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 8px 6px 12px;
  border-bottom: 1px solid var(--n-border-color);
  flex: 0 0 auto;
}
.ddl-title {
  font-size: 12px;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ddl-head-actions { display: flex; align-items: center; gap: 4px; flex: 0 0 auto; }
.ddl-btn {
  height: 20px;
  padding: 0 8px;
  font-size: 11px;
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  background: transparent;
  color: inherit;
  cursor: default;
  transition: background-color 120ms ease;
}
.ddl-btn:hover { background: var(--n-color-target, rgba(127, 127, 127, 0.12)); }
.ddl-close {
  width: 20px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: inherit;
  font-size: 16px;
  line-height: 1;
  cursor: default;
  opacity: 0.6;
  transition: background-color 120ms ease, opacity 120ms ease;
}
.ddl-close:hover { background: var(--n-color-target, rgba(127, 127, 127, 0.12)); opacity: 1; }
.ddl-body { flex: 1 1 auto; min-height: 0; overflow: hidden; }
.ddl-body :deep(.n-spin-container),
.ddl-body :deep(.n-spin-content) { height: 100%; min-height: 0; }
.ddl-host { height: 100%; min-height: 0; overflow: hidden; }
.ddl-host :deep(.cm-content),
.ddl-host :deep(.cm-line) {
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.ddl-empty { padding: 16px 12px; font-size: 12px; text-align: center; }

/* ---- tab variant ---- */
.ddl-tab {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
</style>
