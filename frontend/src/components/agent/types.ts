// Timeline entry model for the agent chat panel. The message area is a flat,
// ordered list of heterogeneous entries: user / assistant messages, tool step
// cards (start+end merged by callId), and system notice lines (namespace
// switch, interruption). Live streaming appends to the trailing assistant
// entry; a tool card breaks the streak so the model's post-tool text lands in
// a fresh assistant entry (AGENT_DESIGN.md §10.4).

export interface UserEntry { kind: 'user'; id: string; text: string }

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

export type Entry = UserEntry | AssistantEntry | ToolEntry | SystemEntry

let seq = 0
export function entryId(): string {
  seq += 1
  return 'e' + seq
}
