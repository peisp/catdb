<script setup lang="ts">
// AgentTraceWindow — dev-only inspector for the agent's full model
// interaction (route `#/agent-trace[?sess=…]`, spawned via
// AgentTraceService.OpenTraceWindow). Three panes:
//   sessions | timeline of one session's records | detail of one record
// Records come from internal/agenttrace JSONL: the exact ChatRequest the
// model saw (system prompt / message array / tool defs), assembled responses,
// tool executions, approvals, plans, compactions. Built for prompt debugging:
// rendered view + raw JSON + one-click copy of the full request.
import { computed, onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { useMessage } from 'naive-ui'
import * as traceApi from '../../api/agentTrace'
import { copyText } from '../../api/system'
import { confirm } from '../../api/dialogs'
import { t as tr } from '../../i18n'

const message = useMessage()

const isMac = navigator.platform.includes('Mac')
const isWin = !isMac
const isMaximised = ref(false)
async function onWindowCtrl(cmd: 'min' | 'max' | 'close') {
  if (cmd === 'min') { await Window.Minimise(); return }
  if (cmd === 'close') { await Window.Close(); return }
  await Window.ToggleMaximise()
  isMaximised.value = await Window.IsMaximised()
}
function toggleMaximise() {
  void Window.ToggleMaximise()
}

// --- sessions pane ---
const sessions = ref<traceApi.TraceSession[]>([])
const selectedSess = ref('')
const records = ref<traceApi.TraceRecord[]>([])
const selectedIdx = ref(-1)
const loadingList = ref(false)
const loadingTrace = ref(false)

async function refresh() {
  loadingList.value = true
  try {
    sessions.value = await traceApi.listTraceSessions()
    if (selectedSess.value && !sessions.value.some((s) => s.sessionId === selectedSess.value)) {
      selectedSess.value = ''
      records.value = []
      selectedIdx.value = -1
    }
  } catch (err) {
    message.error(String(err))
  } finally {
    loadingList.value = false
  }
}

async function selectSession(id: string) {
  selectedSess.value = id
  selectedIdx.value = -1
  loadingTrace.value = true
  try {
    records.value = await traceApi.getTraceRecords(id)
  } catch (err) {
    records.value = []
    message.error(String(err))
  } finally {
    loadingTrace.value = false
  }
}

async function reloadTrace() {
  if (selectedSess.value) await selectSession(selectedSess.value)
}

async function onClearAll() {
  const choice = await confirm({
    kind: 'warning',
    title: tr('agentTrace.clearTitle'),
    message: tr('agentTrace.clearConfirm'),
    buttons: [
      { value: 'clear', label: tr('agentTrace.clearAll') },
      { value: 'cancel', label: tr('common.cancel'), isCancel: true },
    ],
  })
  if (choice !== 'clear') return
  await traceApi.clearTraces()
  selectedSess.value = ''
  records.value = []
  selectedIdx.value = -1
  await refresh()
}

// The Go side re-points an already-open window at a new `?sess=` via SetURL
// (hash change only, no reload) — mirror SettingsWindow's applyHashSection.
async function applyHashSess() {
  const m = /[?&]sess=([A-Za-z0-9-]+)/.exec(window.location.hash)
  if (m) {
    history.replaceState(null, '', '#/agent-trace')
    if (sessions.value.some((s) => s.sessionId === m[1])) await selectSession(m[1])
  }
}

onMounted(async () => {
  await refresh()
  await applyHashSess()
  window.addEventListener('hashchange', () => void applyHashSess())
})

// --- timeline rows ---
interface Row {
  idx: number
  kind: string
  time: string
  label: string
  summary: string
  error: boolean
  group: boolean
}

const KNOWN_KINDS = new Set([
  'user', 'request', 'response', 'tool', 'approval', 'plan', 'compact', 'repair', 'done', 'error',
])

const rows = computed<Row[]>(() =>
  records.value.map((r, idx) => {
    const d = r.data ?? {}
    return {
      idx,
      kind: r.kind,
      time: fmtTime(r.t),
      label: KNOWN_KINDS.has(r.kind) ? tr(`agentTrace.kind.${r.kind}`) : r.kind,
      summary: summarize(r.kind, d),
      error: isErrorRecord(r.kind, d),
      group: r.kind === 'user',
    }
  }),
)

function summarize(kind: string, d: Record<string, any>): string {
  switch (kind) {
    case 'user':
      return trunc(d.text ?? '', 80)
    case 'request': {
      const purpose = d.purpose === 'compact-summary' ? tr('agentTrace.purposeCompact') : (d.req?.Model ?? '')
      const msgs = d.req?.Messages?.length ?? 0
      const est = d.estTokens ? ` · ~${fmtTokens(d.estTokens)}` : ''
      return `${purpose} · ${tr('agentTrace.nMessages', { n: msgs })}${est}`
    }
    case 'response': {
      if (d.error) return trunc(String(d.error), 80)
      const u = d.usage ?? {}
      const tin = (u.InputTokens ?? 0) + (u.CacheReadTokens ?? 0) + (u.CacheWriteTokens ?? 0)
      return `${d.stop ?? ''} · ${fmtTokens(tin)} → ${fmtTokens(u.OutputTokens ?? 0)} · ${fmtMs(d.durationMs)}`
    }
    case 'tool':
      return `${d.name ?? ''} · ${fmtMs(d.durationMs)}`
    case 'approval':
      return `${d.verb ?? ''} · ${decisionText(d)}`
    case 'plan':
      return `${trunc(d.goal ?? '', 40)} · ${decisionText(d)}`
    case 'compact':
      return `${d.foldedCount ?? 0} · ${fmtTokens(d.before ?? 0)} → ${fmtTokens(d.after ?? 0)}`
    case 'repair':
      return String(d.missing ?? '')
    case 'done':
      return String(d.stopReason ?? '')
    case 'error':
      return trunc(String(d.error ?? ''), 80)
    default:
      return ''
  }
}

function isErrorRecord(kind: string, d: Record<string, any>): boolean {
  if (kind === 'error') return true
  if (kind === 'response' && d.error) return true
  if (kind === 'tool' && d.isError) return true
  if ((kind === 'approval' || kind === 'plan') && d.approved === false) return true
  return false
}

function decisionText(d: Record<string, any>): string {
  if (d.error) return tr('agentTrace.cancelled')
  if (d.approved === true) return tr('agentTrace.approved')
  if (d.approved === false) return tr('agentTrace.rejected')
  return ''
}

// --- detail pane ---
const selected = computed(() => records.value[selectedIdx.value] ?? null)
const rawView = ref(false)

function selectRecord(idx: number) {
  selectedIdx.value = idx
  rawView.value = false
}

const selData = computed<Record<string, any>>(() => selected.value?.data ?? {})
const selReq = computed<Record<string, any>>(() => selData.value.req ?? {})

async function onCopy(payload: unknown) {
  try {
    await copyText(typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2))
    message.success(tr('common.copied'))
  } catch (err) {
    message.error(String(err))
  }
}

// --- formatting helpers ---
function trunc(s: string, n: number): string {
  const r = [...s]
  return r.length <= n ? s : r.slice(0, n).join('') + '…'
}
function fmtTime(v: string): string {
  const d = new Date(v)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
function fmtDateTime(v: string | Date): string {
  const d = new Date(v)
  if (isNaN(d.getTime())) return String(v)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function fmtTokens(n: number): string {
  return n >= 10000 ? `${(n / 1000).toFixed(1)}k` : String(n)
}
function fmtMs(ms: unknown): string {
  const n = Number(ms)
  if (!isFinite(n)) return ''
  return n >= 10000 ? `${(n / 1000).toFixed(1)}s` : `${n}ms`
}
function fmtBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`
  return `${n} B`
}
function pretty(v: unknown): string {
  if (typeof v === 'string') {
    try {
      return JSON.stringify(JSON.parse(v), null, 2)
    } catch {
      return v
    }
  }
  return JSON.stringify(v, null, 2)
}
function sessTitle(s: traceApi.TraceSession): string {
  return s.title || s.sessionId
}
function usageIn(u: Record<string, any> | undefined): number {
  if (!u) return 0
  return (u.InputTokens ?? 0) + (u.CacheReadTokens ?? 0) + (u.CacheWriteTokens ?? 0)
}
</script>

<template>
  <div class="root">
    <header class="titlebar" :class="{ win: isWin }" @dblclick="toggleMaximise">
      <span class="title">{{ $t('agentTrace.title') }}</span>
      <div v-if="isWin" class="window-controls">
        <button type="button" class="win-btn" :title="$t('connectionEditor.minimise')" @click="onWindowCtrl('min')">
          <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="0" y="4.5" width="10" height="1" fill="currentColor" /></svg>
        </button>
        <button type="button" class="win-btn" :title="isMaximised ? $t('connectionEditor.restore') : $t('connectionEditor.maximise')" @click="onWindowCtrl('max')">
          <svg viewBox="0 0 10 10" aria-hidden="true">
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
      <!-- Sessions pane -->
      <aside class="pane sessions">
        <div class="pane-head">
          <span class="pane-title">{{ $t('agentTrace.sessions') }}</span>
          <span class="spacer" />
          <button type="button" class="mini-btn" @click="refresh">{{ $t('agentTrace.refresh') }}</button>
          <button type="button" class="mini-btn danger" :disabled="sessions.length === 0" @click="onClearAll">{{ $t('agentTrace.clearAll') }}</button>
        </div>
        <div class="pane-scroll">
          <div v-if="sessions.length === 0" class="empty">{{ loadingList ? '' : $t('agentTrace.emptySessions') }}</div>
          <button
            v-for="s in sessions"
            :key="s.sessionId"
            type="button"
            class="sess-row"
            :class="{ selected: s.sessionId === selectedSess }"
            @click="selectSession(s.sessionId)"
          >
            <span class="sess-title" :title="sessTitle(s)">{{ sessTitle(s) }}</span>
            <span class="sess-meta">{{ fmtDateTime(s.updatedAt) }} · {{ fmtBytes(s.size) }}</span>
          </button>
        </div>
      </aside>

      <!-- Timeline pane -->
      <section class="pane timeline">
        <div class="pane-head">
          <span class="pane-title">{{ $t('agentTrace.timeline') }}</span>
          <span class="spacer" />
          <button type="button" class="mini-btn" :disabled="!selectedSess" @click="reloadTrace">{{ $t('agentTrace.refresh') }}</button>
        </div>
        <div class="pane-scroll">
          <div v-if="!selectedSess" class="empty">{{ $t('agentTrace.pickSession') }}</div>
          <div v-else-if="rows.length === 0" class="empty">{{ loadingTrace ? '' : $t('agentTrace.emptyTrace') }}</div>
          <button
            v-for="r in rows"
            :key="r.idx"
            type="button"
            class="rec-row"
            :class="{ selected: r.idx === selectedIdx, group: r.group, error: r.error }"
            @click="selectRecord(r.idx)"
          >
            <span class="rec-top">
              <span class="rec-kind" :class="`k-${r.kind}`">{{ r.label }}</span>
              <span class="rec-time">{{ r.time }}</span>
            </span>
            <span class="rec-summary" :title="r.summary">{{ r.summary }}</span>
          </button>
        </div>
      </section>

      <!-- Detail pane -->
      <section class="pane detail">
        <div class="pane-head">
          <span class="pane-title">
            {{ selected ? (KNOWN_KINDS.has(selected.kind) ? $t(`agentTrace.kind.${selected.kind}`) : selected.kind) : $t('agentTrace.detail') }}
          </span>
          <span class="spacer" />
          <template v-if="selected">
            <button type="button" class="mini-btn" :class="{ active: !rawView }" @click="rawView = false">{{ $t('agentTrace.rendered') }}</button>
            <button type="button" class="mini-btn" :class="{ active: rawView }" @click="rawView = true">{{ $t('agentTrace.rawJson') }}</button>
            <button type="button" class="mini-btn" @click="onCopy(selData)">{{ $t('agentTrace.copyJson') }}</button>
          </template>
        </div>
        <div class="pane-scroll detail-scroll">
          <div v-if="!selected" class="empty">{{ $t('agentTrace.pickRecord') }}</div>

          <pre v-else-if="rawView" class="code">{{ pretty(selData) }}</pre>

          <!-- user -->
          <template v-else-if="selected.kind === 'user'">
            <div class="kv">
              <span class="k">{{ $t('agentTrace.f.mode') }}</span><span class="v mono">{{ selData.mode }}</span>
              <span class="k">{{ $t('agentTrace.f.model') }}</span><span class="v mono">{{ selData.model }}</span>
              <span class="k">{{ $t('agentTrace.f.namespace') }}</span><span class="v mono">{{ [selData.db, selData.schema].filter(Boolean).join(' / ') || '—' }}</span>
              <span class="k">{{ $t('agentTrace.f.grants') }}</span><span class="v mono">{{ (selData.grants ?? []).join(', ') || '—' }}</span>
            </div>
            <div class="block-title">{{ $t('agentTrace.f.text') }}</div>
            <pre class="code wrap">{{ selData.text }}</pre>
            <template v-if="(selData.mentions ?? []).length">
              <div class="block-title">{{ $t('agentTrace.f.mentions') }}</div>
              <pre class="code">{{ (selData.mentions ?? []).join(', ') }}</pre>
            </template>
          </template>

          <!-- request -->
          <template v-else-if="selected.kind === 'request'">
            <div class="kv">
              <span class="k">{{ $t('agentTrace.f.purpose') }}</span>
              <span class="v">{{ selData.purpose === 'compact-summary' ? $t('agentTrace.purposeCompact') : $t('agentTrace.purposeChat') }}</span>
              <span class="k">{{ $t('agentTrace.f.model') }}</span><span class="v mono">{{ selReq.Model }}</span>
              <span class="k">maxTokens</span><span class="v mono">{{ selReq.MaxTokens }}</span>
              <span v-if="selData.estTokens" class="k">{{ $t('agentTrace.f.estTokens') }}</span>
              <span v-if="selData.estTokens" class="v mono">~{{ selData.estTokens }}</span>
            </div>
            <div class="block-actions">
              <button type="button" class="mini-btn" @click="onCopy(selReq)">{{ $t('agentTrace.copyRequest') }}</button>
            </div>
            <details class="fold">
              <summary>{{ $t('agentTrace.f.system') }} <span class="dim">({{ (selReq.System ?? '').length }})</span></summary>
              <pre class="code wrap">{{ selReq.System }}</pre>
            </details>
            <details v-if="(selReq.Tools ?? []).length" class="fold">
              <summary>{{ $t('agentTrace.f.toolDefs') }} <span class="dim">({{ (selReq.Tools ?? []).length }})</span></summary>
              <div v-for="(td, i) in selReq.Tools" :key="i" class="tooldef">
                <div class="block-title mono">{{ td.Name }}</div>
                <p class="dim desc">{{ td.Description }}</p>
                <pre class="code">{{ pretty(td.InputSchema) }}</pre>
              </div>
            </details>
            <div class="block-title">{{ $t('agentTrace.f.messages') }} ({{ (selReq.Messages ?? []).length }})</div>
            <div v-for="(m, i) in selReq.Messages" :key="i" class="msg" :class="`role-${m.Role}`">
              <span class="role-chip" :class="`role-${m.Role}`">{{ m.Role }}</span>
              <pre v-if="m.Text" class="code wrap">{{ m.Text }}</pre>
              <div v-for="(tc, j) in m.ToolCalls ?? []" :key="j" class="toolcall">
                <span class="mono dim">→ {{ tc.Name }}</span>
                <pre class="code">{{ pretty(tc.Args ?? '{}') }}</pre>
              </div>
              <details v-if="m.ToolResult" class="fold">
                <summary class="mono">
                  {{ $t('agentTrace.f.toolResult') }}
                  <span class="dim">({{ (m.ToolResult.Content ?? '').length }})</span>
                  <span v-if="m.ToolResult.IsError" class="err-tag">{{ $t('agentTrace.f.error') }}</span>
                </summary>
                <pre class="code wrap" :class="{ 'err-text': m.ToolResult.IsError }">{{ m.ToolResult.Content }}</pre>
              </details>
            </div>
          </template>

          <!-- response -->
          <template v-else-if="selected.kind === 'response'">
            <div class="kv">
              <template v-if="!selData.error">
                <span class="k">{{ $t('agentTrace.f.stop') }}</span><span class="v mono">{{ selData.stop }}</span>
                <span class="k">{{ $t('agentTrace.f.usage') }}</span>
                <span class="v mono">
                  in {{ usageIn(selData.usage) }} (cache r{{ selData.usage?.CacheReadTokens ?? 0 }}/w{{ selData.usage?.CacheWriteTokens ?? 0 }}) · out {{ selData.usage?.OutputTokens ?? 0 }}
                </span>
              </template>
              <span class="k">{{ $t('agentTrace.f.duration') }}</span><span class="v mono">{{ fmtMs(selData.durationMs) }}</span>
            </div>
            <template v-if="selData.error">
              <div class="block-title">{{ $t('agentTrace.f.error') }}</div>
              <pre class="code wrap err-text">{{ selData.error }}</pre>
              <template v-if="selData.partialText">
                <div class="block-title">{{ $t('agentTrace.f.partialText') }}</div>
                <pre class="code wrap">{{ selData.partialText }}</pre>
              </template>
            </template>
            <template v-else>
              <details v-if="selData.thinking" class="fold">
                <summary>{{ $t('agentTrace.f.thinking') }} <span class="dim">({{ selData.thinking.length }})</span></summary>
                <pre class="code wrap">{{ selData.thinking }}</pre>
              </details>
              <template v-if="selData.text">
                <div class="block-title">{{ $t('agentTrace.f.text') }}</div>
                <pre class="code wrap">{{ selData.text }}</pre>
              </template>
              <template v-if="(selData.toolCalls ?? []).length">
                <div class="block-title">{{ $t('agentTrace.f.toolCalls') }}</div>
                <div v-for="(tc, j) in selData.toolCalls" :key="j" class="toolcall">
                  <span class="mono dim">→ {{ tc.Name }}</span>
                  <pre class="code">{{ pretty(tc.Args ?? '{}') }}</pre>
                </div>
              </template>
            </template>
          </template>

          <!-- tool -->
          <template v-else-if="selected.kind === 'tool'">
            <div class="kv">
              <span class="k">{{ $t('agentTrace.f.tool') }}</span><span class="v mono">{{ selData.name }}</span>
              <span class="k">{{ $t('agentTrace.f.duration') }}</span><span class="v mono">{{ fmtMs(selData.durationMs) }}</span>
              <span class="k">{{ $t('agentTrace.f.status') }}</span>
              <span class="v" :class="{ 'err-text': selData.isError }">{{ selData.isError ? $t('agentTrace.f.error') : 'ok' }}</span>
            </div>
            <div class="block-title">{{ $t('agentTrace.f.args') }}</div>
            <pre class="code">{{ pretty(selData.args || '{}') }}</pre>
            <div class="block-title">{{ $t('agentTrace.f.result') }}</div>
            <pre class="code wrap" :class="{ 'err-text': selData.isError }">{{ selData.result }}</pre>
          </template>

          <!-- approval -->
          <template v-else-if="selected.kind === 'approval'">
            <div class="kv">
              <span class="k">{{ $t('agentTrace.f.classVerb') }}</span><span class="v mono">{{ selData.class }} / {{ selData.verb }}</span>
              <span v-if="selData.warning" class="k">{{ $t('agentTrace.f.warning') }}</span>
              <span v-if="selData.warning" class="v mono">{{ selData.warning }}</span>
              <span class="k">{{ $t('agentTrace.f.decision') }}</span>
              <span class="v" :class="{ 'err-text': selData.approved === false }">
                {{ decisionText(selData) }}<template v-if="selData.scope"> ({{ selData.scope }})</template>
              </span>
              <span v-if="selData.reason" class="k">{{ $t('agentTrace.f.reason') }}</span>
              <span v-if="selData.reason" class="v">{{ selData.reason }}</span>
              <span class="k">{{ $t('agentTrace.f.waited') }}</span><span class="v mono">{{ fmtMs(selData.waitMs) }}</span>
            </div>
            <div class="block-title">SQL</div>
            <pre class="code wrap">{{ selData.sql }}</pre>
          </template>

          <!-- plan -->
          <template v-else-if="selected.kind === 'plan'">
            <div class="kv">
              <span class="k">{{ $t('agentTrace.f.decision') }}</span>
              <span class="v" :class="{ 'err-text': selData.approved === false }">{{ decisionText(selData) }}</span>
              <span v-if="selData.reason" class="k">{{ $t('agentTrace.f.reason') }}</span>
              <span v-if="selData.reason" class="v">{{ selData.reason }}</span>
              <span class="k">{{ $t('agentTrace.f.waited') }}</span><span class="v mono">{{ fmtMs(selData.waitMs) }}</span>
            </div>
            <div class="block-title">{{ $t('agentTrace.f.goal') }}</div>
            <pre class="code wrap">{{ selData.goal }}</pre>
            <div class="block-title">{{ $t('agentTrace.f.statements') }}</div>
            <pre class="code wrap">{{ (selData.statements ?? []).join('\n') }}</pre>
            <template v-if="selData.impact">
              <div class="block-title">{{ $t('agentTrace.f.impact') }}</div>
              <pre class="code wrap">{{ selData.impact }}</pre>
            </template>
          </template>

          <!-- compact -->
          <template v-else-if="selected.kind === 'compact'">
            <div class="kv">
              <span class="k">{{ $t('agentTrace.f.folded') }}</span><span class="v mono">{{ selData.foldedCount }}</span>
              <span class="k">{{ $t('agentTrace.f.tokens') }}</span>
              <span class="v mono">{{ selData.before }} → {{ selData.after }}</span>
            </div>
            <div class="block-title">{{ $t('agentTrace.f.summary') }}</div>
            <pre class="code wrap">{{ selData.summary }}</pre>
          </template>

          <!-- repair / done / error and any future kinds -->
          <template v-else>
            <pre class="code">{{ pretty(selData) }}</pre>
          </template>
        </div>
      </section>
    </main>
  </div>
</template>

<style scoped>
.root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  width: 100vw;
  overflow: hidden;
  background: var(--catdb-surface-content);
}
.titlebar {
  position: relative;
  flex: 0 0 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--catdb-fs-small);
  font-weight: 600;
  letter-spacing: 0.2px;
  opacity: 0.85;
  --wails-draggable: drag;
}
.titlebar .window-controls {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 10;
  display: flex;
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
  border: none;
  border-radius: 0;
  font: inherit;
  color: inherit;
  cursor: default;
  background: transparent;
}
.titlebar .win-btn svg { width: 14px; height: 14px; opacity: 0.75; }
.titlebar .win-btn:hover { background: var(--catdb-hover-fill); }
.titlebar .win-btn-close:hover { background: var(--catdb-error); }
.titlebar .win-btn-close:hover svg { opacity: 1; }

.body {
  flex: 1 1 0;
  min-height: 0;
  display: flex;
  border-top: 1px solid var(--catdb-separator);
}

.pane {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}
.sessions { flex: 0 0 230px; border-right: 1px solid var(--catdb-separator); }
.timeline { flex: 0 0 320px; border-right: 1px solid var(--catdb-separator); }
.detail { flex: 1 1 0; }

.pane-head {
  flex: 0 0 32px;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 8px;
  border-bottom: 1px solid var(--catdb-separator);
}
.pane-title {
  font-size: var(--catdb-fs-small);
  font-weight: 600;
  color: var(--catdb-text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.spacer { flex: 1 1 0; }

.mini-btn {
  flex: 0 0 auto;
  height: 20px;
  padding: 0 7px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: transparent;
  color: var(--catdb-text-primary);
  font-size: var(--catdb-fs-mini);
  cursor: default;
  white-space: nowrap;
}
.mini-btn:hover:not(:disabled) { background: var(--catdb-hover-fill); }
.mini-btn:active:not(:disabled) { background: var(--catdb-pressed-fill); }
.mini-btn:disabled { opacity: 0.4; }
.mini-btn.active { background: var(--catdb-accent-soft); border-color: var(--catdb-accent); }
.mini-btn.danger:hover:not(:disabled) { color: var(--catdb-error); border-color: var(--catdb-error); }

.pane-scroll {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
}
.empty {
  padding: 24px 12px;
  text-align: center;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-tertiary);
}

/* --- sessions rows --- */
.sess-row {
  display: flex;
  flex-direction: column;
  gap: 1px;
  width: 100%;
  padding: 5px 10px;
  border: none;
  background: transparent;
  text-align: left;
  cursor: default;
  font: inherit;
  color: var(--catdb-text-primary);
}
.sess-row:hover:not(.selected) { background: var(--catdb-hover-fill); }
.sess-row.selected { background: var(--catdb-selection-unfocused); }
.sessions:focus-within .sess-row.selected { background: var(--catdb-selection-focused); color: var(--catdb-text-on-accent); }
.sess-title {
  font-size: var(--catdb-fs-small);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sess-meta { font-size: var(--catdb-fs-micro); color: var(--catdb-text-tertiary); }
.sessions:focus-within .sess-row.selected .sess-meta { color: var(--catdb-text-on-accent); opacity: 0.75; }

/* --- timeline rows --- */
.rec-row {
  display: flex;
  flex-direction: column;
  gap: 1px;
  width: 100%;
  padding: 4px 10px;
  border: none;
  background: transparent;
  text-align: left;
  cursor: default;
  font: inherit;
  color: var(--catdb-text-primary);
}
.rec-row:hover:not(.selected) { background: var(--catdb-hover-fill); }
.rec-row.selected { background: var(--catdb-selection-unfocused); }
.timeline:focus-within .rec-row.selected { background: var(--catdb-selection-focused); color: var(--catdb-text-on-accent); }
.rec-row.group { border-top: 1px solid var(--catdb-separator); background: var(--catdb-row-alternate); }
.rec-row.group:hover:not(.selected) { background: var(--catdb-hover-fill); }
.rec-row.group.selected { background: var(--catdb-selection-unfocused); }
.timeline:focus-within .rec-row.group.selected { background: var(--catdb-selection-focused); }
.rec-top { display: flex; align-items: center; gap: 6px; }
.rec-kind {
  font-size: var(--catdb-fs-micro);
  font-weight: 600;
  padding: 0 5px;
  border-radius: var(--catdb-rounded-xs);
  background: var(--catdb-hover-fill);
  color: var(--catdb-text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.3px;
}
.rec-kind.k-user { background: var(--catdb-accent-soft); color: var(--catdb-accent); }
.rec-kind.k-request { color: var(--catdb-accent); }
.rec-kind.k-tool { color: var(--catdb-warning); }
.rec-kind.k-approval, .rec-kind.k-plan { color: var(--catdb-success); }
.rec-row.error .rec-kind { background: transparent; color: var(--catdb-error); }
.timeline:focus-within .rec-row.selected .rec-kind { background: rgba(255, 255, 255, 0.2); color: var(--catdb-text-on-accent); }
.rec-time { margin-left: auto; font-size: var(--catdb-fs-micro); color: var(--catdb-text-tertiary); font-family: var(--catdb-font-family-mono); }
.timeline:focus-within .rec-row.selected .rec-time { color: var(--catdb-text-on-accent); opacity: 0.75; }
.rec-summary {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.timeline:focus-within .rec-row.selected .rec-summary { color: var(--catdb-text-on-accent); opacity: 0.85; }
.rec-row.error .rec-summary { color: var(--catdb-error); }

/* --- detail pane --- */
.detail-scroll {
  padding: 10px 14px;
  /* Trace content exists to be read and quoted — override the app-wide
     user-select: none so prompts/results can be selected and copied. */
  user-select: text;
  -webkit-user-select: text;
}
.detail-scroll ::selection {
  background: var(--catdb-accent-soft);
}
.kv {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 3px 12px;
  margin-bottom: 10px;
  font-size: var(--catdb-fs-small);
}
.kv .k { color: var(--catdb-text-secondary); white-space: nowrap; }
.kv .v { color: var(--catdb-text-primary); word-break: break-word; }
.mono { font-family: var(--catdb-font-family-mono); font-size: var(--catdb-fs-mono-small); }
.dim { color: var(--catdb-text-tertiary); font-weight: 400; }

.block-title {
  margin: 12px 0 4px;
  font-size: var(--catdb-fs-small);
  font-weight: 600;
  color: var(--catdb-text-secondary);
}
.block-actions { margin: 8px 0; display: flex; gap: 4px; }

.code {
  margin: 0 0 6px;
  padding: 8px 10px;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-row-alternate);
  font-family: var(--catdb-font-family-mono);
  font-size: var(--catdb-fs-mono-small);
  line-height: 1.5;
  color: var(--catdb-text-primary);
  overflow-x: auto;
  max-width: 100%;
}
.code.wrap { white-space: pre-wrap; word-break: break-word; overflow-x: hidden; }
.err-text { color: var(--catdb-error); }
.err-tag {
  margin-left: 6px;
  font-size: var(--catdb-fs-micro);
  font-weight: 600;
  color: var(--catdb-error);
  text-transform: uppercase;
}

.fold { margin: 6px 0; }
.fold > summary {
  font-size: var(--catdb-fs-small);
  font-weight: 600;
  color: var(--catdb-text-secondary);
  cursor: default;
  padding: 2px 0;
  user-select: none;
}
.fold > summary:hover { color: var(--catdb-text-primary); }
.fold[open] > summary { margin-bottom: 4px; }

.msg {
  margin: 6px 0;
  padding: 6px 8px 2px;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
}
.msg.role-user { border-left: 2px solid var(--catdb-accent); }
.msg.role-assistant { border-left: 2px solid var(--catdb-success); }
.msg.role-tool { border-left: 2px solid var(--catdb-warning); }
.role-chip {
  display: inline-block;
  margin-bottom: 4px;
  font-size: var(--catdb-fs-micro);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.3px;
  color: var(--catdb-text-secondary);
}
.toolcall { margin: 4px 0; }
.tooldef { margin: 6px 0 10px; }
.tooldef .desc { margin: 2px 0 4px; font-size: var(--catdb-fs-mini); }
</style>
