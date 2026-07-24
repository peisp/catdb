// Shared AgentSettings state for the split AI settings panels (privacy /
// limits / audit). SetAgentSettings overwrites ALL runtime fields together,
// so every panel loads the full object before editing its own slice and
// persists the whole thing back.
import { reactive } from 'vue'
import { agentSettings } from '../../api'
import type { AgentSettings } from '../../api/agentSettings'

export function useAgentRuntimeSettings() {
  const settings = reactive<AgentSettings>({
    privacySendRowData: true,
    maxIterations: 25,
    stmtTimeoutSec: 60,
    txIdleTimeoutSec: 600,
    llmResultRows: 50,
    sessionTokenBudget: 0,
    compactAuto: true,
    compactThreshold: 0.7,
    auditRetentionDays: 15,
    pricing: {},
  } as AgentSettings)

  async function load() {
    Object.assign(settings, await agentSettings.getAgentSettings())
  }

  async function persist() {
    await agentSettings.setAgentSettings({ ...settings })
  }

  return { settings, load, persist }
}
