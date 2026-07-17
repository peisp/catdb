<script setup lang="ts">
// SqlEditor — thin CodeMirror 6 wrapper. Each instance is independent so
// tab state never bleeds between editors (MVP.md M2).
//
// Completion: the dbx-style engine in editor/sqlCompletion.ts FULLY owns the
// popup via `autocompletion({ override })` — lang-sql only provides syntax
// highlighting/parsing. The engine reads the `catalog` prop lazily, so no
// reconfigure is needed when metadata loads. `sqlSignatureHelp` adds the
// parameter-hint tooltip inside function calls.
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
import { sql } from '@codemirror/lang-sql'
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
import { colors as tokenColors } from '../../styles/tokens'
import { genericUIDialect, type UIDialect } from '../../api/dialect'
import { cmSqlDialect } from '../../editor/cmDialect'
import {
  createSqlCompletionSource,
  createSqlSignatureHelp,
  type CompletionCatalog,
} from '../../editor/sqlCompletion'
import { t } from '../../i18n'
import { emit as wailsEmit } from '../../api/events'

const props = defineProps<{
  modelValue: string
  /** When the user hits Cmd/Ctrl+Enter the parent runs the query. */
  onRun?: () => void
  /** When the user hits Cmd/Ctrl+S the parent saves the query. */
  onSave?: () => void
  /** Live metadata view for completion (databases / tables / columns). The
   *  engine reads it lazily at completion time — no reconfigure needed. */
  catalog?: CompletionCatalog
  /** The connection driver's UI descriptor — SQL dialect for highlighting,
   *  identifier quoting, function/keyword catalogs for completion. Read
   *  lazily by the completion engine; highlighting reconfigures on change. */
  dialect?: UIDialect
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const theme = useThemeStore()

const EMPTY_CATALOG: CompletionCatalog = {
  databases: () => [],
  currentDb: () => undefined,
  tablesFor: () => null,
  ensureTables: () => Promise.resolve(null),
}

const getDialect = () => props.dialect ?? genericUIDialect()

// Built once; the catalog/dialect closures read the latest state at
// completion time.
const sqlSource = createSqlCompletionSource(
  {
    databases: () => (props.catalog ?? EMPTY_CATALOG).databases(),
    visibleDatabases: () => {
      const c = props.catalog ?? EMPTY_CATALOG
      return c.visibleDatabases?.() ?? c.databases()
    },
    currentDb: () => (props.catalog ?? EMPTY_CATALOG).currentDb(),
    tablesFor: (db) => (props.catalog ?? EMPTY_CATALOG).tablesFor(db),
    ensureTables: (db) => (props.catalog ?? EMPTY_CATALOG).ensureTables(db),
  },
  {
    aliasFor: (table) => t('queryTab.aliasFor', { table }),
    nColumns: (n) => t('queryTab.completionColumns', { n }),
    joinCondition: () => t('queryTab.completionJoinCond'),
  },
  getDialect,
)
const sqlSignatureHelp = createSqlSignatureHelp(getDialect)

const host = ref<HTMLDivElement | null>(null)
const view = ref<EditorView | null>(null)
const themeCompartment = new Compartment()

// Editor chrome (syntax theme + surface background) lives in a compartment so
// it can be swapped on light/dark switch. The background overrides oneDark's
// built-in #282c34 — it's placed AFTER oneDark so its property wins.
function editorChrome(dark: boolean) {
  const bg = tokenColors(dark ? 'dark' : 'light')['surface-content']
  return [
    dark ? oneDark : [],
    EditorView.theme({ '&': { backgroundColor: bg } }, { dark }),
  ]
}

// `sql()` provides syntax highlighting/parsing only — its own completion
// sources never run because `autocompletion({ override })` bypasses all
// language-data sources. The dialect follows the connection's driver and
// reconfigures via a compartment when the descriptor resolves.
const sqlLangCompartment = new Compartment()
function buildSqlExt() {
  return sql({ dialect: cmSqlDialect(getDialect().editorDialect), upperCaseKeywords: true })
}

// Crisp inline-SVG completion icons. CodeMirror's default glyphs for these
// types are literal squares (`property`→□, `namespace`→▢) or math-italic
// letters (`type`→𝑡, `variable`→𝑥) that render as tofu in most system/mono
// fonts. These are drawn as masks tinted with the row's text color, so they
// always render and follow the theme (incl. white on the selected row).
const COMPLETION_ICONS: Record<string, string> = {
  // table
  type: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='black' stroke-width='1.3'><rect x='2.6' y='3.6' width='10.8' height='8.8' rx='1'/><path d='M2.6 6.7h10.8M6.4 6.7v5.7'/></svg>",
  // view
  interface: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='black' stroke-width='1.3'><rect x='2.6' y='3.6' width='10.8' height='8.8' rx='1'/><path d='M2.6 6.5h10.8M2.6 9.4h10.8'/></svg>",
  // column
  property: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='black' stroke-width='1.3'><rect x='5.7' y='2.7' width='4.6' height='10.6' rx='1'/></svg>",
  // database
  namespace: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='black' stroke-width='1.3'><ellipse cx='8' cy='4' rx='4.7' ry='1.7'/><path d='M3.3 4v8c0 .95 2.1 1.7 4.7 1.7s4.7-.75 4.7-1.7V4'/></svg>",
  // function
  function: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='black' stroke-width='1.3' stroke-linecap='round'><path d='M6.2 3.6C4.3 5.2 4.3 10.8 6.2 12.4'/><path d='M9.8 3.6c1.9 1.6 1.9 7.2 0 8.8'/><circle cx='8' cy='8' r='1.15' fill='black' stroke='none'/></svg>",
  // alias
  variable: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='black' stroke-width='1.3' stroke-linejoin='round'><path d='M7.6 2.7H13.3V8.4L7.9 13.8 2.2 8.1z'/><circle cx='10.6' cy='5.4' r='1' fill='black' stroke='none'/></svg>",
  // keyword
  keyword: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><rect x='3.5' y='3.5' width='9' height='9' rx='2.3' fill='black'/></svg>",
  // text
  text: "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='black' stroke-width='1.4' stroke-linecap='round'><path d='M4.2 4.6h7.6M8 4.6v6.8'/></svg>",
}

function completionIconRules(): Record<string, Record<string, string>> {
  const rules: Record<string, Record<string, string>> = {
    '.cm-tooltip-autocomplete .cm-completionIcon': {
      width: '1.2em',
      paddingRight: '0.45em',
      opacity: '0.7',
    },
    '.cm-tooltip-autocomplete .cm-completionIcon::after': {
      content: '""',
      display: 'inline-block',
      width: '1em',
      height: '1em',
      verticalAlign: '-0.14em',
      backgroundColor: 'currentColor',
      '-webkit-mask-repeat': 'no-repeat',
      'mask-repeat': 'no-repeat',
      '-webkit-mask-position': 'center',
      'mask-position': 'center',
      '-webkit-mask-size': 'contain',
      'mask-size': 'contain',
    },
  }
  for (const [type, svg] of Object.entries(COMPLETION_ICONS)) {
    const uri = `url("data:image/svg+xml,${encodeURIComponent(svg)}")`
    rules[`.cm-tooltip-autocomplete .cm-completionIcon-${type}::after`] = {
      '-webkit-mask-image': uri,
      'mask-image': uri,
    }
  }
  return rules
}
const COMPLETION_ICON_RULES = completionIconRules()

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
      // The completion popup, fully driven by our engine. `override` replaces
      // every language-data source (lang-sql contributes highlighting only).
      // activateOnTyping fires the source on EVERY typed character — including
      // `.` (qualified completion) and space (auto-open after FROM/ON/SET) —
      // and the source itself decides when to stay quiet.
      autocompletion({
        override: [sqlSource],
        activateOnTyping: true,
        closeOnBlur: true,
        defaultKeymap: true,
        maxRenderedOptions: 50,
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
      ]),
      sqlLangCompartment.of(buildSqlExt()),
      // Parameter hint tooltip while typing inside a known function call.
      sqlSignatureHelp,
      themeCompartment.of(editorChrome(theme.mode === 'dark')),
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
            backgroundColor: 'var(--catdb-accent)',
            color: 'var(--catdb-text-on-accent)',
          },
          '.cm-sql-signature': {
            fontFamily: 'var(--catdb-font-family-mono)',
            fontSize: '12px',
            padding: '2px 8px',
            borderRadius: '4px',
          },
          '.cm-sql-signature .cm-sql-signature-active': {
            fontWeight: 600,
            color: 'var(--catdb-accent)',
          },
          '.cm-completionLabel': { fontWeight: 500 },
          '.cm-completionDetail': {
            fontStyle: 'normal',
            opacity: 0.55,
            marginLeft: '6px',
            fontSize: '11px',
          },
          ...COMPLETION_ICON_RULES,
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
  const dom = view.value.contentDOM
  if (!dom) return
  // Intercept Cmd/Ctrl+Enter at the DOM capture phase, BEFORE CodeMirror's
  // own internal handlers, so DefaultKeymap's insertBlankLine never fires.
  dom.addEventListener('keydown', (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      e.stopImmediatePropagation()
      props.onRun?.()
      return
    }
    if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S')) {
      e.preventDefault()
      e.stopImmediatePropagation()
      props.onSave?.()
    }
  }, { capture: true })
  // On focus, ask the Go backend to switch to English input source so SQL
  // typing starts in the correct layout. The user can manually switch away;
  // only re-triggered on next focus.
  dom.addEventListener('focus', () => {
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
      effects: themeCompartment.reconfigure(editorChrome(m === 'dark')),
    })
  },
)

// The syntax-highlighting dialect follows the driver descriptor (which
// resolves async on first mount).
watch(
  () => props.dialect,
  () => {
    if (!view.value) return
    view.value.dispatch({ effects: sqlLangCompartment.reconfigure(buildSqlExt()) })
  },
)

// No catalog watch needed: the completion engine reads the catalog closures at
// completion time, so newly-loaded metadata is picked up without reconfiguring.

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
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-xs);
  overflow: hidden;
}
.cm-host { flex: 1 1 auto; min-width: 0; min-height: 0; overflow: auto; }
/* CodeMirror paints its own surface via editorChrome; this only covers the
   mount gap behind it. Uses --catdb-surface-content so it tracks the theme. */
.cm-host, .sql-editor { background: var(--catdb-surface-content); }
</style>
