// Timeline entry model for the agent chat panel. The message area is a flat,
// ordered list of heterogeneous entries: user / assistant messages, tool step
// cards (start+end merged by callId), and system notice lines (namespace
// switch, interruption). Live streaming appends to the trailing assistant
// entry; a tool card breaks the streak so the model's post-tool text lands in
// a fresh assistant entry (AGENT_DESIGN.md §10.4).

export interface UserEntry {
  kind: 'user'
  id: string
  text: string
  /** @table mentions attached to this turn (§10.3), rendered as chips above the bubble. */
  mentions?: string[]
}

export interface AssistantEntry {
  kind: 'assistant'
  id: string
  text: string
  thinking: string
  /** True while the turn is still streaming — render plain text; once false,
   *  render markdown + SQL blocks. */
  streaming: boolean
  /** Set on the trailing assistant entry when a turn ends. */
  stopReason?: string
  /** Final answer delivered despite failing the delivery-format check (§6/§8) —
   *  renders a soft warning line at the tail of the entry. */
  deliveryWarning?: boolean
}

export interface ToolEntry {
  kind: 'tool'
  id: string
  callId: string
  name: string
  phase: 'start' | 'end'
  summary: string
  isError: boolean
  /** JSON args string, when known (history). */
  args?: string
  /** Full tool result content, when known (history). */
  result?: string
}

export interface SystemEntry { kind: 'system'; id: string; text: string }

// Centered "context compacted" notice line (§9). count is the number of folded
// messages when known (live agent:compacted); undefined for restored summary
// messages, which render a generic line. Full history stays visible either way.
export interface CompactedEntry { kind: 'compacted'; id: string; count?: number }

// Statement approval card (§5 gate 4). Buttons live while pending; once decided
// the card freezes into a status line (approved / rejected + reason).
export interface ApprovalEntry {
  kind: 'approval'
  id: string
  approvalID: string
  sql: string
  class: string
  verb: string
  /** e.g. "no-where-clause" — red warning card + second-confirm semantics. */
  warning?: string
  /** EXPLAIN estimate (JSON string, may be empty) shown in a collapsible region. */
  explain?: string
  /** Only true offers the "auto-approve same verb" option (未标记 env never). */
  autoOffered?: boolean
  status: 'pending' | 'approved' | 'rejected'
  /** Scope chosen on approval: 'once' | 'task-verb'. */
  scope?: 'once' | 'task-verb'
  /** Reason given on rejection (may be empty). */
  reason?: string
}

// Task plan card (§6). Same pending → frozen lifecycle as ApprovalEntry.
export interface PlanEntry {
  kind: 'plan'
  id: string
  planID: string
  goal: string
  statements: string[]
  impact?: string
  status: 'pending' | 'approved' | 'rejected'
  reason?: string
}

// Inline query result table (§7 user path). columns + capped rows.
export interface ResultEntry {
  kind: 'result'
  id: string
  columns: string[]
  rows: unknown[][]
  truncated: boolean
}

export type Entry =
  | UserEntry
  | AssistantEntry
  | ToolEntry
  | SystemEntry
  | CompactedEntry
  | ApprovalEntry
  | PlanEntry
  | ResultEntry

let seq = 0
export function entryId(): string {
  seq += 1
  return 'e' + seq
}
