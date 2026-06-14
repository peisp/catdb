<script setup lang="ts">
// SqlEditor — thin CodeMirror 6 wrapper. Each instance is independent so
// tab state never bleeds between editors (MVP.md M2).
//
// M3 additions: metadata-driven autocomplete via the `schema` option on
// @codemirror/lang-sql. The schema map is built by the parent (Workspace /
// QueryTab) from the metadata store and passed in as a prop. A Compartment
// lets us swap the schema without rebuilding the editor.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EditorState, Compartment } from '@codemirror/state'
import { EditorView, keymap, lineNumbers, highlightActiveLine } from '@codemirror/view'
import { sql, MySQL, type SQLConfig } from '@codemirror/lang-sql'
import { oneDark } from '@codemirror/theme-one-dark'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { useThemeStore } from '../stores/theme'

const props = defineProps<{
  modelValue: string
  /** When the user hits Cmd/Ctrl+Enter the parent runs the query. */
  onRun?: () => void
  /** Map of tableName -> column names. Powers schemaCompletionSource. */
  schema?: Record<string, string[]>
  /** Default schema name (e.g. current database). */
  defaultSchema?: string
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const theme = useThemeStore()

const host = ref<HTMLDivElement | null>(null)
const view = ref<EditorView | null>(null)
const themeCompartment = new Compartment()
const sqlCompartment = new Compartment()

function buildSqlExt() {
  const cfg: SQLConfig = {
    dialect: MySQL,
    upperCaseKeywords: true,
  }
  if (props.schema && Object.keys(props.schema).length) {
    cfg.schema = props.schema
  }
  if (props.defaultSchema) {
    cfg.defaultSchema = props.defaultSchema
  }
  return sql(cfg)
}

function makeState(initial: string) {
  return EditorState.create({
    doc: initial,
    extensions: [
      lineNumbers(),
      history(),
      highlightActiveLine(),
      keymap.of([
        ...defaultKeymap,
        ...historyKeymap,
        indentWithTab,
        {
          key: 'Mod-Enter',
          run: () => {
            props.onRun?.()
            return true
          },
        },
      ]),
      sqlCompartment.of(buildSqlExt()),
      themeCompartment.of(theme.mode === 'dark' ? oneDark : []),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          emit('update:modelValue', update.state.doc.toString())
        }
      }),
      EditorView.theme(
        {
          '&': { height: '100%', fontSize: '13px' },
          '.cm-scroller': {
            fontFamily:
              'ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace',
          },
          '.cm-gutters': { background: 'transparent', border: 'none' },
        },
        { dark: theme.mode === 'dark' },
      ),
    ],
  })
}

onMounted(() => {
  if (!host.value) return
  view.value = new EditorView({
    state: makeState(props.modelValue ?? ''),
    parent: host.value,
  })
})

onBeforeUnmount(() => {
  view.value?.destroy()
})

watch(
  () => props.modelValue,
  (v) => {
    if (!view.value) return
    const cur = view.value.state.doc.toString()
    if (v !== cur) {
      view.value.dispatch({
        changes: { from: 0, to: cur.length, insert: v ?? '' },
      })
    }
  },
)

watch(
  () => theme.mode,
  (m) => {
    if (!view.value) return
    view.value.dispatch({
      effects: themeCompartment.reconfigure(m === 'dark' ? oneDark : []),
    })
  },
)

watch(
  () => [props.schema, props.defaultSchema],
  () => {
    if (!view.value) return
    view.value.dispatch({
      effects: sqlCompartment.reconfigure(buildSqlExt()),
    })
  },
  { deep: true },
)

defineExpose({
  focus() { view.value?.focus() },
  selectionText(): string {
    const v = view.value
    if (!v) return ''
    const { from, to } = v.state.selection.main
    if (from === to) return ''
    return v.state.sliceDoc(from, to)
  },
})

const containerClass = computed(() => 'sql-editor ' + (theme.mode === 'dark' ? 'dark' : 'light'))
</script>

<template>
  <div :class="containerClass">
    <div ref="host" class="cm-host" />
  </div>
</template>

<style scoped>
/* Editor box fills the slot the parent gives. width: 100% is intentionally
   absent — `flex: 1 1 auto` from the parent provides growth, and
   `min-width: 0` lets it shrink to the slot. Long SQL lines scroll inside
   .cm-host, NOT by widening this container. */
.sql-editor {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  min-height: 0;
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  overflow: hidden;
}
.cm-host { flex: 1 1 auto; min-width: 0; min-height: 0; overflow: auto; }
.sql-editor.light { background: #fdfdfd; }
.sql-editor.dark { background: #1e1e1e; }
</style>
