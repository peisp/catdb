<script setup lang="ts">
// AiLimitsPanel — the "限额与压缩" settings category: agent runtime limits,
// context compaction, and the model pricing table used for cost estimates.
import { onMounted, ref } from 'vue'
import { CButton, CInput, CInputNumber, CSwitch, useMessage } from '../ui'
import { useAgentRuntimeSettings } from './agentRuntime'
import type { AgentSettings } from '../../api/agentSettings'
import { t as tr } from '../../i18n'

const message = useMessage()
const { settings, load, persist } = useAgentRuntimeSettings()

// Pricing map ⇄ editable rows. The map key is the model id.
type PricingRow = { model: string; inputPer1M: number; outputPer1M: number; cacheReadPer1M: number }
const pricingRows = ref<PricingRow[]>([])

onMounted(async () => {
  try {
    await load()
    pricingRows.value = Object.entries(settings.pricing ?? {}).map(([model, p]) => ({
      model,
      inputPer1M: p?.inputPer1M ?? 0,
      outputPer1M: p?.outputPer1M ?? 0,
      cacheReadPer1M: p?.cacheReadPer1M ?? 0,
    }))
  } catch (e) {
    message.error(tr('agent.settings.limits.saveFailed', { error: String(e) }))
  }
})

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

async function saveLimits() {
  try {
    settings.pricing = collectPricing() as AgentSettings['pricing']
    await persist()
    message.success(tr('agent.settings.limits.saved'))
  } catch (e) {
    message.error(tr('agent.settings.limits.saveFailed', { error: String(e) }))
  }
}
</script>

<template>
  <section class="section">
    <div class="section-head">
      <h3 class="section-title">{{ $t('agent.settings.limits.title') }}</h3>
    </div>

    <div class="limit-row">
      <label class="form-label">{{ $t('agent.settings.limits.maxIterations') }}</label>
      <CInputNumber v-model:value="settings.maxIterations" size="small" class="limit-input" :min="1" />
    </div>
    <div class="limit-row">
      <label class="form-label">{{ $t('agent.settings.limits.stmtTimeout') }}</label>
      <CInputNumber v-model:value="settings.stmtTimeoutSec" size="small" class="limit-input" :min="1" />
      <span class="unit-hint">{{ $t('agent.settings.limits.secondsUnit') }}</span>
    </div>
    <div class="limit-row">
      <label class="form-label">{{ $t('agent.settings.limits.txIdleTimeout') }}</label>
      <CInputNumber v-model:value="settings.txIdleTimeoutSec" size="small" class="limit-input" :min="1" />
      <span class="unit-hint">{{ $t('agent.settings.limits.secondsUnit') }}</span>
    </div>
    <div class="limit-row">
      <label class="form-label">{{ $t('agent.settings.limits.llmResultRows') }}</label>
      <CInputNumber v-model:value="settings.llmResultRows" size="small" class="limit-input" :min="1" />
      <span class="unit-hint">{{ $t('agent.settings.limits.rowsUnit') }}</span>
    </div>
    <div class="limit-row">
      <label class="form-label">{{ $t('agent.settings.limits.sessionTokenBudget') }}</label>
      <CInputNumber v-model:value="settings.sessionTokenBudget" size="small" class="limit-input" :min="0" />
      <span class="unit-hint">{{ $t('agent.settings.limits.sessionTokenBudgetHint') }}</span>
    </div>

    <div class="switch-row">
      <CSwitch v-model="settings.compactAuto" />
      <span class="switch-label">{{ $t('agent.settings.limits.compactAuto') }}</span>
    </div>
    <div class="limit-row">
      <label class="form-label">{{ $t('agent.settings.limits.compactThreshold') }}</label>
      <CInputNumber
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
        <CButton size="mini" @click="addPricingRow">{{ $t('agent.settings.pricing.addRow') }}</CButton>
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
        <CInput v-model="r.model" size="small" class="pc-model" :placeholder="$t('agent.settings.pricing.modelPlaceholder')" />
        <CInputNumber v-model:value="r.inputPer1M" size="small" class="pc-num" :min="0" />
        <CInputNumber v-model:value="r.outputPer1M" size="small" class="pc-num" :min="0" />
        <CInputNumber v-model:value="r.cacheReadPer1M" size="small" class="pc-num" :min="0" />
        <CButton size="mini" class="pc-del" @click="removePricingRow(i)">{{ $t('agent.settings.pricing.remove') }}</CButton>
      </div>
      <p v-if="pricingRows.length" class="unit-hint">{{ $t('agent.settings.pricing.perMillion') }}</p>
    </div>

    <div class="editor-actions">
      <CButton size="small" @click="saveLimits">{{ $t('common.save') }}</CButton>
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
.hint {
  margin: 0 0 4px;
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
}
.form-label {
  font-size: var(--catdb-fs-small);
  opacity: 0.85;
}
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
.models-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
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
.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}
</style>
