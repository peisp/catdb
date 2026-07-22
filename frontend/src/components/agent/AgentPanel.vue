<script setup lang="ts">
// AgentPanel — the docked AI assistant (AGENT_DESIGN.md §10). Renders to the
// right of the workspace; owns the current session, the message timeline, the
// agent:* event wiring, and the resize handle. M1 covers the Ask-mode round
// trip: session header + streaming messages + tool cards + SQL exits + input.
// Agent-mode grants / approvals / transaction bar are a later milestone; the
// structure (mode toggle, tool cards) is left open for them.
import { computed, nextTick, onBeforeUnmount, onMounted, provide, ref } from 'vue'
import { useMessage } from 'naive-ui'
import AgentSessionHeader from './AgentSessionHeader.vue'
import AgentHistoryView from './AgentHistoryView.vue'
import AgentMessage from './AgentMessage.vue'
import AgentToolCard from './AgentToolCard.vue'
import AgentApprovalCard from './AgentApprovalCard.vue'
import AgentPlanCard from './AgentPlanCard.vue'
import AgentResultTable from './AgentResultTable.vue'
import AgentTxBar from './AgentTxBar.vue'
import AgentGrants from './AgentGrants.vue'
import AgentComposer, { INPUT_MAX_H, INPUT_MIN_H } from './AgentComposer.vue'
import AppIcon from '../shared/AppIcon.vue'
import botIcon from '../../assets/icons/bot.svg?raw'
import lockIcon from '../../assets/icons/lock.svg?raw'
import { driverLogo } from '../../assets/logo'
import { AGENT_SQL_ACTIONS, type AgentSqlActions } from './sqlActions'
import { entryId, type ApprovalEntry, type AssistantEntry, type Entry, type PlanEntry, type ToolEntry } from './types'
import * as agentApi from '../../api/agent'
import type { AgentSession } from '../../api/agent'
import { getAgentSettings, listProviders, onProvidersChanged, type ModelPricing, type ProviderConfig } from '../../api/agentSettings'
import { useQueryStore } from '../../stores/query'
import { useMetadataStore } from '../../stores/metadata'
import { useConnectionsStore } from '../../stores/connections'
import { confirm } from '../../api/dialogs'
import type { ConnectionProfile } from '../../api/connections'
import { openSettingsWindow } from '../../api/system'
import { i18n, t } from '../../i18n'

// props.connection is only a HINT (§10.2): the cold-start / new-session
// default. The panel's real connection context follows the current session.
const props = defineProps<{ connection: ConnectionProfile | null }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const message = useMessage()
const queryStore = useQueryStore()
const metaStore = useMetadataStore()
const connStore = useConnectionsStore()

// --- session / timeline state ---
const session = ref<AgentSession | null>(null)
const sessions = ref<AgentSession[]>([])
const entries = ref<Entry[]>([])
const busy = ref(false)
const tokens = ref(0)
const tokensIn = ref(0)
const tokensOut = ref(0)
// Latest context watermark 0~1 (§9); undefined until the first usage event of a
// turn (or when the model window is unknown) → the header bar stays hidden.
const watermark = ref<number | undefined>(undefined)
const compacting = ref(false)
const pricing = ref<{ [k: string]: ModelPricing | undefined }>({})
// Configured Provider instances — the session-header model selector's source (§10.1).
const providers = ref<ProviderConfig[]>([])
const errorBar = ref<{ slug: string; detail: string } | null>(null)
// Starts true so the pre-init render doesn't flash the empty state.
const loading = ref(true)
// 'history' swaps the whole panel body for the session-history page (§10.2).
const view = ref<'chat' | 'history'>('chat')

// namespace
// Raw db list as fetched; the visible list applies the object tree's schema
// filter below (computed after panelConn is defined).
const allDatabases = ref<string[]>([])
const schemas = ref<string[]>([])
const schemasSupported = ref(false)
const currentDb = ref('')
const currentSchema = ref('')
// Table names of the current namespace, the @mention completion source (§10.3).
const tableNames = ref<string[]>([])

// Estimated cumulative cost (§9): session model's per-1M pricing × tokens.
// null when the model has no pricing row → the header shows tokens only.
const cost = computed<string | null>(() => {
  const model = session.value?.model
  if (!model) return null
  const p = pricing.value[model]
  if (!p) return null
  const c = (tokensIn.value / 1e6) * (p.inputPer1M || 0) + (tokensOut.value / 1e6) * (p.outputPer1M || 0)
  if (!(c > 0)) return null
  return '$' + c.toFixed(4)
})

const mode = computed<'ask' | 'agent'>(() => (session.value?.mode === 'agent' ? 'agent' : 'ask'))

// Panel-local connection context (§10.2): resolved from the CURRENT session's
// connId, decoupled from the main UI's active connection. Null while there is
// no session, or when the bound connection was deleted (orphan).
const panelConn = computed<ConnectionProfile | null>(() => {
  const id = session.value?.connId
  if (!id) return null
  return connStore.connections.find((c) => c.id === id) ?? null
})
// Orphan session: bound connection deleted → read-only archive (§10.2).
const orphan = computed(() => !!session.value && !panelConn.value)
// No AI provider configured at all — the dock shows a hint linking to Settings.
const noProvider = computed(() => !loading.value && providers.value.length === 0)
const connectionName = computed(() => panelConn.value?.name ?? '')
// A NEW conversation may pick its connection freely (§10.2); once real
// messages exist (system notice lines don't count) the binding is fixed —
// the backend enforces the same rule on persisted messages.
const hasMessages = computed(() => entries.value.some((e) => e.kind !== 'system'))
const canPickConn = computed(() => !!session.value && !busy.value && !hasMessages.value)
// Databases with the object tree's schema filter applied — the panel shows
// the same visible set as the tree / query-tab dropdowns, live-updating when
// the filter changes.
const databases = computed<string[]>(() => {
  const conn = panelConn.value
  if (!conn) return []
  const filter = queryStore.schemaFilterFor(conn.id)
  return filter ? allDatabases.value.filter((d) => filter.includes(d)) : allDatabases.value
})
// Connection name + environment per session, for the global session list's badges.
const connsById = computed<Record<string, { name: string; environment: string }>>(() => {
  const m: Record<string, { name: string; environment: string }> = {}
  for (const c of connStore.connections) m[c.id] = { name: c.name, environment: c.environment ?? '' }
  return m
})

// Environment badge of the panel connection (闸 1): prod = red + lock (hard
// read-only), dev/test/staging = neutral tag, '' = gray "unmarked" nudge.
// Tier names reuse the connection form's localized labels.
const envKind = computed<'prod' | 'other' | 'unmarked'>(() => {
  const e = panelConn.value?.environment ?? ''
  if (e === 'prod') return 'prod'
  if (e === 'dev' || e === 'test' || e === 'staging') return 'other'
  return 'unmarked'
})
const envLabel = computed(() =>
  envKind.value === 'unmarked'
    ? t('agent.panel.env.unmarked')
    : t(`connection.form.environments.${panelConn.value?.environment}`),
)
const envTooltip = computed(() => {
  if (envKind.value === 'prod') return t('agent.panel.env.prodTooltip')
  if (envKind.value === 'unmarked') return t('agent.panel.env.unmarkedTooltip')
  return ''
})

// Model selector (§10.1), in the row under the composer. A <select> value has
// to be a single string, so each option packs providerId + model around a
// separator that neither id contains.
const MODEL_SEP = '\u0000'
const providerGroups = computed(() =>
  providers.value
    .filter((p) => (p.models?.length ?? 0) > 0)
    .map((p) => ({
      id: p.id,
      name: p.name,
      models: (p.models ?? []).map((m) => ({ id: m.ID, value: p.id + MODEL_SEP + m.ID })),
    })),
)
const modelValue = computed(() => {
  const s = session.value
  if (!s || !s.providerId || !s.model) return ''
  return s.providerId + MODEL_SEP + s.model
})
// The session's model may reference a provider/model no longer in the list (e.g.
// provider deleted) — surface it as a standalone option so the value still shows.
const currentInList = computed(() => {
  const s = session.value
  if (!s || !s.providerId || !s.model) return true
  return providers.value.some((p) => p.id === s.providerId && (p.models ?? []).some((m) => m.ID === s.model))
})
function onModelSelect(v: string) {
  const i = v.indexOf(MODEL_SEP)
  if (i < 0) return
  void onChangeModel(v.slice(0, i), v.slice(i + 1))
}

// --- transaction / grants state (Agent mode) ---
// txPending drives the AgentTxBar and disables the composer until the user
// commits or rolls back (§5 gate 5). Not restored across panel reloads — the
// backend rejects new SendMessage with agent.tx-pending-block if one lingers.
const txPending = ref<agentApi.TxStmt[] | null>(null)
const txBusy = ref(false)
const grants = computed(() => session.value?.grants ?? [])
const isProd = computed(() => panelConn.value?.environment === 'prod')

let offEvents: (() => void) | null = null
let offProviders: (() => void) | null = null
let currentSend: { done: Promise<void>; stop: () => void } | null = null

// --- SQL block actions (provided to nested AgentSqlBlock via inject) ---
const sqlActions: AgentSqlActions = {
  insert(sql) {
    const connId = session.value?.connId
    if (!connId || orphan.value) return false
    queryStore.appendSqlToActiveQuery(connId, sql)
    return true
  },
  openTab(sql) {
    const connId = session.value?.connId
    if (!connId || orphan.value) return
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

function finalizeStreaming(stopReason?: string, deliveryWarning?: boolean) {
  // Flush any buffered delta before we freeze the entries.
  if (pendingDelta) { ensureAssistant().text += pendingDelta; pendingDelta = '' }
  let lastAssistant: AssistantEntry | null = null
  for (const e of entries.value) {
    if (e.kind === 'assistant' && e.streaming) { e.streaming = false; lastAssistant = e }
    else if (e.kind === 'assistant') lastAssistant = e
  }
  // token_budget / max_iterations (or a lone delivery warning) may fire before any
  // text; ensure a host entry so the tail hint (AgentMessage) still renders.
  const needHost = stopReason === 'token_budget' || stopReason === 'max_iterations' || deliveryWarning
  if (!lastAssistant && needHost) {
    lastAssistant = ensureAssistant()
    lastAssistant.streaming = false
  }
  if (stopReason && lastAssistant) lastAssistant.stopReason = stopReason
  if (deliveryWarning && lastAssistant) lastAssistant.deliveryWarning = true
}

// A non-text entry (tool/approval/plan/result) means the current round of
// thinking/text is complete — seal the trailing streaming assistant entry so
// its thinking region auto-collapses right away (§10.4) instead of waiting
// for the whole turn to end. Later deltas simply open a fresh entry.
function sealStreamingAssistant() {
  if (pendingDelta) { ensureAssistant().text += pendingDelta; pendingDelta = '' }
  const last = entries.value[entries.value.length - 1]
  if (last && last.kind === 'assistant' && last.streaming) last.streaming = false
}

function attachEvents(sessId: string) {
  offEvents?.()
  offEvents = agentApi.subscribe(sessId, {
    onDelta: (e) => { pendingDelta += e.text; scheduleFlush() },
    onThinking: (e) => { ensureAssistant().thinking += e.text; scrollToBottom() },
    onTool: (e) => {
      sealStreamingAssistant()
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
    onUsage: (e) => {
      tokensIn.value += e.tokensIn || 0
      tokensOut.value += e.tokensOut || 0
      tokens.value = tokensIn.value + tokensOut.value
      if (e.watermark != null) watermark.value = e.watermark
    },
    onCompacted: (e) => {
      entries.value.push({ kind: 'compacted', id: entryId(), count: e.foldedCount })
      if (e.after != null) watermark.value = e.after
      scrollToBottom()
    },
    onDone: (e) => { finalizeStreaming(e.stopReason, e.deliveryWarning) },
    onError: (e) => { errorBar.value = { slug: e.slug, detail: e.detail }; finalizeStreaming() },
    onApproval: (e) => {
      sealStreamingAssistant()
      entries.value.push({
        kind: 'approval', id: entryId(), approvalID: e.approvalID,
        sql: e.sql, class: e.class, verb: e.verb,
        warning: e.warning, autoOffered: e.autoOffered, explain: e.explain, status: 'pending',
      })
      scrollToBottom()
    },
    onPlan: (e) => {
      sealStreamingAssistant()
      entries.value.push({
        kind: 'plan', id: entryId(), planID: e.planID,
        goal: e.goal, statements: e.statements ?? [], impact: e.impact, status: 'pending',
      })
      scrollToBottom()
    },
    onTxPending: (e) => { txPending.value = e.statements ?? []; scrollToBottom() },
    onResult: (e) => {
      sealStreamingAssistant()
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
    // Persisted summary rounds (§9) render as the same centered compacted line;
    // the folded originals stay visible, so we add the line without hiding them.
    if (m.role === 'summary') {
      out.push({ kind: 'compacted', id: entryId() })
      continue
    }
    const c = agentApi.parseContent(m.content)
    if (m.role === 'user') {
      const mentions = (c.extra?.tables ?? []).map((tbl) => tbl.name).filter(Boolean)
      out.push({ kind: 'user', id: entryId(), text: c.text ?? '', mentions: mentions.length ? mentions : undefined })
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
// Lazy connect (§10.2): reading history never opens a database connection.
// The db list loads only when the connection is already live, or on demand
// (force) — the user opened the db selector, or a turn just ran (the engine
// connected server-side anyway).
async function loadNamespace(force = false) {
  const s = session.value
  currentDb.value = s?.currentDb ?? ''
  currentSchema.value = s?.currentSchema ?? ''
  const conn = panelConn.value
  if (!conn || (!force && !connStore.isLive(conn.id))) {
    allDatabases.value = []
    schemas.value = []
    tableNames.value = []
    schemasSupported.value = false
    return
  }
  try {
    const caps = await queryStore.loadCapabilities(conn.driver)
    schemasSupported.value = !!caps.schemas
  } catch { schemasSupported.value = false }
  let dbs: string[] = []
  try {
    dbs = await metaStore.ensureDatabases(conn.id)
    connStore.markLive(conn.id) // the fetch opened the connection server-side
  } catch { dbs = [] }
  if (session.value !== s) return // user switched sessions while loading
  allDatabases.value = dbs
  // A fresh session has no db yet — once the list is here, default to the
  // first VISIBLE one (object tree filter applied) so the user can just type.
  const visible = databases.value
  if (s && !currentDb.value && visible.length > 0) {
    currentDb.value = visible[0]
    s.currentDb = visible[0]
    try { await agentApi.setNamespace(s.id, visible[0], '') } catch { /* best-effort */ }
  }
  if (schemasSupported.value && currentDb.value) {
    try { schemas.value = await metaStore.ensureSchemas(conn.id, currentDb.value) } catch { schemas.value = [] }
  }
  await loadTableNames()
}

// @mention completion source: table names of the current namespace (§10.3),
// served from the metadata store's existing cache.
async function loadTableNames() {
  const conn = panelConn.value
  if (!conn || !currentDb.value) { tableNames.value = []; return }
  try {
    const list = await metaStore.ensureTables(conn.id, currentDb.value, false, currentSchema.value)
    tableNames.value = (list ?? []).map((tbl) => tbl.name)
  } catch { tableNames.value = [] }
}

// Lazy-connect trigger shared by the db selector (pointerdown) and the @
// completion menu (need-tables): connect + load the namespace on the first
// user gesture that needs it, with an in-flight guard against repeats.
const nsLoading = ref(false)
async function ensureNamespace() {
  if (nsLoading.value || orphan.value || !panelConn.value) return
  const loaded = allDatabases.value.length > 0 &&
    (tableNames.value.length > 0 || !currentDb.value)
  if (loaded) return
  nsLoading.value = true
  try { await loadNamespace(true) } finally { nsLoading.value = false }
}
function onRequestNamespace() {
  void ensureNamespace()
}

// --- session lifecycle ---
async function loadSession(s: AgentSession) {
  offEvents?.()
  session.value = s
  entries.value = []
  errorBar.value = null
  tokens.value = 0
  tokensIn.value = 0
  tokensOut.value = 0
  watermark.value = undefined
  compacting.value = false
  busy.value = false
  txPending.value = null
  txBusy.value = false
  try {
    const msgs = await agentApi.getMessages(s.id)
    entries.value = historyToEntries(msgs)
    tokensIn.value = msgs.reduce((sum, m) => sum + (m.tokensIn ?? 0), 0)
    tokensOut.value = msgs.reduce((sum, m) => sum + (m.tokensOut ?? 0), 0)
    tokens.value = tokensIn.value + tokensOut.value
  } catch (e) {
    errorBar.value = { slug: '', detail: String(e) }
  }
  attachEvents(s.id)
  await loadNamespace()
  scrollToBottom()
}

// Cold-start / new-session default connection (§10.2 fallback chain): the
// panel's current connection → the main UI's active connection → an already
// open connection → first in the list. Never eagerly connects.
function defaultNewSessionConn(): ConnectionProfile | null {
  if (panelConn.value) return panelConn.value
  if (props.connection) return props.connection
  const conns = connStore.connections
  return conns.find((c) => connStore.isLive(c.id)) ?? conns[0] ?? null
}

async function init() {
  session.value = null
  sessions.value = []
  entries.value = []
  loading.value = true
  try { pricing.value = (await getAgentSettings()).pricing ?? {} } catch { pricing.value = {} }
  try { providers.value = (await listProviders()) ?? [] } catch { providers.value = [] }
  try {
    if (connStore.connections.length === 0) await connStore.refreshAll()
  } catch { /* the session list still works without connection metadata */ }
  try {
    // Global list (§10.2): every connection's sessions, most recent first.
    // Opening the panel restores the most recent one.
    const list = await agentApi.listSessions()
    sessions.value = list ?? []
    if (sessions.value.length > 0) {
      await loadSession(sessions.value[0])
    } else {
      const conn = defaultNewSessionConn()
      if (conn) {
        const s = await agentApi.createSession(conn.id, 'ask')
        sessions.value = [s]
        await loadSession(s)
      }
      // No connections at all → the empty state renders.
    }
  } catch (e) {
    errorBar.value = { slug: '', detail: String(e) }
  } finally {
    loading.value = false
  }
}

// --- actions ---
function onSend(text: string, mentions: string[] = []) {
  const s = session.value
  if (!s || busy.value || txPending.value || orphan.value) return
  errorBar.value = null
  entries.value.push({ kind: 'user', id: entryId(), text, mentions: mentions.length ? mentions : undefined })
  busy.value = true
  scrollToBottom()
  const h = agentApi.sendMessage(s.id, text, mentions)
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
      // The engine connected server-side to run the turn — if the namespace
      // was never loaded (lazy connect), catch up now for @completion.
      if (session.value === s && allDatabases.value.length === 0) void loadNamespace(true)
    })
}
function onStop() {
  currentSend?.stop()
  busy.value = false
}

// --- manual context compaction (§9) ---
async function onCompact() {
  const s = session.value
  if (!s || compacting.value) return
  compacting.value = true
  try {
    // The compacted line + watermark update arrive via the agent:compacted event.
    await agentApi.compact(s.id)
  } catch (e) {
    message.error(String(e))
  } finally {
    compacting.value = false
  }
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
  // Inherits the panel's current connection (§10.2), falling back down the
  // default chain; without any connection there is nothing to bind to.
  const conn = defaultNewSessionConn()
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
  view.value = 'chat'
  const s = sessions.value.find((x) => x.id === id)
  if (s && s.id !== session.value?.id) await loadSession(s)
  else scrollToBottom() // remount of the scroller resets its position
}

function onHistoryBack() {
  view.value = 'chat'
  scrollToBottom()
}

// Open the full-page history view (§10.2), refreshing the list so ordering
// and updated-at stamps are current.
async function openHistory() {
  view.value = 'history'
  try {
    const list = await agentApi.listSessions()
    sessions.value = list ?? []
  } catch { /* keep the locally known list */ }
}

// Clear-all from the history view: second-confirm, then wipe and start a
// fresh default session (audit records are preserved server-side).
async function onClearHistory() {
  const choice = await confirm({
    title: t('agent.panel.clearTitle'),
    message: t('agent.panel.clearConfirm'),
    buttons: [
      { value: 'cancel', label: t('common.cancel'), isCancel: true },
      { value: 'clear', label: t('agent.panel.clearAll') },
    ],
  })
  if (choice !== 'clear') return
  try {
    await agentApi.clearSessions()
    offEvents?.()
    session.value = null
    sessions.value = []
    entries.value = []
    view.value = 'chat'
    await onNewSession()
  } catch (e) {
    message.error(String(e))
  }
}
// Inline rename from the history view — the new title arrives already
// trimmed and non-empty (AgentHistoryView commits only real changes).
async function onRenameSession(id: string, title: string) {
  const s = sessions.value.find((x) => x.id === id)
  if (!s) return
  try {
    await agentApi.renameSession(id, title)
    s.title = title
    // After a history refetch the list holds fresh objects — mirror onto the
    // loaded session so the chat header title stays in sync.
    if (session.value?.id === id) session.value.title = title
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
      if (sessions.value.length > 0) {
        await loadSession(sessions.value[0])
      } else {
        // No sessions left: clear first so a failed/impossible re-create
        // (no connections) falls through to the empty state.
        offEvents?.()
        session.value = null
        entries.value = []
        await onNewSession()
      }
    }
  } catch (e) {
    message.error(String(e))
  }
}

// Rebind a fresh conversation to another connection (§10.2). Namespace state
// resets; loadNamespace stays lazy — it only fetches if the new connection is
// already live, otherwise the db selector's pointerdown connects on demand.
async function onChangeConn(connId: string) {
  const s = session.value
  if (!s || connId === s.connId) return
  try {
    await agentApi.setConnection(s.id, connId)
  } catch (e) {
    message.error(String(e))
    return
  }
  s.connId = connId
  s.currentDb = ''
  s.currentSchema = ''
  allDatabases.value = []
  schemas.value = []
  tableNames.value = []
  schemasSupported.value = false
  await loadNamespace()
}

async function onChangeDb(db: string) {
  const s = session.value
  if (!s || db === currentDb.value) return
  currentDb.value = db
  currentSchema.value = ''
  if (schemasSupported.value && panelConn.value) {
    try { schemas.value = await metaStore.ensureSchemas(panelConn.value.id, db) } catch { schemas.value = [] }
  }
  try { await agentApi.setNamespace(s.id, db, '') } catch { /* best-effort */ }
  s.currentDb = db
  await loadTableNames()
  entries.value.push({ kind: 'system', id: entryId(), text: t('agent.panel.nsSwitched', { ns: db }) })
  scrollToBottom()
}
async function onChangeSchema(schema: string) {
  const s = session.value
  if (!s || schema === currentSchema.value) return
  currentSchema.value = schema
  try { await agentApi.setNamespace(s.id, currentDb.value, schema) } catch { /* best-effort */ }
  s.currentSchema = schema
  await loadTableNames()
  const ns = [currentDb.value, schema].filter(Boolean).join('.')
  entries.value.push({ kind: 'system', id: entryId(), text: t('agent.panel.nsSwitched', { ns }) })
  scrollToBottom()
}
// Switch the session's provider/model (§10.1). Takes effect next turn; on
// success mirror it onto the local session and drop a system notice line.
async function onChangeModel(providerId: string, model: string) {
  const s = session.value
  if (!s || (s.providerId === providerId && s.model === model)) return
  try {
    await agentApi.setSessionModel(s.id, providerId, model)
    s.providerId = providerId
    s.model = model
    entries.value.push({ kind: 'system', id: entryId(), text: t('agent.panel.modelSwitched', { model }) })
    scrollToBottom()
  } catch (e) {
    message.error(String(e))
  }
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

// --- input-height grip (between the messages area and the dock, §10.1) ---
// null = the composer auto-grows with content; a number = user-set height.
const composerRef = ref<InstanceType<typeof AgentComposer> | null>(null)
const inputH = ref<number | null>(null)
const inputResizing = ref(false)
let inputStartY = 0
let inputStartH = 0
function onInputGripDown(ev: PointerEvent) {
  if (ev.button !== 0) return
  ev.preventDefault()
  inputStartY = ev.clientY
  inputStartH = composerRef.value?.currentHeight() ?? INPUT_MIN_H
  inputResizing.value = true
  ;(ev.currentTarget as HTMLElement).setPointerCapture?.(ev.pointerId)
}
function onInputGripMove(ev: PointerEvent) {
  if (!inputResizing.value) return
  // Dragging up (clientY smaller) makes the bottom-docked input taller.
  inputH.value = Math.min(INPUT_MAX_H, Math.max(INPUT_MIN_H, inputStartH + (inputStartY - ev.clientY)))
}
function onInputGripUp() { inputResizing.value = false }
// Double-click returns to content-driven auto height.
function onInputGripReset() { inputH.value = null }

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
  offProviders = onProvidersChanged(() => {
    void listProviders().then((list) => { providers.value = list ?? [] }).catch(() => {})
  })
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onWindowResize)
  offEvents?.()
  offProviders?.()
})
// Note: the panel deliberately does NOT watch props.connection — its context
// follows the current session (§10.2); the main UI switching connections
// must not reset an open conversation.
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

    <!-- Empty state: no sessions and no connection to bind a new one to. -->
    <div v-if="!loading && !session && sessions.length === 0" class="empty">
      <AppIcon :src="botIcon" :size="40" class="empty-icon" />
      <div class="empty-title">{{ $t('agent.panel.emptyTitle') }}</div>
      <div class="empty-desc">{{ $t('agent.panel.emptyDesc') }}</div>
    </div>

    <!-- Full-page session history (§10.2). -->
    <AgentHistoryView
      v-else-if="view === 'history'"
      :sessions="sessions"
      :conns-by-id="connsById"
      :active-id="session?.id ?? ''"
      @back="onHistoryBack"
      @select="onSelectSession"
      @rename="onRenameSession"
      @delete="onDeleteSession"
      @clear="onClearHistory"
    />

    <template v-else>
      <AgentSessionHeader
        :session="session"
        :tokens="tokens"
        :watermark="watermark"
        :cost="cost"
        :compacting="compacting"
        @new-session="onNewSession"
        @open-history="openHistory"
        @compact="onCompact"
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

      <!-- Composer dock (§10.1): grants / context row / input / mode+model row. -->
      <div class="dock">
        <!-- Input-height grip on the dock's top edge (messages ↔ dock boundary). -->
        <div
          class="input-grip"
          :class="{ active: inputResizing }"
          :title="$t('agent.panel.inputResizeHint')"
          @pointerdown="onInputGripDown"
          @pointermove="onInputGripMove"
          @pointerup="onInputGripUp"
          @pointercancel="onInputGripUp"
          @dblclick="onInputGripReset"
        />
        <AgentGrants
          v-if="mode === 'agent'"
          :grants="grants"
          :readonly="isProd || orphan"
          @update="onChangeGrants"
        />
        <div v-if="orphan" class="dock-hint">{{ $t('agent.panel.orphanHint') }}</div>
        <div v-if="noProvider" class="provider-hint">
          <span class="provider-hint-text">{{ $t('agent.panel.noProviderHint') }}</span>
          <button type="button" class="provider-hint-btn" @click="openSettingsWindow('ai')">{{ $t('agent.panel.openProviderSettings') }}</button>
        </div>

        <!-- Connection + namespace context, above the input box. The database
             selector follows the connection inline (left-aligned, not pushed
             to the right edge); a driver logo marks the connection's type. -->
        <div class="ctx-row">
          <AppIcon class="conn-logo" :src="driverLogo(panelConn?.driver ?? '')" :size="14" />
          <!-- A fresh conversation picks its connection freely; once messages
               exist the binding is fixed and renders as a plain label. -->
          <select
            v-if="canPickConn"
            class="ns-select conn-select"
            :value="session?.connId ?? ''"
            :title="$t('agent.panel.selectConn')"
            @change="onChangeConn(($event.target as HTMLSelectElement).value)"
          >
            <option v-if="orphan" :value="session?.connId ?? ''" disabled>{{ $t('agent.panel.connDeleted') }}</option>
            <option v-for="c in connStore.connections" :key="c.id" :value="c.id">{{ c.name }}</option>
          </select>
          <span v-else class="conn" :title="orphan ? $t('agent.panel.connDeleted') : connectionName">
            <span class="conn-name" :class="{ deleted: orphan }">{{ orphan ? $t('agent.panel.connDeleted') : connectionName }}</span>
          </span>
          <span v-if="!orphan" class="env-badge" :class="`env-${envKind}`" :title="envTooltip">
            <AppIcon v-if="envKind === 'prod'" :src="lockIcon" :size="11" />
            <span class="env-text">{{ envLabel }}</span>
          </span>
          <select
            class="ns-select"
            :value="currentDb"
            :disabled="!session || orphan"
            @pointerdown="onRequestNamespace"
            @change="onChangeDb(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled>{{ $t('agent.panel.selectDb') }}</option>
            <!-- Lazy connect: before the list loads, the session's saved db still shows. -->
            <option v-if="currentDb && !databases.includes(currentDb)" :value="currentDb">{{ currentDb }}</option>
            <option v-for="d in databases" :key="d" :value="d">{{ d }}</option>
          </select>
          <select
            v-if="schemasSupported"
            class="ns-select"
            :value="currentSchema"
            :disabled="!session || orphan || schemas.length === 0"
            @change="onChangeSchema(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled>{{ $t('agent.panel.selectSchema') }}</option>
            <option v-for="sc in schemas" :key="sc" :value="sc">{{ sc }}</option>
          </select>
          <span class="spacer" />
        </div>

        <AgentComposer
          ref="composerRef"
          :busy="busy"
          :disabled="!session || !!txPending || orphan"
          :tables="tableNames"
          :tables-loading="nsLoading"
          :manual-height="inputH"
          @send="onSend"
          @stop="onStop"
          @need-tables="onRequestNamespace"
        />

        <!-- Ask|Agent + model switch, under the input box. -->
        <div class="mode-row">
          <div class="mode-seg">
            <button type="button" :class="{ active: mode === 'ask' }" @click="onChangeMode('ask')">{{ $t('agent.panel.modeAsk') }}</button>
            <button type="button" :class="{ active: mode === 'agent' }" @click="onChangeMode('agent')">{{ $t('agent.panel.modeAgent') }}</button>
          </div>
          <select
            class="ns-select model-select"
            :value="modelValue"
            :disabled="!session || providerGroups.length === 0"
            :title="$t('agent.panel.selectModel')"
            @change="onModelSelect(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled>{{ $t('agent.panel.selectModel') }}</option>
            <option v-if="session && !currentInList && session.model" :value="modelValue">{{ session.model }}</option>
            <optgroup v-for="g in providerGroups" :key="g.id" :label="g.name">
              <option v-for="m in g.models" :key="m.value" :value="m.value">{{ m.id }}</option>
            </optgroup>
          </select>
          <span class="spacer" />
        </div>
      </div>
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

/* --- composer dock (§10.1): context row / input / mode+model row --- */
.dock {
  position: relative;
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
  border-top: 1px solid var(--catdb-separator);
  background: var(--catdb-surface-chrome);
}
/* Input-height grip straddling the messages ↔ dock boundary. */
.input-grip {
  position: absolute;
  left: 0;
  right: 0;
  top: -4px;
  height: 8px;
  z-index: 10;
  cursor: row-resize;
}
.input-grip:hover, .input-grip.active { background: var(--catdb-accent-soft); }
.dock-hint {
  font-size: var(--catdb-fs-mini);
  color: var(--catdb-text-tertiary);
  text-align: center;
}
.provider-hint {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  padding: 4px 8px;
  border-radius: var(--catdb-rounded-sm);
  background: color-mix(in srgb, var(--catdb-warning) 12%, transparent);
}
.provider-hint-text {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
}
.provider-hint-btn {
  padding: 0;
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-accent);
  cursor: default;
}
.provider-hint-btn:hover { text-decoration: underline; }
.ctx-row, .mode-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.spacer { flex: 1 1 0; min-width: 0; }

.conn-logo { flex: 0 0 auto; }
.conn-select { max-width: 130px; }
.conn {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  max-width: 130px;
}
.conn-name {
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.conn-name.deleted { color: var(--catdb-text-tertiary); font-style: italic; }

/* Environment badge (闸 1). Small, semantic color, no shadow (DESIGN.md). */
.env-badge {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
  height: 16px;
  padding: 0 5px;
  border-radius: var(--catdb-rounded-sm);
  font-size: var(--catdb-fs-mini);
  line-height: 1;
  white-space: nowrap;
}
.env-badge .env-text { font-weight: 600; }
.env-prod {
  color: var(--catdb-error);
  background: color-mix(in srgb, var(--catdb-error) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--catdb-error) 32%, transparent);
}
.env-other {
  color: var(--catdb-text-secondary);
  background: var(--catdb-hover-fill);
}
.env-unmarked {
  color: var(--catdb-text-tertiary);
  background: var(--catdb-hover-fill);
}

.ns-select {
  height: 24px;
  max-width: 110px;
  font-size: var(--catdb-fs-small);
  padding: 1px 6px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  outline: none;
  cursor: default;
  font-family: inherit;
}
.ns-select:focus { border-color: var(--catdb-accent); }
.ns-select:disabled { opacity: 0.5; }
.model-select { max-width: 150px; }

.mode-seg {
  display: inline-flex;
  background: var(--catdb-hover-fill);
  border-radius: var(--catdb-rounded-sm);
  padding: 1px;
}
.mode-seg button {
  border: none;
  background: transparent;
  font: inherit;
  font-size: var(--catdb-fs-small);
  color: var(--catdb-text-secondary);
  height: 22px;
  padding: 0 12px;
  border-radius: var(--catdb-rounded-sm);
  cursor: default;
}
.mode-seg button.active {
  background: var(--catdb-surface-content);
  color: var(--catdb-text-primary);
  box-shadow: 0 0 0 0.5px var(--catdb-separator);
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
