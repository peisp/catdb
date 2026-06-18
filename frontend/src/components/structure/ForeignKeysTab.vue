<script setup lang="ts">
// ForeignKeysTab — editable list of FK constraints.
//
// Referenced columns are entered as free-form tags (n-select tag mode)
// because catdb can't reasonably fetch every referenced table's columns just
// for hint completion. The diff layer composes the DDL verbatim from whatever
// the user typed, then MySQL validates at apply time.
import { computed } from 'vue'
import { NButton, NInput, NSelect } from 'naive-ui'
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
            <th>名称</th>
            <th>本表列</th>
            <th>引用库</th>
            <th>引用表</th>
            <th>引用列</th>
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
              <n-select
                v-model:value="row.columns"
                multiple
                size="tiny"
                :options="columnOptions"
                :disabled="busy"
                placeholder="选择列…"
                @update:value="commit"
              />
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
              <n-select
                v-model:value="row.referencedColumns"
                multiple
                tag
                filterable
                size="tiny"
                :disabled="busy"
                placeholder="输入列名 + Enter"
                @update:value="commit"
              />
            </td>
            <td>
              <n-select
                v-model:value="row.onUpdate"
                size="tiny"
                :options="ACTIONS"
                :disabled="busy"
                placeholder="RESTRICT"
                @update:value="commit"
              />
            </td>
            <td>
              <n-select
                v-model:value="row.onDelete"
                size="tiny"
                :options="ACTIONS"
                :disabled="busy"
                placeholder="RESTRICT"
                @update:value="commit"
              />
            </td>
            <td class="td-actions">
              <n-button size="tiny" quaternary :disabled="busy" title="删除" @click="deleteRow(i)">✕</n-button>
            </td>
          </tr>
          <tr v-if="modelValue.length === 0" class="empty-row">
            <td colspan="9" style="text-align: center; color: var(--n-text-color-3); padding: 16px">
              暂无外键，点击下方“添加外键”
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="fk-toolbar">
      <n-button size="tiny" :disabled="busy" @click="addRow">+ 添加外键</n-button>
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
  font-size: 12px;
  table-layout: fixed;
}
.fk-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--n-table-header-color);
  color: var(--n-text-color-2);
  font-weight: 500;
  text-align: left;
  padding: 4px 6px;
  border-bottom: 1px solid var(--n-divider-color);
  white-space: nowrap;
  user-select: none;
}
.fk-table thead th.th-idx { text-align: right; color: var(--n-text-color-3); }
.fk-table tbody td {
  padding: 3px 6px;
  vertical-align: middle;
  border-bottom: 1px solid var(--n-divider-color);
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
  border-top: 1px solid var(--n-divider-color);
  flex: 0 0 auto;
}
</style>
