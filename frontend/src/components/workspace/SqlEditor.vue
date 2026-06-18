<script setup lang="ts">
// SqlEditor — thin CodeMirror 6 wrapper. Each instance is independent so
// tab state never bleeds between editors (MVP.md M2).
//
// Completion stack:
//   * @codemirror/lang-sql's keyword source (driven by MySQL dialect).
//   * @codemirror/lang-sql's schema source (driven by the `schema` prop —
//     a SQLNamespace describing the catalog: databases / tables / columns).
//   * Our `mysqlExtraCompletions` source (built-in functions + snippets).
//
// The `autocompletion()` extension is what actually paints the popup; without
// it the language-data sources sit registered but invisible.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EditorState, Compartment } from '@codemirror/state'
import {
  EditorView,
  drawSelection,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from '@codemirror/view'
import { sql, MySQL, type SQLConfig, type SQLNamespace } from '@codemirror/lang-sql'
import {
  bracketMatching,
  defaultHighlightStyle,
  foldGutter,
  foldKeymap,
  indentOnInput,
  syntaxHighlighting,
} from '@codemirror/language'
import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
} from '@codemirror/autocomplete'
import { searchKeymap } from '@codemirror/search'
import { oneDark } from '@codemirror/theme-one-dark'
import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from '@codemirror/commands'
import { useThemeStore } from '../../stores/theme'
import { mysqlExtraCompletions } from '../../editor/mysqlCompletions'
import { emit as wailsEmit } from '../../api/events'

const props = defineProps<{
  modelValue: string
  /** When the user hits Cmd/Ctrl+Enter the parent runs the query. */
  onRun?: () => void
  /** Catalog description for schema completion. May be either:
   *    - flat: { tableName: ['col1', 'col2'] }                — single DB
   *    - nested: { dbName: { tableName: ['col1', ...] } }     — multi-DB
   *  Both shapes are valid SQLNamespace inputs to @codemirror/lang-sql. */
  schema?: SQLNamespace
  /** Default schema name (e.g. current database). Tables under it become
   *  completable at the top level. */
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
      highlightActiveLineGutter(),
      highlightSpecialChars(),
      history(),
      foldGutter(),
      drawSelection(),
      EditorState.allowMultipleSelections.of(true),
      indentOnInput(),
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      bracketMatching(),
      closeBrackets(),
      // The autocompletion UI itself — without this the lang-sql completion
      // sources are registered but never rendered.
      autocompletion({
        // Built-in keyword/schema sources fire on every word boundary; the
        // popup auto-shows after the first identifier char. Triggering on
        // `.` is essential for `table.column` completion.
        activateOnTyping: true,
        closeOnBlur: true,
        defaultKeymap: true,
        maxRenderedOptions: 50,
        // Our extra source is layered alongside the language-data sources
        // via language data, but registering it explicitly here means it
        // is also active even if the language's data lookup fails (e.g.
        // when the cursor is in a non-SQL node like a comment edge).
        override: undefined,
      }),
      highlightActiveLine(),
      keymap.of([
        ...defaultKeymap,
        ...historyKeymap,
        ...completionKeymap,
        ...closeBracketsKeymap,
        ...searchKeymap,
        ...foldKeymap,
        indentWithTab,
        {
          key: 'Mod-Enter',
          run: () => {
            props.onRun?.()
            return true
          },
        },
      ]),
      sqlCompartment.of([
        buildSqlExt(),
        // Attach the function/snippet source to the SQL language so it only
        // contributes when the cursor is inside SQL (not, say, in a string
        // literal). language.data.of is the same hook lang-sql uses for
        // its own keyword + schema sources.
        MySQL.language.data.of({ autocomplete: mysqlExtraCompletions }),
      ]),
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
          '.cm-tooltip.cm-tooltip-autocomplete': {
            // Tighter popup — desktop density, not web-form spacing.
            fontSize: '12px',
            borderRadius: '4px',
          },
          '.cm-tooltip.cm-tooltip-autocomplete > ul > li': {
            padding: '2px 8px',
            lineHeight: '18px',
          },
          '.cm-tooltip.cm-tooltip-autocomplete > ul > li[aria-selected]': {
            backgroundColor: 'var(--n-primary-color, #2080f0)',
            color: '#fff',
          },
          '.cm-completionLabel': { fontWeight: 500 },
          '.cm-completionDetail': {
            fontStyle: 'normal',
            opacity: 0.55,
            marginLeft: '6px',
            fontSize: '11px',
          },
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
  // On focus, ask the Go backend to switch to English input source so SQL
  // typing starts in the correct layout. The user can manually switch away;
  // only re-triggered on next focus.
  view.value.contentDOM?.addEventListener('focus', () => {
    void wailsEmit('custom:switch-english-input')
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
      effects: sqlCompartment.reconfigure([
        buildSqlExt(),
        MySQL.language.data.of({ autocomplete: mysqlExtraCompletions }),
      ]),
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
  setDoc(value: string) {
    const v = view.value
    if (!v) return
    const cur = v.state.doc.toString()
    v.dispatch({
      changes: { from: 0, to: cur.length, insert: value },
    })
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
