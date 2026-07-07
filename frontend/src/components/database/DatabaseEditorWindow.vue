<script setup lang="ts">
// DatabaseEditorWindow — root view of the "新建/编辑数据库" child window.
// Mirrors ConnectionEditorWindow: titlebar with native traffic-light spacing
// on macOS / custom caption buttons on Windows, then the form, then the
// action bar pinned to the bottom.
//
// Lifecycle:
//   1. Parse connId + optional db out of the hash query string.
//   2. Load charsets + collations once (cached per-conn). In edit mode also
//      load the existing DB's charset/collation.
//   3. User edits; live DDL preview shows what will run.
//   4. On submit: runQuery the DDL, broadcast `database:saved` to all windows
//      so the main shell refreshes its ObjectTree, then close the window.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Window } from '@wailsio/runtime'
import { NButton, NInput, NSelect, NSpin, useMessage } from 'naive-ui'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { sql } from '@codemirror/lang-sql'
import { defaultHighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { useThemeStore } from '../../stores/theme'
import {
  buildAlterDb,
  buildCreateDb,
  loadDbOptionFields,
  loadDbInfo,
  type DatabaseOptionField,
  type DatabaseOptionValues,
} from '../../api/dbEditor'
import { genericUIDialect, uiDialectForConnection, type UIDialect } from '../../api/dialect'
import { cmSqlDialect } from '../../editor/cmDialect'
import { runQuery } from '../../api/query'
import { system as systemApi } from '../../api'
import { t, i18n } from '../../i18n'

const message = useMessage()
const themeStore = useThemeStore()

const isMac = navigator.platform.includes('Mac')
const isWin = !isMac

const isMaximised = ref(false)
async function onWindowCtrl(cmd: 'min' | 'max' | 'close') {
  if (cmd === 'min') { await Window.Minimise(); return }
  if (cmd === 'close') { await Window.Close(); return }
  await Window.ToggleMaximise()
  isMaximised.value = await Window.IsMaximised()
}
function toggleMaximise() { void Window.ToggleMaximise() }

// --- params -----------------------------------------------------------------

function parseHashQuery(): { connId?: string; db?: string } {
  const h = window.location.hash || ''
  const qIdx = h.indexOf('?')
  if (qIdx < 0) return {}
  const params = new URLSearchParams(h.slice(qIdx + 1))
  return {
    connId: params.get('connId') ?? undefined,
    db: params.get('db') ?? undefined,
  }
}

const connId = ref('')
const mode = ref<'create' | 'edit'>('create')
const initialDbName = ref('')

const title = computed(() =>
  mode.value === 'create'
    ? t('databaseEditor.titleCreate')
    : t('databaseEditor.titleEdit', { name: initialDbName.value }),
)
const okText = computed(() => (mode.value === 'create' ? t('databaseEditor.create') : t('common.save')))

// --- form state -------------------------------------------------------------
//
// The option form is driver-described (DatabaseOptionField[]): every field is
// a select; choices may depend on another field's value (MySQL: collation
// depends on charset). Values live in one flat map keyed by field key.

const name = ref('')
const loading = ref(true)
const submitting = ref(false)
const errorMessage = ref<string | null>(null)
const loadError = ref<string | null>(null)

const fields = ref<DatabaseOptionField[]>([])
const values = ref<DatabaseOptionValues>({})
const origValues = ref<DatabaseOptionValues>({})

const dialect = ref<UIDialect>(genericUIDialect())

// Field labels localize by key (databaseEditor.field.*), falling back to the
// driver's English baseline — same contract as the connection form.
function fieldLabel(f: DatabaseOptionField): string {
  const key = `databaseEditor.field.${f.key}`
  return i18n.global.te(key) ? (i18n.global.t(key) as string) : f.label
}

function optionsFor(f: DatabaseOptionField) {
  const list = f.dependsOn
    ? (f.optionsBy?.[values.value[f.dependsOn] ?? ''] ?? [])
    : (f.options ?? [])
  return list.map((o) => ({ label: o, value: o }))
}

function fieldDisabled(f: DatabaseOptionField): boolean {
  if (mode.value === 'edit' && f.fixedOnAlter) return true
  if (f.dependsOn && !values.value[f.dependsOn]) return true
  return false
}

// Keep dependent fields consistent: when the parent changes, snap the child
// to the parent's default if the current pick no longer belongs (the MySQL
// charset → default collation behavior, generalized).
watch(
  values,
  () => {
    for (const f of fields.value) {
      if (!f.dependsOn) continue
      const parent = values.value[f.dependsOn] ?? ''
      const cur = values.value[f.key] ?? ''
      if (!parent) {
        if (cur) values.value[f.key] = ''
        continue
      }
      const opts = f.optionsBy?.[parent] ?? []
      if (cur && opts.includes(cur)) continue
      const def = f.defaultBy?.[parent] ?? ''
      if (cur !== def) values.value[f.key] = def
    }
  },
  { deep: true },
)

// The changed subset (edit mode) — this is exactly what AlterDatabaseSQL gets.
const changedValues = computed<DatabaseOptionValues>(() => {
  const out: DatabaseOptionValues = {}
  for (const f of fields.value) {
    const cur = values.value[f.key] ?? ''
    if (cur !== (origValues.value[f.key] ?? '')) out[f.key] = cur
  }
  return out
})

// DDL rendering happens driver-side (MetadataService.BuildCreate/AlterDatabase)
// so the preview is async — refreshed by a debounced, sequence-guarded watcher.
const ddlPreview = ref('')
let ddlSeq = 0
let ddlTimer: ReturnType<typeof setTimeout> | undefined

async function refreshDdlPreview() {
  const seq = ++ddlSeq
  const n = name.value.trim()
  if (!n) {
    ddlPreview.value = `-- ${t('databaseEditor.ddlEnterName')}`
    return
  }
  if (mode.value === 'edit' && Object.keys(changedValues.value).length === 0) {
    ddlPreview.value = `-- ${t('databaseEditor.ddlUnchanged')}`
    return
  }
  try {
    const stmt = mode.value === 'create'
      ? await buildCreateDb(connId.value, n, { ...values.value })
      : await buildAlterDb(connId.value, n, changedValues.value)
    if (seq === ddlSeq) ddlPreview.value = stmt.endsWith(';') ? stmt : stmt + ';'
  } catch (e) {
    if (seq === ddlSeq) ddlPreview.value = `-- ${String(e)}`
  }
}

watch(
  [name, values, mode],
  () => {
    if (ddlTimer) clearTimeout(ddlTimer)
    ddlTimer = setTimeout(() => void refreshDdlPreview(), 150)
  },
  { deep: true },
)

const canSubmit = computed(() => {
  if (submitting.value || loading.value) return false
  const n = name.value.trim()
  if (!n) return false
  if (mode.value === 'edit') {
    return Object.keys(changedValues.value).length > 0
  }
  return true
})

// --- CodeMirror DDL preview -------------------------------------------------
//
// Read-only EditorView with the connection driver's SQL dialect so the
// preview gets syntax highlighting AND remains selectable (the previous <pre>
// block had the right CSS but a styled <pre> can still be overridden by
// ancestor user-select rules — CodeMirror manages its own selection layer and
// is the project standard, per AlterSqlPanel.vue).
const cmHost = ref<HTMLDivElement | null>(null)
let cmView: EditorView | null = null
const cmThemeComp = new Compartment()

function initCm() {
  if (!cmHost.value) return
  cmView = new EditorView({
    state: EditorState.create({
      doc: ddlPreview.value,
      extensions: [
        sql({ dialect: cmSqlDialect(dialect.value.editorDialect) }),
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
        cmThemeComp.of(themeStore.mode === 'dark' ? oneDark : []),
      ],
    }),
    parent: cmHost.value,
  })
}

// The host div is gated by v-if (visible only after loading completes), so the
// ref appears late. Watching the ref + tearing down any prior view keeps a
// single live editor — see AlterSqlPanel.vue for the same pattern.
watch(cmHost, (el) => {
  if (cmView) { cmView.destroy(); cmView = null }
  if (el) initCm()
})
watch(ddlPreview, (val) => {
  if (!cmView) return
  const cur = cmView.state.doc.toString()
  if (val !== cur) {
    cmView.dispatch({ changes: { from: 0, to: cur.length, insert: val } })
  }
})
watch(
  () => themeStore.mode,
  (mode) => {
    if (!cmView) return
    cmView.dispatch({ effects: cmThemeComp.reconfigure(mode === 'dark' ? oneDark : []) })
  },
)
onBeforeUnmount(() => { cmView?.destroy(); cmView = null })

// --- lifecycle --------------------------------------------------------------

onMounted(async () => {
  const { connId: cid, db } = parseHashQuery()
  if (!cid) {
    loadError.value = t('databaseEditor.missingConnParam')
    loading.value = false
    return
  }
  connId.value = cid
  if (db) {
    mode.value = 'edit'
    initialDbName.value = db
    name.value = db
  } else {
    mode.value = 'create'
  }
  try {
    dialect.value = await uiDialectForConnection(cid)
    fields.value = await loadDbOptionFields(cid)
    if (mode.value === 'edit') {
      const info = await loadDbInfo(cid, db!)
      if (info) {
        origValues.value = { ...info }
        values.value = { ...info }
      }
    } else {
      const init: DatabaseOptionValues = {}
      for (const f of fields.value) {
        init[f.key] = f.default ?? ''
      }
      values.value = init
    }
    void refreshDdlPreview()
  } catch (e: any) {
    loadError.value = t('databaseEditor.loadOptionsFailed', { error: e?.message ?? e })
  } finally {
    loading.value = false
  }
})

// --- actions ----------------------------------------------------------------

function onCancel() {
  if (submitting.value) return
  void Window.Close()
}

async function onConfirm() {
  const n = name.value.trim()
  if (!n) {
    errorMessage.value = t('databaseEditor.nameRequired')
    return
  }
  if (/[`"\s.]/.test(n)) {
    errorMessage.value = t('databaseEditor.nameInvalidChars')
    return
  }
  submitting.value = true
  errorMessage.value = null
  try {
    const sql = mode.value === 'create'
      ? await buildCreateDb(connId.value, n, { ...values.value })
      : await buildAlterDb(connId.value, n, changedValues.value)
    await runQuery(connId.value, sql)
    try {
      await systemApi.broadcastDatabaseSaved(connId.value, n)
    } catch (e) {
      // Non-fatal — main window can refresh manually.
      console.warn('database:saved broadcast failed', e)
    }
    message.success(
      mode.value === 'create'
        ? t('databaseEditor.created', { name: n })
        : t('databaseEditor.updated', { name: n }),
    )
    setTimeout(() => { void Window.Close() }, 200)
  } catch (e: any) {
    errorMessage.value = e?.message ?? String(e)
    submitting.value = false
  }
}
</script>

<template>
  <div class="root">
    <header class="titlebar" :class="{ win: isWin }" @dblclick="toggleMaximise">
      <span class="title">{{ title }}</span>
      <div v-if="isWin" class="window-controls">
        <button type="button" class="win-btn win-btn-min" :title="$t('databaseEditor.minimize')" @click="onWindowCtrl('min')">
          <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="0" y="4.5" width="10" height="1" fill="currentColor" /></svg>
        </button>
        <button type="button" class="win-btn win-btn-max" :title="isMaximised ? $t('databaseEditor.restore') : $t('databaseEditor.maximize')" @click="onWindowCtrl('max')">
          <svg v-if="isMaximised" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1.5" y="3.5" width="6" height="6" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
            <path d="M3.5 3.5V2A0.5 0.5 0 0 1 4 1.5h4A0.5 0.5 0 0 1 8.5 2v4a0.5 0.5 0 0 1-.5.5H7.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
          <svg v-else viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1" y="1" width="8" height="8" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
        </button>
        <button type="button" class="win-btn win-btn-close" :title="$t('common.close')" @click="onWindowCtrl('close')">
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    </header>

    <main class="body">
      <div v-if="loading" class="loading">
        <n-spin size="small" />
        <span>{{ $t('databaseEditor.loading') }}</span>
      </div>
      <div v-else-if="loadError" class="error">{{ loadError }}</div>
      <div v-else class="content">
        <div class="form">
          <div class="row">
            <label class="lbl">{{ $t('databaseEditor.dbName') }}</label>
            <n-input
              v-model:value="name"
              size="small"
              :disabled="mode === 'edit'"
              :placeholder="$t('databaseEditor.dbNamePlaceholder')"
            />
          </div>
          <div v-for="f in fields" :key="f.key" class="row">
            <label class="lbl">{{ fieldLabel(f) }}</label>
            <n-select
              :value="values[f.key] || null"
              size="small"
              filterable
              clearable
              :options="optionsFor(f)"
              :disabled="fieldDisabled(f)"
              :placeholder="$t('databaseEditor.optionPlaceholder')"
              @update:value="values[f.key] = $event ?? ''"
            />
          </div>
        </div>

        <div class="ddl">
          <div class="ddl-head">{{ $t('databaseEditor.sqlPreview') }}</div>
          <div ref="cmHost" class="ddl-body" />
        </div>

        <div v-if="errorMessage" class="err">{{ errorMessage }}</div>
      </div>

      <footer class="actions">
        <n-button size="small" :disabled="submitting" @click="onCancel">{{ $t('common.cancel') }}</n-button>
        <n-button
          size="small"
          type="primary"
          :loading="submitting"
          :disabled="!canSubmit"
          @click="onConfirm"
        >{{ okText }}</n-button>
      </footer>
    </main>
  </div>
</template>

<style scoped>
.root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  width: 100vw;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--n-color);
}

.titlebar {
  position: relative;
  flex: 0 0 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.2px;
  opacity: 0.85;
  --wails-draggable: drag;
}
.titlebar .title { padding-left: 60px; padding-right: 12px; }
.titlebar.win .title { padding-left: 150px; padding-right: 150px; }

.titlebar .window-controls {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 10;
  display: flex;
  flex-direction: row;
  align-items: stretch;
  height: 100%;
  -webkit-app-region: no-drag;
}
.titlebar .win-btn {
  --wails-draggable: no-drag;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  padding: 0;
  margin: 0;
  border: none;
  border-radius: 0;
  font: inherit;
  color: inherit;
  cursor: default;
  background: transparent;
  transition: background 80ms ease;
}
.titlebar .win-btn svg { width: 14px; height: 14px; opacity: 0.75; }
.titlebar .win-btn:hover { background: rgba(127, 127, 127, 0.15); }
.titlebar .win-btn:active { background: rgba(127, 127, 127, 0.25); }
.titlebar .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
.titlebar .win-btn-close:hover svg { opacity: 1; }
.titlebar .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
.titlebar .win-btn-close:active svg { opacity: 1; }
@media (prefers-color-scheme: dark) {
  .titlebar .win-btn:hover { background: rgba(255, 255, 255, 0.1); }
  .titlebar .win-btn:active { background: rgba(255, 255, 255, 0.16); }
}

.body {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.loading,
.error {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 20px;
  font-size: 13px;
  opacity: 0.8;
}
.error { color: var(--n-error-color, #d03050); }

.content {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 22px 8px;
  overflow: auto;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.row {
  display: grid;
  grid-template-columns: 90px 1fr;
  align-items: center;
  gap: 12px;
}

.lbl {
  font-size: 12px;
  opacity: 0.75;
  text-align: right;
}

.ddl {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 4px;
}
.ddl-head {
  font-size: 11px;
  opacity: 0.6;
}
.ddl-body {
  background: rgba(127, 127, 127, 0.08);
  border: 1px solid var(--n-border-color, rgba(127,127,127,0.18));
  border-radius: 4px;
  height: 120px;
  min-height: 80px;
  overflow: hidden;
  user-select: text;
  -webkit-user-select: text;
}
.ddl-body :deep(.cm-editor) { height: 100%; background: transparent; }
.ddl-body :deep(.cm-content) { padding: 8px 10px; }
.ddl-body :deep(.cm-gutters) { display: none; }

.err {
  font-size: 12px;
  color: #d03050;
  word-break: break-all;
}

.actions {
  flex: 0 0 auto;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 10px 22px 14px;
  border-top: 1px solid var(--n-border-color, rgba(127,127,127,0.18));
  background: var(--n-color);
}
</style>
