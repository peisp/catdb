<script setup lang="ts">
// ColumnsTab — editable list of column definitions.
//
// Edit model: the parent owns the ColumnDraft[] array; this component mutates
// items in place + uses Vue's reactivity to propagate. New/dropped rows go
// through splice() / push() / a single `update:modelValue` emit so the parent
// stays in sync even when re-renders are needed.
//
// Reorder: native HTML5 drag-and-drop, but only the leftmost drag-handle cell
// is `draggable="true"`. That keeps input fields fully clickable + selectable;
// only the handle initiates a drag.
//
// Drop-visualization layers (the user has to see exactly where the column
// will land at a glance):
//  1. The dragged row dims + gets a dashed outline (no doubt which one moves).
//  2. The drop-target row gets a 3 px primary-color bar on top or bottom edge,
//     plus a filled circular marker anchored to the drag-handle column at the
//     same edge — easy to spot even when the row is wide.
//  3. The whole table wrap shifts to `cursor: grabbing` while a drag is in
//     flight so the user feels the modal state.
//  4. A live status chip in the toolbar reads "拖动: 第 N → 第 M" with the
//     computed final landing slot (post-splice compensation), so the user can
//     double-check the destination before releasing.
import { computed, ref } from 'vue'
import { NButton, NCheckbox, NInput, NTooltip } from 'naive-ui'
import {
  baseTypeGroups,
  emptyColumnDraft,
  typeFormatFor,
  type ColumnDraft,
} from '../../lib/alterPlan'
import { autoIncrementAllowed, type UIDialect } from '../../api/dialect'
import { t } from '../../i18n'

const props = defineProps<{
  modelValue: ColumnDraft[]
  /** The driver's UI descriptor — type catalog, modifier + AI rules. */
  dialect: UIDialect
  /** Disable editing while we're in the middle of an Apply. */
  busy?: boolean
}>()

// computed so the type-group labels re-translate on locale switch.
const typeGroups = computed(() => baseTypeGroups(props.dialect))
// Hide the UNSIGNED column entirely for dialects without the modifier.
const showUnsigned = computed(() => !!props.dialect.hasUnsigned)
const totalCols = computed(() => (showUnsigned.value ? 12 : 11))
const emit = defineEmits<{
  (e: 'update:modelValue', v: ColumnDraft[]): void
}>()

// We mutate in place + emit the array reference back. The parent's
// reactivity tracking picks up the change.
function commit() {
  emit('update:modelValue', props.modelValue)
}

// ---- row actions ----------------------------------------------------------

function addRow(insertAfter?: number) {
  const list = props.modelValue.slice()
  const row = emptyColumnDraft(props.dialect)
  if (insertAfter == null || insertAfter >= list.length - 1) {
    list.push(row)
  } else {
    list.splice(insertAfter + 1, 0, row)
  }
  emit('update:modelValue', list)
}

function deleteRow(idx: number) {
  const list = props.modelValue.slice()
  list.splice(idx, 1)
  emit('update:modelValue', list)
}

// ---- drag-to-reorder ------------------------------------------------------

const dragFrom = ref<number | null>(null)
const dragOverIdx = ref<number | null>(null)
const dragOverPos = ref<'top' | 'bottom'>('top')

function onDragStart(e: DragEvent, idx: number) {
  if (props.busy) return
  if (!e.dataTransfer) return
  dragFrom.value = idx
  e.dataTransfer.effectAllowed = 'move'
  // Firefox requires *some* data on the transfer for the drag to start at all.
  e.dataTransfer.setData('text/plain', String(idx))

  // Only the handle cell is `draggable="true"`, so the browser's default drag
  // image would be that tiny ⋮⋮ chip alone — looks broken on a wide row.
  // Build a one-off offscreen table containing just this row and feed it to
  // setDragImage so the *whole row* follows the cursor.
  const handle = e.currentTarget as HTMLElement
  const tr = handle.closest('tr') as HTMLTableRowElement | null
  if (tr) {
    const ghost = buildDragGhost(tr)
    const rect = tr.getBoundingClientRect()
    e.dataTransfer.setDragImage(
      ghost,
      Math.max(0, Math.min(rect.width, e.clientX - rect.left)),
      Math.max(0, Math.min(rect.height, e.clientY - rect.top)),
    )
    // The browser snapshots synchronously inside setDragImage; remove the
    // node next frame so we don't leak detached DOM into the body.
    requestAnimationFrame(() => ghost.remove())
  }
}

// Build a standalone `<table>` wrapping a clone of `row`, positioned offscreen
// but rendered, suitable for use as a setDragImage source. Critical details:
//  - cloneNode misses live `<input>` values; we copy them via setAttribute.
//  - the parent table's <colgroup> defines column widths via fixed layout, so
//    we clone it into the ghost or all cells would collapse to content width.
//  - is-dragging / is-drop-* classes on the clone would override the ghost
//    look-and-feel, so we strip them.
function buildDragGhost(row: HTMLTableRowElement): HTMLElement {
  const origTable = row.closest('table')
  const refRect = (origTable ?? row).getBoundingClientRect()

  const wrap = document.createElement('div')
  wrap.style.position = 'fixed'
  wrap.style.top = '0'
  wrap.style.left = '-10000px'
  wrap.style.pointerEvents = 'none'
  wrap.style.width = `${refRect.width}px`
  wrap.style.background = 'var(--n-card-color, #fff)'
  wrap.style.boxShadow = '0 8px 24px rgba(0, 0, 0, 0.25)'
  wrap.style.borderRadius = '4px'
  wrap.style.overflow = 'hidden'
  wrap.style.opacity = '0.95'

  const table = document.createElement('table')
  table.style.width = '100%'
  table.style.tableLayout = 'fixed'
  table.style.borderCollapse = 'separate'
  table.style.fontSize = '12px'

  if (origTable) {
    const cg = origTable.querySelector('colgroup')
    if (cg) table.appendChild(cg.cloneNode(true))
  }

  const tbody = document.createElement('tbody')
  const clone = row.cloneNode(true) as HTMLTableRowElement
  clone.className = ''
  const origInputs = row.querySelectorAll('input')
  const cloneInputs = clone.querySelectorAll('input')
  origInputs.forEach((src, i) => {
    const dst = cloneInputs[i] as HTMLInputElement | undefined
    if (dst) dst.setAttribute('value', src.value)
  })
  tbody.appendChild(clone)
  table.appendChild(tbody)
  wrap.appendChild(table)
  document.body.appendChild(wrap)
  return wrap
}

function onRowDragOver(e: DragEvent, idx: number) {
  if (dragFrom.value == null) return
  // preventDefault on dragover is what tells the browser this is a valid drop
  // target. Without it onDrop never fires.
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  const tr = e.currentTarget as HTMLElement
  const rect = tr.getBoundingClientRect()
  const mid = rect.top + rect.height / 2
  dragOverIdx.value = idx
  dragOverPos.value = e.clientY < mid ? 'top' : 'bottom'
}

function onRowDrop(e: DragEvent, idx: number) {
  if (dragFrom.value == null) {
    resetDragState()
    return
  }
  e.preventDefault()
  const from = dragFrom.value
  // Target slot (gap between rows) — `top` means insert above idx, `bottom`
  // means insert below idx.
  let to = idx + (dragOverPos.value === 'bottom' ? 1 : 0)
  // Dropping below the dragged row's original position shifts the target by
  // one once we splice the row out — compensate so the visual indicator
  // matches the actual landing slot.
  if (from < to) to--
  if (from !== to) {
    const list = props.modelValue.slice()
    const [row] = list.splice(from, 1)
    list.splice(to, 0, row)
    emit('update:modelValue', list)
  }
  resetDragState()
}

// Container-level fallback. Row-level @dragover only fires while the cursor
// sits *on* a row — so the gap between the last row's bottom and the wrap's
// bottom is a dead zone, which made it impossible to drop a column at the
// very end. We catch that here: if the cursor is past the last row, force
// dragOverIdx = lastIdx + pos = 'bottom' so the same drop pipeline runs.
function onWrapDragOver(e: DragEvent) {
  if (dragFrom.value == null) return
  if (props.modelValue.length === 0) return
  const wrap = e.currentTarget as HTMLElement
  const lastRow = wrap.querySelector('tbody tr:last-of-type') as HTMLElement | null
  if (!lastRow) return
  if (e.clientY > lastRow.getBoundingClientRect().bottom) {
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    dragOverIdx.value = props.modelValue.length - 1
    dragOverPos.value = 'bottom'
  }
}

function onWrapDrop(e: DragEvent) {
  if (dragFrom.value == null) {
    resetDragState()
    return
  }
  // If a row's @drop already handled this, it cleared dragFrom in
  // resetDragState; the early-return above takes that path.
  const wrap = e.currentTarget as HTMLElement
  const lastRow = wrap.querySelector('tbody tr:last-of-type') as HTMLElement | null
  if (lastRow && e.clientY > lastRow.getBoundingClientRect().bottom) {
    onRowDrop(e, props.modelValue.length - 1)
  } else {
    resetDragState()
  }
}

function onDragEnd() {
  resetDragState()
}

function resetDragState() {
  dragFrom.value = null
  dragOverIdx.value = null
}

// Computed landing slot — replicates the same splice-compensation logic used
// in onRowDrop so the status chip and the drop indicator agree on the final
// position even when dragging downward.
const dropTargetIndex = computed<number | null>(() => {
  if (dragFrom.value == null || dragOverIdx.value == null) return null
  const from = dragFrom.value
  let to = dragOverIdx.value + (dragOverPos.value === 'bottom' ? 1 : 0)
  if (from < to) to--
  return to
})
const isDragging = computed(() => dragFrom.value != null)
const showStatusChip = computed(
  () =>
    isDragging.value &&
    dropTargetIndex.value != null &&
    dropTargetIndex.value !== dragFrom.value,
)

function rowClass(idx: number, row: ColumnDraft): Record<string, boolean> {
  return {
    'is-new': !row.origName,
    'is-renamed': !!row.origName && row.origName !== row.name,
    'is-dragging': dragFrom.value === idx,
    'is-drop-top': dragOverIdx.value === idx && dragOverPos.value === 'top' && dragFrom.value !== idx,
    'is-drop-bottom': dragOverIdx.value === idx && dragOverPos.value === 'bottom' && dragFrom.value !== idx,
  }
}

// ---- default-clause widget binding ----------------------------------------

function hasDefault(row: ColumnDraft): boolean {
  return row.default !== undefined
}
function toggleDefault(row: ColumnDraft, on: boolean) {
  row.default = on ? '' : undefined
  commit()
}
function setDefault(row: ColumnDraft, val: string) {
  row.default = val
  commit()
}

// ---- type dropdown + params input + UNSIGNED toggle -----------------------
//
// The draft now stores baseType + typeParams + unsigned as three independent
// fields (see lib/alterPlan.ts). buildNativeType reassembles the canonical
// string when generating DDL — the UI never has to do string surgery on
// nativeType. typeFormatFor tells us, per base type, what the params field
// means (length / precision,scale / fractional-seconds / none / …) so we can
// pick a placeholder and disable the input for types that don't take params.

/** Type-aware metadata for the params input of a given draft row. */
function fmtFor(row: ColumnDraft) {
  return typeFormatFor(props.dialect, row.baseType)
}

function onTypeChange(row: ColumnDraft, base: string) {
  const prev = typeFormatFor(props.dialect, row.baseType)
  const next = typeFormatFor(props.dialect, base)
  row.baseType = base.toUpperCase()
  // Clear params when moving to a type that can't carry them, OR when the
  // param shape changes incompatibly (e.g. length "255" stays meaningful when
  // switching VARCHAR→CHAR, but "10,2" makes no sense when switching DECIMAL→INT).
  if (next.kind === 'none') {
    row.typeParams = ''
  } else if (prev.kind !== next.kind && row.typeParams) {
    row.typeParams = ''
  }
  // Clear UNSIGNED if the new type doesn't accept it.
  if (!next.supportsUnsigned) row.unsigned = false
  // Drop AI when moving off an integer type (the checkbox would be disabled
  // anyway, but the model needs to follow so the value doesn't ghost in DDL).
  maybeClearAiOnTypeChange(row)
  commit()
}

function onParamsChange(row: ColumnDraft, value: string) {
  row.typeParams = value
  commit()
}

function onUnsignedChange(row: ColumnDraft, v: boolean) {
  row.unsigned = v
  commit()
}

// ---- auto-increment constraints ---------------------------------------------
//
// The dialect's UIDialect.autoIncrement declares the rules the UI mirrors so
// the user never stages a DDL the server will reject — which base types may
// carry the flag, and how many columns per table may have it (MySQL: integer
// types, one per table). Unsupported dialects render "—" on every row.

function aiAllowed(row: ColumnDraft): boolean {
  return autoIncrementAllowed(props.dialect, row.baseType)
}

/**
 * Whether the AI checkbox renders on this row. Disallowed rows show "—"
 * (the type can never carry AI); every allowed row always renders a live
 * checkbox — with MaxPerTable=1 the flag behaves like a radio across rows
 * (checking one auto-clears the others), so we never need to disable them.
 */
function aiSelectable(row: ColumnDraft): boolean {
  return aiAllowed(row)
}

function aiTitle(row: ColumnDraft): string {
  if (!aiAllowed(row)) return t('structure.columns.aiIntOnly')
  return COL_TITLES.value.ai
}

// Some dialects (MySQL) require every PRIMARY KEY column to be NOT NULL; when
// declared, checking PK also forces NOT NULL so the resulting DDL is valid
// without an extra click. Unchecking PK does NOT re-enable nullable — the
// user might have independently meant NOT NULL.
function onPkChange(row: ColumnDraft, v: boolean) {
  row.isPrimaryKey = v
  if (v && props.dialect.primaryKeyForcesNotNull) row.nullable = false
  commit()
}

function onAiChange(row: ColumnDraft, v: boolean) {
  // Defensive: shouldn't fire on disallowed rows (UI hides the checkbox),
  // but guard anyway so the model can't drift.
  if (v && !aiAllowed(row)) return
  if (v && props.dialect.autoIncrement?.maxPerTable === 1) {
    // Radio-style: checking a row auto-clears every other row's flag in one
    // pass, honoring the dialect's one-per-table rule.
    for (const r of props.modelValue) {
      if (r !== row && r.isAutoIncrement) r.isAutoIncrement = false
    }
  }
  row.isAutoIncrement = v
  commit()
}

// When a row's type changes to one that can't carry the flag, drop it
// silently so the model stays consistent with the visible disabled state.
function maybeClearAiOnTypeChange(row: ColumnDraft) {
  if (row.isAutoIncrement && !aiAllowed(row)) {
    row.isAutoIncrement = false
  }
}

// ---- header tooltips ------------------------------------------------------

// computed so the tooltips re-translate live on locale switch.
const COL_TITLES = computed<Record<string, string>>(() => ({
  pk: t('structure.columns.tip.pk'),
  ai: t('structure.columns.tip.ai'),
  nn: t('structure.columns.tip.nn'),
  default: t('structure.columns.tip.default'),
  drag: t('structure.columns.tip.drag'),
  params: t('structure.columns.tip.params'),
  unsigned: t('structure.columns.tip.unsigned'),
}))
</script>

<template>
  <div class="cols-tab" :class="{ 'drag-active': isDragging }">
    <div
      class="cols-table-wrap"
      @dragend="onDragEnd"
      @dragover="onWrapDragOver"
      @drop="onWrapDrop"
    >
      <table class="cols-table">
        <colgroup>
          <col style="width: 22px" />
          <col style="width: 32px" />
          <col style="width: 22%" />
          <col style="width: 13%" />
          <col style="width: 11%" />
          <col v-if="showUnsigned" style="width: 52px" />
          <col style="width: 44px" />
          <col style="width: 44px" />
          <col style="width: 44px" />
          <col style="width: 22%" />
          <col style="width: 24%" />
          <col style="width: 40px" />
        </colgroup>
        <thead>
          <tr>
            <th class="th-drag" :title="COL_TITLES.drag"></th>
            <th class="th-idx">#</th>
            <th>{{ $t('structure.columns.thName') }}</th>
            <th>{{ $t('structure.columns.thType') }}</th>
            <th>
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger><span class="th-tip">{{ $t('structure.columns.thParams') }}</span></template>
                {{ COL_TITLES.params }}
              </n-tooltip>
            </th>
            <th v-if="showUnsigned" class="th-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger><span class="th-tip">UN</span></template>
                {{ COL_TITLES.unsigned }}
              </n-tooltip>
            </th>
            <th class="th-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger><span class="th-tip">PK</span></template>
                {{ COL_TITLES.pk }}
              </n-tooltip>
            </th>
            <th class="th-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger><span class="th-tip">NN</span></template>
                {{ COL_TITLES.nn }}
              </n-tooltip>
            </th>
            <th class="th-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger><span class="th-tip">AI</span></template>
                {{ COL_TITLES.ai }}
              </n-tooltip>
            </th>
            <th>
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger><span class="th-tip">{{ $t('structure.columns.thDefault') }}</span></template>
                {{ COL_TITLES.default }}
              </n-tooltip>
            </th>
            <th>{{ $t('structure.columns.thComment') }}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, i) in modelValue"
            :key="row._key"
            :class="rowClass(i, row)"
            @dragover="onRowDragOver($event, i)"
            @drop="onRowDrop($event, i)"
          >
            <td
              class="td-drag"
              :draggable="!busy"
              @dragstart="onDragStart($event, i)"
            >
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger><span class="drag-handle">⋮⋮</span></template>
                {{ COL_TITLES.drag }}
              </n-tooltip>
            </td>
            <td class="td-idx">{{ i + 1 }}</td>
            <td>
              <n-input
                v-model:value="row.name"
                size="tiny"
                placeholder="column_name"
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td class="td-type">
              <select
                :value="row.baseType"
                class="native-sel type-sel"
                :disabled="busy"
                @change="onTypeChange(row, ($event.target as HTMLSelectElement).value)"
              >
                <optgroup
                  v-for="g in typeGroups"
                  :key="g.label"
                  :label="g.label"
                >
                  <option v-for="bt in g.types" :key="bt" :value="bt">{{ bt }}</option>
                </optgroup>
                <!-- Custom/legacy types not in the catalog: render a single
                     extra option so they round-trip cleanly. -->
                <option
                  v-if="row.baseType && !typeGroups.some(g => g.types.includes(row.baseType))"
                  :value="row.baseType"
                >
                  {{ row.baseType }}
                </option>
              </select>
            </td>
            <td class="td-params">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger>
                  <input
                    :value="row.typeParams"
                    class="native-input params-input"
                    :class="{ 'is-required': fmtFor(row).paramsRequired && !row.typeParams.trim() }"
                    :placeholder="fmtFor(row).placeholder"
                    :disabled="busy || fmtFor(row).kind === 'none'"
                    @input="onParamsChange(row, ($event.target as HTMLInputElement).value)"
                  />
                </template>
                {{ COL_TITLES.params }}
              </n-tooltip>
            </td>
            <td v-if="showUnsigned" class="td-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger>
                  <span class="td-tip-wrap">
                    <n-checkbox
                      v-if="fmtFor(row).supportsUnsigned"
                      :checked="row.unsigned"
                      :disabled="busy"
                      @update:checked="(v) => onUnsignedChange(row, !!v)"
                    />
                    <span v-else class="td-na">—</span>
                  </span>
                </template>
                {{ fmtFor(row).supportsUnsigned ? COL_TITLES.unsigned : $t('structure.columns.unsignedUnsupported') }}
              </n-tooltip>
            </td>
            <td class="td-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger>
                  <span class="td-tip-wrap">
                    <n-checkbox
                      :checked="row.isPrimaryKey"
                      :disabled="busy"
                      @update:checked="(v) => onPkChange(row, !!v)"
                    />
                  </span>
                </template>
                {{ COL_TITLES.pk }}
              </n-tooltip>
            </td>
            <td class="td-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger>
                  <span class="td-tip-wrap">
                    <n-checkbox
                      :checked="!row.nullable"
                      :disabled="busy"
                      @update:checked="(v) => { row.nullable = !v; commit() }"
                    />
                  </span>
                </template>
                {{ COL_TITLES.nn }}
              </n-tooltip>
            </td>
            <td class="td-center">
              <n-tooltip placement="top" :delay="100" :show-arrow="false">
                <template #trigger>
                  <span class="td-tip-wrap">
                    <n-checkbox
                      v-if="aiSelectable(row)"
                      :checked="row.isAutoIncrement"
                      :disabled="busy"
                      @update:checked="(v) => onAiChange(row, !!v)"
                    />
                    <span v-else class="td-na">—</span>
                  </span>
                </template>
                {{ aiTitle(row) }}
              </n-tooltip>
            </td>
            <td>
              <div class="default-cell">
                <n-checkbox
                  :checked="hasDefault(row)"
                  :disabled="busy"
                  @update:checked="(v) => toggleDefault(row, !!v)"
                />
                <n-input
                  :value="row.default ?? ''"
                  size="tiny"
                  :placeholder="$t('structure.columns.nonePlaceholder')"
                  :disabled="busy || !hasDefault(row)"
                  @update:value="(v: string) => setDefault(row, v)"
                />
              </div>
            </td>
            <td>
              <n-input
                v-model:value="row.comment"
                size="tiny"
                placeholder=""
                :disabled="busy"
                @update:value="commit"
              />
            </td>
            <td class="td-actions">
              <n-button size="tiny" quaternary :disabled="busy" :title="$t('common.delete')" @click="deleteRow(i)">✕</n-button>
            </td>
          </tr>
          <tr v-if="modelValue.length === 0" class="empty-row">
            <td :colspan="totalCols" style="text-align: center; color: var(--n-text-color-3); padding: 16px">
              {{ $t('structure.columns.empty') }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="cols-toolbar">
      <n-button size="tiny" :disabled="busy" @click="addRow()">{{ $t('structure.columns.addField') }}</n-button>
      <transition name="fade">
        <span v-if="showStatusChip" class="drag-status">
          <i18n-t keypath="structure.columns.dragStatus" tag="span">
            <template #from><b>{{ (dragFrom ?? 0) + 1 }}</b></template>
            <template #to><b>{{ (dropTargetIndex ?? 0) + 1 }}</b></template>
          </i18n-t>
        </span>
      </transition>
    </div>
  </div>
</template>

<style scoped>
.cols-tab {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  background-color: var(--catdb-surface-content);
}
.cols-table-wrap {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}
.cols-table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  font-size: var(--catdb-fs-small);
  table-layout: fixed;
}
.cols-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--catdb-surface-chrome);
  color: var(--n-text-color-2);
  font-weight: 600;
  text-align: left;
  padding: 4px 6px;
  border-bottom: 1px solid var(--catdb-separator);
  white-space: nowrap;
  user-select: none;
}
.cols-table thead th.th-drag {
  padding: 0;
  width: 22px;
}
.cols-table thead th.th-idx {
  text-align: right;
  color: var(--n-text-color-3);
}
.cols-table thead th.th-center {
  text-align: center;
}
/* Inline trigger for n-tooltip on header cells. The underline cue makes it
   discoverable that hovering shows an explanation; without it the bare "UN"/
   "NN"/etc. read as static labels. */
.th-tip {
  display: inline-block;
  cursor: help;
  border-bottom: 1px dotted var(--n-text-color-3);
  line-height: 1.2;
}
.cols-table tbody td {
  padding: 3px 6px;
  vertical-align: middle;
  border-bottom: 1px solid var(--catdb-separator);
}
.cols-table tbody td.td-drag {
  padding: 0 2px 0 4px;
  cursor: grab;
  user-select: none;
  color: var(--n-text-color-3);
  text-align: center;
  font-size: var(--catdb-fs-body);
  line-height: 1;
}
.cols-table tbody td.td-drag:active {
  cursor: grabbing;
}
.drag-handle {
  display: inline-block;
  letter-spacing: -3px; /* visually tighten the two-column dot pattern */
}
.cols-table tbody td.td-idx {
  text-align: right;
  color: var(--n-text-color-2);
  user-select: none;
}
/* Changed rows (new or renamed) — grey out the row number as a subtle
   "this row differs from server state" hint. Replaces the earlier +/~
   markers in the drag-handle cell. */
.cols-table tbody tr.is-new td.td-idx,
.cols-table tbody tr.is-renamed td.td-idx {
  color: var(--n-text-color-3);
}
.cols-table tbody td.td-center {
  text-align: center;
}
/* Inline wrapper so n-tooltip's trigger slot has a single root element even
   when the inner control swaps between n-checkbox and the "—" placeholder. */
.td-tip-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.cols-table tbody td.td-actions {
  white-space: nowrap;
  text-align: right;
}
.cols-table tbody td.td-actions :deep(.n-button) {
  padding: 0 4px;
  min-width: 20px;
}
.cols-table tbody tr:hover td {
  background: var(--n-hover-color);
}

/* ---- Drag visuals --------------------------------------------------------
   Layered cues so the user can't miss the destination:
   - dragged row: dimmed + dashed-feel outline via inset shadow
   - drop target: 3 px primary bar on top/bottom + filled circle anchored to
     the drag-handle column at that same edge
   - while any drag is in flight, the whole table dims slightly + cursor flips
     to grabbing so the modal state is obvious
*/
.cols-tab.drag-active .cols-table-wrap {
  cursor: grabbing;
}
.cols-tab.drag-active .cols-table tbody tr:not(.is-dragging) td {
  /* Subtle de-emphasis on non-target rows so the indicator pops harder */
  background: transparent;
}
.cols-table tbody tr.is-dragging td {
  opacity: 0.4;
  background: var(--n-color) !important;
  box-shadow: inset 0 0 0 1px var(--catdb-separator);
}
.cols-table tbody tr.is-dragging td.td-drag {
  color: var(--n-primary-color);
}

/* Drop indicator: 3px primary bar across the entire row */
.cols-table tbody tr.is-drop-top td {
  box-shadow: inset 0 3px 0 0 var(--n-primary-color);
}
.cols-table tbody tr.is-drop-bottom td {
  box-shadow: inset 0 -3px 0 0 var(--n-primary-color);
}
/* Highlighted drop target row gets a faint primary tint as backdrop */
.cols-table tbody tr.is-drop-top td,
.cols-table tbody tr.is-drop-bottom td {
  background: color-mix(in srgb, var(--n-primary-color) 8%, transparent);
}

/* Filled circle marker anchored to the drag-handle column edge — gives the
   bar a clear "anchor point" and is visible even on very wide tables. */
.cols-table tbody td.td-drag {
  position: relative;
}
.cols-table tbody tr.is-drop-top td.td-drag::before,
.cols-table tbody tr.is-drop-bottom td.td-drag::before {
  content: '';
  position: absolute;
  left: 2px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--n-primary-color);
  box-shadow: 0 0 0 2px var(--n-card-color);
  pointer-events: none;
  z-index: 2;
}
.cols-table tbody tr.is-drop-top td.td-drag::before {
  top: -5px;
}
.cols-table tbody tr.is-drop-bottom td.td-drag::before {
  bottom: -5px;
}

.default-cell {
  display: flex;
  gap: 6px;
  align-items: center;
}
.default-cell :deep(.n-input) {
  flex: 1 1 auto;
  min-width: 0;
}

/* ---- native type cell: base type select ---- */
.td-type,
.td-params {
  /* Each lives in its own column now; keep them simple so the cells stay
     vertically centered with the n-input siblings on the row. */
  vertical-align: middle;
}
.type-sel,
.params-input {
  width: 100%;
  min-width: 0;
}
.native-sel,
.native-input {
  font-size: var(--catdb-fs-small);
  font-family: inherit;
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
/* Soft red outline when a required param is missing — VARCHAR / VARBINARY /
   ENUM / SET all need params; the visual nudges the user to fill them in. */
.params-input.is-required {
  border-color: var(--catdb-error);
  background: color-mix(in srgb, var(--catdb-error) 6%, transparent);
}
/* "—" placeholder shown in the UN column when the row's type doesn't support
   UNSIGNED — keeps the column readable instead of looking empty/broken. */
.td-na {
  color: var(--n-text-color-3);
  user-select: none;
}
.cols-toolbar {
  padding: 6px 8px;
  border-top: 1px solid var(--catdb-separator);
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 10px;
}
/* Live "from N → to M" chip while dragging */
.drag-status {
  font-size: var(--catdb-fs-mini);
  color: var(--n-text-color-2);
  padding: 2px 8px;
  border-radius: var(--catdb-rounded-lg);
  background: color-mix(in srgb, var(--n-primary-color) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--n-primary-color) 30%, transparent);
  white-space: nowrap;
  user-select: none;
}
.drag-status b {
  color: var(--n-primary-color);
  font-weight: 600;
  padding: 0 1px;
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
