// Inject key + type for the SQL-block actions the panel provides to nested
// AgentSqlBlock components (copy is handled locally; insert / openTab need the
// session's connection, which only the panel knows).
import type { InjectionKey } from 'vue'

export interface AgentSqlActions {
  /** Append the SQL into the connection's active query editor. Returns false
   *  when there is no connection to target. */
  insert: (sql: string) => boolean
  /** Open the SQL in a fresh query tab on the session's connection. */
  openTab: (sql: string) => void
}

export const AGENT_SQL_ACTIONS: InjectionKey<AgentSqlActions> = Symbol('agentSqlActions')
