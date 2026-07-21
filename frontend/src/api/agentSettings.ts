// api/agentSettings — front-end facade over AgentSettingsService bindings.
//
// Components import from here, never from `bindings/` directly (CLAUDE.md #1).
// Manages AI Agent Provider config: instances (CRUD) + keyring-backed API keys
// (write-only) + default provider/model + a connectivity test. Keys are never
// read back — HasProviderKey only reports a boolean "configured" state.
import { AgentSettingsService } from '../../bindings/catdb/internal/services'
import { ProviderConfig as BoundProviderConfig } from '../../bindings/catdb/internal/llmconfig/models'
import { ModelInfo as BoundModelInfo } from '../../bindings/catdb/internal/llm/models'
import { AgentDefaults as BoundAgentDefaults } from '../../bindings/catdb/internal/services/models'

export type ProviderConfig = BoundProviderConfig
export type ModelInfo = BoundModelInfo
export type AgentDefaults = BoundAgentDefaults

/** A draft for SaveProvider: id empty → insert, id set → update. */
export interface ProviderDraft {
  id?: string
  name: string
  type: string
  baseURL: string
  models: ModelInfo[]
  defaultModel: string
}

export function listProviders(): Promise<ProviderConfig[]> {
  return AgentSettingsService.ListProviders()
}

export function saveProvider(draft: ProviderDraft): Promise<ProviderConfig> {
  return AgentSettingsService.SaveProvider(BoundProviderConfig.createFrom(draft))
}

export function deleteProvider(id: string): Promise<void> {
  return AgentSettingsService.DeleteProvider(id)
}

/** Store (write-only) the API key for a provider. Empty key is rejected server-side. */
export function setProviderKey(id: string, key: string): Promise<void> {
  return AgentSettingsService.SetProviderKey(id, key)
}

/** Whether a non-empty API key is stored — never reveals the key itself. */
export function hasProviderKey(id: string): Promise<boolean> {
  return AgentSettingsService.HasProviderKey(id)
}

export function getDefaults(): Promise<AgentDefaults> {
  return AgentSettingsService.GetDefaults()
}

export function setDefaults(providerId: string, model: string): Promise<void> {
  return AgentSettingsService.SetDefaults(providerId, model)
}

/** Probe connectivity with a minimal ping stream; resolves on success, rejects with the raw error. */
export function testProvider(id: string, model: string): Promise<void> {
  return AgentSettingsService.TestProvider(id, model)
}
