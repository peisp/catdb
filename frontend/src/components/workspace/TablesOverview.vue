<script setup lang="ts">
// TablesOverview — "所有表" 视图。
//
// 在 ObjectTree 中点击一个数据库（schema）节点时，在右侧打开一个 tab，
// 用 DataGrid 列出该数据库下的所有表及其元信息（Name / Engine / Rows / Comment）。
// 双击表所在的行跳转到该表的数据浏览 tab。
import { computed, ref, watch } from 'vue'
import { NButton, NSpin, useMessage } from 'naive-ui'
import { metadata as metaApi } from '../../api'
import { useQueryStore } from '../../stores/query'
import { LogicalType } from '../../../bindings/catdb/internal/dbdriver/models'
import type { ColumnMeta, TableInfo } from '../../api/metadata'
import DataGrid from '../data-grid/DataGrid.vue'

const props = defineProps<{
  connId: string
  db: string
}>()

const queryStore = useQueryStore()
const message = useMessage()

const tables = ref<TableInfo[]>([])
const loading = ref(false)

// 合成列元数据 — DataGrid 用 nativeType/logicalType 决定对齐 & 编辑器（只读所以不需要编辑器）
const columns: ColumnMeta[] = [
  {
    name: '表名',
    nativeType: 'varchar',
    logicalType: LogicalType.TypeString,
    nullable: true,
    comment: '表名',
  },
  {
    name: '引擎',
    nativeType: 'varchar',
    logicalType: LogicalType.TypeString,
    nullable: true,
    comment: '存储引擎',
  },
  {
    name: '行数',
    nativeType: 'bigint',
    logicalType: LogicalType.TypeBigInt,
    nullable: true,
    comment: '估算行数',
  },
  {
    name: '数据大小',
    nativeType: 'bigint',
    logicalType: LogicalType.TypeBigInt,
    nullable: true,
    comment: '数据占用空间',
  },
  {
    name: '排序规则',
    nativeType: 'varchar',
    logicalType: LogicalType.TypeString,
    nullable: true,
    comment: '字符集排序规则',
  },
  {
    name: '创建时间',
    nativeType: 'datetime',
    logicalType: LogicalType.TypeString,
    nullable: true,
    comment: '表的创建时间',
  },
  {
    name: '修改时间',
    nativeType: 'datetime',
    logicalType: LogicalType.TypeString,
    nullable: true,
    comment: '最近一次修改时间',
  },
  {
    name: '备注',
    nativeType: 'text',
    logicalType: LogicalType.TypeText,
    nullable: true,
    comment: '表备注',
  },
] as ColumnMeta[]

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const v = bytes / Math.pow(1024, i)
  return v < 10 ? v.toFixed(1) + ' ' + units[i] : Math.round(v) + ' ' + units[i]
}

function formatTime(s: string): string {
  if (!s || s === '0000-00-00 00:00:00') return ''
  // Trim trailing sub-second precision if present
  return s.replace(/\.\d+$/, '')
}

// 把 TableInfo[] → any[][] 给 DataGrid
const rows = computed<any[][]>(() => {
  return tables.value.map((t) => [
    t.name,
    t.engine ?? '',
    t.rows ?? 0,
    formatSize(t.dataLength ?? 0),
    t.collation ?? '',
    formatTime(t.createTime ?? ''),
    formatTime(t.updateTime ?? ''),
    t.comment ?? '',
  ])
})

async function load() {
  if (!props.db) {
    tables.value = []
    return
  }
  loading.value = true
  try {
    tables.value = await metaApi.listTables(props.connId, props.db)
  } catch (e: any) {
    message.error(`加载表列表失败: ${String(e)}`)
  } finally {
    loading.value = false
  }
}

// 监听 db 切换 —— 同一个固定的 Overview tab 在 ObjectTree 点不同库时会复用，
// 此处随 props.db 变化重新拉取。
watch(
  () => [props.connId, props.db] as const,
  () => { void load() },
  { immediate: true },
)

// 双击单元格 → 跳到该表的数据浏览 tab
function onDblClickCell(p: { row: number }) {
  const table = tables.value[p.row]
  if (!table) return
  queryStore.openTableTab(props.connId, props.db, table.name, 'table')
}
</script>

<template>
  <div class="to">
    <div class="toolbar">
      <span class="title mono">{{ db || '数据库概览' }}</span>
      <span v-if="db" class="mute">· {{ tables.length }} 张表</span>
      <span class="grow" />
      <n-button size="tiny" :disabled="loading || !db" @click="load">刷新</n-button>
    </div>

    <div v-if="!db" class="empty">
      <span class="mute">在左侧对象树点击一个数据库以查看表概览。</span>
    </div>
    <n-spin v-else :show="loading" class="data-spin">
      <DataGrid
        :columns="columns"
        :rows="rows"
        :editable="false"
        :sortable="true"
        :sort-remote="false"
        :row-height="28"
        @cell-dblclick="onDblClickCell"
      />
    </n-spin>
  </div>
</template>

<style scoped>
.to { display: flex; flex-direction: column; height: 100%; min-width: 0; min-height: 0; overflow: hidden; }
.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--n-border-color, rgba(127,127,127,0.2));
  background: var(--n-color);
  font-size: 12px;
  min-width: 0;
  flex: 0 0 auto;
}
.title { font-size: 12px; }
.mute { opacity: 0.55; font-size: 11px; }
.grow { flex: 1 1 auto; }
.data-spin { flex: 1 1 auto; min-width: 0; min-height: 0; overflow: hidden; padding: 6px; }
.data-spin :deep(.n-spin-container),
.data-spin :deep(.n-spin-content) {
  height: 100%;
  min-width: 0;
  min-height: 0;
}
.empty {
  flex: 1 1 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  padding: 24px;
}
.empty .mute { opacity: 0.55; }
</style>
