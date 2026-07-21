// api/agent — front-end façade over AgentService bindings + agent:* event
// stream (AGENT_DESIGN.md §13).
//
// Components import from here, never from `bindings/` or `@wailsio/runtime`
// directly (CLAUDE.md #1). The streaming turn output does NOT come back as a
// return value — it arrives over agent:* events, which this module collapses
// into a single per-session subscription.
import { AgentService } from '../../bindings/catdb/internal/services'
import type {
  AgentSession as BoundSession,
  AgentMessage as BoundMessage,
} from '../../bindings/catdb/internal/storage/models'
import { on } from './events'

export type AgentSession = BoundSession
export type AgentMessage = BoundMessage

// --- event payloads (camelCase, mirror the Go emit maps in internal/agent) ---
// seq: per-turn monotonic sequence stamped by the Go emitter — the Wails event
// channel does not guarantee delivery order, so this module reassembles by seq
// before dispatching to handlers (agent:error from a failed turn carries none).

export interface DeltaEvent { sessId: string; seq?: number; text: string }
export interface ThinkingEvent { sessId: string; seq?: number; text: string }
export interface ToolEvent {
  sessId: string
  seq?: number
  callId: string
  name: string
  phase: 'start' | 'end'
  summary?: string
}
export interface UsageEvent { sessId: string; seq?: number; tokensIn: number; tokensOut: number; watermark?: number }
export interface DoneEvent { sessId: string; seq?: number; stopReason: string }
export interface ErrorEvent { sessId: string; seq?: number; slug: string; detail: string }

// --- in-order dispatch --------------------------------------------------------
// One reorder state per session. Events dispatch strictly in seq order; a
// seq-less event (or a runaway gap) flushes whatever is buffered, in order —
// degrade to best-effort rather than stalling the stream.

interface StreamOrder { expected: number; buf: Map<number, () => void> }
const streamOrders = new Map<string, StreamOrder>()

function orderFor(sessId: string): StreamOrder {
  let s = streamOrders.get(sessId)
  if (!s) { s = { expected: 0, buf: new Map() }; streamOrders.set(sessId, s) }
  return s
}

/** Reset a session's reorder state — call at the start of each turn. */
function resetOrder(sessId: string) {
  const s = orderFor(sessId)
  s.expected = 0
  s.buf.clear()
}

function drainBuffered(s: StreamOrder) {
  for (const k of [...s.buf.keys()].sort((a, b) => a - b)) {
    const fn = s.buf.get(k)!
    s.buf.delete(k)
    s.expected = k + 1
    fn()
  }
}

function dispatchOrdered(sessId: string, seq: number | undefined, fn: () => void) {
  const s = orderFor(sessId)
  if (seq == null) { drainBuffered(s); fn(); return }
  if (seq < s.expected) return // already dispatched from the buffer — drop the duplicate
  s.buf.set(seq, fn)
  while (s.buf.has(s.expected)) {
    const next = s.buf.get(s.expected)!
    s.buf.delete(s.expected)
    s.expected++
    next()
  }
  // A hole this large means an event was lost, not delayed — don't stall.
  if (s.buf.size > 256) drainBuffered(s)
}

// --- session CRUD & control -------------------------------------------------

/** Open a new session bound to connID. mode is "ask" | "agent". */
export function createSession(connId: string, mode: 'ask' | 'agent'): Promise<AgentSession> {
  return Promise.resolve(AgentService.CreateSession(connId, mode))
}

/** Sessions of a connection, most recent first. */
export function listSessions(connId: string): Promise<AgentSession[]> {
  return Promise.resolve(AgentService.ListSessions(connId))
}

/** Full message history (compacted included) for rendering a restored session. */
export function getMessages(sessId: string): Promise<AgentMessage[]> {
  return Promise.resolve(AgentService.GetMessages(sessId))
}

export function renameSession(sessId: string, title: string): Promise<void> {
  return Promise.resolve(AgentService.RenameSession(sessId, title))
}

export function deleteSession(sessId: string): Promise<void> {
  return Promise.resolve(AgentService.DeleteSession(sessId))
}

export function setMode(sessId: string, mode: 'ask' | 'agent'): Promise<void> {
  return Promise.resolve(AgentService.SetMode(sessId, mode))
}

export function setGrants(sessId: string, grants: string[]): Promise<void> {
  return Promise.resolve(AgentService.SetGrants(sessId, grants))
}

/** Switch the session's selected database/schema (§10.2). */
export function setNamespace(sessId: string, db: string, schema = ''): Promise<void> {
  return Promise.resolve(AgentService.SetNamespace(sessId, db, schema))
}

export function setSessionModel(sessId: string, providerId: string, model: string): Promise<void> {
  return Promise.resolve(AgentService.SetSessionModel(sessId, providerId, model))
}

/** Cancel the session's running loop, if any. */
export function cancel(sessId: string): Promise<void> {
  return Promise.resolve(AgentService.Cancel(sessId))
}

/**
 * Run one agent turn. Returns a handle whose `done` promise settles when the
 * turn ends and whose `stop()` aborts it — mirroring the api/query cancel
 * pattern: cancelling the front-end promise routes to the Go ctx, and we also
 * fire Cancel so the loop is torn down even if the promise is already settled.
 */
export function sendMessage(sessId: string, text: string): { done: Promise<void>; stop: () => void } {
  resetOrder(sessId) // each turn's seq restarts at 0 (fresh emitter per run)
  const p = AgentService.SendMessage(sessId, text)
  return {
    done: Promise.resolve(p as PromiseLike<void>),
    stop: () => {
      try { (p as { cancel?: () => void }).cancel?.() } catch { /* ignore */ }
      void AgentService.Cancel(sessId)
    },
  }
}

// --- streaming subscription -------------------------------------------------

export interface AgentEventHandlers {
  onDelta?: (e: DeltaEvent) => void
  onThinking?: (e: ThinkingEvent) => void
  onTool?: (e: ToolEvent) => void
  onUsage?: (e: UsageEvent) => void
  onDone?: (e: DoneEvent) => void
  onError?: (e: ErrorEvent) => void
}

/**
 * Subscribe to a session's agent:* stream. Every handler is filtered by
 * sessId so a component only ever sees its own session's events. Returns a
 * single unsubscribe that detaches all of them.
 */
export function subscribe(sessId: string, handlers: AgentEventHandlers): () => void {
  const offs: Array<() => void> = []
  const bind = <T extends { sessId: string; seq?: number }>(name: string, cb?: (e: T) => void) => {
    if (!cb) return
    offs.push(on<T>(name, (d) => {
      if (d && d.sessId === sessId) dispatchOrdered(sessId, d.seq, () => cb(d))
    }))
  }
  bind<DeltaEvent>('agent:delta', handlers.onDelta)
  bind<ThinkingEvent>('agent:thinking', handlers.onThinking)
  bind<ToolEvent>('agent:tool', handlers.onTool)
  bind<UsageEvent>('agent:usage', handlers.onUsage)
  bind<DoneEvent>('agent:done', handlers.onDone)
  bind<ErrorEvent>('agent:error', handlers.onError)
  return () => { for (const off of offs) off() }
}

// --- persisted message content ----------------------------------------------
// Content column is a JSON blob the agent package serialized:
//   { text, thinking, toolCalls:[{id,name,args}], result:{callId,content,isError} }
// Storage does not interpret it; the panel parses per role.

export interface StoredCall { id: string; name: string; args?: unknown }
export interface StoredResult { callId: string; content: string; isError?: boolean }
export interface MessageContent {
  text?: string
  thinking?: string
  toolCalls?: StoredCall[]
  result?: StoredResult
}

export function parseContent(raw: string): MessageContent {
  if (!raw) return {}
  try {
    const c = JSON.parse(raw)
    return (c && typeof c === 'object') ? c as MessageContent : {}
  } catch {
    // Legacy / non-JSON content — treat the whole blob as plain text.
    return { text: raw }
  }
}
