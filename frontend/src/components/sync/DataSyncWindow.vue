<script setup lang="ts">
// DataSyncWindow — the root view of the standalone Data Synchronization
// child window. Loaded when location.hash starts with #/data-sync.
//
// Flow: pick source + target → pick tables → Compare (dry-run merge in the
// backend, counts + bounded samples only cross IPC) → review per-table
// insert/update/delete counts → Execute the checked tables. DELETE of
// target-only rows is opt-in and confirmed natively.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Window } from '@wailsio/runtime'
import { NButton, NCheckbox, NInputNumber, NSpin, useMessage } from 'naive-ui'
import { useConnectionsStore } from '../../stores/connections'
import { sync as syncApi, metadata as metadataApi, dialogs, connections as connectionsApi } from '../../api'
import type { DataTableDiff } from '../../api/sync'
import { t } from '../../i18n'

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

// --- source / target ----------------------------------------------------------

const connections = computed(() => store.connections.filter((c) => c.driver !== ''))
const connOptions = computed(() => connections.value.map((c) => ({ label: c.name, value: c.id })))

const srcConnId = ref('')
const srcDb = ref('')
const srcDatabases = ref<string[]>([])
const srcServerInfo = ref('')
const tgtConnId = ref('')
const tgtDb = ref('')
const tgtDatabases = ref<string[]>([])
const tgtServerInfo = ref('')

const srcTables = ref<string[]>([])
const selectedTables = ref<Set<string>>(new Set())
const tableSearch = ref('')
const loadingTables = ref(false)

// Target can only connect to the same database type as the source — cross-
// driver data sync isn't meaningful. Source stays unrestricted.
const srcDriver = computed(() => connections.value.find((c) => c.id === srcConnId.value)?.driver ?? '')
const tgtConnOptions = computed(() =>
  srcDriver.value
    ? connections.value.filter((c) => c.driver === srcDriver.value).map((c) => ({ label: c.name, value: c.id }))
    : connOptions.value,
)

async function loadDatabases(connId: string): Promise<string[]> {
  if (!connId) return []
  try {
    return await metadataApi.listDatabases(connId)
  } catch {
    return []
  }
}

async function fetchServerInfo(connId: string, driver: string): Promise<string> {
  try {
    const info = await connectionsApi.getServerInfo(connId)
    return info.version ? `${driver} ${info.version}` : driver
  } catch {
    return driver
  }
}

watch(srcConnId, async () => {
  clearResult()
  srcDb.value = ''
  srcServerInfo.value = ''
  const id = srcConnId.value
  srcDatabases.value = await loadDatabases(id)
  if (srcDatabases.value.length === 1) srcDb.value = srcDatabases.value[0]
  if (id) {
    const driver = connections.value.find((c) => c.id === id)?.driver ?? ''
    // Source driver changed under an already-picked target — drop the
    // target so a mismatched connection can't linger selected.
    if (tgtConnId.value) {
      const tgtDriver = connections.value.find((c) => c.id === tgtConnId.value)?.driver ?? ''
      if (tgtDriver !== driver) tgtConnId.value = ''
    }
    const info = await fetchServerInfo(id, driver)
    if (srcConnId.value === id) srcServerInfo.value = info
  }
})
watch(srcDb, async () => {
  clearResult()
  srcTables.value = []
  selectedTables.value = new Set()
  if (!srcConnId.value || !srcDb.value) return
  loadingTables.value = true
  try {
    const tbls = await metadataApi.listTables(srcConnId.value, srcDb.value)
    srcTables.value = tbls.map((x) => x.name)
  } catch {
    srcTables.value = []
  } finally {
    loadingTables.value = false
  }
})
watch(tgtConnId, async () => {
  clearResult()
  tgtDb.value = ''
  tgtServerInfo.value = ''
  const id = tgtConnId.value
  tgtDatabases.value = await loadDatabases(id)
  if (tgtDatabases.value.length === 1) tgtDb.value = tgtDatabases.value[0]
  if (id) {
    const driver = connections.value.find((c) => c.id === id)?.driver ?? ''
    const info = await fetchServerInfo(id, driver)
    if (tgtConnId.value === id) tgtServerInfo.value = info
  }
})
watch(tgtDb, clearResult)

const filteredTables = computed(() => {
  const q = tableSearch.value.toLowerCase()
  return q ? srcTables.value.filter((x) => x.toLowerCase().includes(q)) : srcTables.value
})
const allSelected = computed(
  () => filteredTables.value.length > 0 && filteredTables.value.every((x) => selectedTables.value.has(x)),
)
function toggleSelectAll() {
  clearResult()
  const s = new Set(selectedTables.value)
  if (allSelected.value) filteredTables.value.forEach((x) => s.delete(x))
  else filteredTables.value.forEach((x) => s.add(x))
  selectedTables.value = s
}
function toggleTable(name: string) {
  clearResult()
  const s = new Set(selectedTables.value)
  if (s.has(name)) s.delete(name)
  else s.add(name)
  selectedTables.value = s
}

const sameSourceTarget = computed(
  () => !!srcConnId.value && !!tgtConnId.value &&
    srcConnId.value + srcDb.value === tgtConnId.value + tgtDb.value,
)

// --- options -------------------------------------------------------------------

const allowDelete = ref(false)
const batchSize = ref(500)

// --- compare / execute -----------------------------------------------------------

const isComparing = ref(false)
const isExecuting = ref(false)
const compared = ref(false)
const tables = ref<DataTableDiff[]>([])
const expanded = ref<Set<string>>(new Set())
const aborter = ref<AbortController | null>(null)
const liveTable = ref('')
const liveStats = ref({ inserts: 0, updates: 0, deletes: 0 })

// Live per-table rows shown WHILE a compare runs, so a big database never
// looks frozen: every selected table starts as pending, flips to running with
// live counts, then done (or skipped/error).
type LiveRow = {
  table: string
  state: 'pending' | 'running' | 'done'
  inserts: number
  updates: number
  deletes: number
  scannedSource: number
  scannedTarget: number
  skipped?: string
  error?: string
}
const liveRows = ref<LiveRow[]>([])
const liveDoneCount = computed(() => liveRows.value.filter((r) => r.state === 'done').length)

function initLiveRows() {
  liveRows.value = Array.from(selectedTables.value)
    .sort()
    .map((x) => ({ table: x, state: 'pending' as const, inserts: 0, updates: 0, deletes: 0, scannedSource: 0, scannedTarget: 0 }))
}

// Live events are buffered and flushed at most ~10×/s — merge progress fires
// every batch per table, and with many tables the per-event array rebuild +
// full list re-render would freeze the UI on exactly the workloads the live
// list is meant to keep visible.
let pendingLive: syncApi.DataSyncProgress[] = []
let liveFlushTimer: ReturnType<typeof setTimeout> | undefined

function flushLiveEvents() {
  liveFlushTimer = undefined
  if (pendingLive.length === 0) return
  const evts = pendingLive
  pendingLive = []
  const rows = [...liveRows.value]
  const idxByTable = new Map(rows.map((r, i) => [r.table, i]))
  for (const p of evts) {
    if (!p.table) continue
    const i = idxByTable.get(p.table)
    if (i === undefined) continue
    const r = { ...rows[i] }
    r.inserts = p.inserts
    r.updates = p.updates
    r.deletes = p.deletes
    r.scannedSource = p.scannedSource
    r.scannedTarget = p.scannedTarget
    if (p.phase === 'table-start' || p.phase === 'progress') {
      r.state = 'running'
    } else if (p.phase === 'table-done') {
      r.state = 'done'
      r.skipped = p.skipped || undefined
      r.error = p.error || undefined
    }
    rows[i] = r
  }
  liveRows.value = rows
}

function queueLiveEvent(p: syncApi.DataSyncProgress) {
  pendingLive.push(p)
  if (!liveFlushTimer) liveFlushTimer = setTimeout(flushLiveEvents, 100)
}

function stopLiveBuffer() {
  if (liveFlushTimer) {
    clearTimeout(liveFlushTimer)
    liveFlushTimer = undefined
  }
  pendingLive = []
}

let offProgress: (() => void) | null = null
onUnmounted(() => { offProgress?.() })

function clearResult() {
  compared.value = false
  tables.value = []
  expanded.value = new Set()
  liveRows.value = []
}

const canCompare = computed(
  () => !!(srcConnId.value && srcDb.value && tgtConnId.value && tgtDb.value) &&
    selectedTables.value.size > 0 && !sameSourceTarget.value &&
    !isComparing.value && !isExecuting.value,
)

const diffTables = computed(() =>
  tables.value.filter((x) => !x.skipped && !x.error && (x.inserts + x.updates + x.deletes) > 0),
)
const executableCount = computed(() => diffTables.value.length)
const totalDeletes = computed(() => diffTables.value.reduce((n, x) => n + x.deletes, 0))

function subscribeProgress(prefix: string) {
  offProgress?.()
  offProgress = syncApi.onDataSyncProgress((p) => {
    if (!p.syncId.startsWith(prefix)) return
    liveTable.value = p.table
    liveStats.value = { inserts: p.inserts, updates: p.updates, deletes: p.deletes }
  })
}

async function runCompare() {
  if (!canCompare.value) return
  isComparing.value = true
  clearResult()
  liveTable.value = ''
  liveStats.value = { inserts: 0, updates: 0, deletes: 0 }
  initLiveRows()
  const ac = new AbortController()
  aborter.value = ac
  offProgress?.()
  offProgress = syncApi.onDataSyncProgress((p) => {
    if (p.syncId.startsWith('dc-')) queueLiveEvent(p)
  })
  try {
    const res = await syncApi.compareData({
      sourceConnId: srcConnId.value,
      sourceDb: srcDb.value,
      sourceSchema: '',
      targetConnId: tgtConnId.value,
      targetDb: tgtDb.value,
      targetSchema: '',
      tables: Array.from(selectedTables.value),
      batchSize: batchSize.value,
    }, ac.signal)
    tables.value = res.tables ?? []
    compared.value = true
  } catch (err: any) {
    if (err?.name === 'AbortError' || String(err?.message ?? err).includes('cancel')) {
      message.info(t('dataSync.compareCancelled'))
    } else {
      message.error(t('dataSync.compareFailed', { error: err?.message ?? String(err) }))
    }
  } finally {
    offProgress?.()
    offProgress = null
    stopLiveBuffer()
    aborter.value = null
    isComparing.value = false
  }
}

async function runExecute() {
  if (executableCount.value === 0 || isExecuting.value) return
  if (allowDelete.value && totalDeletes.value > 0) {
    const choice = await dialogs.confirm({
      kind: 'warning',
      title: t('dataSync.confirmTitle'),
      message: t('dataSync.confirmDelete', { n: totalDeletes.value }),
      buttons: [
        { value: 'execute', label: t('dataSync.confirmExecute') },
        { value: 'cancel', label: t('common.cancel'), isCancel: true, isDefault: true },
      ],
    })
    if (choice !== 'execute') return
  }
  isExecuting.value = true
  liveTable.value = ''
  liveStats.value = { inserts: 0, updates: 0, deletes: 0 }
  const ac = new AbortController()
  aborter.value = ac
  subscribeProgress('de-')
  try {
    const res = await syncApi.executeDataSync({
      sourceConnId: srcConnId.value,
      sourceDb: srcDb.value,
      sourceSchema: '',
      targetConnId: tgtConnId.value,
      targetDb: tgtDb.value,
      targetSchema: '',
      tables: diffTables.value.map((x) => x.table),
      allowDelete: allowDelete.value,
      batchSize: batchSize.value,
    }, ac.signal)
    const failed = (res.tables ?? []).filter((x) => x.error).length
    if (failed > 0) {
      message.warning(t('dataSync.executeFailed', { n: failed }))
    } else {
      message.success(t('dataSync.executeDone'))
    }
    // Re-compare so counts reflect the new target state (should be all zero).
    await runCompare()
  } catch (err: any) {
    if (err?.name === 'AbortError' || String(err?.message ?? err).includes('cancel')) {
      message.info(t('dataSync.executeCancelled'))
    } else {
      message.error(t('dataSync.executeError', { error: err?.message ?? String(err) }))
    }
  } finally {
    offProgress?.()
    offProgress = null
    aborter.value = null
    isExecuting.value = false
  }
}

function cancelRun() {
  aborter.value?.abort()
}

function onClose() {
  if (isComparing.value || isExecuting.value) return
  void Window.Close()
}

function toggleExpanded(name: string) {
  const s = new Set(expanded.value)
  if (s.has(name)) s.delete(name)
  else s.add(name)
  expanded.value = s
}

function skipLabel(slug: string): string {
  switch (slug) {
    case 'no-primary-key': return t('dataSync.skipNoPrimaryKey')
    case 'pk-mismatch': return t('dataSync.skipPkMismatch')
    case 'missing-on-target': return t('dataSync.skipMissingOnTarget')
    case 'no-common-columns': return t('dataSync.skipNoCommonColumns')
    default: return slug
  }
}

function sampleText(d: DataTableDiff): string {
  return (d.samples ?? [])
    .map((s) => {
      const key = (s.key ?? []).join(', ')
      const cols = s.columns?.length ? ` [${s.columns.join(', ')}]` : ''
      return `${s.kind.toUpperCase()}  (${key})${cols}`
    })
    .join('\n')
}
</script>

<template>
  <div class="root">
    <header class="titlebar" :class="{ win: isWin }" @dblclick="toggleMaximise">
      <span class="title">{{ $t('dataSync.title') }}</span>
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
      <div v-if="loading" class="loading"><n-spin size="small" /></div>
      <div v-else-if="loadError" class="error">{{ loadError }}</div>
      <template v-else>
        <div class="wrapper">
          <div class="content">
            <!-- Source / Target -->
            <div class="grid-cols-2">
              <div class="section">
                <h3 class="section-label source-label">{{ $t('dataSync.source') }}</h3>
                <div class="field">
                  <label class="field-label">{{ $t('dataSync.connection') }}</label>
                  <select v-model="srcConnId" class="native-select" :disabled="isComparing || isExecuting">
                    <option value="" disabled>{{ $t('dataSync.selectConnection') }}</option>
                    <option v-for="c in connOptions" :key="c.value" :value="c.value">{{ c.label }}</option>
                  </select>
                  <div v-if="srcServerInfo" class="conn-hint">{{ srcServerInfo }}</div>
                </div>
                <div class="field">
                  <label class="field-label">{{ $t('dataSync.database') }}</label>
                  <select v-model="srcDb" class="native-select" :disabled="isComparing || isExecuting || !srcConnId">
                    <option value="" disabled>{{ $t('dataSync.selectDatabase') }}</option>
                    <option v-for="d in srcDatabases" :key="d" :value="d">{{ d }}</option>
                  </select>
                </div>
              </div>
              <div class="arrow-col">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="arrow-icon">
                  <path d="M5 12h14"/><path d="m12 5 7 7-7 7"/>
                </svg>
              </div>
              <div class="section">
                <h3 class="section-label target-label">{{ $t('dataSync.target') }}</h3>
                <div class="field">
                  <label class="field-label">{{ $t('dataSync.connection') }}</label>
                  <select
                    v-model="tgtConnId" class="native-select"
                    :disabled="isComparing || isExecuting"
                    :title="$t('common.sameDriverOnly')"
                  >
                    <option value="" disabled>{{ $t('dataSync.selectConnection') }}</option>
                    <option v-for="c in tgtConnOptions" :key="c.value" :value="c.value">{{ c.label }}</option>
                  </select>
                  <div v-if="tgtServerInfo" class="conn-hint">{{ tgtServerInfo }}</div>
                  <div v-else-if="srcConnId && tgtConnOptions.length === 0" class="conn-hint">{{ $t('common.noSameDriverConn') }}</div>
                </div>
                <div class="field">
                  <label class="field-label">{{ $t('dataSync.database') }}</label>
                  <select v-model="tgtDb" class="native-select" :disabled="isComparing || isExecuting || !tgtConnId">
                    <option value="" disabled>{{ $t('dataSync.selectDatabase') }}</option>
                    <option v-for="d in tgtDatabases" :key="d" :value="d">{{ d }}</option>
                  </select>
                </div>
              </div>
            </div>

            <div v-if="sameSourceTarget" class="warning-row">{{ $t('dataSync.sameSourceTarget') }}</div>

            <!-- Table selection (pre-compare) -->
            <div v-if="!compared && !isComparing && srcTables.length > 0" class="section tables-section">
              <h3 class="section-label">
                {{ $t('dataSync.tables') }}
                <span class="table-count">({{ selectedTables.size }} / {{ srcTables.length }})</span>
              </h3>
              <div class="table-toolbar">
                <input
                  v-model="tableSearch"
                  class="search-input"
                  :placeholder="$t('dataSync.searchTables')"
                  :disabled="isComparing || isExecuting"
                />
                <button type="button" class="select-btn" @click="toggleSelectAll" :disabled="isComparing || isExecuting">
                  {{ allSelected ? $t('dataSync.deselectAll') : $t('dataSync.selectAll') }}
                </button>
              </div>
              <n-spin :show="loadingTables" size="small">
                <div class="table-list" v-if="filteredTables.length > 0">
                  <label v-for="x in filteredTables" :key="x" class="table-row" :class="{ disabled: isComparing || isExecuting }">
                    <input type="checkbox" :checked="selectedTables.has(x)" :disabled="isComparing || isExecuting" @change="toggleTable(x)" />
                    <span>{{ x }}</span>
                  </label>
                </div>
                <div v-else class="empty-tables">{{ $t('dataSync.noTables') }}</div>
              </n-spin>
            </div>

            <!-- Live compare rows: every selected table pending → running(counts) → done -->
            <div v-if="isComparing" class="diff-section">
              <div class="diff-header">
                <span>{{ $t('dataSync.comparingLive', { done: liveDoneCount, total: liveRows.length }) }}</span>
              </div>
              <div class="diff-list">
                <div v-for="r in liveRows" :key="r.table" class="diff-row" :class="{ pending: r.state === 'pending' }">
                  <span class="diff-name">{{ r.table }}</span>
                  <span v-if="r.state === 'pending'" class="tag skipped">{{ $t('dataSync.statePending') }}</span>
                  <template v-else>
                    <span v-if="r.skipped" class="tag skipped">{{ skipLabel(r.skipped) }}</span>
                    <span v-else-if="r.error" class="diff-error">{{ r.error }}</span>
                    <template v-else>
                      <span class="stat ins" :class="{ zero: r.inserts === 0 }">+{{ r.inserts }}</span>
                      <span class="stat upd" :class="{ zero: r.updates === 0 }">~{{ r.updates }}</span>
                      <span class="stat del" :class="{ zero: r.deletes === 0 }">−{{ r.deletes }}</span>
                      <span class="scanned">{{ $t('dataSync.scanned', { src: r.scannedSource, tgt: r.scannedTarget }) }}</span>
                    </template>
                    <span v-if="r.state === 'running'" class="tag running">{{ $t('dataSync.stateRunning') }}</span>
                  </template>
                </div>
              </div>
            </div>

            <!-- Compare result -->
            <div v-if="compared && !isComparing" class="diff-section">
              <div class="diff-header">
                <span>{{ $t('dataSync.diffSummary', { total: tables.length, diff: diffTables.length }) }}</span>
                <button type="button" class="select-btn" @click="clearResult">{{ $t('dataSync.reselect') }}</button>
              </div>
              <div class="diff-list">
                <template v-for="d in tables" :key="d.table">
                  <div class="diff-row" :class="{ inactive: !!d.skipped || !!d.error }">
                    <span class="diff-name" @click="toggleExpanded(d.table)">{{ d.table }}</span>
                    <span v-if="d.skipped" class="tag skipped">{{ skipLabel(d.skipped) }}</span>
                    <span v-else-if="d.error" class="diff-error">{{ d.error }}</span>
                    <template v-else>
                      <span class="stat ins" :class="{ zero: d.inserts === 0 }">+{{ d.inserts }}</span>
                      <span class="stat upd" :class="{ zero: d.updates === 0 }">~{{ d.updates }}</span>
                      <span class="stat del" :class="{ zero: d.deletes === 0 }">−{{ d.deletes }}</span>
                      <span class="scanned">{{ $t('dataSync.scanned', { src: d.scannedSource, tgt: d.scannedTarget }) }}</span>
                    </template>
                    <span
                      v-if="(d.samples?.length ?? 0) > 0"
                      class="stmt-count"
                      @click="toggleExpanded(d.table)"
                    >
                      {{ $t('dataSync.sampleCount', { n: d.samples!.length }) }}
                      <svg viewBox="0 0 10 10" class="chev" :class="{ open: expanded.has(d.table) }" aria-hidden="true">
                        <path d="M3 2l4 3-4 3" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
                      </svg>
                    </span>
                  </div>
                  <pre v-if="expanded.has(d.table) && (d.samples?.length ?? 0) > 0" class="stmt-block">{{ sampleText(d) }}</pre>
                </template>
              </div>
            </div>

            <!-- Live progress (execute pass) -->
            <div v-if="isExecuting" class="progress-row">
              <span class="progress-text">
                {{ $t('dataSync.executing') }}
                <template v-if="liveTable"> — {{ liveTable }}：+{{ liveStats.inserts }} ~{{ liveStats.updates }} −{{ liveStats.deletes }}</template>
              </span>
            </div>
          </div>

          <!-- Footer -->
          <div class="footer">
            <n-checkbox v-model:checked="allowDelete" :disabled="isComparing || isExecuting" size="small">
              {{ $t('dataSync.allowDelete') }}
            </n-checkbox>
            <div class="option-group">
              <label class="field-label">{{ $t('dataSync.batchSize') }}</label>
              <n-input-number
                v-model:value="batchSize"
                :min="100" :max="10000" :step="100"
                :disabled="isComparing || isExecuting"
                size="small" style="width: 100px"
              />
            </div>
            <span class="footer-spacer" />
            <n-button v-if="isComparing || isExecuting" type="error" size="small" @click="cancelRun">
              {{ $t('common.cancel') }}
            </n-button>
            <n-button v-else size="small" @click="onClose">{{ $t('common.close') }}</n-button>
            <n-button
              type="default" size="small"
              :disabled="!canCompare" :loading="isComparing"
              @click="runCompare"
            >
              {{ $t('dataSync.compare') }}
            </n-button>
            <n-button
              type="primary" size="small"
              :disabled="!compared || executableCount === 0 || isExecuting || isComparing"
              @click="runExecute"
            >
              {{ $t('dataSync.execute', { n: executableCount }) }}
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
  background: var(--catdb-surface-content);
}

/* --- Titlebar (same chrome as the other tool windows) ---------------------- */
.titlebar {
  position: relative;
  flex: 0 0 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--catdb-fs-small);
  font-weight: 600;
  letter-spacing: 0.2px;
  opacity: 0.85;
  --wails-draggable: drag;
}
.titlebar.win .title { padding-left: 150px; padding-right: 150px; }
.titlebar .window-controls {
  position: absolute;
  top: 0; right: 0; z-index: 10;
  display: flex; flex-direction: row; align-items: stretch;
  height: 100%;
  -webkit-app-region: no-drag;
}
.titlebar .win-btn {
  --wails-draggable: no-drag;
  display: flex; align-items: center; justify-content: center;
  width: 46px; padding: 0; margin: 0;
  border: none; border-radius: 0;
  font: inherit; color: inherit; cursor: default;
  background: transparent;
  transition: background 80ms ease;
}
.titlebar .win-btn svg { width: 14px; height: 14px; opacity: 0.75; }
.titlebar .win-btn:hover { background: var(--catdb-hover-fill); }
.titlebar .win-btn:active { background: var(--catdb-pressed-fill); }
.titlebar .win-btn-close:hover { background: var(--catdb-error); }
.titlebar .win-btn-close:hover svg { opacity: 1; }
.titlebar .win-btn-close:active { background: var(--catdb-error); }

/* --- Body ------------------------------------------------------------------ */
.body {
  flex: 1 1 0;
  min-width: 0; min-height: 0;
  overflow: hidden;
  display: flex;
  border-top: 1px solid var(--catdb-separator);
}
.body > * { flex: 1 1 0; min-width: 0; min-height: 0; }
.loading, .error {
  display: flex; align-items: center; gap: 8px;
  padding: 20px; font-size: var(--catdb-fs-body); opacity: 0.8;
}
.error { color: var(--catdb-error); user-select: text; -webkit-user-select: text; cursor: text; }
.wrapper {
  min-width: 0; min-height: 0; overflow: hidden;
  display: grid; grid-template-rows: 1fr auto;
}
.content {
  min-width: 0; min-height: 0;
  overflow: hidden;
  padding: 16px 20px;
  display: flex; flex-direction: column;
}
.content > :not(.diff-section):not(.tables-section) { flex-shrink: 0; }

/* --- Footer ------------------------------------------------------------------ */
.footer {
  display: flex; align-items: center; gap: 14px;
  padding: 8px 18px;
  border-top: 1px solid var(--catdb-separator);
}
.footer-spacer { flex: 1 1 auto; }
.option-group { display: flex; align-items: center; gap: 6px; }
.option-group .field-label { margin-bottom: 0; }

/* --- Form -------------------------------------------------------------------- */
.grid-cols-2 {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  gap: 8px;
  margin-bottom: 12px;
}
.section { min-width: 0; }
.section-label {
  margin: 0 0 8px; font-size: var(--catdb-fs-body); font-weight: 600;
  display: flex; align-items: center; gap: 6px;
}
.source-label { color: var(--catdb-accent); }
.target-label { color: var(--catdb-success); }
.arrow-col { display: flex; align-items: center; justify-content: center; padding-top: 24px; }
.arrow-icon { width: 22px; height: 22px; opacity: 0.45; }
.field { margin-bottom: 8px; }
.field:last-child { margin-bottom: 0; }
.field-label { display: block; font-size: var(--catdb-fs-small); margin-bottom: 3px; opacity: 0.72; }
.conn-hint { font-size: var(--catdb-fs-small); opacity: 0.6; margin-top: 3px; }
.native-select {
  width: 100%; height: 28px; padding: 0 8px;
  font: inherit; font-size: var(--catdb-fs-body); color: inherit;
  background: var(--catdb-surface-content);
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm); outline: none; box-sizing: border-box;
  transition: border-color 120ms ease, box-shadow 120ms ease;
}
.native-select:hover:not(:disabled) { border-color: var(--catdb-accent); }
.native-select:focus { border-color: var(--catdb-accent); box-shadow: var(--catdb-focus-ring); }
.native-select:disabled { opacity: 0.5; cursor: not-allowed; }
.warning-row {
  background: color-mix(in srgb, var(--catdb-error) 8%, transparent);
  border: 1px solid color-mix(in srgb, var(--catdb-error) 25%, transparent);
  border-radius: var(--catdb-rounded-xs);
  padding: 6px 10px; font-size: var(--catdb-fs-small); color: var(--catdb-error); margin-bottom: 12px;
}

/* --- Tables ------------------------------------------------------------------- */
.tables-section {
  flex: 1 1 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.tables-section :deep(.n-spin-container) { flex: 1 1 0; min-height: 0; }
.table-toolbar { display: flex; gap: 8px; margin-bottom: 6px; }
.search-input {
  flex: 1; height: 26px; padding: 0 8px;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm); background: transparent;
  color: inherit; font: inherit; font-size: var(--catdb-fs-small); outline: none;
}
.search-input:focus { border-color: var(--catdb-accent); }
.select-btn {
  border: none; background: transparent; color: var(--catdb-accent);
  font: inherit; font-size: var(--catdb-fs-small); cursor: default;
  padding: 0 6px; white-space: nowrap;
}
.select-btn:disabled { opacity: 0.4; pointer-events: none; }
.table-list {
  max-height: 240px;
  overflow-y: auto;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
}
.table-row {
  display: flex; align-items: center; gap: 6px;
  padding: 3px 8px; font-size: var(--catdb-fs-small); cursor: default;
}
.table-row:hover { background: var(--catdb-hover-fill); }
.table-row.disabled { opacity: 0.5; pointer-events: none; }
.table-row input[type="checkbox"] { margin: 0; }
.empty-tables { font-size: var(--catdb-fs-small); opacity: 0.5; padding: 8px 0; }
.table-count { font-weight: 400; opacity: 0.55; font-size: var(--catdb-fs-small); }

/* --- Diff list ------------------------------------------------------------------ */
.diff-section {
  flex: 1 1 0;
  min-height: 0;
  display: flex; flex-direction: column;
  margin-top: 4px;
}
.diff-header {
  font-size: var(--catdb-fs-small); opacity: 0.72;
  padding-bottom: 6px;
  display: flex; align-items: center; justify-content: space-between;
}
.diff-list {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
}
.diff-row {
  display: flex; align-items: center; gap: 10px;
  padding: 4px 8px;
  font-size: var(--catdb-fs-small);
  border-bottom: 1px solid var(--catdb-separator);
  /* Skip layout/paint of off-screen rows — keeps 1000+ row lists cheap
     without a virtual scroller (unsupported engines simply ignore it). */
  content-visibility: auto;
  contain-intrinsic-size: auto 25px;
}
.diff-row.inactive { opacity: 0.55; }
.diff-name { font-weight: 600; cursor: default; min-width: 120px; }
.stat { font-variant-numeric: tabular-nums; font-weight: 600; }
.stat.zero { opacity: 0.3; font-weight: 400; }
.stat.ins { color: var(--catdb-success); }
.stat.upd { color: var(--catdb-warning); }
.stat.del { color: var(--catdb-error); }
.scanned { font-size: var(--catdb-fs-mini); opacity: 0.5; }
.tag {
  font-size: var(--catdb-fs-micro); line-height: 1;
  padding: 2px 5px; border-radius: var(--catdb-rounded-xs);
  border: 1px solid transparent; white-space: nowrap;
}
.tag.skipped { background: var(--catdb-hover-fill); opacity: 0.8; }
.tag.running { background: var(--catdb-accent-soft); color: var(--catdb-accent); }
.diff-row.pending { opacity: 0.45; }
.diff-error { color: var(--catdb-error); font-size: var(--catdb-fs-mini); user-select: text; -webkit-user-select: text; cursor: text; }
.stmt-count {
  margin-left: auto;
  font-size: var(--catdb-fs-mini); opacity: 0.6;
  display: inline-flex; align-items: center; gap: 3px;
  white-space: nowrap; cursor: default;
}
.chev { width: 9px; height: 9px; transition: transform 100ms ease; }
.chev.open { transform: rotate(90deg); }
.stmt-block {
  content-visibility: auto;
  contain-intrinsic-size: auto 60px;
  margin: 0;
  padding: 6px 10px 6px 28px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: var(--catdb-fs-mono-small); line-height: 1.5;
  white-space: pre-wrap; word-break: break-all;
  background: var(--catdb-hover-fill);
  border-bottom: 1px solid var(--catdb-separator);
}

/* --- Progress -------------------------------------------------------------------- */
.progress-row { display: flex; flex-direction: column; gap: 4px; margin-top: 10px; padding: 6px 0; }
.progress-text { font-size: var(--catdb-fs-small); opacity: 0.72; font-variant-numeric: tabular-nums; }
</style>
