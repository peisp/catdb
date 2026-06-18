<script setup lang="ts">
// ExportDialog — small modal that picks a format + path + runs the export.
//
// Path is chosen via the native Save dialog (wailsbridge); we never carry
// the bytes through IPC. Progress events update the Pinia transfer store
// which renders the running row count below.
import { computed, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCheckbox,
  NModal,
  NProgress,
  NRadioButton,
  NRadioGroup,
  NSpace,
  NSpin,
  useMessage,
} from 'naive-ui'
import { system as systemApi, transfer as transferApi } from '../../api'
import type { ExportOptions } from '../../api/transfer'

const props = defineProps<{
  show: boolean
  /** Either { connId, sql } for ad-hoc SQL OR { connId, db, table } for a table dump. */
  source:
    | { kind: 'query'; connId: string; sql: string; defaultName?: string }
    | { kind: 'table'; connId: string; db: string; table: string; defaultName?: string }
}>()
const emit = defineEmits<{ (e: 'update:show', v: boolean): void }>()

const message = useMessage()

type Format = 'csv' | 'json' | 'sql' | 'xlsx'
const format = ref<Format>('csv')
const includeHeader = ref(true)
const includeDDL = ref(true)

const filters = computed(() => {
  switch (format.value) {
    case 'csv': return [{ displayName: 'CSV', pattern: '*.csv' }]
    case 'json': return [{ displayName: 'JSON Lines', pattern: '*.json' }]
    case 'sql': return [{ displayName: 'SQL', pattern: '*.sql' }]
    case 'xlsx': return [{ displayName: 'Excel', pattern: '*.xlsx' }]
  }
})

const defaultName = computed(() => {
  const base = props.source.defaultName
    ?? (props.source.kind === 'table' ? `${props.source.db}.${props.source.table}` : 'export')
  return `${base}.${format.value}`
})

const running = ref(false)
const rowsWritten = ref(0)
const error = ref('')
const lastPath = ref('')

let unsubscribe: (() => void) | null = null
let currentTransferId: string | null = null

async function start() {
  if (running.value) return
  const filtersValue = filters.value
  const path = await systemApi.pickSaveFile('Export', defaultName.value, filtersValue)
  if (!path) return
  lastPath.value = path

  running.value = true
  rowsWritten.value = 0
  error.value = ''
  currentTransferId = null

  unsubscribe?.()
  unsubscribe = transferApi.onProgress((p) => {
    if (currentTransferId && p.transferId !== currentTransferId) return
    rowsWritten.value = p.rows
    if (p.error) error.value = p.error
    if (p.done) {
      running.value = false
      unsubscribe?.()
      unsubscribe = null
    }
  })

  const opts: ExportOptions = {
    format,
    path,
    batchSize: 1000,
    includeHeader: format.value === 'csv' ? includeHeader.value : false,
    includeDDL: format.value === 'sql' ? includeDDL.value : false,
    tableName: props.source.kind === 'table' ? props.source.table : '',
  } as unknown as ExportOptions
  opts.format = format.value as any

  try {
    let result
    if (props.source.kind === 'table') {
      result = await transferApi.exportTable(props.source.connId, props.source.db, props.source.table, opts)
    } else {
      result = await transferApi.exportQuery(props.source.connId, props.source.sql, opts)
    }
    currentTransferId = result.transferId
    rowsWritten.value = Number(result.rowsTotal ?? rowsWritten.value)
    message.success(`Exported ${result.rowsTotal} rows → ${path}`)
  } catch (e) {
    error.value = String(e)
    message.error(`Export failed: ${String(e)}`)
  } finally {
    running.value = false
    unsubscribe?.()
    unsubscribe = null
  }
}

watch(
  () => props.show,
  (v) => {
    if (!v) {
      unsubscribe?.()
      unsubscribe = null
      running.value = false
      rowsWritten.value = 0
      error.value = ''
      lastPath.value = ''
    }
  },
)
</script>

<template>
  <n-modal
    :show="show"
    title="Export"
    preset="card"
    size="small"
    style="width: 480px"
    :mask-closable="!running"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <n-space vertical :size="12">
      <div>
        <span class="lbl">Format</span>
        <n-radio-group v-model:value="format" size="small">
          <n-radio-button value="csv">CSV</n-radio-button>
          <n-radio-button value="json">JSON Lines</n-radio-button>
          <n-radio-button value="sql">SQL</n-radio-button>
          <n-radio-button value="xlsx">Excel</n-radio-button>
        </n-radio-group>
      </div>

      <div v-if="format === 'csv'">
        <n-checkbox v-model:checked="includeHeader" size="small">Include header row</n-checkbox>
      </div>
      <div v-if="format === 'sql' && source.kind === 'table'">
        <n-checkbox v-model:checked="includeDDL" size="small">Include CREATE TABLE statement</n-checkbox>
      </div>

      <n-alert v-if="error" type="error" :show-icon="false" size="small" class="mono">
        {{ error }}
      </n-alert>

      <div v-if="running || rowsWritten > 0" class="status">
        <n-spin v-if="running" size="small" />
        <span class="mono">{{ rowsWritten }} rows written</span>
        <span v-if="lastPath" class="mono mute">→ {{ lastPath }}</span>
      </div>

      <n-space justify="end" :size="8">
        <n-button size="small" @click="emit('update:show', false)" :disabled="running">Close</n-button>
        <n-button size="small" type="primary" @click="start" :loading="running">Export…</n-button>
      </n-space>
    </n-space>
  </n-modal>
</template>

<style scoped>
.lbl { font-size: 12px; opacity: 0.7; margin-right: 8px; }
.status {
  display: flex; align-items: center; gap: 10px;
  font-size: 12px;
}
.mute { opacity: 0.5; }
</style>
