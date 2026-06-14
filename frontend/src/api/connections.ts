// api/connections — front-end facade over ConnectionService bindings.
//
// Components import from here. Do not import the bindings module directly —
// they will move when wails3 changes its layout (CLAUDE.md #1).
import { ConnectionService } from '../../bindings/catdb/internal/services'
import type {
  ConnectionDraft as BoundDraft,
  DriverInfo as BoundDriverInfo,
} from '../../bindings/catdb/internal/services/models'
import type {
  ConnectionProfile as BoundProfile,
  Group as BoundGroup,
} from '../../bindings/catdb/internal/storage/models'

// Re-export the binding types so components have stable import names.
export type DriverInfo = BoundDriverInfo
export type ConnectionProfile = BoundProfile
export type ConnectionDraft = BoundDraft
export type Group = BoundGroup

export function listDrivers(): Promise<DriverInfo[]> {
  return ConnectionService.ListDrivers()
}

export function listConnections(): Promise<ConnectionProfile[]> {
  return ConnectionService.ListConnections()
}

export function getConnection(id: string): Promise<ConnectionProfile> {
  return ConnectionService.GetConnection(id)
}

export function saveConnection(draft: ConnectionDraft): Promise<ConnectionProfile> {
  return ConnectionService.SaveConnection(draft)
}

export function deleteConnection(id: string): Promise<void> {
  return ConnectionService.DeleteConnection(id)
}

export function listGroups(): Promise<Group[]> {
  return ConnectionService.ListGroups()
}

export function saveGroup(group: Group): Promise<Group> {
  return ConnectionService.SaveGroup(group)
}

export function deleteGroup(id: string): Promise<void> {
  return ConnectionService.DeleteGroup(id)
}

/** Test the draft without persisting. Resolves on success, rejects on failure. */
export function testConnection(draft: ConnectionDraft, signal?: AbortSignal): Promise<void> {
  const p = ConnectionService.TestConnection(draft)
  if (signal) {
    if (signal.aborted) p.cancel()
    else signal.addEventListener('abort', () => p.cancel(), { once: true })
  }
  return p
}

export function connect(id: string): Promise<void> {
  return ConnectionService.Connect(id)
}

export function disconnect(id: string): Promise<void> {
  return ConnectionService.Disconnect(id)
}

export function ping(id: string): Promise<void> {
  return ConnectionService.Ping(id)
}

export function isConnected(id: string): Promise<boolean> {
  return ConnectionService.IsConnected(id)
}

export function connectedIds(): Promise<string[]> {
  return ConnectionService.ConnectedIDs()
}
