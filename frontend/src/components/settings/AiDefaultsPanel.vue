<script setup lang="ts">
// AiDefaultsPanel — the "默认模型" settings category: which provider/model a
// new agent session starts with.
import { computed, onMounted, ref } from 'vue'
import { NButton, NSelect, useMessage } from 'naive-ui'
import { agentSettings } from '../../api'
import type { ProviderConfig } from '../../api/agentSettings'
import { t as tr } from '../../i18n'

const message = useMessage()

const providers = ref<ProviderConfig[]>([])
const defaultProviderId = ref('')
const defaultModel = ref('')

const defaultProviderOptions = computed(() => [
  { value: '', label: tr('agent.settings.defaults.none') },
  ...providers.value.map((p) => ({ value: p.id, label: p.name || p.id })),
])
const defaultModelOptions = computed(() => {
  const p = providers.value.find((x) => x.id === defaultProviderId.value)
  const models = p?.models ?? []
  return models.map((m) => ({ value: m.ID, label: m.ID }))
})

async function load() {
  try {
    providers.value = await agentSettings.listProviders()
    const d = await agentSettings.getDefaults()
    defaultProviderId.value = d.providerId
    defaultModel.value = d.model
  } catch (e) {
    message.error(tr('agent.settings.providers.loadFailed', { error: String(e) }))
  }
}
onMounted(load)

function onDefaultProviderChange(v: string) {
  defaultProviderId.value = v
  // Reset the model to the provider's default (or first) so the pair stays valid.
  const p = providers.value.find((x) => x.id === v)
  defaultModel.value = p ? p.defaultModel || p.models[0]?.ID || '' : ''
}

async function saveDefaults() {
  try {
    await agentSettings.setDefaults(defaultProviderId.value, defaultProviderId.value ? defaultModel.value : '')
    message.success(tr('agent.settings.defaults.saved'))
  } catch (e) {
    message.error(tr('agent.settings.defaults.saveFailed', { error: String(e) }))
  }
}
</script>

<template>
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
</template>

<style scoped>
.section {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 420px;
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
.hint {
  margin: 0 0 4px;
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
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
.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}
</style>
