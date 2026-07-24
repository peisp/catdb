<script setup lang="ts">
// AiPrivacyPanel — the "Agent 隐私" settings category: whether run_sql row
// data is sent to the model (AGENT_DESIGN.md §7).
import { onMounted } from 'vue'
import { NButton, NSwitch, useMessage } from 'naive-ui'
import { useAgentRuntimeSettings } from './agentRuntime'
import { t as tr } from '../../i18n'

const message = useMessage()
const { settings, load, persist } = useAgentRuntimeSettings()

onMounted(() => {
  load().catch((e) => message.error(tr('agent.settings.limits.saveFailed', { error: String(e) })))
})

async function savePrivacy() {
  try {
    await persist()
    message.success(tr('agent.settings.privacy.saved'))
  } catch (e) {
    message.error(tr('agent.settings.privacy.saveFailed', { error: String(e) }))
  }
}
</script>

<template>
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
</template>

<style scoped>
.section {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 560px;
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
.switch-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.switch-label {
  font-size: var(--catdb-fs-body);
}
.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}
</style>
