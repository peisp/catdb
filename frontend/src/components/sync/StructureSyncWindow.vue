<script setup lang="ts">
// StructureSyncWindow — the root view of the standalone Structure
// Synchronization child window. Loaded when location.hash starts with
// #/structure-sync.
//
// Flow: pick source + target → Compare (read-only, backend schemadiff) →
// review per-object DDL (destructive items default to unchecked) → Execute
// the checked statements on the target with per-statement progress.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Window } from '@wailsio/runtime'
import { NButton, NCheckbox, NProgress, NSpin, useMessage } from 'naive-ui'
import { useConnectionsStore } from '../../stores/connections'
import { sync as syncApi, metadata as metadataApi, dialogs, connections as connectionsApi } from '../../api'
import type { SchemaObjectDiff } from '../../api/sync'
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

// Target can only connect to the same database type as the source — cross-
// driver structure sync isn't meaningful. Source stays unrestricted.
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
watch([srcDb, tgtDb], clearResult)

const sameSourceTarget = computed(
  () => !!srcConnId.value && !!tgtConnId.value &&
    srcConnId.value + srcDb.value === tgtConnId.value + tgtDb.value,
)

const canCompare = computed(
  () => !!(srcConnId.value && srcDb.value && tgtConnId.value && tgtDb.value) &&
    !sameSourceTarget.value && !isComparing.value && !isExecuting.value,
)

// --- compare -------------------------------------------------------------------

const isComparing = ref(false)
const compared = ref(false)
const objects = ref<SchemaObjectDiff[]>([])
const checked = ref<Set<string>>(new Set())
const expanded = ref<Set<string>>(new Set())
const aborter = ref<AbortController | null>(null)
// Live-compare state: which object is being read right now + how many are done.
const comparingName = ref('')
const comparedDone = ref(0)

// Stable row key — live rows get replaced in place as results stream in, so
// selection/expansion must not be index-based.
function objKey(o: SchemaObjectDiff): string {
  return `${o.kind}:${o.name}`
}

function clearResult() {
  compared.value = false
  objects.value = []
  checked.value = new Set()
  expanded.value = new Set()
  execDone.value = false
  execFailures.value = []
  comparingName.value = ''
  comparedDone.value = 0
}

function selectable(o: SchemaObjectDiff): boolean {
  return !o.error && o.status !== 'same' && o.status !== 'comparing' && (o.statements?.length ?? 0) > 0
}

// Progress events are buffered and flushed at most ~10×/s: with the bulk
// metadata fast path a 1000-table compare can burst thousands of events in a
// couple of seconds, and re-rendering the whole list per event would freeze
// the very UI the events exist to keep alive.
let pendingCmp: syncApi.SchemaCompareProgress[] = []
let cmpFlushTimer: ReturnType<typeof setTimeout> | undefined

function flushCmpEvents() {
  cmpFlushTimer = undefined
  if (pendingCmp.length === 0) return
  const evts = pendingCmp
  pendingCmp = []
  const list = [...objects.value]
  const idxByKey = new Map(list.map((o, i) => [objKey(o), i]))
  let doneDelta = 0
  let lastComparing = comparingName.value
  for (const p of evts) {
    const key = `${p.kind}:${p.name}`
    if (p.phase === 'object-start') {
      lastComparing = p.name
      if (!idxByKey.has(key)) {
        idxByKey.set(key, list.length)
        list.push({ name: p.name, kind: p.kind, status: 'comparing', statements: [] } as unknown as SchemaObjectDiff)
      }
    } else if (p.phase === 'object-done' && p.object) {
      doneDelta++
      lastComparing = ''
      const i = idxByKey.get(key)
      if (i !== undefined) list[i] = p.object
      else {
        idxByKey.set(key, list.length)
        list.push(p.object)
      }
    }
  }
  objects.value = list
  comparedDone.value += doneDelta
  comparingName.value = lastComparing
}

function stopCmpBuffer() {
  if (cmpFlushTimer) {
    clearTimeout(cmpFlushTimer)
    cmpFlushTimer = undefined
  }
  pendingCmp = []
}

async function runCompare() {
  if (!canCompare.value) return
  isComparing.value = true
  clearResult()
  const ac = new AbortController()
  aborter.value = ac
  // Stream per-object results into the list while the compare runs — large
  // databases would otherwise look frozen.
  const offCmp = syncApi.onSchemaCompareProgress((p) => {
    if (!p.syncId.startsWith('sc-')) return
    pendingCmp.push(p)
    if (!cmpFlushTimer) cmpFlushTimer = setTimeout(flushCmpEvents, 100)
  })
  try {
    const res = await syncApi.compareSchemas({
      sourceConnId: srcConnId.value,
      sourceDb: srcDb.value,
      sourceSchema: '',
      targetConnId: tgtConnId.value,
      targetDb: tgtDb.value,
      targetSchema: '',
      tables: [],
    }, ac.signal)
    objects.value = res.objects ?? []
    // Default selection: every actionable object except destructive ones.
    const sel = new Set<string>()
    objects.value.forEach((o) => {
      if (selectable(o) && !o.destructive) sel.add(objKey(o))
    })
    checked.value = sel
    compared.value = true
  } catch (err: any) {
    if (err?.name === 'AbortError' || String(err?.message ?? err).includes('cancel')) {
      message.info(t('structSync.compareCancelled'))
    } else {
      message.error(t('structSync.compareFailed', { error: err?.message ?? String(err) }))
    }
  } finally {
    offCmp()
    stopCmpBuffer()
    aborter.value = null
    isComparing.value = false
  }
}

const diffCount = computed(() => objects.value.filter((o) => o.status !== 'same').length)

function toggleChecked(o: SchemaObjectDiff) {
  if (!selectable(o)) return
  const s = new Set(checked.value)
  const k = objKey(o)
  if (s.has(k)) s.delete(k)
  else s.add(k)
  checked.value = s
}

function toggleExpanded(o: SchemaObjectDiff) {
  const s = new Set(expanded.value)
  const k = objKey(o)
  if (s.has(k)) s.delete(k)
  else s.add(k)
  expanded.value = s
}

const selectedStatements = computed(() => {
  const out: string[] = []
  objects.value.forEach((o) => {
    if (checked.value.has(objKey(o))) out.push(...(o.statements ?? []))
  })
  return out
})

const hasDestructiveSelected = computed(() =>
  objects.value.some((o) => checked.value.has(objKey(o)) && o.destructive),
)

// --- execute -------------------------------------------------------------------

const isExecuting = ref(false)
const stopOnError = ref(true)
const execProgress = ref({ index: 0, total: 0 })
const execDone = ref(false)
const execFailures = ref<{ statement: string; error: string }[]>([])

let offProgress: (() => void) | null = null
onUnmounted(() => { offProgress?.() })

async function runExecute() {
  if (selectedStatements.value.length === 0 || isExecuting.value) return
  if (hasDestructiveSelected.value) {
    const choice = await dialogs.confirm({
      kind: 'warning',
      title: t('structSync.confirmTitle'),
      message: t('structSync.confirmDestructive'),
      buttons: [
        { value: 'execute', label: t('structSync.confirmExecute') },
        { value: 'cancel', label: t('common.cancel'), isCancel: true, isDefault: true },
      ],
    })
    if (choice !== 'execute') return
  }
  isExecuting.value = true
  execDone.value = false
  execFailures.value = []
  execProgress.value = { index: 0, total: selectedStatements.value.length }
  const ac = new AbortController()
  aborter.value = ac
  offProgress = syncApi.onSyncProgress((p) => {
    if (p.syncId.startsWith('ss-')) {
      execProgress.value = { index: p.index, total: p.total }
    }
  })
  try {
    const res = await syncApi.executeSchemaSync({
      targetConnId: tgtConnId.value,
      targetDb: tgtDb.value,
      statements: selectedStatements.value,
      stopOnError: stopOnError.value,
    }, ac.signal)
    execFailures.value = (res.results ?? [])
      .filter((r) => r.error)
      .map((r) => ({ statement: r.statement, error: r.error! }))
    execDone.value = true
    if (res.failed > 0) {
      message.warning(t('structSync.executeFailed', { n: res.failed }))
    } else {
      message.success(t('structSync.executeDone', { n: res.executed }))
    }
    // Re-compare so the list reflects the new target state.
    await runCompare()
  } catch (err: any) {
    if (err?.name === 'AbortError' || String(err?.message ?? err).includes('cancel')) {
      message.info(t('structSync.executeCancelled'))
    } else {
      message.error(t('structSync.executeError', { error: err?.message ?? String(err) }))
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

function statusLabel(s: string): string {
  switch (s) {
    case 'create': return t('structSync.statusCreate')
    case 'drop': return t('structSync.statusDrop')
    case 'alter': return t('structSync.statusAlter')
    case 'comparing': return t('structSync.statusComparing')
    default: return t('structSync.statusSame')
  }
}
</script>

<template>
  <div class="root">
    <header class="titlebar" :class="{ win: isWin }" @dblclick="toggleMaximise">
      <span class="title">{{ $t('structSync.title') }}</span>
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
                <h3 class="section-label source-label">{{ $t('structSync.source') }}</h3>
                <div class="field">
                  <label class="field-label">{{ $t('structSync.connection') }}</label>
                  <select v-model="srcConnId" class="native-select" :disabled="isComparing || isExecuting">
                    <option value="" disabled>{{ $t('structSync.selectConnection') }}</option>
                    <option v-for="c in connOptions" :key="c.value" :value="c.value">{{ c.label }}</option>
                  </select>
                  <div v-if="srcServerInfo" class="conn-hint">{{ srcServerInfo }}</div>
                </div>
                <div class="field">
                  <label class="field-label">{{ $t('structSync.database') }}</label>
                  <select v-model="srcDb" class="native-select" :disabled="isComparing || isExecuting || !srcConnId">
                    <option value="" disabled>{{ $t('structSync.selectDatabase') }}</option>
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
                <h3 class="section-label target-label">{{ $t('structSync.target') }}</h3>
                <div class="field">
                  <label class="field-label">{{ $t('structSync.connection') }}</label>
                  <select
                    v-model="tgtConnId" class="native-select"
                    :disabled="isComparing || isExecuting"
                    :title="$t('common.sameDriverOnly')"
                  >
                    <option value="" disabled>{{ $t('structSync.selectConnection') }}</option>
                    <option v-for="c in tgtConnOptions" :key="c.value" :value="c.value">{{ c.label }}</option>
                  </select>
                  <div v-if="tgtServerInfo" class="conn-hint">{{ tgtServerInfo }}</div>
                  <div v-else-if="srcConnId && tgtConnOptions.length === 0" class="conn-hint">{{ $t('common.noSameDriverConn') }}</div>
                </div>
                <div class="field">
                  <label class="field-label">{{ $t('structSync.database') }}</label>
                  <select v-model="tgtDb" class="native-select" :disabled="isComparing || isExecuting || !tgtConnId">
                    <option value="" disabled>{{ $t('structSync.selectDatabase') }}</option>
                    <option v-for="d in tgtDatabases" :key="d" :value="d">{{ d }}</option>
                  </select>
                </div>
              </div>
            </div>

            <div v-if="sameSourceTarget" class="warning-row">{{ $t('structSync.sameSourceTarget') }}</div>

            <!-- Diff list (fills live while comparing) -->
            <div v-if="compared || isComparing" class="diff-section">
              <div class="diff-header">
                <span v-if="isComparing">
                  {{ $t('structSync.comparingLive', { n: comparedDone }) }}
                  <template v-if="comparingName"> — {{ comparingName }}</template>
                </span>
                <span v-else>{{ $t('structSync.diffSummary', { total: objects.length, diff: diffCount }) }}</span>
              </div>
              <div v-if="compared && diffCount === 0" class="empty-diff">{{ $t('structSync.noDiff') }}</div>
              <div v-else class="diff-list">
                <template v-for="o in objects" :key="objKey(o)">
                  <div v-if="o.status !== 'same'" class="diff-row" :class="{ inactive: !selectable(o), comparing: o.status === 'comparing' }">
                    <input
                      type="checkbox"
                      :checked="checked.has(objKey(o))"
                      :disabled="!selectable(o) || isExecuting"
                      @change="toggleChecked(o)"
                    />
                    <span class="diff-name" @click="toggleExpanded(o)">{{ o.name }}</span>
                    <span class="tag kind">{{ o.kind === 'view' ? $t('structSync.kindView') : $t('structSync.kindTable') }}</span>
                    <span class="tag" :class="'st-' + o.status">{{ statusLabel(o.status) }}</span>
                    <span v-if="o.destructive" class="tag destructive">{{ $t('structSync.destructive') }}</span>
                    <span v-if="o.error" class="diff-error">{{ o.error }}</span>
                    <span v-if="o.status !== 'comparing'" class="stmt-count" @click="toggleExpanded(o)">
                      {{ $t('structSync.statementCount', { n: o.statements?.length ?? 0 }) }}
                      <svg viewBox="0 0 10 10" class="chev" :class="{ open: expanded.has(objKey(o)) }" aria-hidden="true">
                        <path d="M3 2l4 3-4 3" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
                      </svg>
                    </span>
                  </div>
                  <pre v-if="o.status !== 'same' && expanded.has(objKey(o))" class="stmt-block">{{ (o.statements ?? []).join('\n') }}</pre>
                </template>
              </div>
            </div>

            <!-- Execution progress -->
            <div v-if="isExecuting" class="progress-row">
              <n-progress
                type="line"
                :percentage="execProgress.total ? Math.round((execProgress.index / execProgress.total) * 100) : 0"
                :show-indicator="false" :height="4" :border-radius="2"
              />
              <span class="progress-text">{{ $t('structSync.executing', { done: execProgress.index, total: execProgress.total }) }}</span>
            </div>

            <!-- Failures -->
            <div v-if="execDone && execFailures.length > 0" class="failures">
              <div v-for="(f, fi) in execFailures" :key="fi" class="failure-item">
                <code class="failure-stmt">{{ f.statement }}</code>
                <span class="failure-error">{{ f.error }}</span>
              </div>
            </div>
          </div>

          <!-- Footer -->
          <div class="footer">
            <n-checkbox v-model:checked="stopOnError" :disabled="isExecuting" size="small">
              {{ $t('structSync.stopOnError') }}
            </n-checkbox>
            <span v-if="compared" class="footer-info">
              {{ $t('structSync.selectedStatements', { n: selectedStatements.length }) }}
            </span>
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
              {{ $t('structSync.compare') }}
            </n-button>
            <n-button
              type="primary" size="small"
              :disabled="!compared || selectedStatements.length === 0 || isExecuting || isComparing"
              @click="runExecute"
            >
              {{ $t('structSync.execute') }}
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

/* --- Titlebar (same chrome as TransferEditorWindow) ------------------------ */
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
.content > :not(.diff-section) { flex-shrink: 0; }

/* --- Footer ------------------------------------------------------------------ */
.footer {
  display: flex; align-items: center; gap: 10px;
  padding: 8px 18px;
  border-top: 1px solid var(--catdb-separator);
}
.footer-info { font-size: var(--catdb-fs-mini); opacity: 0.55; }
.footer-spacer { flex: 1 1 auto; }

/* --- Form (same as transfer) ------------------------------------------------- */
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

/* --- Diff list ----------------------------------------------------------------- */
.diff-section {
  flex: 1 1 0;
  min-height: 0;
  display: flex; flex-direction: column;
  margin-top: 4px;
}
.diff-header {
  font-size: var(--catdb-fs-small); opacity: 0.72;
  padding-bottom: 6px;
}
.empty-diff { font-size: var(--catdb-fs-small); opacity: 0.5; padding: 12px 0; }
.diff-list {
  flex: 1 1 0;
  min-height: 0;
  overflow-y: auto;
  border: 1px solid var(--catdb-control-border);
  border-radius: var(--catdb-rounded-sm);
}
.diff-row {
  display: flex; align-items: center; gap: 8px;
  padding: 4px 8px;
  font-size: var(--catdb-fs-small);
  border-bottom: 1px solid var(--catdb-separator);
  /* Skip layout/paint of off-screen rows — keeps 1000+ row lists cheap
     without a virtual scroller (unsupported engines simply ignore it). */
  content-visibility: auto;
  contain-intrinsic-size: auto 25px;
}
.diff-row.inactive { opacity: 0.55; }
.diff-row input[type="checkbox"] { margin: 0; }
.diff-name { font-weight: 600; cursor: default; }
.tag {
  font-size: var(--catdb-fs-micro); line-height: 1;
  padding: 2px 5px; border-radius: var(--catdb-rounded-xs);
  border: 1px solid transparent;
  white-space: nowrap;
}
.tag.kind { border-color: var(--catdb-control-border); opacity: 0.7; }
.tag.st-comparing { background: var(--catdb-hover-fill); opacity: 0.75; }
.diff-row.comparing .diff-name { opacity: 0.6; }
.tag.st-create { background: color-mix(in srgb, var(--catdb-success) 12%, transparent); color: var(--catdb-success); }
.tag.st-alter { background: color-mix(in srgb, var(--catdb-warning) 14%, transparent); color: var(--catdb-warning); }
.tag.st-drop { background: color-mix(in srgb, var(--catdb-error) 12%, transparent); color: var(--catdb-error); }
.tag.destructive { background: color-mix(in srgb, var(--catdb-error) 16%, transparent); color: var(--catdb-error); font-weight: 600; }
.diff-error { color: var(--catdb-error); font-size: var(--catdb-fs-mini); user-select: text; -webkit-user-select: text; cursor: text; }
.stmt-count {
  margin-left: auto;
  font-size: var(--catdb-fs-mini); opacity: 0.6;
  display: inline-flex; align-items: center; gap: 3px;
  white-space: nowrap;
  cursor: default;
}
.chev { width: 9px; height: 9px; transition: transform 100ms ease; }
.chev.open { transform: rotate(90deg); }
.stmt-block {
  content-visibility: auto;
  contain-intrinsic-size: auto 60px;
  margin: 0;
  padding: 6px 10px 6px 28px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: var(--catdb-fs-mono-small);
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--catdb-hover-fill);
  border-bottom: 1px solid var(--catdb-separator);
}

/* --- Progress / failures ---------------------------------------------------- */
.progress-row { display: flex; flex-direction: column; gap: 4px; margin-top: 12px; padding: 8px 0; }
.progress-text { font-size: var(--catdb-fs-small); opacity: 0.72; }
.failures {
  margin-top: 8px;
  max-height: 140px;
  overflow-y: auto;
  border: 1px solid color-mix(in srgb, var(--catdb-error) 35%, transparent);
  border-radius: var(--catdb-rounded-xs);
  padding: 6px 10px;
}
.failure-item { padding: 3px 0; font-size: var(--catdb-fs-mini); }
.failure-stmt { display: block; opacity: 0.75; word-break: break-all; user-select: text; -webkit-user-select: text; cursor: text; }
.failure-error { color: var(--catdb-error); user-select: text; -webkit-user-select: text; cursor: text; }
</style>
