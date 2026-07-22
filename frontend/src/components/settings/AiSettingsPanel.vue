<script setup lang="ts">
// AiSettingsPanel — the "AI" category of the settings window. M1 scope: two
// sections only — Provider management (CRUD + connectivity test + write-only
// API key) and Default model. Privacy / limits / audit sections come later.
//
// Talks only to api/agentSettings (never bindings directly, CLAUDE.md #1). API
// keys are write-only: HasProviderKey reports a boolean "configured" state and
// the key itself is never read back into the UI.
import { computed, onMounted, reactive, ref } from 'vue'
import { NButton, NInput, NInputNumber, NSelect, NCheckbox, NSwitch, useMessage } from 'naive-ui'
import { agentSettings, connections, system, dialogs } from '../../api'
import type { ProviderConfig, ModelInfo, AgentSettings, AuditEntry } from '../../api/agentSettings'
import { t as tr } from '../../i18n'

const message = useMessage()

const providers = ref<ProviderConfig[]>([])
const keyStatus = reactive<Record<string, boolean>>({})
const defaultProviderId = ref('')
const defaultModel = ref('')
const testingId = ref('')

type DraftModel = { ID: string; ContextWindow: number; SupportsTools: boolean }
interface Draft {
  id: string
  name: string
  type: string
  baseURL: string
  models: DraftModel[]
  defaultModel: string
  apiKey: string
  hasKey: boolean
}
const editing = ref<Draft | null>(null)
const saving = ref(false)
const fetchingModels = ref(false)
const modelFilter = ref('')
const filteredModels = computed(() => {
  const d = editing.value
  if (!d) return []
  const q = modelFilter.value.trim().toLowerCase()
  if (!q) return d.models
  return d.models.filter((m) => m.ID.toLowerCase().includes(q))
})

const TYPE_OPTIONS = computed(() => [
  { value: 'anthropic', label: tr('agent.settings.form.typeAnthropic') },
  { value: 'openai-compat', label: tr('agent.settings.form.typeOpenAICompat') },
])

const defaultProviderOptions = computed(() => [
  { value: '', label: tr('agent.settings.defaults.none') },
  ...providers.value.map((p) => ({ value: p.id, label: p.name || p.id })),
])
const defaultModelOptions = computed(() => {
  const p = providers.value.find((x) => x.id === defaultProviderId.value)
  const models = p?.models ?? []
  return models.map((m) => ({ value: m.ID, label: m.ID }))
})

function providerTypeLabel(type: string): string {
  return type === 'anthropic'
    ? tr('agent.settings.form.typeAnthropic')
    : tr('agent.settings.form.typeOpenAICompat')
}

async function load() {
  try {
    providers.value = await agentSettings.listProviders()
    await Promise.all(
      providers.value.map(async (p) => {
        keyStatus[p.id] = await agentSettings.hasProviderKey(p.id)
      }),
    )
    const d = await agentSettings.getDefaults()
    defaultProviderId.value = d.providerId
    defaultModel.value = d.model
  } catch (e) {
    message.error(tr('agent.settings.providers.loadFailed', { error: String(e) }))
  }
}
onMounted(load)

function newDraft(): Draft {
  return { id: '', name: '', type: 'anthropic', baseURL: '', models: [], defaultModel: '', apiKey: '', hasKey: false }
}

function startAdd() {
  modelFilter.value = ''
  editing.value = newDraft()
}
function startEdit(p: ProviderConfig) {
  modelFilter.value = ''
  editing.value = {
    id: p.id,
    name: p.name,
    type: p.type,
    baseURL: p.baseURL,
    models: p.models.map((m) => ({ ID: m.ID, ContextWindow: m.ContextWindow, SupportsTools: m.SupportsTools })),
    defaultModel: p.defaultModel,
    apiKey: '',
    hasKey: keyStatus[p.id] ?? false,
  }
}
function cancelEdit() {
  editing.value = null
}

function addModelRow() {
  modelFilter.value = ''
  editing.value?.models.push({ ID: '', ContextWindow: 128000, SupportsTools: true })
}
function removeModel(m: DraftModel) {
  const d = editing.value
  if (!d) return
  const idx = d.models.indexOf(m)
  if (idx !== -1) d.models.splice(idx, 1)
}

async function fetchModels() {
  const d = editing.value
  if (!d) return
  if (d.type === 'openai-compat' && !d.baseURL.trim()) {
    message.error(tr('agent.settings.form.baseUrlRequired'))
    return
  }
  fetchingModels.value = true
  try {
    const fetched = await agentSettings.fetchProviderModels({
      id: d.id,
      type: d.type,
      baseURL: d.baseURL.trim(),
      key: d.apiKey.trim(),
    })
    const existingIds = new Set(d.models.map((m) => m.ID.trim()))
    let added = 0
    for (const m of fetched) {
      if (existingIds.has(m.ID.trim())) continue
      d.models.push({ ID: m.ID, ContextWindow: m.ContextWindow || 128000, SupportsTools: m.SupportsTools })
      existingIds.add(m.ID.trim())
      added++
    }
    if (!d.defaultModel.trim() && d.models.length) {
      d.defaultModel = d.models[0].ID
    }
    if (added > 0) {
      modelFilter.value = ''
      message.success(tr('agent.settings.form.fetchModelsOk', { n: added }))
    } else {
      message.info(tr('agent.settings.form.fetchModelsNone'))
    }
  } catch (e) {
    message.error(tr('agent.settings.form.fetchModelsFailed', { error: String(e) }))
  } finally {
    fetchingModels.value = false
  }
}

function validate(d: Draft): string | null {
  if (!d.name.trim()) return tr('agent.settings.form.nameRequired')
  if (d.type === 'openai-compat' && !d.baseURL.trim()) return tr('agent.settings.form.baseUrlRequired')
  if (d.models.length === 0) return tr('agent.settings.form.noModels')
  if (d.models.some((m) => !m.ID.trim())) return tr('agent.settings.form.modelIdRequired')
  return null
}

async function saveDraft() {
  const d = editing.value
  if (!d) return
  const err = validate(d)
  if (err) {
    message.error(err)
    return
  }
  saving.value = true
  try {
    const models: ModelInfo[] = d.models.map((m) => ({
      ID: m.ID.trim(),
      ContextWindow: m.ContextWindow || 0,
      SupportsTools: m.SupportsTools,
    })) as unknown as ModelInfo[]
    const saved = await agentSettings.saveProvider({
      id: d.id || undefined,
      name: d.name.trim(),
      type: d.type,
      baseURL: d.baseURL.trim(),
      models,
      defaultModel: d.defaultModel,
    })
    if (d.apiKey.trim()) {
      await agentSettings.setProviderKey(saved.id, d.apiKey.trim())
    }
    editing.value = null
    await load()
    message.success(tr('common.saved'))
  } catch (e) {
    message.error(tr('agent.settings.providers.saveFailed', { error: String(e) }))
  } finally {
    saving.value = false
  }
}

async function removeProvider(p: ProviderConfig) {
  const choice = await dialogs.confirm({
    title: tr('agent.settings.providers.deleteConfirmTitle'),
    message: tr('agent.settings.providers.deleteConfirm', { name: p.name || p.id }),
    buttons: [
      { value: 'cancel', label: tr('common.cancel'), isCancel: true },
      { value: 'delete', label: tr('common.delete'), isDefault: true },
    ],
  })
  if (choice !== 'delete') return
  try {
    await agentSettings.deleteProvider(p.id)
    await load()
    message.success(tr('common.deleted'))
  } catch (e) {
    message.error(tr('agent.settings.providers.deleteFailed', { error: String(e) }))
  }
}

async function testProvider(p: ProviderConfig) {
  const model = p.defaultModel || p.models[0]?.ID
  if (!model) {
    message.error(tr('agent.settings.form.noModels'))
    return
  }
  testingId.value = p.id
  try {
    await agentSettings.testProvider(p.id, model)
    message.success(tr('agent.settings.providers.testOk'))
  } catch (e) {
    message.error(tr('agent.settings.providers.testFailed', { error: String(e) }))
  } finally {
    testingId.value = ''
  }
}

async function saveDefaults() {
  try {
    await agentSettings.setDefaults(defaultProviderId.value, defaultProviderId.value ? defaultModel.value : '')
    message.success(tr('agent.settings.defaults.saved'))
  } catch (e) {
    message.error(tr('agent.settings.defaults.saveFailed', { error: String(e) }))
  }
}

function onDefaultProviderChange(v: string) {
  defaultProviderId.value = v
  // Reset the model to the provider's default (or first) so the pair stays valid.
  const p = providers.value.find((x) => x.id === v)
  defaultModel.value = p ? p.defaultModel || p.models[0]?.ID || '' : ''
}

// ── Agent runtime settings (privacy / limits / compaction / pricing) ──
const settings = reactive<AgentSettings>({
  privacySendRowData: true,
  maxIterations: 25,
  stmtTimeoutSec: 60,
  txIdleTimeoutSec: 600,
  llmResultRows: 50,
  sessionTokenBudget: 0,
  compactAuto: true,
  compactThreshold: 0.7,
  pricing: {},
} as AgentSettings)

// Pricing map ⇄ editable rows. The map key is the model id.
type PricingRow = { model: string; inputPer1M: number; outputPer1M: number; cacheReadPer1M: number }
const pricingRows = ref<PricingRow[]>([])

async function loadSettings() {
  try {
    const s = await agentSettings.getAgentSettings()
    Object.assign(settings, s)
    pricingRows.value = Object.entries(s.pricing ?? {}).map(([model, p]) => ({
      model,
      inputPer1M: p?.inputPer1M ?? 0,
      outputPer1M: p?.outputPer1M ?? 0,
      cacheReadPer1M: p?.cacheReadPer1M ?? 0,
    }))
  } catch (e) {
    message.error(tr('agent.settings.limits.saveFailed', { error: String(e) }))
  }
}

function addPricingRow() {
  pricingRows.value.push({ model: '', inputPer1M: 0, outputPer1M: 0, cacheReadPer1M: 0 })
}
function removePricingRow(i: number) {
  pricingRows.value.splice(i, 1)
}

function collectPricing(): Record<string, { inputPer1M: number; outputPer1M: number; cacheReadPer1M: number }> {
  const out: Record<string, { inputPer1M: number; outputPer1M: number; cacheReadPer1M: number }> = {}
  for (const r of pricingRows.value) {
    const model = r.model.trim()
    if (!model) continue
    out[model] = {
      inputPer1M: r.inputPer1M || 0,
      outputPer1M: r.outputPer1M || 0,
      cacheReadPer1M: r.cacheReadPer1M || 0,
    }
  }
  return out
}

async function persistSettings(): Promise<boolean> {
  settings.pricing = collectPricing() as AgentSettings['pricing']
  await agentSettings.setAgentSettings({ ...settings })
  return true
}

async function savePrivacy() {
  try {
    await persistSettings()
    message.success(tr('agent.settings.privacy.saved'))
  } catch (e) {
    message.error(tr('agent.settings.privacy.saveFailed', { error: String(e) }))
  }
}

async function saveLimits() {
  try {
    await persistSettings()
    message.success(tr('agent.settings.limits.saved'))
  } catch (e) {
    message.error(tr('agent.settings.limits.saveFailed', { error: String(e) }))
  }
}

// ── Audit ──
const auditConns = ref<{ value: string; label: string }[]>([])
const auditConnId = ref('')
const auditSinceDays = ref(0)
const auditEntries = ref<AuditEntry[]>([])
const auditPage = ref(0)
const auditHasMore = ref(false)
const auditLoading = ref(false)
const clearDays = ref(30)
const AUDIT_PAGE_SIZE = 50

const auditConnOptions = computed(() => [
  { value: '', label: tr('agent.settings.audit.filterAllConns') },
  ...auditConns.value,
])

function auditQuery(offset: number) {
  const q: { connId: string; offset: number; limit: number; sinceUnix?: number } = {
    connId: auditConnId.value,
    offset,
    limit: AUDIT_PAGE_SIZE,
  }
  if (auditSinceDays.value > 0) {
    q.sinceUnix = Math.floor(Date.now() / 1000) - auditSinceDays.value * 86400
  }
  return q
}

async function loadAuditConns() {
  try {
    const list = await connections.listConnections()
    auditConns.value = list.map((c) => ({ value: c.id, label: c.name || c.id }))
  } catch {
    // Non-fatal: the filter just shows "all connections".
  }
}

async function loadAudit(page = 0) {
  auditLoading.value = true
  try {
    const res = await agentSettings.listAudit(auditQuery(page * AUDIT_PAGE_SIZE))
    auditEntries.value = res.entries ?? []
    auditHasMore.value = res.hasMore
    auditPage.value = page
  } catch (e) {
    message.error(tr('agent.settings.audit.loadFailed', { error: String(e) }))
  } finally {
    auditLoading.value = false
  }
}

function onAuditFilterChange() {
  loadAudit(0)
}

async function exportAudit(format: 'json' | 'csv') {
  const ext = format === 'csv' ? 'csv' : 'json'
  const filter =
    format === 'csv'
      ? { displayName: 'CSV files (*.csv)', pattern: '*.csv' }
      : { displayName: 'JSON files (*.json)', pattern: '*.json' }
  let path = ''
  try {
    path = await system.pickSaveFile(tr('agent.settings.audit.export'), `agent-audit.${ext}`, [filter])
  } catch (e) {
    message.error(tr('agent.settings.audit.exportFailed', { error: String(e) }))
    return
  }
  if (!path) return
  try {
    const res = await agentSettings.exportAudit(auditQuery(0), format, path)
    message.success(tr('agent.settings.audit.exported', { n: res.rows }))
  } catch (e) {
    message.error(tr('agent.settings.audit.exportFailed', { error: String(e) }))
  }
}

async function clearAudit() {
  const days = clearDays.value
  const choice = await dialogs.confirm({
    title: tr('agent.settings.audit.clearConfirmTitle'),
    message: tr('agent.settings.audit.clearConfirm', { days }),
    buttons: [
      { value: 'cancel', label: tr('common.cancel'), isCancel: true },
      { value: 'clear', label: tr('agent.settings.audit.clear'), isDefault: true },
    ],
  })
  if (choice !== 'clear') return
  try {
    const before = Math.floor(Date.now() / 1000) - days * 86400
    await agentSettings.clearAudit(before)
    message.success(tr('agent.settings.audit.cleared'))
    await loadAudit(0)
  } catch (e) {
    message.error(tr('agent.settings.audit.clearFailed', { error: String(e) }))
  }
}

function fmtTime(v: unknown): string {
  const d = new Date(v as string)
  return isNaN(d.getTime()) ? String(v) : d.toLocaleString()
}
function truncSql(s: string): string {
  return s.length > 80 ? s.slice(0, 80) + '…' : s
}

onMounted(() => {
  loadSettings()
  loadAuditConns()
  loadAudit(0)
})
</script>

<template>
  <div class="ai-panel">
    <!-- ── Provider management ── -->
    <section class="section">
      <div class="section-head">
        <h3 class="section-title">{{ $t('agent.settings.providers.title') }}</h3>
        <n-button size="small" @click="startAdd">{{ $t('agent.settings.providers.add') }}</n-button>
      </div>

      <p v-if="providers.length === 0 && !editing" class="empty">
        {{ $t('agent.settings.providers.empty') }}
      </p>

      <ul v-if="providers.length" class="provider-list">
        <li v-for="p in providers" :key="p.id" class="provider-row">
          <div class="provider-info">
            <span class="provider-name">{{ p.name || p.id }}</span>
            <span class="provider-type">{{ providerTypeLabel(p.type) }}</span>
            <span class="provider-key" :class="{ ok: keyStatus[p.id] }">
              {{ keyStatus[p.id] ? $t('agent.settings.providers.keyConfigured') : $t('agent.settings.providers.keyMissing') }}
            </span>
          </div>
          <div class="provider-actions">
            <n-button size="tiny" :loading="testingId === p.id" @click="testProvider(p)">
              {{ testingId === p.id ? $t('agent.settings.providers.testing') : $t('agent.settings.providers.test') }}
            </n-button>
            <n-button size="tiny" @click="startEdit(p)">{{ $t('agent.settings.providers.edit') }}</n-button>
            <n-button size="tiny" @click="removeProvider(p)">{{ $t('agent.settings.providers.delete') }}</n-button>
          </div>
        </li>
      </ul>

      <!-- Inline editor -->
      <div v-if="editing" class="editor">
        <div class="editor-head">
          {{ editing.id ? $t('agent.settings.form.titleEdit') : $t('agent.settings.form.titleAdd') }}
        </div>

        <div class="form-field">
          <label class="form-label">{{ $t('agent.settings.form.name') }}</label>
          <n-input v-model:value="editing.name" size="small" :placeholder="$t('agent.settings.form.namePlaceholder')" />
        </div>

        <div class="form-field">
          <label class="form-label">{{ $t('agent.settings.form.type') }}</label>
          <n-select v-model:value="editing.type" size="small" :options="TYPE_OPTIONS" />
        </div>

        <div class="form-field">
          <label class="form-label">{{ $t('agent.settings.form.baseUrl') }}</label>
          <n-input
            v-model:value="editing.baseURL"
            size="small"
            :placeholder="editing.type === 'anthropic' ? $t('agent.settings.form.baseUrlPlaceholderAnthropic') : $t('agent.settings.form.baseUrlPlaceholderOpenAI')"
          />
        </div>

        <div class="form-field">
          <label class="form-label">{{ $t('agent.settings.form.apiKey') }}</label>
          <n-input
            v-model:value="editing.apiKey"
            type="password"
            show-password-on="click"
            size="small"
            :placeholder="$t('agent.settings.form.apiKeyPlaceholder')"
          />
        </div>
        <p class="form-hint">
          {{ editing.hasKey ? $t('agent.settings.form.apiKeyConfigured') : $t('agent.settings.form.apiKeyLeaveBlank') }}
        </p>

        <div class="models-block">
          <div class="models-head">
            <label class="form-label">{{ $t('agent.settings.form.models') }} ({{ editing.models.length }})</label>
            <div class="models-head-actions">
              <n-button size="tiny" :loading="fetchingModels" @click="fetchModels">
                {{ $t('agent.settings.form.fetchModels') }}
              </n-button>
              <n-button size="tiny" @click="addModelRow">{{ $t('agent.settings.form.addModel') }}</n-button>
            </div>
          </div>
          <n-input
            v-if="editing.models.length > 8 || modelFilter"
            v-model:value="modelFilter"
            size="small"
            clearable
            :placeholder="$t('agent.settings.form.modelFilter')"
          />
          <div v-if="editing.models.length" class="model-head">
            <span class="mh-id">{{ $t('agent.settings.form.modelId') }}</span>
            <span class="mh-ctx">{{ $t('agent.settings.form.contextWindow') }}</span>
            <span class="mh-tools">{{ $t('agent.settings.form.supportsTools') }}</span>
            <span class="mh-del"></span>
          </div>
          <div v-if="editing.models.length" class="model-list-wrap">
            <div v-for="(m, i) in filteredModels" :key="i" class="model-row">
              <n-input v-model:value="m.ID" size="small" class="model-id" :placeholder="$t('agent.settings.form.modelIdPlaceholder')" />
              <n-input-number v-model:value="m.ContextWindow" size="small" class="model-ctx" :min="0" :show-button="false" :placeholder="$t('agent.settings.form.contextWindow')" />
              <n-checkbox v-model:checked="m.SupportsTools" class="model-tools" />
              <n-button size="tiny" quaternary class="model-del" @click="removeModel(m)">{{ $t('agent.settings.form.removeModel') }}</n-button>
            </div>
          </div>
        </div>

        <div class="form-field">
          <label class="form-label">{{ $t('agent.settings.form.defaultModel') }}</label>
          <n-select
            v-model:value="editing.defaultModel"
            size="small"
            filterable
            :options="editing.models.filter((m) => m.ID.trim()).map((m) => ({ value: m.ID, label: m.ID }))"
          />
        </div>

        <div class="editor-actions">
          <n-button size="small" @click="cancelEdit">{{ $t('common.cancel') }}</n-button>
          <n-button size="small" type="primary" :loading="saving" @click="saveDraft">{{ $t('common.save') }}</n-button>
        </div>
      </div>
    </section>

    <!-- ── Default model ── -->
    <section class="section">
      <div class="section-head">
        <h3 class="section-title">{{ $t('agent.settings.defaults.title') }}</h3>
      </div>
      <p class="hint">{{ $t('agent.settings.defaults.hint') }}</p>
      <div class="form-field">
        <label class="form-label">{{ $t('agent.settings.defaults.provider') }}</label>
        <n-select :value="defaultProviderId" size="small" filterable :options="defaultProviderOptions" @update:value="onDefaultProviderChange" />
      </div>
      <div class="form-field">
        <label class="form-label">{{ $t('agent.settings.defaults.model') }}</label>
        <n-select v-model:value="defaultModel" size="small" filterable :options="defaultModelOptions" :disabled="!defaultProviderId" />
      </div>
      <div class="editor-actions">
        <n-button size="small" @click="saveDefaults">{{ $t('common.save') }}</n-button>
      </div>
    </section>

    <!-- ── Privacy ── -->
    <section class="section">
      <div class="section-head">
        <h3 class="section-title">{{ $t('agent.settings.privacy.title') }}</h3>
      </div>
      <div class="switch-row">
        <n-switch v-model:value="settings.privacySendRowData" size="small" />
        <span class="switch-label">{{ $t('agent.settings.privacy.sendRowData') }}</span>
      </div>
      <p class="hint">{{ $t('agent.settings.privacy.sendRowDataHint') }}</p>
      <div class="editor-actions">
        <n-button size="small" @click="savePrivacy">{{ $t('common.save') }}</n-button>
      </div>
    </section>

    <!-- ── Limits & Compaction ── -->
    <section class="section">
      <div class="section-head">
        <h3 class="section-title">{{ $t('agent.settings.limits.title') }}</h3>
      </div>

      <div class="limit-row">
        <label class="form-label">{{ $t('agent.settings.limits.maxIterations') }}</label>
        <n-input-number v-model:value="settings.maxIterations" size="small" class="limit-input" :min="1" />
      </div>
      <div class="limit-row">
        <label class="form-label">{{ $t('agent.settings.limits.stmtTimeout') }}</label>
        <n-input-number v-model:value="settings.stmtTimeoutSec" size="small" class="limit-input" :min="1">
          <template #suffix>{{ $t('agent.settings.limits.secondsUnit') }}</template>
        </n-input-number>
      </div>
      <div class="limit-row">
        <label class="form-label">{{ $t('agent.settings.limits.txIdleTimeout') }}</label>
        <n-input-number v-model:value="settings.txIdleTimeoutSec" size="small" class="limit-input" :min="1">
          <template #suffix>{{ $t('agent.settings.limits.secondsUnit') }}</template>
        </n-input-number>
      </div>
      <div class="limit-row">
        <label class="form-label">{{ $t('agent.settings.limits.llmResultRows') }}</label>
        <n-input-number v-model:value="settings.llmResultRows" size="small" class="limit-input" :min="1">
          <template #suffix>{{ $t('agent.settings.limits.rowsUnit') }}</template>
        </n-input-number>
      </div>
      <div class="limit-row">
        <label class="form-label">{{ $t('agent.settings.limits.sessionTokenBudget') }}</label>
        <n-input-number v-model:value="settings.sessionTokenBudget" size="small" class="limit-input" :min="0" />
        <span class="unit-hint">{{ $t('agent.settings.limits.sessionTokenBudgetHint') }}</span>
      </div>

      <div class="switch-row">
        <n-switch v-model:value="settings.compactAuto" size="small" />
        <span class="switch-label">{{ $t('agent.settings.limits.compactAuto') }}</span>
      </div>
      <div class="limit-row">
        <label class="form-label">{{ $t('agent.settings.limits.compactThreshold') }}</label>
        <n-input-number
          v-model:value="settings.compactThreshold"
          size="small"
          class="limit-input"
          :min="0"
          :max="1"
          :step="0.05"
        />
        <span class="unit-hint">{{ $t('agent.settings.limits.compactThresholdHint') }}</span>
      </div>

      <!-- Pricing table -->
      <div class="pricing-block">
        <div class="models-head">
          <label class="form-label">{{ $t('agent.settings.pricing.title') }}</label>
          <n-button size="tiny" @click="addPricingRow">{{ $t('agent.settings.pricing.addRow') }}</n-button>
        </div>
        <p class="hint">{{ $t('agent.settings.pricing.hint') }}</p>
        <p v-if="pricingRows.length === 0" class="empty">{{ $t('agent.settings.pricing.empty') }}</p>
        <div v-if="pricingRows.length" class="pricing-head">
          <span class="pc-model">{{ $t('agent.settings.pricing.model') }}</span>
          <span class="pc-num">{{ $t('agent.settings.pricing.input') }}</span>
          <span class="pc-num">{{ $t('agent.settings.pricing.output') }}</span>
          <span class="pc-num">{{ $t('agent.settings.pricing.cacheRead') }}</span>
          <span class="pc-del"></span>
        </div>
        <div v-for="(r, i) in pricingRows" :key="i" class="pricing-row">
          <n-input v-model:value="r.model" size="small" class="pc-model" :placeholder="$t('agent.settings.pricing.modelPlaceholder')" />
          <n-input-number v-model:value="r.inputPer1M" size="small" class="pc-num" :min="0" :show-button="false" />
          <n-input-number v-model:value="r.outputPer1M" size="small" class="pc-num" :min="0" :show-button="false" />
          <n-input-number v-model:value="r.cacheReadPer1M" size="small" class="pc-num" :min="0" :show-button="false" />
          <n-button size="tiny" quaternary class="pc-del" @click="removePricingRow(i)">{{ $t('agent.settings.pricing.remove') }}</n-button>
        </div>
        <p v-if="pricingRows.length" class="unit-hint">{{ $t('agent.settings.pricing.perMillion') }}</p>
      </div>

      <div class="editor-actions">
        <n-button size="small" @click="saveLimits">{{ $t('common.save') }}</n-button>
      </div>
    </section>

    <!-- ── Audit ── -->
    <section class="section">
      <div class="section-head">
        <h3 class="section-title">{{ $t('agent.settings.audit.title') }}</h3>
      </div>

      <div class="audit-filters">
        <div class="form-field audit-filter-field">
          <label class="form-label">{{ $t('agent.settings.audit.filterConn') }}</label>
          <n-select
            v-model:value="auditConnId"
            size="small"
            :options="auditConnOptions"
            @update:value="onAuditFilterChange"
          />
        </div>
        <n-button size="small" @click="loadAudit(auditPage)">{{ $t('agent.settings.audit.refresh') }}</n-button>
        <n-button size="small" @click="exportAudit('json')">{{ $t('agent.settings.audit.exportJson') }}</n-button>
        <n-button size="small" @click="exportAudit('csv')">{{ $t('agent.settings.audit.exportCsv') }}</n-button>
      </div>

      <div class="audit-table-wrap">
        <table class="audit-table">
          <thead>
            <tr>
              <th>{{ $t('agent.settings.audit.colTime') }}</th>
              <th>{{ $t('agent.settings.audit.colConn') }}</th>
              <th>{{ $t('agent.settings.audit.colClass') }}</th>
              <th>{{ $t('agent.settings.audit.colSql') }}</th>
              <th>{{ $t('agent.settings.audit.colStatus') }}</th>
              <th class="num">{{ $t('agent.settings.audit.colRows') }}</th>
              <th class="num">{{ $t('agent.settings.audit.colDuration') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="e in auditEntries" :key="e.id">
              <td>{{ fmtTime(e.createdAt) }}</td>
              <td>{{ e.connId }}</td>
              <td>{{ e.class }}</td>
              <td class="sql-cell" :title="e.sql">{{ truncSql(e.sql) }}</td>
              <td>{{ e.status }}</td>
              <td class="num">{{ e.rows ?? '' }}</td>
              <td class="num">{{ e.durationMs != null ? e.durationMs + ' ms' : '' }}</td>
            </tr>
          </tbody>
        </table>
        <p v-if="!auditEntries.length && !auditLoading" class="empty">{{ $t('agent.settings.audit.empty') }}</p>
      </div>

      <div class="audit-footer">
        <div class="audit-paging">
          <n-button size="tiny" :disabled="auditPage === 0" @click="loadAudit(auditPage - 1)">
            {{ $t('agent.settings.audit.prev') }}
          </n-button>
          <span class="page-label">{{ $t('agent.settings.audit.page', { n: auditPage + 1 }) }}</span>
          <n-button size="tiny" :disabled="!auditHasMore" @click="loadAudit(auditPage + 1)">
            {{ $t('agent.settings.audit.next') }}
          </n-button>
        </div>
        <div class="audit-clear">
          <label class="form-label">{{ $t('agent.settings.audit.clearDaysLabel') }}</label>
          <n-input-number v-model:value="clearDays" size="small" class="limit-input" :min="0" />
          <n-button size="small" @click="clearAudit">{{ $t('agent.settings.audit.clear') }}</n-button>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.ai-panel {
  display: flex;
  flex-direction: column;
  gap: 24px;
}
.section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.section-title {
  margin: 0;
  font-size: var(--catdb-fs-body);
  font-weight: 600;
}
.empty {
  margin: 0;
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
}
.hint {
  margin: 0 0 4px;
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
}

.provider-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.provider-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 10px;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
}
.provider-info {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}
.provider-name {
  font-size: var(--catdb-fs-body);
  font-weight: 600;
}
.provider-type {
  font-size: var(--catdb-fs-small);
  opacity: 0.6;
}
.provider-key {
  font-size: var(--catdb-fs-small);
  opacity: 0.6;
}
.provider-key.ok {
  color: var(--catdb-success, #18a058);
  opacity: 1;
}
.provider-actions {
  display: flex;
  gap: 6px;
  flex: 0 0 auto;
}

.editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  margin-top: 6px;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  background: var(--catdb-surface-content);
}
.editor-head {
  font-size: var(--catdb-fs-body);
  font-weight: 600;
}
.form-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.form-label {
  font-size: var(--catdb-fs-small);
  opacity: 0.85;
}
.form-hint {
  margin: 0;
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
}
.models-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.models-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.models-head-actions {
  display: flex;
  gap: 6px;
}
.model-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--catdb-fs-small);
  opacity: 0.6;
  /* Match .model-list-wrap's 6px padding + 1px border so columns line up. */
  padding: 0 7px;
}
.mh-id {
  flex: 1 1 auto;
}
.mh-ctx {
  flex: 0 0 130px;
}
.mh-tools {
  flex: 0 0 70px;
  text-align: center;
}
.mh-del {
  flex: 0 0 64px;
}
.model-list-wrap {
  max-height: 260px;
  overflow-y: auto;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.model-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.model-id {
  flex: 1 1 auto;
}
.model-ctx {
  flex: 0 0 130px;
}
.model-tools {
  flex: 0 0 70px;
  display: flex;
  justify-content: center;
}
.model-del {
  flex: 0 0 64px;
}
.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}

/* privacy / limits */
.switch-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.switch-label {
  font-size: var(--catdb-fs-body);
}
.limit-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
.limit-row .form-label {
  flex: 0 0 200px;
}
.limit-input {
  flex: 0 0 150px;
}
.unit-hint {
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
}

/* pricing */
.pricing-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 4px;
}
.pricing-head,
.pricing-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.pricing-head {
  font-size: var(--catdb-fs-small);
  opacity: 0.6;
  padding: 0 2px;
}
.pc-model {
  flex: 1 1 auto;
  min-width: 0;
}
.pc-num {
  flex: 0 0 110px;
}
.pc-del {
  flex: 0 0 64px;
}

/* audit */
.audit-filters {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}
.audit-filter-field {
  flex: 1 1 auto;
  max-width: 280px;
}
.audit-table-wrap {
  overflow-x: auto;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
}
.audit-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--catdb-fs-small);
}
.audit-table th,
.audit-table td {
  text-align: left;
  padding: 5px 8px;
  border-bottom: 1px solid var(--catdb-separator);
  white-space: nowrap;
}
.audit-table th {
  font-weight: 600;
  opacity: 0.7;
}
.audit-table tbody tr:last-child td {
  border-bottom: none;
}
.audit-table .num {
  text-align: right;
}
.audit-table .sql-cell {
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  font-family: var(--catdb-font-mono, monospace);
}
.audit-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.audit-paging {
  display: flex;
  align-items: center;
  gap: 8px;
}
.page-label {
  font-size: var(--catdb-fs-small);
  opacity: 0.6;
}
.audit-clear {
  display: flex;
  align-items: center;
  gap: 8px;
}
.audit-clear .form-label {
  opacity: 0.85;
}
</style>
