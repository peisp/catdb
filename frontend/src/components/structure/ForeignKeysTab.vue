<script setup lang="ts">
// ForeignKeysTab — editable list of FK constraints.
//
// Referenced columns are entered as comma-separated text in a native <input>
// (with <datalist> hints from the current table's column names) because catdb
// can't reasonably fetch every referenced table's columns just for completion.
// The diff layer composes the DDL verbatim from whatever the user typed, then
// MySQL validates at apply time.
import { computed } from 'vue'
import { NButton, NInput } from 'naive-ui'
import {
  emptyForeignKeyDraft,
  type ColumnDraft,
  type ForeignKeyDraft,
} from '../../lib/alterPlan'

const props = defineProps<{
  modelValue: ForeignKeyDraft[]
  columnsDraft: ColumnDraft[]
  /** Current database name — used as the default referencedSchema for new FKs. */
  currentDb: string
  busy?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: ForeignKeyDraft[]): void
}>()

function commit() {
  emit('update:modelValue', props.modelValue)
}
function addRow() {
  const row = emptyForeignKeyDraft()
  row.referencedSchema = props.currentDb
  emit('update:modelValue', [...props.modelValue, row])
}
function deleteRow(idx: number) {
  const list = props.modelValue.slice()
  list.splice(idx, 1)
  emit('update:modelValue', list)
}

/** Parse comma-separated text input into string array. */
function onRefColsInput(row: ForeignKeyDraft, val: string) {
  row.referencedColumns = val.split(',').map((s) => s.trim()).filter(Boolean)
  commit()
}

/** Parse comma-separated local column names. */
function onLocalColsInput(row: ForeignKeyDraft, val: string) {
  row.columns = val.split(',').map((s) => s.trim()).filter(Boolean)
  commit()
}

const columnOptions = computed(() =>
  props.columnsDraft
    .filter((c) => c.name.trim() !== '')
    .map((c) => ({ label: c.name, value: c.name })),
)

const ACTIONS = [
  { label: 'RESTRICT', value: 'RESTRICT' },
  { label: 'CASCADE', value: 'CASCADE' },
  { label: 'SET NULL', value: 'SET NULL' },
  { label: 'NO ACTION', value: 'NO ACTION' },
]
</script>

<template>
  <div class="fk-tab">
    <div class="fk-table-wrap">
      <table class="fk-table">
        <colgroup>
          <col style="width: 32px" />
          <col style="width: 16%" />
          <col style="width: 18%" />
          <col style="width: 12%" />
          <col style="width: 14%" />
          <col style="width: 18%" />
          <col style="width: 10%" />
          <col style="width: 10%" />
          <col style="width: 50px" />
        </colgroup>
        <thead>
          <tr>
            <th class="th-idx">#</th>
            <th>{{ $t('structure.fk.thName') }}</th>
            <th>{{ $t('structure.fk.thLocalCols') }}</th>
            <th>{{ $t('structure.fk.thRefDb') }}</th>
            <th>{{ $t('structure.fk.thRefTable') }}</th>
            <th>{{ $t('structure.fk.thRefCols') }}</th>
            <th>ON UPDATE</th>
            <th>ON DELETE</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, i) in modelValue"
            :key="row._key"
            :class="{ 'is-new': !row.origName }"
          >
            <td class="td-idx">{{ i + 1 }}</td>
            <td>
              <n-input
                v-model:value="row.name"
                size="tiny"
                placeholder="fk_name"
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td>
                          <!-- Local column multi-select: comma-separated text + datalist -->
            <input
              :value="(row.columns ?? []).join(', ')"
              placeholder="col1, col2, …"
              :disabled="busy"
              class="native-input"
              list="fkLocalColsDatalist"
              @input="onLocalColsInput(row, ($event.target as HTMLInputElement).value)"
            />
            <datalist id="fkLocalColsDatalist">
              <option v-for="opt in columnOptions" :key="opt.value" :value="opt.value" />
            </datalist>
            </td>
            <td>
              <n-input
                v-model:value="row.referencedSchema"
                size="tiny"
                :placeholder="currentDb"
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td>
              <n-input
                v-model:value="row.referencedTable"
                size="tiny"
                placeholder="table"
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td>
                          <!-- Referenced columns: text input with datalist hints (comma-separated) -->
            <input
              :value="(row.referencedColumns ?? []).join(', ')"
              placeholder="col1, col2, …"
              :disabled="busy"
              class="native-input"
              list="refColsDatalist"
              @input="onRefColsInput(row, ($event.target as HTMLInputElement).value)"
            />
            <datalist id="refColsDatalist">
              <option v-for="opt in columnOptions" :key="opt.value" :value="opt.value" />
            </datalist>
            </td>
            <td>
                          <!-- ON UPDATE -->
            <select
              v-model="row.onUpdate"
              :disabled="busy"
              class="native-sel"
              @change="commit"
            >
              <option value="">RESTRICT (default)</option>
              <option v-for="a in ACTIONS" :key="a.value" :value="a.value">{{ a.label }}</option>
            </select>
            </td>
            <td>
                          <!-- ON DELETE -->
            <select
              v-model="row.onDelete"
              :disabled="busy"
              class="native-sel"
              @change="commit"
            >
              <option value="">RESTRICT (default)</option>
              <option v-for="a in ACTIONS" :key="a.value" :value="a.value">{{ a.label }}</option>
            </select>
            </td>
            <td class="td-actions">
              <n-button size="tiny" quaternary :disabled="busy" :title="$t('common.delete')" @click="deleteRow(i)">✕</n-button>
            </td>
          </tr>
          <tr v-if="modelValue.length === 0" class="empty-row">
            <td colspan="9" style="text-align: center; color: var(--n-text-color-3); padding: 16px">
              {{ $t('structure.fk.empty') }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="fk-toolbar">
      <n-button size="tiny" :disabled="busy" @click="addRow">{{ $t('structure.fk.addRow') }}</n-button>
    </div>
  </div>
</template>

<style scoped>
.fk-tab {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  margin: 6px 6px;
  background-color: var(--catdb-surface-content);
}
.fk-table-wrap {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}
.fk-table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  font-size: var(--catdb-fs-small);
  table-layout: fixed;
}
.fk-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--n-color-segment);
  color: var(--n-text-color-2);
  font-weight: 600;
  text-align: left;
  padding: 4px 6px;
  border-bottom: 1px solid var(--catdb-separator);
  white-space: nowrap;
  user-select: none;
}
.fk-table thead th.th-idx { text-align: right; color: var(--n-text-color-2); }
.fk-table tbody td {
  padding: 3px 6px;
  vertical-align: middle;
  border-bottom: 1px solid var(--catdb-separator);
}
.fk-table tbody td.td-idx {
  text-align: right;
  color: var(--n-text-color-3);
  user-select: none;
}
.fk-table tbody td.td-actions { text-align: right; }
.fk-table tbody td.td-actions :deep(.n-button) {
  padding: 0 4px;
  min-width: 20px;
}
.fk-table tbody tr:hover td { background: var(--n-hover-color); }
.fk-table tbody tr.is-new td:first-child::before {
  content: '+';
  color: var(--n-success-color);
  margin-right: 2px;
}
.fk-toolbar {
  padding: 6px 8px;
  border-top: 1px solid var(--catdb-separator);
  flex: 0 0 auto;
}

/* ---- native select / input ---- */
.native-sel,
.native-input {
  font-size: var(--catdb-fs-small);
  font-family: inherit;
  width: 100%;
  padding: 2px 4px;
  border: var(--catdb-separator);
  border-radius: var(--catdb-rounded-sm);
  background: var(--n-input-color, var(--n-card-color));
  color: var(--n-text-color-1);
  outline: none;
  box-sizing: border-box;
  line-height: 1.5;
}
.native-sel { cursor: pointer; }
.native-sel:disabled,
.native-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 120ms ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
