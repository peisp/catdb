<script setup lang="ts">
// AiAuditPanel — the "Agent 审计" settings category: the audited statement
// list (filter / paging / export) plus the retention setting and manual clean.
// The list renders with the shared canvas DataGrid (the app-wide table
// component), read-only, with synthetic column metadata.
import { computed, onMounted, ref } from 'vue'
import { CButton, CInputNumber, CSelect, useMessage } from '../ui'
import { agentSettings, connections, system, dialogs } from '../../api'
import type { AuditEntry } from '../../api/agentSettings'
import DataGrid from '../data-grid/DataGrid.vue'
import { setActiveGridContext } from '../../api/gridContextMenu'
import { LogicalType, type ColumnMeta } from '../../api/metadata'
import type { SelectionRange } from '../../composables/useTableSelection'
import { useAgentRuntimeSettings } from './agentRuntime'
import { t as tr } from '../../i18n'

const message = useMessage()
const { settings, load: loadSettings, persist } = useAgentRuntimeSettings()

const auditConns = ref<{ value: string; label: string }[]>([])
const auditConnId = ref('')
const auditSinceDays = ref(0)
const auditEntries = ref<AuditEntry[]>([])
const auditPage = ref(0)
const auditHasMore = ref(false)
const auditLoading = ref(false)
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

// Saves the retention setting (1–30 days; auto-clean runs at app startup and
// on every settings save) and applies it immediately.
async function clearAudit() {
  const days = settings.auditRetentionDays
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
    await persist() // SetAgentSettings auto-cleans server-side
    message.success(tr('agent.settings.audit.cleared'))
    await loadAudit(0)
  } catch (e) {
    message.error(tr('agent.settings.audit.clearFailed', { error: String(e) }))
  }
}

// Audit rows carry the raw connId — show the connection's display name and
// fall back to the id for connections deleted since.
function connLabel(id: string): string {
  return auditConns.value.find((o) => o.value === id)?.label ?? id
}

function fmtTime(v: unknown): string {
  const d = new Date(v as string)
  if (isNaN(d.getTime())) return String(v)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

// ── DataGrid wiring ──
// Synthetic column metadata (computed so headers follow language switch);
// numeric logicalTypes drive right alignment + numeric client sort.
function col(name: string, logicalType: LogicalType): ColumnMeta {
  return { name, nativeType: '', logicalType, nullable: true } as ColumnMeta
}
const gridColumns = computed<ColumnMeta[]>(() => [
  col(tr('agent.settings.audit.colTime'), LogicalType.TypeString),
  col(tr('agent.settings.audit.colConn'), LogicalType.TypeString),
  col(tr('agent.settings.audit.colClass'), LogicalType.TypeString),
  col(tr('agent.settings.audit.colSql'), LogicalType.TypeString),
  col(tr('agent.settings.audit.colStatus'), LogicalType.TypeString),
  col(tr('agent.settings.audit.colRows'), LogicalType.TypeBigInt),
  col(tr('agent.settings.audit.colDuration'), LogicalType.TypeBigInt),
])
const gridColumnNames = computed(() => gridColumns.value.map((c) => c.name))
const gridRows = computed<any[][]>(() =>
  auditEntries.value.map((e) => [
    fmtTime(e.createdAt),
    connLabel(e.connId),
    e.class,
    e.sql,
    e.status,
    e.rows ?? null,
    e.durationMs ?? null,
  ]),
)

// Native copy menu support (catdb-grid-cell), same pattern as ResultTable.
const gridSelection = ref<SelectionRange | null>(null)
function onGridSelectionChange(p: { range: SelectionRange | null }) {
  gridSelection.value = p.range
}
function onGridContextMenu() {
  setActiveGridContext({
    rows: gridRows.value,
    columnNames: gridColumnNames.value,
    selection: gridSelection.value,
  })
}

onMounted(() => {
  loadSettings().catch(() => { /* retention input just shows the default */ })
  loadAuditConns()
  loadAudit(0)
})
</script>

<template>
  <section class="section">
    <div class="audit-filters">
      <CSelect
        v-model:value="auditConnId"
        size="small"
        class="audit-filter-field"
        :options="auditConnOptions"
        @update:value="onAuditFilterChange"
      />
      <CButton size="small" @click="loadAudit(auditPage)">{{ $t('agent.settings.audit.refresh') }}</CButton>
      <CButton size="small" @click="exportAudit('json')">{{ $t('agent.settings.audit.exportJson') }}</CButton>
      <CButton size="small" @click="exportAudit('csv')">{{ $t('agent.settings.audit.exportCsv') }}</CButton>
    </div>

    <div class="audit-grid">
      <p v-if="!auditEntries.length && !auditLoading" class="empty">{{ $t('agent.settings.audit.empty') }}</p>
      <!-- All columns start at their header minimum except SQL (index 3). -->
      <DataGrid
        v-else
        :columns="gridColumns"
        :rows="gridRows"
        :editable="false"
        :sortable="true"
        :sort-remote="false"
        :default-column-width="0"
        :initial-col-widths="[100, 100, null, 320, null, null, null]"
        :row-number-width="34"
        @selection-change="onGridSelectionChange"
        @cell-context-menu="onGridContextMenu"
      />
    </div>

    <div class="audit-footer">
      <div class="audit-paging">
        <CButton size="mini" :disabled="auditPage === 0" @click="loadAudit(auditPage - 1)">
          {{ $t('agent.settings.audit.prev') }}
        </CButton>
        <span class="page-label">{{ $t('agent.settings.audit.page', { n: auditPage + 1 }) }}</span>
        <CButton size="mini" :disabled="!auditHasMore" @click="loadAudit(auditPage + 1)">
          {{ $t('agent.settings.audit.next') }}
        </CButton>
      </div>
      <div class="audit-clear">
        <label class="form-label">{{ $t('agent.settings.audit.clearDaysLabel') }}</label>
        <CInputNumber v-model:value="settings.auditRetentionDays" size="small" class="limit-input" :min="1" :max="30" />
        <CButton size="small" @click="clearAudit">{{ $t('agent.settings.audit.clear') }}</CButton>
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
.empty {
  margin: 0;
  font-size: var(--catdb-fs-small);
  opacity: 0.55;
}
.form-label {
  font-size: var(--catdb-fs-small);
  opacity: 0.85;
}
.limit-input {
  flex: 0 0 150px;
}

.audit-filters {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}
.audit-filter-field {
  flex: 1 1 auto;
  max-width: 280px;
}
.audit-grid {
  /* The grid scrolls inside itself instead of growing the settings page. */
  height: 320px;
  min-height: 0;
  border: 1px solid var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  overflow: hidden;
}
.audit-grid .empty {
  padding: 12px;
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
