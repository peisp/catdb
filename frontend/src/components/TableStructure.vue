<script setup lang="ts">
// TableStructure — Columns | Indexes | Foreign Keys | DDL panels driven by
// MetadataService.GetTableSummary + GetCreateTable. Read-only — actual
// schema changes (ALTER TABLE) are M5+ territory.
import { computed, onMounted, ref, watch } from 'vue'
import {
  NEmpty,
  NSpin,
  NTabPane,
  NTabs,
  NTag,
  useMessage,
} from 'naive-ui'
import { metadata as metaApi } from '../api'
import type { TableSummary } from '../api/metadata'

const props = defineProps<{
  connId: string
  db: string
  table: string
}>()

const message = useMessage()
const summary = ref<TableSummary | null>(null)
const ddl = ref<string>('')
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const [s, d] = await Promise.all([
      metaApi.getTableSummary(props.connId, props.db, props.table),
      metaApi.getCreateTable(props.connId, props.db, props.table),
    ])
    summary.value = s
    ddl.value = d
  } catch (e) {
    message.error(`load structure failed: ${String(e)}`)
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(() => [props.connId, props.db, props.table], load)

const columns = computed(() => summary.value?.columns ?? [])
const indexes = computed(() => summary.value?.indexes ?? [])
const fks = computed(() => summary.value?.foreignKeys ?? [])
</script>

<template>
  <n-spin :show="loading" class="ts">
    <n-tabs type="line" size="small" default-value="cols" pane-class="ts-pane">
      <n-tab-pane name="cols" tab="Columns">
        <div v-if="!columns.length" class="empty"><n-empty size="small" /></div>
        <div v-else class="grid">
          <div class="hd">#</div>
          <div class="hd">Name</div>
          <div class="hd">Type</div>
          <div class="hd">Null</div>
          <div class="hd">Default</div>
          <div class="hd">Extra</div>
          <div class="hd">Comment</div>

          <template v-for="(c, i) in columns" :key="c.name">
            <div class="cell mono mute">{{ i + 1 }}</div>
            <div class="cell">
              {{ c.name }}
              <n-tag v-if="c.isPrimaryKey" size="tiny" type="warning" class="pk">PK</n-tag>
              <n-tag v-if="c.isAutoIncrement" size="tiny" type="info" class="ai">AI</n-tag>
            </div>
            <div class="cell mono">{{ c.nativeType }}</div>
            <div class="cell mono">{{ c.nullable ? 'YES' : 'NO' }}</div>
            <div class="cell mono mute">{{ c.default ?? '' }}</div>
            <div class="cell mono mute">{{ c.isAutoIncrement ? 'auto_increment' : '' }}</div>
            <div class="cell mute">{{ c.comment ?? '' }}</div>
          </template>
        </div>
      </n-tab-pane>

      <n-tab-pane name="ix" tab="Indexes">
        <div v-if="!indexes.length" class="empty"><n-empty size="small" /></div>
        <div v-else class="grid grid-ix">
          <div class="hd">Name</div>
          <div class="hd">Columns</div>
          <div class="hd">Unique</div>
          <div class="hd">Primary</div>
          <div class="hd">Type</div>
          <template v-for="ix in indexes" :key="ix.name">
            <div class="cell">{{ ix.name }}</div>
            <div class="cell mono">{{ (ix.columns ?? []).join(', ') }}</div>
            <div class="cell mono">{{ ix.unique ? 'YES' : 'NO' }}</div>
            <div class="cell mono">{{ ix.primary ? 'YES' : 'NO' }}</div>
            <div class="cell mono">{{ ix.type ?? '' }}</div>
          </template>
        </div>
      </n-tab-pane>

      <n-tab-pane name="fk" tab="Foreign Keys">
        <div v-if="!fks.length" class="empty"><n-empty size="small" /></div>
        <div v-else class="grid grid-fk">
          <div class="hd">Name</div>
          <div class="hd">Columns</div>
          <div class="hd">References</div>
          <div class="hd">On Update</div>
          <div class="hd">On Delete</div>
          <template v-for="fk in fks" :key="fk.name">
            <div class="cell">{{ fk.name }}</div>
            <div class="cell mono">{{ (fk.columns ?? []).join(', ') }}</div>
            <div class="cell mono">
              {{ fk.referencedSchema ? fk.referencedSchema + '.' : '' }}{{ fk.referencedTable }}({{ (fk.referencedColumns ?? []).join(', ') }})
            </div>
            <div class="cell mono">{{ fk.onUpdate ?? '' }}</div>
            <div class="cell mono">{{ fk.onDelete ?? '' }}</div>
          </template>
        </div>
      </n-tab-pane>

      <n-tab-pane name="ddl" tab="DDL">
        <pre class="ddl mono">{{ ddl }}</pre>
      </n-tab-pane>
    </n-tabs>
  </n-spin>
</template>

<style scoped>
.ts { height: 100%; display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden; }
.ts :deep(.n-tabs) { flex: 1 1 auto; min-width: 0; min-height: 0; display: flex; flex-direction: column; }
.ts :deep(.n-tabs-nav) { background: var(--n-color); }
.ts :deep(.n-tab-pane), .ts :deep(.ts-pane) {
  padding: 8px;
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow: auto;
}
.empty { padding: 16px; display: flex; justify-content: center; }
.grid {
  display: grid;
  grid-template-columns: 40px 1fr 1fr 60px 1fr 1fr 1fr;
  gap: 0;
  font-size: 12px;
  border-top: 1px solid var(--n-border-color);
  border-left: 1px solid var(--n-border-color);
  border-radius: 3px;
  overflow: hidden;
}
.grid-ix { grid-template-columns: 1.4fr 2fr 60px 60px 1fr; }
.grid-fk { grid-template-columns: 1.4fr 1.4fr 2fr 1fr 1fr; }
.hd {
  background: var(--n-color-target, rgba(127,127,127,0.08));
  padding: 5px 8px;
  font-weight: 500;
  border-bottom: 1px solid var(--n-border-color);
  border-right: 1px solid var(--n-divider-color);
  position: sticky;
  top: 0;
  z-index: 1;
}
.cell {
  padding: 4px 8px;
  border-bottom: 1px solid var(--n-divider-color);
  border-right: 1px solid var(--n-divider-color);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mute { opacity: 0.6; }
.pk, .ai { margin-left: 6px; }
.ddl {
  margin: 0;
  padding: 10px;
  background: var(--n-card-color);
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  font-size: 12px;
  line-height: 1.45;
  overflow: auto;
  height: 100%;
}
</style>
