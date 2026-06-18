<script setup lang="ts">
// FilterBar —— WHERE / ORDER BY 过滤输入栏，类似 JetBrains DataGrid 的 Filter Bar。
//
// 双输入框 + 列名自动补全 + 历史记录。
// 按回车触发 @apply，清空按钮触发 @clear。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { ColumnMeta } from '../api/metadata'

const props = defineProps<{
  connId: string
  db: string
  table: string
  columns: ColumnMeta[]
}>()

const emit = defineEmits<{
  apply: [where: string, orderByClause: string]
  clear: []
}>()

// ---- 输入框绑定 ----
const whereValue = ref('')
const orderByValue = ref('')
const activeInput = ref<'where' | 'orderBy' | null>(null)

// ---- 自动补全 ----
const completions = ref<string[]>([])
const completionIndex = ref(0)
const showCompletions = ref(false)
const whereInputRef = ref<HTMLInputElement | null>(null)
const orderByInputRef = ref<HTMLInputElement | null>(null)
const whereMeasureRef = ref<HTMLSpanElement | null>(null)
const orderByMeasureRef = ref<HTMLSpanElement | null>(null)
const cursorLeft = ref(0)

// ---- 历史记录 ----
type FilterHistoryEntry = { where: string; orderByClause: string }

// 按表名存储历史，最多 20 条，按 MRU 排序，自动去重
const historyMap = ref(new Map<string, FilterHistoryEntry[]>())
const showHistory = ref(false)
const historyKey = computed(() => `${props.db}.${props.table}`)

const currentHistory = computed(() => {
  return historyMap.value.get(historyKey.value) ?? []
})

// ---- 自动补全数据源 ----
const columnNames = computed(() => props.columns.map((c) => c.name))

// 可组合关键词：每个条目可能是多词组合，匹配时检查其中任意一词是否命中前缀
const whereKeywords = [
  '=', '!=', '<', '>', '<=', '>=',
  'LIKE', 'NOT LIKE',
  'IN', 'NOT IN',
  'IS', 'IS NOT', 'IS NULL', 'IS NOT NULL',
  'NOT',
  'AND', 'OR', 'BETWEEN', 'NULL',
]
const orderByKeywords = ['ASC', 'DESC']

function getLastWord(input: string, cursorPos: number): { word: string; start: number; end: number } {
  // 找到光标所在位置，向前找最后一个分隔符之后的单词
  let start = cursorPos
  while (start > 0 && !/[\s(),]/.test(input[start - 1])) start--
  let end = cursorPos
  while (end < input.length && !/[\s(),]/.test(input[end])) end++
  return { word: input.slice(start, end).toUpperCase(), start, end }
}

/** 检查关键词的任意组成单词是否以 prefix 开头 */
function keywordMatches(keyword: string, prefix: string): boolean {
  if (prefix === '') return false
  return keyword.split(/\s+/).some((part) => part.toUpperCase().startsWith(prefix))
}

function buildCompletions(input: string, cursorPos: number, mode: 'where' | 'orderBy'): string[] {
  const { word, start, end } = getLastWord(input, cursorPos)
  // 光标在单词中间或在空白处且前面没有可补全内容时不显示
  if (word === '' && start !== end) return []
  if (word === '' && start === end) {
    // 光标在空白处，检查前一个分词是否构成复合关键词前缀
    const before = input.slice(0, cursorPos).trimEnd()
    const lastSpace = before.lastIndexOf(' ')
    const prevWord = lastSpace >= 0 ? before.slice(lastSpace + 1).toUpperCase() : ''
    if (prevWord === '') return []
    // 看是否有以 prevWord 开头且后面还有词的关键词
    // 例如 prevWord='IS' → IS NOT, IS NULL, IS NOT NULL 等
    const keywords = mode === 'where' ? whereKeywords : orderByKeywords
    const source = [...(mode === 'where' ? columnNames.value : []), ...keywords]
    const results = source.filter((kw) => {
      const parts = kw.toUpperCase().split(/\s+/)
      return parts.some((p) => p === prevWord && parts.length > 1)
    })
    return results
  }

  const keywords = mode === 'where' ? whereKeywords : orderByKeywords
  const source = mode === 'where' ? [...columnNames.value, ...keywords] : [...columnNames.value, ...keywords]

  // 去重（列名可能和关键词重名）
  const seen = new Set<string>()
  const scored: Array<{ item: string; score: number }> = []
  for (const item of source) {
    const upper = item.toUpperCase()
    if (seen.has(upper)) continue
    seen.add(upper)
    const parts = upper.split(/\s+/)
    const first = parts[0]

    if (!item.includes(' ')) {
      // 单条目（列名或单关键词）
      if (!first.startsWith(word)) continue
      if (first === word) {
        scored.push({ item, score: 0 }) // 精确匹配
      } else {
        scored.push({ item, score: 1 }) // 前缀匹配
      }
    } else {
      // 复合关键词：检查各组成单词
      if (!keywordMatches(item, word)) continue
      if (parts.some((p) => p === word)) {
        scored.push({ item, score: parts[0] === word ? 0 : 1 }) // 有精确匹配的组成词
      } else if (parts[0].startsWith(word)) {
        scored.push({ item, score: 2 }) // 第一个词前缀匹配
      } else {
        scored.push({ item, score: 3 }) // 后续词前缀匹配
      }
    }
  }

  // 按分数排序，同分按字母序
  scored.sort((a, b) => a.score - b.score || a.item.localeCompare(b.item))
  return scored.map((s) => s.item)
}

function updateCursorPixel(input: HTMLInputElement, measureSpan: HTMLSpanElement | null) {
  if (!measureSpan) return
  const text = input.value.slice(0, input.selectionStart ?? input.value.length)
  measureSpan.textContent = text || ''
  cursorLeft.value = measureSpan.offsetWidth
}

function onInput(e: Event, mode: 'where' | 'orderBy') {
  const input = e.target as HTMLInputElement
  const cursorPos = input.selectionStart ?? input.value.length
  const items = buildCompletions(input.value, cursorPos, mode)
  completions.value = items
  completionIndex.value = 0
  showCompletions.value = items.length > 0

  const measure = mode === 'where' ? whereMeasureRef.value : orderByMeasureRef.value
  updateCursorPixel(input, measure)
}

function onInputKeydown(e: KeyboardEvent, mode: 'where' | 'orderBy') {
  if (!showCompletions.value) return

  if (e.key === 'ArrowDown') {
    e.preventDefault()
    completionIndex.value = (completionIndex.value + 1) % completions.value.length
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    completionIndex.value = (completionIndex.value - 1 + completions.value.length) % completions.value.length
  } else if (e.key === 'Enter' || e.key === 'Tab') {
    if (completionIndex.value >= 0 && completionIndex.value < completions.value.length) {
      e.preventDefault()
      applyCompletion(mode)
    }
  } else if (e.key === 'Escape') {
    showCompletions.value = false
  }
}

function applyCompletion(mode: 'where' | 'orderBy') {
  const item = completions.value[completionIndex.value]
  if (!item) return

  const inp = mode === 'where' ? whereInputRef.value : orderByInputRef.value
  if (!inp) return

  const cursorPos = inp.selectionStart ?? inp.value.length
  const { start, end } = getLastWord(inp.value, cursorPos)
  const before = inp.value.slice(0, start)
  const after = inp.value.slice(end)
  // 尾部已有空格或接分隔符时不追加空格
  const needsSpace = after === '' || !/^[\s(),]/.test(after)
  const suffix = needsSpace ? ' ' : ''
  const newValue = before + item + suffix + after

  if (mode === 'where') {
    whereValue.value = newValue
  } else {
    orderByValue.value = newValue
  }

  showCompletions.value = false
  nextTick(() => {
    const pos = before.length + item.length + suffix.length
    inp.focus()
    inp.setSelectionRange(pos, pos)
  })
}

function onWhereKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !showCompletions.value) {
    e.preventDefault()
    emitApply()
    return
  }
  onInputKeydown(e, 'where')
}

function onOrderByKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !showCompletions.value) {
    e.preventDefault()
    emitApply()
    return
  }
  onInputKeydown(e, 'orderBy')
}

function onWhereInput(e: Event) {
  onInput(e, 'where')
  const input = e.target as HTMLInputElement
  updateCursorPixel(input, whereMeasureRef.value)
}

function onOrderByInput(e: Event) {
  onInput(e, 'orderBy')
  const input = e.target as HTMLInputElement
  updateCursorPixel(input, orderByMeasureRef.value)
}

function onWhereClick(e: MouseEvent) {
  const input = e.target as HTMLInputElement
  updateCursorPixel(input, whereMeasureRef.value)
}

function onOrderByClick(e: MouseEvent) {
  const input = e.target as HTMLInputElement
  updateCursorPixel(input, orderByMeasureRef.value)
}

function onWhereFocus() {
  activeInput.value = 'where'
  const input = whereInputRef.value
  if (input) updateCursorPixel(input, whereMeasureRef.value)
}

function onOrderByFocus() {
  activeInput.value = 'orderBy'
  const input = orderByInputRef.value
  if (input) updateCursorPixel(input, orderByMeasureRef.value)
}

// ---- History ----
function pushHistory() {
  const key = historyKey.value
  const entry: FilterHistoryEntry = { where: whereValue.value, orderByClause: orderByValue.value }
  const list = historyMap.value.get(key) ?? []
  // 去重：移除相同条目
  const filtered = list.filter(
    (e) => !(e.where === entry.where && e.orderByClause === entry.orderByClause),
  )
  // 插入到最前面
  filtered.unshift(entry)
  // 限制 20 条
  if (filtered.length > 20) filtered.length = 20
  historyMap.value.set(key, filtered)
}

function onHistoryItemClick(entry: FilterHistoryEntry) {
  whereValue.value = entry.where
  orderByValue.value = entry.orderByClause
  showHistory.value = false
  emitApply()
}

function toggleHistory(e: MouseEvent) {
  e.stopPropagation()
  showHistory.value = !showHistory.value
}

function onClear() {
  whereValue.value = ''
  orderByValue.value = ''
  showHistory.value = false
  showCompletions.value = false
  emit('clear')
}

function emitApply() {
  pushHistory()
  emit('apply', whereValue.value, orderByValue.value)
}

// 点击外部关闭弹窗
function onDocClick() {
  if (showHistory.value) showHistory.value = false
  if (showCompletions.value) showCompletions.value = false
}

onMounted(() => document.addEventListener('click', onDocClick))

onBeforeUnmount(() => {
  // 清除当前表的历史
  historyMap.value.delete(historyKey.value)
  document.removeEventListener('click', onDocClick)
})

// 重置：表切换时重置输入框
watch(
  () => [props.db, props.table],
  () => {
    whereValue.value = ''
    orderByValue.value = ''
    showCompletions.value = false
    showHistory.value = false
  },
)
</script>

<template>
  <div class="filter-bar" @click.stop>
    <!-- WHERE 输入框 -->
    <div class="filter-input-wrap" :class="{ active: activeInput === 'where' }">
      <span class="filter-label">WHERE</span>
      <div class="filter-input-outer">
        <input
          ref="whereInputRef"
          v-model="whereValue"
          class="filter-input mono"
          placeholder="过滤条件…"
          spellcheck="false"
          @input="onWhereInput"
          @keydown="onWhereKeydown"
          @click="onWhereClick"
          @focus="onWhereFocus"
          @blur="activeInput = null"
        />
        <span ref="whereMeasureRef" class="measure-span" aria-hidden="true" />
        <!-- 自动补全弹窗 -->
        <div
          v-if="showCompletions && activeInput === 'where' && completions.length"
          class="completions-popup"
          :style="{ left: cursorLeft + 'px' }"
        >
          <div
            v-for="(item, i) in completions"
            :key="item"
            class="completion-item"
            :class="{ selected: i === completionIndex }"
            @mousedown.prevent="completionIndex = i; applyCompletion('where')"
          >{{ item }}</div>
        </div>
      </div>
      <button
        v-if="whereValue"
        class="clear-btn"
        @click="whereValue = ''"
      >×</button>
    </div>

    <!-- ORDER BY 输入框 -->
    <div class="filter-input-wrap" :class="{ active: activeInput === 'orderBy' }">
      <span class="filter-label">ORDER BY</span>
      <div class="filter-input-outer">
        <input
          ref="orderByInputRef"
          v-model="orderByValue"
          class="filter-input mono"
          placeholder="排序条件…"
          spellcheck="false"
          @input="onOrderByInput"
          @keydown="onOrderByKeydown"
          @click="onOrderByClick"
          @focus="onOrderByFocus"
          @blur="activeInput = null"
        />
        <span ref="orderByMeasureRef" class="measure-span" aria-hidden="true" />
        <!-- 自动补全弹窗 -->
        <div
          v-if="showCompletions && activeInput === 'orderBy' && completions.length"
          class="completions-popup"
          :style="{ left: cursorLeft + 'px' }"
        >
          <div
            v-for="(item, i) in completions"
            :key="item"
            class="completion-item"
            :class="{ selected: i === completionIndex }"
            @mousedown.prevent="completionIndex = i; applyCompletion('orderBy')"
          >{{ item }}</div>
        </div>
      </div>
      <button
        v-if="orderByValue"
        class="clear-btn"
        @click="orderByValue = ''"
      >×</button>
    </div>

    <!-- 操作按钮组 -->
    <div class="filter-actions">
      <button
        class="action-btn"
        title="历史记录"
        @click="toggleHistory"
      >⏱</button>
      <button
        class="action-btn"
        title="应用过滤"
        @click="emitApply"
      >↵</button>
      <button
        class="action-btn clear-all"
        title="清空所有过滤"
        @click="onClear"
      >×</button>
    </div>

    <!-- 历史记录弹窗 -->
    <div
      v-if="showHistory && currentHistory.length"
      class="history-popup"
      @click.stop
    >
      <div class="history-header">历史记录</div>
      <div
        v-for="(entry, i) in currentHistory"
        :key="i"
        class="history-item"
        @mousedown.prevent="onHistoryItemClick(entry)"
      >
        <div class="history-item-where" v-if="entry.where">WHERE {{ entry.where }}</div>
        <div class="history-item-order" v-if="entry.orderByClause">ORDER BY {{ entry.orderByClause }}</div>
        <div v-if="!entry.where && !entry.orderByClause" class="history-item-empty">无过滤</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.filter-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  border-bottom: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.2));
  background: var(--n-color, transparent);
  font-size: 12px;
  position: relative;
  flex: 0 0 auto;
  z-index: 10;
}

.filter-input-wrap {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: 1 1 0;
  min-width: 0;
  position: relative;
  border: 1px solid transparent;
  border-radius: 3px;
  padding: 0 4px;
  transition: border-color 120ms ease;
}

.filter-input-wrap.active {
  border-color: var(--n-primary-color, #18a058);
}

.filter-label {
  font-size: 11px;
  font-weight: 600;
  opacity: 0.6;
  flex: 0 0 auto;
  user-select: none;
  -webkit-user-select: none;
}

.filter-input-outer {
  flex: 1 1 0;
  min-width: 0;
  position: relative;
  display: flex;
  align-items: center;
}

.filter-input {
  flex: 1 1 0;
  min-width: 0;
  height: 24px;
  font-size: 12px;
  background: transparent;
  border: none;
  outline: none;
  color: inherit;
  padding: 0;
  width: 100%;
}

.filter-input::placeholder {
  opacity: 0.35;
  font-style: italic;
}

/* 用于测量光标位置的隐藏 span */
.measure-span {
  position: absolute;
  left: 0;
  top: 0;
  visibility: hidden;
  white-space: pre;
  font-size: 12px;
  font-family: inherit;
  pointer-events: none;
}

.clear-btn {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  color: inherit;
  opacity: 0.4;
  font-size: 13px;
  line-height: 1;
  cursor: default;
  padding: 0;
  border-radius: 2px;
}

.clear-btn:hover {
  opacity: 0.8;
  background: rgba(127, 127, 127, 0.12);
}

.filter-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
}

.action-btn {
  width: 22px;
  height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 3px;
  font-size: 12px;
  line-height: 1;
  color: inherit;
  cursor: default;
  padding: 0;
  transition: background-color 120ms ease, border-color 120ms ease;
}

.action-btn:hover {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}

.clear-all {
  opacity: 0.5;
}

.clear-all:hover {
  opacity: 1;
}

/* 自动补全弹窗 — 浮动于光标位置下方 */
.completions-popup {
  position: absolute;
  top: 100%;
  margin-top: 2px;
  min-width: 100px;
  background: var(--n-color, #fff);
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
  max-height: 200px;
  overflow-y: auto;
  z-index: 100;
}

.completion-item {
  padding: 4px 8px;
  font-size: 12px;
  cursor: default;
  white-space: nowrap;
}

.completion-item:hover,
.completion-item.selected {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}

/* 历史记录弹窗 */
.history-popup {
  position: absolute;
  top: 100%;
  right: 8px;
  margin-top: 2px;
  min-width: 240px;
  max-height: 300px;
  overflow-y: auto;
  background: var(--n-color, #fff);
  border: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.25));
  border-radius: 3px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.12);
  z-index: 100;
}

.history-header {
  padding: 6px 8px;
  font-size: 11px;
  font-weight: 600;
  opacity: 0.6;
  border-bottom: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.15));
  user-select: none;
  -webkit-user-select: none;
}

.history-item {
  padding: 6px 8px;
  font-size: 11px;
  cursor: default;
  border-bottom: 1px solid var(--n-border-color, rgba(127, 127, 127, 0.08));
}

.history-item:last-child {
  border-bottom: none;
}

.history-item:hover {
  background: var(--n-color-target, rgba(127, 127, 127, 0.12));
}

.history-item-where {
  opacity: 0.85;
}

.history-item-order {
  opacity: 0.55;
  font-size: 10px;
  margin-top: 2px;
}

.history-item-empty {
  opacity: 0.4;
  font-style: italic;
}
</style>
