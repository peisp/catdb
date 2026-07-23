// api/agentTrace — front-end facade over AgentTraceService bindings.
//
// Dev-only agent interaction traces (internal/agenttrace): the Trace window
// lists traced sessions and renders their JSONL records. In production builds
// traceEnabled() is false and every entry point is hidden.
import { AgentTraceService } from '../../bindings/catdb/internal/services'
import { TraceSession as BoundTraceSession } from '../../bindings/catdb/internal/services/models'

export type TraceSession = BoundTraceSession

/** One parsed trace record (one JSONL line). Data is kind-specific. */
export interface TraceRecord {
  t: string
  kind: string
  data: Record<string, any>
}

export function traceEnabled(): Promise<boolean> {
  return AgentTraceService.TraceEnabled()
}

export function listTraceSessions(): Promise<TraceSession[]> {
  return AgentTraceService.ListTraceSessions()
}

/** Load and parse one session's trace; corrupt lines are skipped. */
export async function getTraceRecords(sessId: string): Promise<TraceRecord[]> {
  const raw = await AgentTraceService.GetTrace(sessId)
  const out: TraceRecord[] = []
  for (const line of raw.split('\n')) {
    const s = line.trim()
    if (!s) continue
    try {
      out.push(JSON.parse(s) as TraceRecord)
    } catch {
      // skip corrupt line
    }
  }
  return out
}

export function clearTraces(): Promise<void> {
  return AgentTraceService.ClearTraces()
}

/** Open (or focus) the Trace child window, optionally pre-selecting a session. */
export function openTraceWindow(sessId = ''): Promise<void> {
  return AgentTraceService.OpenTraceWindow(sessId)
}
