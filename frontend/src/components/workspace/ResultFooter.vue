<script setup lang="ts">
// ResultFooter —— TableBrowser 与 ResultTable 共用的表格底部条。
// 布局：‹ 页码 › [总行数/点击加载] [slot] [SQL 展示+复制] [每页条数] [导出?]
import { computed, ref, watch } from 'vue'
import { useMessage } from 'naive-ui'
import { t } from '../../i18n'

const props = withDefaults(defineProps<{
  page: number
  pageSize: number
  pageSizeOptions: Array<{ label: string; value: number }>
  hasPrev: boolean
  hasNext: boolean
  /** All-rows 模式下禁用页码输入。 */
  pagerDisabled?: boolean
  /** 总行数；null = 未知。 */
  total?: number | null
  /** 总数只是已加载数的下界（编辑器结果被截断/未拉完）→ 显示 "N+"。 */
  totalPartial?: boolean
  /** total 为 null 时显示「点击加载总行数」按钮（表数据浏览）。 */
  canLoadTotal?: boolean
  countLoading?: boolean
  sql?: string
  dmlSql?: string
  dmlLabel?: string
  showExport?: boolean
  exportDisabled?: boolean
}>(), { total: null })

const emit = defineEmits<{
  (e: 'update:page', v: number): void
  (e: 'update:pageSize', v: number): void
  (e: 'load-total'): void
  (e: 'export', format: string): void
}>()

const message = useMessage()

// 页码输入与 page 解耦：用户可自由输入，Enter/blur 才提交。
const pageInput = ref(String(props.page))
watch(() => props.page, (v) => { pageInput.value = String(v) })

function commitPageInput() {
  const n = Math.floor(Number(pageInput.value))
  if (!Number.isFinite(n) || n < 1) { pageInput.value = String(props.page); return }
  if (n !== props.page) emit('update:page', n)
}

// 总页数：总数已知且分页模式时显示在页码旁。
const totalPages = computed(() => {
  if (props.total == null || props.pagerDisabled || props.pageSize <= 0) return null
  return Math.max(1, Math.ceil(props.total / props.pageSize))
})

const sizeModel = computed({
  get: () => props.pageSize,
  set: (v: number) => emit('update:pageSize', Number(v)),
})

const sqlHover = ref(false)

function displaySql(): string {
  return props.dmlSql || props.sql || ''
}

async function copySql() {
  const sql = displaySql()
  if (!sql) return
  try { await navigator.clipboard.writeText(sql); message.success(t('resultFooter.sqlCopied')) }
  catch (e) { message.error(t('common.copyFailed', { error: String(e) })) }
}

function onExportSelect(ev: Event) {
  const el = ev.target as HTMLSelectElement
  if (!el.value) return
  emit('export', el.value)
  el.value = ''
}
</script>

<template>
  <div class="footer">
    <div class="pager">
      <button
        class="pgbtn"
        :disabled="!hasPrev"
        :title="$t('resultFooter.prevPage')"
        @click="emit('update:page', page - 1)"
      >‹</button>
      <input
        v-model="pageInput"
        class="page-input mono"
        inputmode="numeric"
        :disabled="pagerDisabled"
        @keydown.enter.prevent="commitPageInput"
        @blur="commitPageInput"
      />
      <span v-if="totalPages !== null" class="mono mute total-pages">/ {{ totalPages }}</span>
      <button
        class="pgbtn"
        :disabled="!hasNext"
        :title="$t('resultFooter.nextPage')"
        @click="emit('update:page', page + 1)"
      >›</button>
    </div>

    <span v-if="total !== null" class="mono mute total">
      {{ $t(totalPartial ? 'resultFooter.totalRowsPartial' : 'resultFooter.totalRows', { n: total }) }}
    </span>
    <button
      v-else-if="canLoadTotal"
      class="count-btn mute"
      :disabled="countLoading"
      @click="emit('load-total')"
    >{{ countLoading ? $t('resultFooter.counting') : $t('resultFooter.loadTotal') }}</button>

    <slot />

    <div
      class="sql-display"
      @mouseenter="sqlHover = true"
      @mouseleave="sqlHover = false"
    >
      <div class="sql-lines">
        <div v-if="dmlSql" class="sql-line">
          <code class="sql-text mono" :title="dmlSql">{{ dmlSql }}</code>
          <span v-if="dmlLabel" class="sql-tag mono">{{ dmlLabel }}</span>
        </div>
        <div class="sql-line">
          <code class="sql-text mono" :title="sql || ''">{{ sql || '' }}</code>
        </div>
      </div>
      <button
        v-if="displaySql()"
        class="copy-btn"
        :class="{ visible: sqlHover }"
        :title="$t('common.copySql')"
        @click="copySql"
      >{{ $t('common.copy') }}</button>
    </div>

    <select v-model="sizeModel" class="size-select">
      <option v-for="opt in pageSizeOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
    </select>

    <select v-if="showExport" class="export-select" :disabled="exportDisabled" @change="onExportSelect">
      <option value="" disabled selected>{{ $t('common.exportPlaceholder') }}</option>
      <option value="csv">CSV</option>
      <option value="xlsx">Excel</option>
      <option value="json">JSON</option>
      <option value="sql">SQL</option>
    </select>
  </div>
</template>

<style scoped>
.footer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 10px;
  border-top: 1px solid var(--n-border-color);
  background: var(--n-color);
  flex: 0 0 auto;
  min-width: 0;
}

.pager {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
}
.pgbtn {
  width: 22px;
  height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 3px;
  font-size: 14px;
  line-height: 1;
  color: inherit;
  cursor: default;
  padding: 0;
  transition: background-color 120ms ease, border-color 120ms ease;
}
.pgbtn:hover:not(:disabled) {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}
.pgbtn:disabled {
  opacity: 0.3;
  cursor: default;
}
.page-input {
  width: 44px;
  height: 22px;
  text-align: center;
  font-size: 12px;
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  background: transparent;
  color: inherit;
  padding: 0 4px;
  outline: none;
  transition: border-color 120ms ease;
}
.page-input:focus {
  border-color: var(--n-primary-color, #18a058);
}
.page-input:disabled {
  opacity: 0.4;
}
.total-pages { flex: 0 0 auto; padding: 0 2px; }

.total { flex: 0 0 auto; white-space: nowrap; }
.count-btn {
  flex: 0 0 auto;
  height: 20px;
  padding: 0 8px;
  font-size: 11px;
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  background: transparent;
  color: inherit;
  cursor: default;
  white-space: nowrap;
  transition: background-color 120ms ease;
}
.count-btn:hover:not(:disabled) {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}
.count-btn:disabled { opacity: 0.5; }

.sql-display {
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  position: relative;
}
.sql-lines {
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.sql-line {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.sql-tag {
  flex: 0 0 auto;
  line-height: 1;
  opacity: 0.6;
  font-size: 9px;
}
.sql-text {
  flex: 1 1 0;
  min-width: 0;
  font-size: 11px;
  opacity: 0.7;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  user-select: text;
  -webkit-user-select: text;
  cursor: text;
}
.copy-btn {
  flex: 0 0 auto;
  height: 20px;
  padding: 0 8px;
  font-size: 11px;
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  background: var(--n-color, transparent);
  color: inherit;
  cursor: default;
  opacity: 0;
  pointer-events: none;
  transition: opacity 120ms ease, background-color 120ms ease;
}
.copy-btn.visible {
  opacity: 1;
  pointer-events: auto;
}
.copy-btn:hover {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}

.size-select,
.export-select {
  flex: 0 0 auto;
  font-size: 12px;
  height: 22px;
  padding: 0 4px;
  border-radius: 3px;
  border: 1px solid var(--n-border-color, rgba(127,127,127,0.25));
  background: transparent;
  color: inherit;
  cursor: pointer;
  outline: none;
  font-family: inherit;
}
.size-select { width: 80px; }
.size-select:hover:not(:disabled),
.export-select:hover:not(:disabled) {
  background: var(--n-color-target, rgba(127,127,127,0.12));
}
.size-select:disabled,
.export-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.mute { opacity: 0.55; font-size: 10px; }
</style>
