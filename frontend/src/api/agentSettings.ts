// api/agentSettings — front-end facade over AgentSettingsService bindings.
//
// Components import from here, never from `bindings/` directly (CLAUDE.md #1).
// Manages AI Agent Provider config: instances (CRUD) + keyring-backed API keys
// (write-only) + default provider/model + a connectivity test. Keys are never
// read back — HasProviderKey only reports a boolean "configured" state.
import { AgentSettingsService } from '../../bindings/catdb/internal/services'
import { ProviderConfig as BoundProviderConfig } from '../../bindings/catdb/internal/llmconfig/models'
import {
  AgentSettings as BoundAgentSettings,
  ModelPricing as BoundModelPricing,
} from '../../bindings/catdb/internal/llmconfig/models'
import { ModelInfo as BoundModelInfo } from '../../bindings/catdb/internal/llm/models'
import {
  AgentDefaults as BoundAgentDefaults,
  AuditQuery as BoundAuditQuery,
  AuditPage as BoundAuditPage,
  AuditExportResult as BoundAuditExportResult,
  FetchModelsRequest as BoundFetchModelsRequest,
} from '../../bindings/catdb/internal/services/models'
import { AgentAuditEntry as BoundAuditEntry } from '../../bindings/catdb/internal/storage/models'
import { on } from './events'

export type ProviderConfig = BoundProviderConfig
export type ModelInfo = BoundModelInfo
export type AgentDefaults = BoundAgentDefaults
export type AgentSettings = BoundAgentSettings
export type ModelPricing = BoundModelPricing
export type AuditQuery = BoundAuditQuery
export type AuditPage = BoundAuditPage
export type AuditEntry = BoundAuditEntry
export type AuditExportResult = BoundAuditExportResult
export type FetchModelsRequest = BoundFetchModelsRequest

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

/** Fired by the Go side after a provider is saved or deleted (any window). */
export function onProvidersChanged(cb: () => void): () => void {
  return on<null>('agent:providers-changed', () => cb())
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

/** Query the provider's live model list using draft config (key falls back to keyring when empty). */
export function fetchProviderModels(req: {
  id: string
  type: string
  baseURL: string
  key: string
}): Promise<ModelInfo[]> {
  return AgentSettingsService.FetchProviderModels(BoundFetchModelsRequest.createFrom(req))
}

// --- Agent runtime settings (privacy / limits / compaction / pricing) ---

export function getAgentSettings(): Promise<AgentSettings> {
  return AgentSettingsService.GetAgentSettings()
}

export function setAgentSettings(settings: AgentSettings): Promise<void> {
  return AgentSettingsService.SetAgentSettings(BoundAgentSettings.createFrom(settings))
}

// --- Audit ---

/** One page of audit entries, most recent first. Offset+Limit paginate. */
export function listAudit(q: Partial<AuditQuery>): Promise<AuditPage> {
  return AgentSettingsService.ListAudit(BoundAuditQuery.createFrom(q))
}

/** Delete audit entries created strictly before the given epoch-seconds. */
export function clearAudit(beforeUnixSec: number): Promise<void> {
  return AgentSettingsService.ClearAudit(beforeUnixSec)
}

/** Stream all matching audit entries to `path` (never crosses IPC as bulk). format: 'json' | 'csv'. */
export function exportAudit(
  q: Partial<AuditQuery>,
  format: 'json' | 'csv',
  path: string,
): Promise<AuditExportResult> {
  return AgentSettingsService.ExportAudit(BoundAuditQuery.createFrom(q), format, path)
}
