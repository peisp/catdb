<script setup lang="ts">
// IndexesTab — master-detail editor for table indexes.
//
//   ┌── sidebar ──┬─────── detail form ──────────────┐
//   │ idx list    │  name / comment / unique / type   │
//   │             │  ┌── col list ── col editor ──┐  │
//   │             │  │ + - ↑ ↓     │ name / order │  │
//   │             │  └─────────────┴──────────────┘  │
//   └─────────────┴───────────────────────────────────┘
//
// PRIMARY is shown in the list (read-only) because it lives on the same
// concept, but every field is disabled when it's selected: the source of
// truth for PK is the column-level checkbox in ColumnsTab.
import { computed, ref, watch } from 'vue'
import { NCheckbox, NInput } from 'naive-ui'
import ResizeHandle from '../shared/ResizeHandle.vue'
import {
  emptyIndexDraft,
  type ColumnDraft,
  type IndexDraft,
} from '../../lib/alterPlan'

const props = defineProps<{
  modelValue: IndexDraft[]
  /** Column draft list — populates the per-row column dropdown. */
  columnsDraft: ColumnDraft[]
  busy?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: IndexDraft[]): void
}>()

// ---- selection state ------------------------------------------------------

const selectedKey = ref<string | null>(null)
const selectedColIdx = ref<number>(0)

// ---- aside resize ---------------------------------------------------------

const ixTabRef = ref<HTMLElement | null>(null)

const SIDE_MIN = 20
const SIDE_MAX = 50
const sidePct = ref(25)
const dragging = ref(false)

let startX = 0
let startPct = 0

/** Capture pointer on the drag handle so move/up fire even outside it. */
function onPointerDown(e: PointerEvent) {
  dragging.value = true
  startX = e.clientX
  startPct = sidePct.value
  const handle = e.currentTarget as HTMLElement
  handle.setPointerCapture(e.pointerId)
  handle.addEventListener('pointermove', onPointerMove)
  handle.addEventListener('pointerup', onPointerUp)
  handle.addEventListener('pointercancel', onPointerUp)
}

function calcSidePct(currentX: number, containerEl: HTMLElement) {
  const rect = containerEl.getBoundingClientRect()
  const dx = currentX - startX
  return Math.round(startPct + (dx / rect.width) * 100)
}

function onPointerMove(e: PointerEvent) {
  if (!ixTabRef.value) return
  sidePct.value = Math.max(SIDE_MIN, Math.min(SIDE_MAX, calcSidePct(e.clientX, ixTabRef.value)))
}

function onPointerUp(e: PointerEvent) {
  dragging.value = false
  const handle = e.currentTarget as HTMLElement
  handle.removeEventListener('pointermove', onPointerMove)
  handle.removeEventListener('pointerup', onPointerUp)
  handle.removeEventListener('pointercancel', onPointerUp)
}

const selectedIndex = computed(() =>
  props.modelValue.find((ix) => ix._key === selectedKey.value) ?? null,
)
const selectedIsPrimary = computed(() => !!selectedIndex.value?.primary)

// Auto-pick a sensible default: prefer the first non-primary editable row;
// fall back to primary so the right pane is never blank when indexes exist.
function pickDefaultSelection() {
  const list = props.modelValue
  if (list.length === 0) {
    selectedKey.value = null
    return
  }
  const stillThere = list.some((ix) => ix._key === selectedKey.value)
  if (stillThere) return
  const firstEditable = list.find((ix) => !ix.primary)
  selectedKey.value = (firstEditable ?? list[0])._key
  selectedColIdx.value = 0
}

watch(
  () => props.modelValue,
  () => pickDefaultSelection(),
  { immediate: true, deep: false },
)

// Keep selectedColIdx in range as columns change.
watch(
  () => selectedIndex.value?.columns.length ?? 0,
  (n) => {
    if (selectedColIdx.value >= n) selectedColIdx.value = Math.max(0, n - 1)
  },
)

// ---- index list ops -------------------------------------------------------

function commit() {
  emit('update:modelValue', props.modelValue)
}

function addIndex() {
  const row = emptyIndexDraft()
  // Newly-created indexes pre-populate one empty column slot so the user
  // sees the column editor immediately rather than an empty box.
  row.columns.push({ name: '', order: '' })
  const list = [...props.modelValue, row]
  emit('update:modelValue', list)
  selectedKey.value = row._key
  selectedColIdx.value = 0
}

function deleteSelectedIndex() {
  if (!selectedIndex.value || selectedIndex.value.primary) return
  const idx = props.modelValue.indexOf(selectedIndex.value)
  if (idx < 0) return
  const list = props.modelValue.slice()
  list.splice(idx, 1)
  selectedKey.value = null
  emit('update:modelValue', list)
}

function selectIndex(key: string) {
  selectedKey.value = key
  selectedColIdx.value = 0
}

// ---- column rows inside the selected index --------------------------------

function addCol() {
  const ix = selectedIndex.value
  if (!ix || ix.primary) return
  ix.columns.push({ name: '', order: '' })
  selectedColIdx.value = ix.columns.length - 1
  commit()
}

function removeCol() {
  const ix = selectedIndex.value
  if (!ix || ix.primary || ix.columns.length === 0) return
  ix.columns.splice(selectedColIdx.value, 1)
  selectedColIdx.value = Math.min(selectedColIdx.value, ix.columns.length - 1)
  if (selectedColIdx.value < 0) selectedColIdx.value = 0
  commit()
}

function moveCol(delta: -1 | 1) {
  const ix = selectedIndex.value
  if (!ix || ix.primary) return
  const i = selectedColIdx.value
  const j = i + delta
  if (j < 0 || j >= ix.columns.length) return
  const tmp = ix.columns[i]
  ix.columns[i] = ix.columns[j]
  ix.columns[j] = tmp
  selectedColIdx.value = j
  commit()
}

function updateColName(val: string) {
  const ix = selectedIndex.value
  if (!ix || ix.primary) return
  const c = ix.columns[selectedColIdx.value]
  if (!c) return
  c.name = val
  commit()
}

function updateColOrder(val: string) {
  const ix = selectedIndex.value
  if (!ix || ix.primary) return
  const c = ix.columns[selectedColIdx.value]
  if (!c) return
  c.order = val
  commit()
}

// ---- derived view-models --------------------------------------------------

/** Compact "(col1, col2) UNIQUE" string for the sidebar list. */
function previewLine(ix: IndexDraft): string {
  const cols = ix.columns.map((c) => c.name).filter(Boolean).join(', ')
  const flag = ix.primary ? 'PK' : ix.unique ? 'UNIQUE' : ''
  return cols ? `(${cols})${flag ? ' ' + flag : ''}` : flag
}

const columnOptions = computed(() =>
  props.columnsDraft
    .filter((c) => c.name.trim() !== '')
    .map((c) => ({ label: c.name, value: c.name })),
)

const ORDER_OPTIONS = [
  { label: 'NONE', value: '' },
  { label: 'ASC', value: 'ASC' },
  { label: 'DESC', value: 'DESC' },
]

const TYPE_OPTIONS = [
  { label: 'BTREE', value: '' },
  { label: 'HASH', value: 'HASH' },
  { label: 'FULLTEXT', value: 'FULLTEXT' },
]
</script>

<template>
  <div ref="ixTabRef" class="ix-tab">
    <!-- Sidebar: index list -->
    <aside class="ix-side" :style="{ flex: `0 0 ${sidePct}%` }">
      <div class="ix-side-head">
        <span>{{ $t('structure.indexes.title') }}</span>
        <div class="ix-side-tools">
          <button
            type="button"
            class="icon-btn"
            :disabled="busy"
            :title="$t('structure.indexes.addTitle')"
            @click="addIndex"
          >+</button>
          <button
            type="button"
            class="icon-btn"
            :disabled="busy || !selectedIndex || selectedIsPrimary"
            :title="$t('structure.indexes.deleteTitle')"
            @click="deleteSelectedIndex"
          >−</button>
        </div>
      </div>
      <div class="ix-list">
        <div
          v-for="ix in modelValue"
          :key="ix._key"
          class="ix-item"
          :class="{
            selected: ix._key === selectedKey,
            'is-new': !ix.origName,
          }"
          @click="selectIndex(ix._key)"
        >
          <span class="ix-icon" :class="{ 'is-unique': ix.unique || ix.primary }">
            {{ ix.unique || ix.primary ? 'iu' : 'i' }}
          </span>
          <span class="ix-name" :title="ix.name || $t('structure.indexes.unnamed')">{{ ix.name || $t('structure.indexes.unnamed') }}</span>
          <span class="ix-detail" :title="previewLine(ix)">{{ previewLine(ix) }}</span>
        </div>
        <div v-if="modelValue.length === 0" class="ix-empty">{{ $t('structure.indexes.empty') }}</div>
      </div>
      <ResizeHandle orientation="vertical" :active="dragging" @pointerdown="onPointerDown" />
    </aside>

    <!-- Detail pane -->
    <section class="ix-detail">
      <template v-if="!selectedIndex">
        <div class="ix-empty-detail">{{ $t('structure.indexes.emptyDetail') }}</div>
      </template>
      <template v-else>
        <div v-if="selectedIsPrimary" class="pk-hint">
          {{ $t('structure.indexes.primaryReadonly') }}
        </div>

        <div class="row">
          <div class="label">{{ $t('structure.indexes.labelName') }}</div>
          <n-input
            :value="selectedIndex.name"
            size="tiny"
            placeholder="idx_name"
            :disabled="busy || selectedIsPrimary"
            @update:value="(v: string) => { selectedIndex!.name = v; commit() }"
          />
        </div>

        <div class="row">
          <div class="label">{{ $t('structure.indexes.labelComment') }}</div>
          <n-input
            :value="selectedIndex.comment"
            size="tiny"
            placeholder=""
            :disabled="busy || selectedIsPrimary"
            @update:value="(v: string) => { selectedIndex!.comment = v; commit() }"
          />
        </div>

        <div class="row">
          <div class="label"></div>
          <label class="inline-check">
            <n-checkbox
              :checked="selectedIndex.unique"
              :disabled="busy || selectedIsPrimary"
              @update:checked="(v: boolean) => { selectedIndex!.unique = !!v; commit() }"
            />
            <span>{{ $t('structure.indexes.unique') }}</span>
          </label>
        </div>

        <div class="row">
          <div class="label">{{ $t('structure.indexes.labelType') }}</div>
          <select
            :value="(selectedIndex.type || '').toUpperCase() === 'BTREE' ? '' : (selectedIndex.type || '').toUpperCase()"
            :disabled="busy || selectedIsPrimary"
            class="native-sel"
            @change="(e: any) => { selectedIndex!.type = e.target.value; commit() }"
          >
            <option v-for="t in TYPE_OPTIONS" :key="t.label" :value="t.value">{{ t.label }}</option>
          </select>
        </div>

        <!-- Column editor: left list + right inspector -->
        <div class="row top">
          <div class="label">{{ $t('structure.indexes.labelColumns') }}</div>
          <div class="col-wrapper">
            <div class="col-left">
              <div class="col-tools">
                <button
                  type="button"
                  class="icon-btn"
                  :disabled="busy || selectedIsPrimary"
                  :title="$t('structure.indexes.addColTitle')"
                  @click="addCol"
                >+</button>
                <button
                  type="button"
                  class="icon-btn"
                  :disabled="busy || selectedIsPrimary || selectedIndex.columns.length === 0"
                  :title="$t('structure.indexes.removeColTitle')"
                  @click="removeCol"
                >−</button>
                <button
                  type="button"
                  class="icon-btn"
                  :disabled="busy || selectedIsPrimary || selectedColIdx <= 0"
                  :title="$t('structure.indexes.moveUp')"
                  @click="moveCol(-1)"
                >↑</button>
                <button
                  type="button"
                  class="icon-btn"
                  :disabled="busy || selectedIsPrimary || selectedColIdx >= selectedIndex.columns.length - 1"
                  :title="$t('structure.indexes.moveDown')"
                  @click="moveCol(1)"
                >↓</button>
              </div>
              <div class="col-items">
                <div
                  v-for="(c, i) in selectedIndex.columns"
                  :key="i"
                  class="col-item"
                  :class="{ active: i === selectedColIdx }"
                  @click="selectedColIdx = i"
                >{{ c.name || $t('structure.indexes.colUnselected') }}</div>
                <div v-if="selectedIndex.columns.length === 0" class="col-empty">
                  {{ $t('structure.indexes.none') }}
                </div>
              </div>
            </div>
            <div class="col-right">
              <template v-if="selectedIndex.columns[selectedColIdx]">
                <div class="col-row">
                  <div class="col-label">{{ $t('structure.indexes.colName') }}</div>
                  <select
                    :value="selectedIndex.columns[selectedColIdx].name"
                    :disabled="busy || selectedIsPrimary"
                    class="native-sel"
                    @change="(e: any) => updateColName(e.target.value)"
                  >
                    <option value="">{{ $t('structure.indexes.selectPlaceholder') }}</option>
                    <option v-for="opt in columnOptions" :key="opt.value" :value="opt.value">
                      {{ opt.label }}
                    </option>
                  </select>
                </div>
                <div class="col-row">
                  <div class="col-label">{{ $t('structure.indexes.order') }}</div>
                  <select
                    :value="(selectedIndex.columns[selectedColIdx].order || '').toUpperCase()"
                    :disabled="busy || selectedIsPrimary"
                    class="native-sel"
                    @change="(e: any) => updateColOrder(e.target.value)"
                  >
                    <option v-for="o in ORDER_OPTIONS" :key="o.label" :value="o.value">
                      {{ o.label }}
                    </option>
                  </select>
                </div>
              </template>
              <div v-else class="col-empty-detail">{{ $t('structure.indexes.addColFirst') }}</div>
            </div>
          </div>
        </div>
      </template>
    </section>
  </div>
</template>

<style scoped>
.ix-tab {
  display: flex;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  font-size: 12px;
}

/* ---- sidebar ---- */
.ix-side {
  flex: 0 0 25%;
  min-width: 20%;
  max-width: 50%;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--n-border-color);
  min-height: 0;
  position: relative;
}
.ix-side-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  border-bottom: 1px solid var(--n-divider-color);
  color: var(--n-text-color-2);
  user-select: none;
}
.ix-side-tools {
  display: flex;
  gap: 2px;
}
.ix-list {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  padding: 4px 0;
}
.ix-empty {
  padding: 16px;
  text-align: center;
  color: var(--n-text-color-3);
}
.ix-item {
  display: flex;
  align-items: center;
  gap: 6px;
  border-left: 3px solid transparent;
  cursor: pointer;
  user-select: none;
  white-space: nowrap;
  overflow: hidden;
}
.ix-item:hover {
  background: var(--n-hover-color);
}
.ix-item.selected {
  background: var(--n-action-color, var(--n-hover-color));
  border-left-color: var(--n-color-info, #3b8ee0);
}
.ix-item.is-new .ix-name::before {
  content: '+';
  color: var(--n-success-color);
  margin-right: 2px;
}
.ix-icon {
  display: inline-block;
  min-width: 14px;
  text-align: center;
  font-style: italic;
  font-weight: 600;
  color: var(--n-text-color-3);
  flex: 0 0 auto;
}
.ix-icon.is-unique {
  color: var(--n-color-info, #3b8ee0);
}
.ix-name {
  flex: 0 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--n-text-color-1);
}
.ix-detail {
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--n-text-color-3);
  font-size: 11px;
}

/* ---- detail pane ---- */
.ix-detail {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow: auto;
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.ix-empty-detail {
  margin: auto;
  color: var(--n-text-color-3);
}
.pk-hint {
  padding: 4px 8px;
  border-radius: 3px;
  background: var(--n-action-color, var(--n-hover-color));
  color: var(--n-text-color-3);
  font-size: 11px;
}
.row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.row.top {
  align-items: flex-start;
}
.label {
  flex: 0 0 40px;
  color: var(--n-text-color-2);
  font-size: 12px;
}
.inline-check {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  user-select: none;
}

/* ---- column editor (split) ---- */
.col-wrapper {
  flex: 1 1 auto;
  display: flex;
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  min-height: 160px;
  max-height: 240px;
  overflow: hidden;
  background: var(--n-input-color, var(--n-card-color));
}
.col-left {
  border-right: 1px solid var(--n-border-color);
  flex: 0 0 130px;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.col-tools {
  display: flex;
  gap: 2px;
  padding: 3px 4px;
  border-bottom: 1px solid var(--n-border-color);
}
.col-items {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  padding: 2px 0;
}
.col-item {
  padding: 3px 8px 3px 10px;
  border-left: 3px solid transparent;
  cursor: pointer;
  user-select: none;
  color: var(--n-text-color-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.col-item:hover {
  background: var(--n-hover-color);
}
.col-item.active {
  background: var(--n-action-color, var(--n-hover-color));
  border-left-color: var(--n-color-info, #3b8ee0);
}
.col-empty {
  padding: 12px;
  text-align: center;
  color: var(--n-text-color-3);
  font-size: 11px;
}
.col-right {
  flex: 1 1 auto;
  min-width: 0;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.col-empty-detail {
  margin: auto;
  color: var(--n-text-color-3);
  font-size: 11px;
}
.col-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.col-label {
  flex: 0 0 36px;
  color: var(--n-text-color-2);
}

/* ---- icon button (sidebar & col tools) ---- */
.icon-btn {
  width: 20px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid transparent;
  background: transparent;
  color: var(--n-text-color-2);
  font-size: 13px;
  line-height: 1;
  border-radius: 3px;
  cursor: pointer;
  padding: 0;
}
.icon-btn:hover:not(:disabled) {
  background: var(--n-hover-color);
  border-color: var(--n-border-color);
  color: var(--n-text-color-1);
}
.icon-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* ---- native select ---- */
.native-sel {
  font-size: 12px;
  font-family: inherit;
  flex: 1 1 auto;
  min-width: 0;
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
.native-sel:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
