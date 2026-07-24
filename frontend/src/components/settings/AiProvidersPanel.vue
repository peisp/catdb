<script setup lang="ts">
// AiProvidersPanel — the "模型供应商" settings category: provider CRUD +
// connectivity test + write-only API key. Talks only to api/agentSettings
// (never bindings directly, CLAUDE.md #1). API keys are write-only:
// HasProviderKey reports a boolean "configured" state and the key itself is
// never read back into the UI.
import { computed, onMounted, reactive, ref } from 'vue'
import { NButton, NInput, NInputNumber, NSelect, NCheckbox, NVirtualList, useMessage } from 'naive-ui'
import { agentSettings, dialogs } from '../../api'
import type { ProviderConfig, ModelInfo } from '../../api/agentSettings'
import { t as tr } from '../../i18n'

const message = useMessage()

const providers = ref<ProviderConfig[]>([])
const keyStatus = reactive<Record<string, boolean>>({})
const testingId = ref('')

// key: local uid for virtualization — model IDs are editable (may be empty or
// duplicated mid-edit), so rows carry their own stable identity.
type DraftModel = { key: number; ID: string; ContextWindow: number; SupportsTools: boolean }
let modelUid = 0
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
    models: p.models.map((m) => ({ key: ++modelUid, ID: m.ID, ContextWindow: m.ContextWindow, SupportsTools: m.SupportsTools })),
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
  editing.value?.models.push({ key: ++modelUid, ID: '', ContextWindow: 128000, SupportsTools: true })
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
      d.models.push({ key: ++modelUid, ID: m.ID, ContextWindow: m.ContextWindow || 128000, SupportsTools: m.SupportsTools })
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
</script>

<template>
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
        <!-- Virtualized: fetched provider lists can hold hundreds of models —
             only the visible rows mount their (heavy) form controls. -->
        <n-virtual-list
          v-if="editing.models.length"
          class="model-list-wrap"
          :style="{ maxHeight: '260px' }"
          :item-size="34"
          :items="filteredModels"
          key-field="key"
        >
          <template #default="{ item: m }">
            <div :key="m.key" class="model-row">
              <n-input v-model:value="m.ID" size="small" class="model-id" :placeholder="$t('agent.settings.form.modelIdPlaceholder')" />
              <n-input-number v-model:value="m.ContextWindow" size="small" class="model-ctx" :min="0" :show-button="false" :placeholder="$t('agent.settings.form.contextWindow')" />
              <n-checkbox v-model:checked="m.SupportsTools" class="model-tools" />
              <n-button size="tiny" quaternary class="model-del" @click="removeModel(m)">{{ $t('agent.settings.form.removeModel') }}</n-button>
            </div>
          </template>
        </n-virtual-list>
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
</template>

<style scoped>
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
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  padding: 6px;
}
/* Fixed 34px stride (28px controls + 6px gap) — must match :item-size. */
.model-row {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 34px;
  padding-bottom: 6px;
  box-sizing: border-box;
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
</style>
