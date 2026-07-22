<script setup lang="ts">
// AgentPanel — the docked AI assistant (AGENT_DESIGN.md §10). Renders to the
// right of the workspace; owns the current session, the message timeline, the
// agent:* event wiring, and the resize handle. M1 covers the Ask-mode round
// trip: session header + streaming messages + tool cards + SQL exits + input.
// Agent-mode grants / approvals / transaction bar are a later milestone; the
// structure (mode toggle, tool cards) is left open for them.
import { computed, nextTick, onBeforeUnmount, onMounted, provide, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import AgentSessionHeader from './AgentSessionHeader.vue'
import AgentMessage from './AgentMessage.vue'
import AgentToolCard from './AgentToolCard.vue'
import AgentApprovalCard from './AgentApprovalCard.vue'
import AgentPlanCard from './AgentPlanCard.vue'
import AgentResultTable from './AgentResultTable.vue'
import AgentTxBar from './AgentTxBar.vue'
import AgentGrants from './AgentGrants.vue'
import AgentComposer from './AgentComposer.vue'
import AppIcon from '../shared/AppIcon.vue'
import botIcon from '../../assets/icons/bot.svg?raw'
import { AGENT_SQL_ACTIONS, type AgentSqlActions } from './sqlActions'
import { entryId, type ApprovalEntry, type AssistantEntry, type Entry, type PlanEntry, type ToolEntry } from './types'
import * as agentApi from '../../api/agent'
import type { AgentSession } from '../../api/agent'
import { useQueryStore } from '../../stores/query'
import { useMetadataStore } from '../../stores/metadata'
import { openTextPrompt } from '../../api/prompts'
import { confirm } from '../../api/dialogs'
import type { ConnectionProfile } from '../../api/connections'
import { i18n, t } from '../../i18n'

const props = defineProps<{ connection: ConnectionProfile | null }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const message = useMessage()
const queryStore = useQueryStore()
const metaStore = useMetadataStore()

// --- session / timeline state ---
const session = ref<AgentSession | null>(null)
const sessions = ref<AgentSession[]>([])
const entries = ref<Entry[]>([])
const busy = ref(false)
const tokens = ref(0)
const errorBar = ref<{ slug: string; detail: string } | null>(null)
const loading = ref(false)

// namespace
const databases = ref<string[]>([])
const schemas = ref<string[]>([])
const schemasSupported = ref(false)
const currentDb = ref('')
const currentSchema = ref('')

const mode = computed<'ask' | 'agent'>(() => (session.value?.mode === 'agent' ? 'agent' : 'ask'))
const connectionName = computed(() => props.connection?.name ?? '')

// --- transaction / grants state (Agent mode) ---
// txPending drives the AgentTxBar and disables the composer until the user
// commits or rolls back (§5 gate 5). Not restored across panel reloads — the
// backend rejects new SendMessage with agent.tx-pending-block if one lingers.
const txPending = ref<agentApi.TxStmt[] | null>(null)
const txBusy = ref(false)
const grants = computed(() => session.value?.grants ?? [])
const isProd = computed(() => props.connection?.environment === 'prod')

let offEvents: (() => void) | null = null
let currentSend: { done: Promise<void>; stop: () => void } | null = null

// --- SQL block actions (provided to nested AgentSqlBlock via inject) ---
const sqlActions: AgentSqlActions = {
  insert(sql) {
    const connId = session.value?.connId
    if (!connId) return false
    queryStore.appendSqlToActiveQuery(connId, sql)
    return true
  },
  openTab(sql) {
    const connId = session.value?.connId
    if (!connId) return
    queryStore.addTab(connId, { kind: 'query', sql, title: t('agent.panel.sql.tabTitle') })
  },
}
provide(AGENT_SQL_ACTIONS, sqlActions)

// --- scrolling ---
const scrollerRef = ref<HTMLElement | null>(null)
function scrollToBottom() {
  void nextTick(() => {
    const el = scrollerRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

// --- streaming helpers ---
function ensureAssistant(): AssistantEntry {
  const last = entries.value[entries.value.length - 1]
  if (last && last.kind === 'assistant' && last.streaming) return last
  const a: AssistantEntry = { kind: 'assistant', id: entryId(), text: '', thinking: '', streaming: true }
  entries.value.push(a)
  // Return the entry as read back from the reactive array — mutating the raw
  // local object would bypass reactivity and the first delta wouldn't render.
  return entries.value[entries.value.length - 1] as AssistantEntry
}

// Batch deltas per animation frame — never reflow per event (§10.4).
let pendingDelta = ''
let rafScheduled = false
function scheduleFlush() {
  if (rafScheduled) return
  rafScheduled = true
  requestAnimationFrame(() => {
    rafScheduled = false
    if (pendingDelta) {
      ensureAssistant().text += pendingDelta
      pendingDelta = ''
      scrollToBottom()
    }
  })
}

function finalizeStreaming(stopReason?: string) {
  // Flush any buffered delta before we freeze the entries.
  if (pendingDelta) { ensureAssistant().text += pendingDelta; pendingDelta = '' }
  let lastAssistant: AssistantEntry | null = null
  for (const e of entries.value) {
    if (e.kind === 'assistant' && e.streaming) { e.streaming = false; lastAssistant = e }
    else if (e.kind === 'assistant') lastAssistant = e
  }
  if (lastAssistant && stopReason) lastAssistant.stopReason = stopReason
}

function attachEvents(sessId: string) {
  offEvents?.()
  offEvents = agentApi.subscribe(sessId, {
    onDelta: (e) => { pendingDelta += e.text; scheduleFlush() },
    onThinking: (e) => { ensureAssistant().thinking += e.text; scrollToBottom() },
    onTool: (e) => {
      if (e.phase === 'start') {
        entries.value.push({ kind: 'tool', id: entryId(), callId: e.callId, name: e.name, phase: 'start', summary: '', isError: false })
      } else {
        const te = [...entries.value].reverse().find(
          (x) => x.kind === 'tool' && (x as ToolEntry).callId === e.callId,
        ) as ToolEntry | undefined
        if (te) { te.phase = 'end'; te.summary = e.summary ?? '' }
        else entries.value.push({ kind: 'tool', id: entryId(), callId: e.callId, name: e.name, phase: 'end', summary: e.summary ?? '', isError: false })
      }
      scrollToBottom()
    },
    onUsage: (e) => { tokens.value += (e.tokensIn || 0) + (e.tokensOut || 0) },
    onDone: (e) => { finalizeStreaming(e.stopReason) },
    onError: (e) => { errorBar.value = { slug: e.slug, detail: e.detail }; finalizeStreaming() },
    onApproval: (e) => {
      entries.value.push({
        kind: 'approval', id: entryId(), approvalID: e.approvalID,
        sql: e.sql, class: e.class, verb: e.verb,
        warning: e.warning, autoOffered: e.autoOffered, status: 'pending',
      })
      scrollToBottom()
    },
    onPlan: (e) => {
      entries.value.push({
        kind: 'plan', id: entryId(), planID: e.planID,
        goal: e.goal, statements: e.statements ?? [], impact: e.impact, status: 'pending',
      })
      scrollToBottom()
    },
    onTxPending: (e) => { txPending.value = e.statements ?? []; scrollToBottom() },
    onResult: (e) => {
      entries.value.push({
        kind: 'result', id: entryId(),
        columns: e.columns ?? [], rows: e.rows ?? [], truncated: !!e.truncated,
      })
      scrollToBottom()
    },
  })
}

// --- history rendering ---
function summarize(s: string): string {
  const line = (s ?? '').split('\n', 1)[0]
  return line.length > 120 ? line.slice(0, 117) + '…' : line
}
function historyToEntries(msgs: agentApi.AgentMessage[]): Entry[] {
  const out: Entry[] = []
  const toolById = new Map<string, ToolEntry>()
  for (const m of msgs) {
    const c = agentApi.parseContent(m.content)
    if (m.role === 'user') {
      out.push({ kind: 'user', id: entryId(), text: c.text ?? '' })
    } else if (m.role === 'assistant') {
      if ((c.text && c.text.trim()) || (c.thinking && c.thinking.trim())) {
        out.push({ kind: 'assistant', id: entryId(), text: c.text ?? '', thinking: c.thinking ?? '', streaming: false })
      }
      for (const call of c.toolCalls ?? []) {
        const args = call.args == null ? undefined : (typeof call.args === 'string' ? call.args : JSON.stringify(call.args))
        const te: ToolEntry = { kind: 'tool', id: entryId(), callId: call.id, name: call.name, phase: 'end', summary: '', isError: false, args }
        out.push(te)
        toolById.set(call.id, te)
      }
    } else if (m.role === 'tool' && c.result) {
      const te = toolById.get(c.result.callId)
      if (te) { te.result = c.result.content; te.isError = !!c.result.isError; te.summary = te.summary || summarize(c.result.content) }
      else out.push({ kind: 'tool', id: entryId(), callId: c.result.callId, name: '', phase: 'end', summary: summarize(c.result.content), isError: !!c.result.isError, result: c.result.content })
    }
  }
  return out
}

// --- namespace loading ---
async function loadNamespace() {
  const conn = props.connection
  if (!conn) return
  try {
    const caps = await queryStore.loadCapabilities(conn.driver)
    schemasSupported.value = !!caps.schemas
  } catch { schemasSupported.value = false }
  try {
    databases.value = await metaStore.ensureDatabases(conn.id)
  } catch { databases.value = [] }
  currentDb.value = session.value?.currentDb ?? ''
  currentSchema.value = session.value?.currentSchema ?? ''
  if (schemasSupported.value && currentDb.value) {
    try { schemas.value = await metaStore.ensureSchemas(conn.id, currentDb.value) } catch { schemas.value = [] }
  }
}

// --- session lifecycle ---
async function loadSession(s: AgentSession) {
  offEvents?.()
  session.value = s
  entries.value = []
  errorBar.value = null
  tokens.value = 0
  busy.value = false
  txPending.value = null
  txBusy.value = false
  try {
    const msgs = await agentApi.getMessages(s.id)
    entries.value = historyToEntries(msgs)
    tokens.value = msgs.reduce((sum, m) => sum + (m.tokensIn ?? 0) + (m.tokensOut ?? 0), 0)
  } catch (e) {
    errorBar.value = { slug: '', detail: String(e) }
  }
  attachEvents(s.id)
  await loadNamespace()
  scrollToBottom()
}

async function init() {
  const conn = props.connection
  session.value = null
  sessions.value = []
  entries.value = []
  if (!conn) return
  loading.value = true
  try {
    const list = await agentApi.listSessions(conn.id)
    sessions.value = list ?? []
    if (sessions.value.length > 0) {
      await loadSession(sessions.value[0])
    } else {
      const s = await agentApi.createSession(conn.id, 'ask')
      sessions.value = [s]
      await loadSession(s)
    }
  } catch (e) {
    errorBar.value = { slug: '', detail: String(e) }
  } finally {
    loading.value = false
  }
}

// --- actions ---
function onSend(text: string) {
  const s = session.value
  if (!s || busy.value || txPending.value) return
  errorBar.value = null
  entries.value.push({ kind: 'user', id: entryId(), text })
  busy.value = true
  scrollToBottom()
  const h = agentApi.sendMessage(s.id, text)
  currentSend = h
  h.done
    .catch((err: unknown) => {
      const msg = String(err)
      if (!/cancel/i.test(msg) && !errorBar.value) errorBar.value = { slug: '', detail: msg }
    })
    .finally(() => {
      finalizeStreaming()
      busy.value = false
      currentSend = null
      scrollToBottom()
    })
}
function onStop() {
  currentSend?.stop()
  busy.value = false
}

// --- approval / plan resolution (mutate the reactive entry in place) ---
async function onApprove(entry: ApprovalEntry, scope: 'once' | 'task-verb') {
  try {
    await agentApi.approve(entry.approvalID, scope)
    entry.status = 'approved'
    entry.scope = scope
  } catch (e) { message.error(String(e)) }
}
async function onReject(entry: ApprovalEntry, reason: string) {
  try {
    await agentApi.reject(entry.approvalID, reason)
    entry.status = 'rejected'
    entry.reason = reason
  } catch (e) { message.error(String(e)) }
}
async function onApprovePlan(entry: PlanEntry) {
  try {
    await agentApi.approve(entry.planID, 'once')
    entry.status = 'approved'
  } catch (e) { message.error(String(e)) }
}
async function onRejectPlan(entry: PlanEntry, reason: string) {
  try {
    await agentApi.reject(entry.planID, reason)
    entry.status = 'rejected'
    entry.reason = reason
  } catch (e) { message.error(String(e)) }
}

// --- transaction commit / rollback (§5 gate 5) ---
async function onCommitTx() {
  const s = session.value
  if (!s || !txPending.value || txBusy.value) return
  const n = txPending.value.length
  txBusy.value = true
  try {
    await agentApi.commitTx(s.id)
    entries.value.push({ kind: 'system', id: entryId(), text: t('agent.tx.committed', { n }) })
    txPending.value = null
  } catch (e) { message.error(String(e)) }
  finally { txBusy.value = false; scrollToBottom() }
}
async function onRollbackTx() {
  const s = session.value
  if (!s || !txPending.value || txBusy.value) return
  txBusy.value = true
  try {
    await agentApi.rollbackTx(s.id)
    entries.value.push({ kind: 'system', id: entryId(), text: t('agent.tx.rolledBack') })
    txPending.value = null
  } catch (e) { message.error(String(e)) }
  finally { txBusy.value = false; scrollToBottom() }
}

// --- session grants (§5 gate 3) ---
async function onChangeGrants(next: string[]) {
  const s = session.value
  if (!s) return
  const prev = s.grants
  s.grants = next
  try {
    await agentApi.setGrants(s.id, next)
  } catch (e) {
    s.grants = prev
    message.error(String(e))
  }
}

async function onNewSession() {
  const conn = props.connection
  if (!conn) return
  try {
    const s = await agentApi.createSession(conn.id, 'ask')
    sessions.value = [s, ...sessions.value]
    await loadSession(s)
  } catch (e) {
    message.error(t('agent.panel.createFailed', { error: String(e) }))
  }
}
async function onSelectSession(id: string) {
  const s = sessions.value.find((x) => x.id === id)
  if (s) await loadSession(s)
}
async function onRenameSession(id: string) {
  const s = sessions.value.find((x) => x.id === id)
  if (!s) return
  const title = await openTextPrompt({
    title: t('agent.panel.renameTitle'),
    label: t('agent.panel.renameLabel'),
    initial: s.title,
    okText: t('common.rename'),
    validate: (v) => (v.trim() ? null : t('common.nameEmpty')),
  })
  if (title === null) return
  try {
    await agentApi.renameSession(id, title.trim())
    s.title = title.trim()
  } catch (e) {
    message.error(String(e))
  }
}
async function onDeleteSession(id: string) {
  const choice = await confirm({
    title: t('agent.panel.deleteTitle'),
    message: t('agent.panel.deleteConfirm'),
    buttons: [
      { value: 'cancel', label: t('common.cancel'), isCancel: true },
      { value: 'delete', label: t('common.delete') },
    ],
  })
  if (choice !== 'delete') return
  try {
    await agentApi.deleteSession(id)
    sessions.value = sessions.value.filter((x) => x.id !== id)
    if (session.value?.id === id) {
      if (sessions.value.length > 0) await loadSession(sessions.value[0])
      else await onNewSession()
    }
  } catch (e) {
    message.error(String(e))
  }
}

async function onChangeDb(db: string) {
  const s = session.value
  if (!s || db === currentDb.value) return
  currentDb.value = db
  currentSchema.value = ''
  if (schemasSupported.value && props.connection) {
    try { schemas.value = await metaStore.ensureSchemas(props.connection.id, db) } catch { schemas.value = [] }
  }
  try { await agentApi.setNamespace(s.id, db, '') } catch { /* best-effort */ }
  s.currentDb = db
  entries.value.push({ kind: 'system', id: entryId(), text: t('agent.panel.nsSwitched', { ns: db }) })
  scrollToBottom()
}
async function onChangeSchema(schema: string) {
  const s = session.value
  if (!s || schema === currentSchema.value) return
  currentSchema.value = schema
  try { await agentApi.setNamespace(s.id, currentDb.value, schema) } catch { /* best-effort */ }
  s.currentSchema = schema
  const ns = [currentDb.value, schema].filter(Boolean).join('.')
  entries.value.push({ kind: 'system', id: entryId(), text: t('agent.panel.nsSwitched', { ns }) })
  scrollToBottom()
}
async function onChangeMode(m: 'ask' | 'agent') {
  const s = session.value
  if (!s || s.mode === m) return
  try {
    await agentApi.setMode(s.id, m)
    s.mode = m
  } catch (e) {
    message.error(String(e))
  }
}

// error bar text: slug → error.* mapping, else raw detail.
const errorText = computed(() => {
  const eb = errorBar.value
  if (!eb) return ''
  if (eb.slug) {
    const key = 'error.' + eb.slug
    if (i18n.global.te(key)) return i18n.global.t(key)
  }
  return eb.detail || t('agent.panel.genericError')
})

// --- resize (handle on the panel's LEFT edge) ---
const width = ref(380)
const MIN_W = 300
const maxW = () => Math.max(MIN_W, Math.floor(window.innerWidth * 0.6))
const dragging = ref(false)
let startX = 0
let startWidth = 0
function onResizeDown(ev: PointerEvent) {
  if (ev.button !== 0) return
  ev.preventDefault()
  startX = ev.clientX
  startWidth = width.value
  dragging.value = true
  ;(ev.currentTarget as HTMLElement).setPointerCapture?.(ev.pointerId)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}
function onResizeMove(ev: PointerEvent) {
  if (!dragging.value) return
  // Dragging left (clientX smaller) widens the right-docked panel.
  const raw = startWidth - (ev.clientX - startX)
  width.value = Math.min(maxW(), Math.max(MIN_W, raw))
}
function onResizeUp() {
  dragging.value = false
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
}
function onWindowResize() {
  const cap = maxW()
  if (width.value > cap) width.value = cap
}

onMounted(() => {
  window.addEventListener('resize', onWindowResize)
  void init()
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onWindowResize)
  offEvents?.()
})
// Re-anchor to a new connection while the panel stays open.
watch(() => props.connection?.id, () => { void init() })
</script>

<template>
  <aside class="agent-panel" :class="{ dragging }" :style="{ width: width + 'px', flexBasis: width + 'px' }">
    <div
      class="resize-left"
      :class="{ active: dragging }"
      :title="$t('agent.panel.resizeHint')"
      @pointerdown="onResizeDown"
      @pointermove="onResizeMove"
      @pointerup="onResizeUp"
      @pointercancel="onResizeUp"
    />

    <!-- Empty state: no connection to anchor a session on. -->
    <div v-if="!connection" class="empty">
      <AppIcon :src="botIcon" :size="40" class="empty-icon" />
      <div class="empty-title">{{ $t('agent.panel.emptyTitle') }}</div>
      <div class="empty-desc">{{ $t('agent.panel.emptyDesc') }}</div>
    </div>

    <template v-else>
      <AgentSessionHeader
        :connection-name="connectionName"
        :environment="connection?.environment ?? ''"
        :session="session"
        :sessions="sessions"
        :databases="databases"
        :schemas="schemas"
        :schemas-supported="schemasSupported"
        :current-db="currentDb"
        :current-schema="currentSchema"
        :tokens="tokens"
        :mode="mode"
        @new-session="onNewSession"
        @select-session="onSelectSession"
        @rename-session="onRenameSession"
        @delete-session="onDeleteSession"
        @change-db="onChangeDb"
        @change-schema="onChangeSchema"
        @change-mode="onChangeMode"
      />

      <div ref="scrollerRef" class="messages">
        <div v-if="entries.length === 0 && !loading" class="hint-empty">{{ $t('agent.panel.startHint') }}</div>
        <template v-for="e in entries" :key="e.id">
          <AgentToolCard v-if="e.kind === 'tool'" :entry="e" />
          <AgentApprovalCard
            v-else-if="e.kind === 'approval'"
            :entry="e"
            @approve="(scope) => onApprove(e, scope)"
            @reject="(reason) => onReject(e, reason)"
          />
          <AgentPlanCard
            v-else-if="e.kind === 'plan'"
            :entry="e"
            @approve="() => onApprovePlan(e)"
            @reject="(reason) => onRejectPlan(e, reason)"
          />
          <AgentResultTable v-else-if="e.kind === 'result'" :entry="e" />
          <AgentMessage v-else :entry="e" />
        </template>
      </div>

      <div v-if="errorBar" class="error-bar">
        <span class="error-text">{{ errorText }}</span>
        <button type="button" class="error-close" :title="$t('common.close')" @click="errorBar = null">×</button>
      </div>

      <AgentTxBar
        v-if="txPending"
        :statements="txPending"
        :busy="txBusy"
        @commit="onCommitTx"
        @rollback="onRollbackTx"
      />
      <div v-if="txPending" class="tx-hint">{{ $t('agent.tx.blockHint') }}</div>

      <AgentGrants
        v-if="mode === 'agent'"
        :grants="grants"
        :readonly="isProd"
        @update="onChangeGrants"
      />

      <AgentComposer :busy="busy" :disabled="!session || !!txPending" @send="onSend" @stop="onStop" />
    </template>
  </aside>
</template>

<style scoped>
.agent-panel {
  position: relative;
  flex: 0 0 380px;
  width: 380px;
  min-width: 0;
  min-height: 0;
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-left: 1px solid var(--catdb-separator);
  background: var(--catdb-surface-content);
}
.agent-panel.dragging { user-select: none; }

.resize-left {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 4px;
  z-index: 10;
  cursor: col-resize;
  background: transparent;
  transition: background-color 0.2s ease;
}
.resize-left:hover, .resize-left.active { background: var(--catdb-accent-soft); }

.messages {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  padding: 8px 10px;
}
.hint-empty {
  color: var(--catdb-text-tertiary);
  font-size: var(--catdb-fs-small);
  text-align: center;
  padding: 20px 8px;
}

.error-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: 0 8px 6px;
  padding: 6px 8px;
  border-radius: var(--catdb-rounded-sm);
  background: color-mix(in srgb, var(--catdb-error) 10%, transparent);
  border: 1px solid var(--catdb-error);
}
.error-text {
  flex: 1 1 auto;
  min-width: 0;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-error);
  user-select: text;
  -webkit-user-select: text;
  word-break: break-word;
}
.error-close {
  flex: 0 0 auto;
  border: none;
  background: transparent;
  color: var(--catdb-error);
  font-size: 14px;
  line-height: 1;
  cursor: default;
  padding: 0 2px;
}

.tx-hint {
  flex: 0 0 auto;
  margin: 0 8px 6px;
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-tertiary);
  text-align: center;
}

.empty {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 24px;
  text-align: center;
}
.empty-icon { opacity: 0.4; }
.empty-title { font-size: var(--catdb-fs-title); font-weight: 600; color: var(--catdb-text-primary); }
.empty-desc { font-size: var(--catdb-fs-body); color: var(--catdb-text-secondary); max-width: 260px; }
</style>
