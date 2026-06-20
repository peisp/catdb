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
import { sql, MySQL } from '@codemirror/lang-sql'
import { defaultHighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { useThemeStore } from '../../stores/theme'
import {
  buildAlterDb,
  buildCreateDb,
  loadCharsetsAndCollations,
  loadDbInfo,
  type CharsetInfo,
  type CollationInfo,
} from '../../api/dbEditor'
import { runQuery } from '../../api/query'
import { system as systemApi } from '../../api'

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

const title = computed(() => (mode.value === 'create' ? '新建数据库' : `编辑数据库 — ${initialDbName.value}`))
const okText = computed(() => (mode.value === 'create' ? '创建' : '保存'))

// --- form state -------------------------------------------------------------

const name = ref('')
const charset = ref('')
const collation = ref('')
const loading = ref(true)
const submitting = ref(false)
const errorMessage = ref<string | null>(null)
const loadError = ref<string | null>(null)

const charsetList = ref<CharsetInfo[]>([])
const collationList = ref<CollationInfo[]>([])
const origCharset = ref('')
const origCollation = ref('')

const charsetOptions = computed(() =>
  charsetList.value.map((c) => ({ label: c.name, value: c.name })),
)

const collationOptions = computed(() => {
  if (!charset.value) return []
  return collationList.value
    .filter((c) => c.charset === charset.value)
    .map((c) => ({ label: c.name, value: c.name }))
})

const ddlPreview = computed(() => {
  const n = name.value.trim()
  if (!n) return '-- 请输入数据库名称'
  if (mode.value === 'create') {
    return buildCreateDb(n, charset.value, collation.value) + ';'
  }
  if (charset.value === origCharset.value && collation.value === origCollation.value) {
    return '-- 未修改'
  }
  return buildAlterDb(n, charset.value, collation.value) + ';'
})

const canSubmit = computed(() => {
  if (submitting.value || loading.value) return false
  const n = name.value.trim()
  if (!n) return false
  if (mode.value === 'edit') {
    return charset.value !== origCharset.value || collation.value !== origCollation.value
  }
  return true
})

// --- CodeMirror DDL preview -------------------------------------------------
//
// Read-only EditorView with the MySQL dialect so the preview gets syntax
// highlighting AND remains selectable (the previous <pre> block had the right
// CSS but a styled <pre> can still be overridden by ancestor user-select rules
// — CodeMirror manages its own selection layer and is the project standard,
// per AlterSqlPanel.vue).
const cmHost = ref<HTMLDivElement | null>(null)
let cmView: EditorView | null = null
const cmThemeComp = new Compartment()

function initCm() {
  if (!cmHost.value) return
  cmView = new EditorView({
    state: EditorState.create({
      doc: ddlPreview.value,
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

// When charset changes, snap collation to the new charset's default if the
// current pick doesn't belong to it — matches MySQL server behavior.
watch(charset, (cs, prev) => {
  if (cs === prev) return
  if (!cs) { collation.value = ''; return }
  const belongs = collationList.value.some((c) => c.charset === cs && c.name === collation.value)
  if (belongs) return
  const cInfo = charsetList.value.find((c) => c.name === cs)
  collation.value = cInfo?.defaultCollation ?? ''
})

// --- lifecycle --------------------------------------------------------------

onMounted(async () => {
  const { connId: cid, db } = parseHashQuery()
  if (!cid) {
    loadError.value = '缺少连接参数'
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
    const cs = await loadCharsetsAndCollations(cid)
    charsetList.value = cs.charsets
    collationList.value = cs.collations
    if (mode.value === 'edit') {
      const info = await loadDbInfo(cid, db!)
      if (info) {
        origCharset.value = info.charset
        origCollation.value = info.collation
        charset.value = info.charset
        collation.value = info.collation
      }
    } else {
      const def = cs.charsets.find((c) => c.name === 'utf8mb4')
      if (def) {
        charset.value = def.name
        collation.value = def.defaultCollation
      }
    }
  } catch (e: any) {
    loadError.value = `加载字符集失败: ${e?.message ?? e}`
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
    errorMessage.value = '数据库名称不能为空'
    return
  }
  if (/[`\s.]/.test(n)) {
    errorMessage.value = '数据库名称不能包含空格、点或反引号'
    return
  }
  const sql = mode.value === 'create'
    ? buildCreateDb(n, charset.value, collation.value)
    : buildAlterDb(n, charset.value, collation.value)
  submitting.value = true
  errorMessage.value = null
  try {
    await runQuery(connId.value, sql)
    try {
      await systemApi.broadcastDatabaseSaved(connId.value, n)
    } catch (e) {
      // Non-fatal — main window can refresh manually.
      console.warn('database:saved broadcast failed', e)
    }
    message.success(mode.value === 'create' ? `已创建 ${n}` : `已更新 ${n}`)
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
        <button type="button" class="win-btn win-btn-min" title="最小化" @click="onWindowCtrl('min')">
          <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="0" y="4.5" width="10" height="1" fill="currentColor" /></svg>
        </button>
        <button type="button" class="win-btn win-btn-max" :title="isMaximised ? '还原' : '最大化'" @click="onWindowCtrl('max')">
          <svg v-if="isMaximised" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1.5" y="3.5" width="6" height="6" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
            <path d="M3.5 3.5V2A0.5 0.5 0 0 1 4 1.5h4A0.5 0.5 0 0 1 8.5 2v4a0.5 0.5 0 0 1-.5.5H7.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
          <svg v-else viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1" y="1" width="8" height="8" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
        </button>
        <button type="button" class="win-btn win-btn-close" title="关闭" @click="onWindowCtrl('close')">
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    </header>

    <main class="body">
      <div v-if="loading" class="loading">
        <n-spin size="small" />
        <span>加载中…</span>
      </div>
      <div v-else-if="loadError" class="error">{{ loadError }}</div>
      <div v-else class="content">
        <div class="form">
          <div class="row">
            <label class="lbl">数据库名称</label>
            <n-input
              v-model:value="name"
              size="small"
              :disabled="mode === 'edit'"
              placeholder="例如 my_app"
            />
          </div>
          <div class="row">
            <label class="lbl">字符集</label>
            <n-select
              v-model:value="charset"
              size="small"
              filterable
              clearable
              :options="charsetOptions"
              placeholder="选择字符集"
            />
          </div>
          <div class="row">
            <label class="lbl">排序规则</label>
            <n-select
              v-model:value="collation"
              size="small"
              filterable
              clearable
              :options="collationOptions"
              :disabled="!charset"
              placeholder="选择排序规则"
            />
          </div>
        </div>

        <div class="ddl">
          <div class="ddl-head">SQL 预览</div>
          <div ref="cmHost" class="ddl-body" />
        </div>

        <div v-if="errorMessage" class="err">{{ errorMessage }}</div>
      </div>

      <footer class="actions">
        <n-button size="small" :disabled="submitting" @click="onCancel">取消</n-button>
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
