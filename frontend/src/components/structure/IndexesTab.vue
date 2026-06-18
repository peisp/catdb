<script setup lang="ts">
// IndexesTab — editable list of (non-primary) table indexes.
//
// PRIMARY KEY is owned by the column-level PK checkbox in ColumnsTab; we
// filter the primary entry out here both for display and on edit.
import { computed } from 'vue'
import { NButton, NCheckbox, NInput } from 'naive-ui'
import {
  emptyIndexDraft,
  type ColumnDraft,
  type IndexDraft,
} from '../../lib/alterPlan'

const props = defineProps<{
  modelValue: IndexDraft[]
  /** Column draft list — used to populate the columns-multi-select. */
  columnsDraft: ColumnDraft[]
  busy?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: IndexDraft[]): void
}>()

function commit() {
  emit('update:modelValue', props.modelValue)
}

function addRow() {
  emit('update:modelValue', [...props.modelValue, emptyIndexDraft()])
}
function deleteRow(idx: number) {
  const list = props.modelValue.slice()
  list.splice(idx, 1)
  emit('update:modelValue', list)
}

/** Parse comma-separated column names into string array. */
function onColsInput(row: IndexDraft, val: string) {
  row.columns = val.split(',').map((s) => s.trim()).filter(Boolean)
  commit()
}

// Column option list, filtered to rows with a non-blank name. Sourced from the
// CURRENT columns draft, so a freshly-added column appears in the index editor.
const columnOptions = computed(() =>
  props.columnsDraft
    .filter((c) => c.name.trim() !== '')
    .map((c) => ({ label: c.name, value: c.name })),
)

const editable = computed(() => props.modelValue.filter((ix) => !ix.primary))
const primaryDisplay = computed(() => props.modelValue.find((ix) => ix.primary))
</script>

<template>
  <div class="ix-tab">
    <div v-if="primaryDisplay" class="primary-hint">
      <span class="pk-tag">PRIMARY</span>
      <span class="pk-cols">{{ primaryDisplay.columns.join(', ') }}</span>
      <span class="pk-note">主键在「字段」tab 通过 PK 复选框管理</span>
    </div>
    <div class="ix-table-wrap">
      <table class="ix-table">
        <colgroup>
          <col style="width: 32px" />
          <col style="width: 22%" />
          <col style="width: 38%" />
          <col style="width: 70px" />
          <col style="width: 22%" />
          <col style="width: 60px" />
        </colgroup>
        <thead>
          <tr>
            <th class="th-idx">#</th>
            <th>名称</th>
            <th>列</th>
            <th>UNIQUE</th>
            <th>类型</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, i) in editable"
            :key="row._key"
            :class="{ 'is-new': !row.origName }"
          >
            <td class="td-idx">{{ i + 1 }}</td>
            <td>
              <n-input
                v-model:value="row.name"
                size="tiny"
                placeholder="idx_name"
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td>
              <!-- Column multi-select: comma-separated text + datalist hints -->
            <input
              :value="(row.columns ?? []).join(', ')"
              placeholder="col1, col2, …"
              :disabled="busy"
              class="native-input"
              list="ixColsDatalist"
              @input="onColsInput(row, ($event.target as HTMLInputElement).value)"
            />
            <datalist id="ixColsDatalist">
              <option v-for="opt in columnOptions" :key="opt.value" :value="opt.value" />
            </datalist>
            </td>
            <td class="td-center">
              <n-checkbox
                :checked="row.unique"
                :disabled="busy"
                @update:checked="(v) => { row.unique = !!v; commit() }"
              />
            </td>
            <td>
              <!-- Index type -->
            <select
              :value="(row.type || '').toUpperCase() === 'BTREE' ? '' : (row.type || '').toUpperCase()"
              :disabled="busy"
              class="native-sel"
              @change="(e: any) => { row.type = e.target.value; commit() }"
            >
              <option value="">BTREE (default)</option>
              <option value="HASH">HASH</option>
              <option value="FULLTEXT">FULLTEXT</option>
            </select>
            </td>
            <td class="td-actions">
              <n-button size="tiny" quaternary :disabled="busy" title="删除" @click="deleteRow(modelValue.indexOf(row))">✕</n-button>
            </td>
          </tr>
          <tr v-if="editable.length === 0" class="empty-row">
            <td colspan="6" style="text-align: center; color: var(--n-text-color-3); padding: 16px">
              暂无索引，点击下方“添加索引”
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="ix-toolbar">
      <n-button size="tiny" :disabled="busy" @click="addRow">+ 添加索引</n-button>
    </div>
  </div>
</template>

<style scoped>
.ix-tab {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  padding-left: 6px;
  padding-right: 6px;
}
.primary-hint {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-bottom: 1px solid var(--n-divider-color);
  font-size: 11px;
}
.pk-tag {
  font-weight: 600;
  color: var(--n-info-color);
}
.pk-cols {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  color: var(--n-text-color-1);
}
.pk-note {
  color: var(--n-text-color-3);
}
.ix-table-wrap {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}
.ix-table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  table-layout: fixed;
}
.ix-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--n-color-segment);
  color: var(--n-text-color-2);
  font-weight: 500;
  text-align: left;
  padding: 4px 6px;
  border-bottom: 1px solid var(--n-divider-color);
  white-space: nowrap;
  user-select: none;
}
.ix-table thead th.th-idx { text-align: right; color: var(--n-text-color-2); }
.ix-table tbody td {
  padding: 3px 6px;
  vertical-align: middle;
  border-bottom: 1px solid var(--n-divider-color);
}
.ix-table tbody td.td-idx {
  text-align: right;
  color: var(--n-text-color-3);
  user-select: none;
}
.ix-table tbody td.td-center { text-align: center; }
.ix-table tbody td.td-actions {
  text-align: right;
}
.ix-table tbody td.td-actions :deep(.n-button) {
  padding: 0 4px;
  min-width: 20px;
}
.ix-table tbody tr:hover td { background: var(--n-hover-color); }
.ix-table tbody tr.is-new td:first-child::before {
  content: '+';
  color: var(--n-success-color);
  margin-right: 2px;
}
.ix-toolbar {
  padding: 6px 8px;
  border-top: 1px solid var(--n-divider-color);
  flex: 0 0 auto;
}

/* ---- native select / input ---- */
.native-sel,
.native-input {
  font-size: 12px;
  font-family: inherit;
  width: 100%;
  padding: 2px 4px;
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  background: var(--n-input-color, var(--n-card-color));
  color: var(--n-text-color-1);
  outline: none;
  box-sizing: border-box;
  cursor: pointer;
  line-height: 1.5;
}
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
