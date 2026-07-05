<script setup lang="ts">
// TransferEditorWindow — the root view of the standalone Data Transfer
// child window. Loaded when location.hash starts with #/transfer-editor.
import { computed, onMounted, ref, watch } from 'vue'
import { Window } from '@wailsio/runtime'
import {
  NSelect, NButton, NCheckbox,
  NRadioGroup, NRadio, NInputNumber, NProgress,
  NSpin, useMessage,
} from 'naive-ui'
import { useConnectionsStore } from '../../stores/connections'
import { transfer as transferApi, metadata as metadataApi } from '../../api'
import type { DataTransferRequest, DataTransferResult } from '../../api/transfer'

const store = useConnectionsStore()
const message = useMessage()

// --- window chrome -----------------------------------------------------------

const loading = ref(true)
const loadError = ref('')

const isWin = !navigator.platform.includes('Mac')
const isMaximised = ref(false)
async function onWindowCtrl(cmd: 'min' | 'max' | 'close') {
  if (cmd === 'min') { await Window.Minimise(); return }
  if (cmd === 'close') { await Window.Close(); return }
  await Window.ToggleMaximise()
  isMaximised.value = await Window.IsMaximised()
}
function toggleMaximise() {
  void Window.ToggleMaximise()
}

onMounted(async () => {
  try {
    await store.refreshAll()
  } catch (e: any) {
    loadError.value = e?.message ?? String(e)
  } finally {
    loading.value = false
  }
})

// --- available connections ---------------------------------------------------

const connections = computed(() =>
  store.connections.filter((c) => c.driver !== '')
)
const connOptions = computed(() =>
  connections.value.map((c) => ({ label: c.name, value: c.id }))
)

// --- source state -----------------------------------------------------------

const srcConnId = ref('')
const srcDb = ref('')
const srcDatabases = ref<string[]>([])
const srcDbOptions = computed(() =>
  srcDatabases.value.map((d) => ({ label: d, value: d }))
)
const srcTables = ref<string[]>([])
const selectedTables = ref<Set<string>>(new Set())
const tableSearch = ref('')
const loadingTables = ref(false)

// --- target state -----------------------------------------------------------

const tgtConnId = ref('')
const tgtDb = ref('')
const tgtDatabases = ref<string[]>([])
const tgtDbOptions = computed(() =>
  tgtDatabases.value.map((d) => ({ label: d, value: d }))
)

// --- options ----------------------------------------------------------------

const createTable = ref(true)
const transferMode = ref<'append' | 'overwrite'>('append')
const batchSize = ref(1000)

// --- transfer state ---------------------------------------------------------

const isRunning = ref(false)
const progressRows = ref(0)
const result = ref<DataTransferResult | null>(null)
const transferError = ref('')
const aborter = ref<AbortController | null>(null)
const startTime = ref('')
const endTime = ref('')

// --- derived ----------------------------------------------------------------

const filteredTables = computed(() => {
  const q = tableSearch.value.toLowerCase()
  return q ? srcTables.value.filter((t) => t.toLowerCase().includes(q)) : srcTables.value
})

const allSelected = computed(
  () => filteredTables.value.length > 0 && filteredTables.value.every((t) => selectedTables.value.has(t))
)

const canStart = computed(
  () =>
    srcConnId.value &&
    srcDb.value &&
    tgtConnId.value &&
    tgtDb.value &&
    selectedTables.value.size > 0 &&
    (srcConnId.value + srcDb.value !== tgtConnId.value + tgtDb.value) &&
    !isRunning.value
)

const sameSourceTarget = computed(
  () => srcConnId.value && tgtConnId.value && srcConnId.value + srcDb.value === tgtConnId.value + tgtDb.value
)

function clearResult() {
  result.value = null
  transferError.value = ''
  startTime.value = ''
  endTime.value = ''
}

// --- watchers ---------------------------------------------------------------

async function onSrcConnChange() {
  clearResult()
  srcDb.value = ''
  srcDatabases.value = []
  srcTables.value = []
  selectedTables.value = new Set()
  if (!srcConnId.value) return
  try {
    const names = await metadataApi.listDatabases(srcConnId.value)
    srcDatabases.value = names
    if (names.length === 1) srcDb.value = names[0]
  } catch {
    srcDatabases.value = []
  }
}

async function onSrcDbChange() {
  clearResult()
  srcTables.value = []
  selectedTables.value = new Set()
  if (!srcConnId.value || !srcDb.value) return
  loadingTables.value = true
  try {
    const tbls = await metadataApi.listTables(srcConnId.value, srcDb.value)
    srcTables.value = tbls.map((t) => t.name)
  } catch {
    srcTables.value = []
  } finally {
    loadingTables.value = false
  }
}

async function onTgtConnChange() {
  clearResult()
  tgtDb.value = ''
  tgtDatabases.value = []
  if (!tgtConnId.value) return
  try {
    const names = await metadataApi.listDatabases(tgtConnId.value)
    tgtDatabases.value = names
    if (names.length === 1) tgtDb.value = names[0]
  } catch {
    tgtDatabases.value = []
  }
}

watch(srcConnId, onSrcConnChange)
watch(srcDb, onSrcDbChange)
watch(tgtConnId, onTgtConnChange)
watch([tgtDb, createTable, transferMode, batchSize], clearResult)

// --- table selection --------------------------------------------------------

function toggleSelectAll() {
  clearResult()
  if (allSelected.value) {
    filteredTables.value.forEach((t) => selectedTables.value.delete(t))
  } else {
    filteredTables.value.forEach((t) => selectedTables.value.add(t))
  }
}

function toggleTable(table: string) {
  clearResult()
  if (selectedTables.value.has(table)) {
    selectedTables.value.delete(table)
  } else {
    selectedTables.value.add(table)
  }
}

// --- transfer ---------------------------------------------------------------

async function startTransfer() {
  if (!canStart.value) return
  isRunning.value = true
  progressRows.value = 0
  result.value = null
  transferError.value = ''
  startTime.value = new Date().toLocaleString()
  endTime.value = ''

  const ac = new AbortController()
  aborter.value = ac

  const req: DataTransferRequest = {
    sourceConnId: srcConnId.value,
    sourceDb: srcDb.value,
    targetConnId: tgtConnId.value,
    targetDb: tgtDb.value,
    tables: Array.from(selectedTables.value),
    createTable: createTable.value,
    transferMode: transferMode.value,
    batchSize: batchSize.value,
  }

  const off = transferApi.onProgress((p) => {
    if (p.transferId.startsWith('t-')) {
      progressRows.value = p.rows
    }
  })

  try {
    const r = await transferApi.startTransfer(req, ac.signal)
    result.value = r
    const tableErrors = Object.entries(r.tableResults)
      .filter(([, tr]) => tr?.error)
      .map(([t, tr]) => `${t}: ${tr!.error}`)
    if (tableErrors.length > 0) {
      transferError.value = tableErrors.join('\n')
      message.warning(`Transfer completed with ${tableErrors.length} table error(s)`)
    } else {
      message.success('Transfer completed')
    }
  } catch (err: any) {
    if (err?.name === 'AbortError' || err?.message?.includes('aborted') || err?.message?.includes('canceled')) {
      transferError.value = 'Cancelled'
      message.info('Transfer cancelled')
    } else {
      transferError.value = err?.message || String(err)
      message.error(`Transfer failed: ${transferError.value}`)
    }
  } finally {
    endTime.value = new Date().toLocaleString()
    off()
    aborter.value = null
    isRunning.value = false
  }
}

function cancelTransfer() {
  aborter.value?.abort()
}

function onClose() {
  if (isRunning.value) return
  void Window.Close()
}
</script>

<template>
  <div class="root">
    <header class="titlebar" :class="{ win: isWin }" @dblclick="toggleMaximise">
      <span class="title">{{ $t('transfer.title') }}</span>
      <div v-if="isWin" class="window-controls">
        <button type="button" class="win-btn win-btn-min" @click="onWindowCtrl('min')">
          <svg viewBox="0 0 10 10" aria-hidden="true"><rect x="0" y="4.5" width="10" height="1" fill="currentColor" /></svg>
        </button>
        <button type="button" class="win-btn win-btn-max" @click="onWindowCtrl('max')">
          <svg v-if="isMaximised" viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1.5" y="3.5" width="6" height="6" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
            <path d="M3.5 3.5V2A0.5 0.5 0 0 1 4 1.5h4A0.5 0.5 0 0 1 8.5 2v4a0.5 0.5 0 0 1-.5.5H7.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
          <svg v-else viewBox="0 0 10 10" aria-hidden="true">
            <rect x="1" y="1" width="8" height="8" rx="0.5" fill="none" stroke="currentColor" stroke-width="0.8" />
          </svg>
        </button>
        <button type="button" class="win-btn win-btn-close" @click="onWindowCtrl('close')">
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M1 1l8 8M9 1l-8 8" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    </header>
    <main class="body">
      <div v-if="loading" class="loading">
        <n-spin size="small" />
      </div>
      <div v-else-if="loadError" class="error">{{ loadError }}</div>
      <template v-else>
        <div class="wrapper">
          <div class="content">
            <!-- Source / Target side-by-side -->
            <div class="grid-cols-2">
              <!-- Source -->
              <div class="section">
                <h3 class="section-label source-label">{{ $t('transfer.source') }}</h3>
                <div class="field">
                  <label class="field-label">{{ $t('transfer.sourceConnection') }}</label>
                  <n-select
                    v-model:value="srcConnId"
                    :options="connOptions"
                    :placeholder="$t('transfer.selectConnection')"
                    :disabled="isRunning"
                    filterable
                    size="small"
                  />
                </div>
                <div class="field">
                  <label class="field-label">{{ $t('transfer.sourceDatabase') }}</label>
                  <n-select
                    v-model:value="srcDb"
                    :options="srcDbOptions"
                    :placeholder="$t('transfer.selectDatabase')"
                    :disabled="isRunning || !srcConnId"
                    :loading="loadingTables"
                    filterable
                    size="small"
                  />
                </div>
              </div>

              <!-- Arrow -->
              <div class="arrow-col">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="arrow-icon">
                  <path d="M5 12h14"/>
                  <path d="m12 5 7 7-7 7"/>
                </svg>
              </div>

              <!-- Target -->
              <div class="section">
                <h3 class="section-label target-label">{{ $t('transfer.target') }}</h3>
                <div class="field">
                  <label class="field-label">{{ $t('transfer.targetConnection') }}</label>
                  <n-select
                    v-model:value="tgtConnId"
                    :options="connOptions"
                    :placeholder="$t('transfer.selectConnection')"
                    :disabled="isRunning"
                    filterable
                    size="small"
                  />
                </div>
                <div class="field">
                  <label class="field-label">{{ $t('transfer.targetDatabase') }}</label>
                  <n-select
                    v-model:value="tgtDb"
                    :options="tgtDbOptions"
                    :placeholder="$t('transfer.selectDatabase')"
                    :disabled="isRunning || !tgtConnId"
                    filterable
                    size="small"
                  />
                </div>
              </div>
            </div>

            <!-- Same-source warning -->
            <div v-if="sameSourceTarget" class="warning-row">
              {{ $t('transfer.sameSourceTarget') }}
            </div>

            <!-- Tables -->
            <div v-if="srcTables.length > 0" class="section">
              <h3 class="section-label">
                {{ $t('transfer.tables') }}
                <span class="table-count">({{ selectedTables.size }} / {{ srcTables.length }})</span>
              </h3>
              <div class="table-toolbar">
                <input
                  v-model="tableSearch"
                  class="search-input"
                  :placeholder="$t('transfer.searchTables')"
                  :disabled="isRunning"
                />
                <button type="button" class="select-btn" @click="toggleSelectAll" :disabled="isRunning">
                  {{ allSelected ? $t('transfer.deselectAll') : $t('transfer.selectAll') }}
                </button>
              </div>
              <div class="table-list" v-if="filteredTables.length > 0">
                <label
                  v-for="t in filteredTables"
                  :key="t"
                  class="table-row"
                  :class="{ disabled: isRunning }"
                >
                  <input
                    type="checkbox"
                    :checked="selectedTables.has(t)"
                    :disabled="isRunning"
                    @change="toggleTable(t)"
                  />
                  <span>{{ t }}</span>
                </label>
              </div>
              <div v-else class="empty-tables">{{ $t('transfer.noTables') }}</div>
            </div>

            <!-- Options -->
            <div class="section options-row">
              <n-checkbox v-model:checked="createTable" :disabled="isRunning">
                {{ $t('transfer.createTable') }}
              </n-checkbox>
              <div class="option-group">
                <label class="field-label">{{ $t('transfer.transferMode') }}</label>
                <n-radio-group v-model:value="transferMode" :disabled="isRunning" size="small">
                  <n-radio value="append">{{ $t('transfer.append') }}</n-radio>
                  <n-radio value="overwrite">{{ $t('transfer.overwrite') }}</n-radio>
                </n-radio-group>
              </div>
              <div class="option-group batch-size">
                <label class="field-label">{{ $t('transfer.batchSize') }}</label>
                <n-input-number
                  v-model:value="batchSize"
                  :min="100"
                  :max="10000"
                  :step="100"
                  :disabled="isRunning"
                  size="small"
                  style="width: 110px"
                />
              </div>
            </div>

            <!-- Progress -->
            <div v-if="isRunning" class="progress-row">
              <n-progress type="line" :percentage="66" :show-indicator="false" :height="4" :border-radius="2" />
              <span class="progress-text">{{ $t('transfer.transferring') }} — {{ progressRows }} {{ $t('transfer.rowsTransferred', { n: progressRows }) }}</span>
            </div>

            <!-- Result summary -->
            <div v-if="result && !isRunning" class="result-section">
              <div class="result-grid" v-if="result.tableResults">
                <template v-for="(tr, name) in result.tableResults" :key="name">
                  <div v-if="tr" class="result-row-item">
                    <span class="result-name">{{ name }}</span>
                    <span v-if="tr.error" class="result-error">{{ tr.error }}</span>
                    <span v-else class="result-ok">{{ tr.rows }} {{ $t('transfer.rowsTransferred', { n: tr.rows }) }}</span>
                  </div>
                </template>
              </div>
              <div v-if="transferError" class="result-error-block">{{ transferError }}</div>
            </div>
          </div>

          <!-- Footer pinned at bottom -->
          <div class="footer">
            <span class="footer-time" v-if="startTime">{{ startTime }} → {{ endTime || '…' }}</span>
            <span class="footer-spacer" />
            <n-button v-if="isRunning" type="error" size="small" @click="cancelTransfer">
              {{ $t('transfer.cancel') }}
            </n-button>
            <n-button v-else size="small" @click="onClose">
              {{ $t('common.close') }}
            </n-button>
            <n-button
              v-if="!isRunning"
              type="primary"
              size="small"
              :disabled="!canStart"
              @click="startTransfer"
            >
              {{ $t('transfer.start') }}
            </n-button>
          </div>
        </div>
      </template>
    </main>
  </div>
</template>

<style scoped>
.root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  width: 100vw;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--n-color);
}

/* --- Titlebar -------------------------------------------------------------- */
.titlebar {
  position: relative;
  flex: 0 0 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.2px;
  opacity: 0.85;
  --wails-draggable: drag;
}
.titlebar .title {
  padding-left: 60px;
  padding-right: 12px;
}
.titlebar.win .title {
  padding-left: 150px;
  padding-right: 150px;
}
.titlebar .window-controls {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 10;
  display: flex;
  flex-direction: row;
  align-items: stretch;
  height: 100%;
  -webkit-app-region: no-drag;
}
.titlebar .win-btn {
  --wails-draggable: no-drag;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  padding: 0;
  margin: 0;
  border: none;
  border-radius: 0;
  font: inherit;
  color: inherit;
  cursor: default;
  background: transparent;
  transition: background 80ms ease;
}
.titlebar .win-btn svg {
  width: 14px;
  height: 14px;
  opacity: 0.75;
}
.titlebar .win-btn:hover { background: rgba(127, 127, 127, 0.15); }
.titlebar .win-btn:active { background: rgba(127, 127, 127, 0.25); }
.titlebar .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
.titlebar .win-btn-close:hover svg { opacity: 1; }
.titlebar .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
.titlebar .win-btn-close:active svg { opacity: 1; }
@media (prefers-color-scheme: dark) {
  .titlebar .win-btn:hover { background: rgba(255, 255, 255, 0.1); }
  .titlebar .win-btn:active { background: rgba(255, 255, 255, 0.16); }
  .titlebar .win-btn-close:hover { background: rgba(196, 43, 28, 0.9); }
  .titlebar .win-btn-close:hover svg { opacity: 1; }
  .titlebar .win-btn-close:active { background: rgba(180, 30, 20, 0.95); }
}

/* --- Body ------------------------------------------------------------------ */
.body {
  flex: 1 1 0;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: flex;
}
.body > * { flex: 1 1 0; min-width: 0; min-height: 0; }

.loading,
.error {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 20px;
  font-size: 13px;
  opacity: 0.8;
}
.error { color: var(--n-error-color, #d03050); }

.wrapper {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  display: grid;
  grid-template-rows: 1fr auto;
}
.content {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
}
.content > :not(.result-section) { flex-shrink: 0; }

/* --- Footer ---------------------------------------------------------------- */
.footer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 20px 12px;
  border-top: 1px solid var(--n-border-color, #e8e8e8);
}
.footer-time {
  font-size: 11px;
  opacity: 0.55;
}
.footer-spacer { flex: 1 1 auto; }

/* --- Form ------------------------------------------------------------------ */
.grid-cols-2 {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  gap: 8px;
  margin-bottom: 12px;
}
.section { min-width: 0; }
.section-label {
  margin: 0 0 8px;
  font-size: 13px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
}
.source-label { color: #1677ff; }
.target-label { color: #52c41a; }
.arrow-col {
  display: flex;
  align-items: center;
  justify-content: center;
  padding-top: 24px;
}
.arrow-icon { width: 22px; height: 22px; opacity: 0.45; }
.field { margin-bottom: 8px; }
.field:last-child { margin-bottom: 0; }
.field-label {
  display: block;
  font-size: 12px;
  margin-bottom: 3px;
  opacity: 0.72;
}
.warning-row {
  background: #fff2f0;
  border: 1px solid #ffccc7;
  border-radius: 4px;
  padding: 6px 10px;
  font-size: 12px;
  color: #cf1322;
  margin-bottom: 12px;
}
@media (prefers-color-scheme: dark) {
  .warning-row { background: #2a0f0d; border-color: #5c1a1a; color: #ff7875; }
}

/* --- Tables ---------------------------------------------------------------- */
.table-toolbar {
  display: flex;
  gap: 8px;
  margin-bottom: 6px;
}
.search-input {
  flex: 1;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--n-border-color, #d9d9d9);
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 12px;
  outline: none;
}
.search-input:focus { border-color: #1677ff; }
.select-btn {
  border: none;
  background: transparent;
  color: #1677ff;
  font: inherit;
  font-size: 12px;
  cursor: default;
  padding: 0 6px;
  white-space: nowrap;
}
.select-btn:disabled { opacity: 0.4; pointer-events: none; }
.table-list {
  max-height: 180px;
  overflow-y: auto;
  border: 1px solid var(--n-border-color, #d9d9d9);
  border-radius: 4px;
}
.table-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  font-size: 12px;
  cursor: default;
}
.table-row:hover { background: rgba(127, 127, 127, 0.06); }
.table-row.disabled { opacity: 0.5; pointer-events: none; }
.table-row input[type="checkbox"] { margin: 0; }
.empty-tables {
  font-size: 12px;
  opacity: 0.5;
  padding: 8px 0;
}
.table-count { font-weight: 400; opacity: 0.55; font-size: 12px; }

/* --- Options --------------------------------------------------------------- */
.options-row {
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
  margin-top: 8px;
}
.option-group { display: flex; align-items: center; gap: 6px; }
.option-group .field-label { margin-bottom: 0; }
.batch-size { margin-left: auto; }

/* --- Progress -------------------------------------------------------------- */
.progress-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 12px;
  padding: 8px 0;
}
.progress-text { font-size: 12px; opacity: 0.72; }

/* --- Result ---------------------------------------------------------------- */
.result-section {
  margin-top: 12px;
  border-top: 1px solid var(--n-border-color, #e8e8e8);
  padding-top: 8px;
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
}
.result-grid {
  max-height: 200px;
}
.result-row-item {
  display: flex;
  justify-content: space-between;
  padding: 3px 0;
  font-size: 12px;
  border-bottom: 1px solid var(--n-border-color, #f0f0f0);
}
.result-name { font-weight: 500; }
.result-error { color: #cf1322; }
.result-ok { opacity: 0.7; }
.result-error-block {
  color: #cf1322;
  font-size: 12px;
  white-space: pre-wrap;
  margin-top: 6px;
}
</style>
