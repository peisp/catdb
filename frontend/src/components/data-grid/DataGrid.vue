<script setup lang="ts">
// DataGrid —— 自研 canvas 网格（替换 VTable，对外契约不变）。
//
// 结构：原生 overflow:auto scroller + 撑出总尺寸的占位 div（真实滚动条）
//       + position:sticky 的 canvas 钉在视口重绘可视区（canvasGridRenderer）
//       + DOM 编辑器 overlay（input/textarea/date/time/datetime-local）。
//
// 设计要点：
//   - IPC 形状不动：rows 保持 any[][]，列元数据单传一次（CLAUDE.md 规则 5）
//   - 所有对外 emit 的行列号都是 body 坐标系（直接可索引 props.rows）
//   - 客户端排序（sortRemote=false）原地稳定排序 props.rows —— 复制/Set NULL
//     都按行号索引 rows（gridContextMenu 单例），排序后坐标必须仍然一致；
//     粘贴/编辑本就原地改 props.rows，同一模式。取消排序时从快照恢复原序。
//   - 服务端排序（sortRemote=true）只发射 sort-change，指示器画 props.sortState
//   - NULL / BLOB / JSON / bigint 在 cellText 里统一渲染
//   - 主题从 Naive 的 useThemeVars() 派生，light/dark 切换走同一通道
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useThemeVars } from 'naive-ui'
import { useThemeStore } from '../../stores/theme'
import { editorSurface } from '../../styles/theme'
import type { ColumnMeta } from '../../api/metadata'
import { LogicalType } from '../../api/metadata'
import { parseTSV, planPaste, type SelectionRange } from '../../composables/useTableSelection'
import {
  buildOffsets,
  drawGrid,
  hitTest,
  RESIZE_ZONE,
  SORT_ZONE_WIDTH,
  type GridHit,
  type GridTheme,
  type NormRange,
} from './canvasGridRenderer'

/** Sort state for indicator sync (server-side sort). field = column index. */
export interface SortState {
  field: number
  order: 'asc' | 'desc'
}

interface Props {
  columns: ColumnMeta[]
  rows: any[][]
  /** 是否允许单击进入编辑态（read-only 模式传 false） */
  editable?: boolean
  /** PK 列名 —— 用于右键菜单判定（PK 列不显示「Set to NULL」）。PK 列本身可编辑。 */
  pkColumns?: string[]
  /** 提示性 fetching，用于禁用编辑触发等 */
  fetching?: boolean
  /** 行高，默认 24px 桌面风格 */
  rowHeight?: number
  /** 单列默认宽度 */
  defaultColumnWidth?: number
  /** 是否启用手动列排序; 默认 true */
  sortable?: boolean
  /** true=服务端排序（发射 sort-change）；false=客户端原地排序（默认）。 */
  sortRemote?: boolean
  /** 当前服务端排序状态，用于同步排序指示器。sortRemote=true 时使用。 */
  sortState?: SortState | null
  /** 未保存的脏单元格集合（"row:col" 格式的 key），用于灰色渲染提示 */
  dirtyCells?: Set<string>
  /** 标记删除的行号集合（body 坐标系 row index），整行灰色渲染 */
  deletedRows?: Set<number>
  /** 有未保存编辑的行号集合（body 坐标系 row index），# 列灰色渲染 */
  dirtyRows?: Set<number>
  /** Wails 原生右键菜单名（覆盖默认的 catdb-grid-cell / catdb-grid-cell-edit 切换逻辑）。 */
  contextMenuName?: string
  /** 表头第二行显示字段类型（nativeType）。合成列的表（如 TablesOverview）不开。 */
  showTypes?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  editable: false,
  pkColumns: () => [],
  dirtyCells: () => new Set<string>(),
  deletedRows: () => new Set<number>(),
  dirtyRows: () => new Set<number>(),
  fetching: false,
  rowHeight: 24,
  defaultColumnWidth: 160,
  sortable: true,
  sortRemote: false,
  sortState: null,
  contextMenuName: '',
  showTypes: false,
})

const emit = defineEmits<{
  /** 编辑提交：row/col 为 body 坐标系（row 从 0 起） */
  (e: 'edit-commit', p: { row: number; col: number; oldValue: any; newValue: any; column: ColumnMeta }): void
  /** 右键单元格：x/y 为屏幕坐标（pageX/pageY） */
  (e: 'cell-context-menu', p: { row: number; col: number; x: number; y: number; value: any }): void
  /** 选区变化：range 为 null 表示清空 */
  (e: 'selection-change', p: { range: SelectionRange | null }): void
  /** 滚到底部，触发分页/流式追加 */
  (e: 'load-more'): void
  /** 排序变化（sortRemote=true 时发射）：field 为列下标；null 表示清除排序 */
  (e: 'sort-change', p: { field: number; order: 'asc' | 'desc' } | null): void
  /** 双击单元格：row 为 body 行号（0 起），col 为 body 列号 */
  (e: 'cell-dblclick', p: { row: number; col: number; value: any }): void
}>()

const themeVars = useThemeVars()
const theme = useThemeStore()

const ROWNUM_W = 50
// 显示字段类型时表头两行布局，加高
const headerH = computed(() => (props.showTypes ? 38 : 28))
const FONT_SIZE = 12
const FONT_FAMILY =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif'

// ---- refs / 状态 ----
const wrapRef = ref<HTMLElement | null>(null)
const scrollerRef = ref<HTMLElement | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)
const editorEl = ref<HTMLInputElement | HTMLTextAreaElement | null>(null)

const viewW = ref(0)
const viewH = ref(0)
const scrollTop = ref(0)
const scrollLeft = ref(0)

const colWidths = ref<number[]>([])
const selAnchor = ref<{ row: number; col: number } | null>(null)
const selHead = ref<{ row: number; col: number } | null>(null)
const hover = ref<{ row: number; col: number } | null>(null)

type EditorKind = 'input' | 'textarea' | 'date' | 'time' | 'datetime'
const editing = ref<{ row: number; col: number; kind: EditorKind } | null>(null)
const editValue = ref('')
let editOriginal: any = null
let commitGuard = false

const clientSort = ref<{ col: number; order: 'asc' | 'desc' } | null>(null)
let originalOrder: any[][] | null = null

let loadMoreArmed = true

// ---- 单元格显示渲染 ----
function renderCellValue(v: any): string {
  if (v == null) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'number') return String(v)
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  if (typeof v === 'object') {
    if (v.__type__ === 'bytes') return `bytes(${v.length})`
    if (v.__type__ === 'bigint') return String(v.value)
    try { return JSON.stringify(v) } catch { return String(v) }
  }
  return String(v)
}

function cellText(row: number, col: number): string {
  const v = props.rows[row]?.[col]
  if (v == null) return 'NULL'
  return renderCellValue(v)
}

// ---- 列类型 → 编辑器种类 ----
// PK / auto-increment 列允许编辑：UPDATE 的 WHERE 用改前的原始 PK 定位行。
function pickEditorKind(col: ColumnMeta | undefined): EditorKind | null {
  if (!col) return null
  switch (col.logicalType) {
    case LogicalType.TypeDate:
      return 'date'
    case LogicalType.TypeTime:
      return 'time'
    case LogicalType.TypeDateTime:
    case LogicalType.TypeTimestamp:
      return 'datetime'
    case LogicalType.TypeJSON:
    case LogicalType.TypeText:
      return 'textarea'
    case LogicalType.TypeBytes:
      // 二进制不在表格内编辑
      return null
    default:
      return 'input'
  }
}

// ---- 时间值：单元格字符串 ↔ 原生 input.value ----
// 单元格里的时间值统一是 "YYYY-MM-DD HH:mm:ss[.fff]"（见后端 scanner）。
function toInputValue(kind: EditorKind, value: any): string {
  if (value == null) return ''
  const s = renderCellValue(value)
  if (kind === 'datetime') {
    const m = s.match(/^(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2}(?::\d{2})?)/)
    return m ? `${m[1]}T${m[2]}` : s.replace(' ', 'T')
  }
  return s
}
function fromInputValue(kind: EditorKind, raw: string): string {
  const v = raw.trim()
  if (v === '') return ''
  if (kind === 'datetime') {
    let out = v.replace('T', ' ')
    if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(out)) out += ':00'
    return out
  }
  if (kind === 'time' && /^\d{2}:\d{2}$/.test(v)) return v + ':00'
  return v
}

// ---- 列类型 → 对齐方式 ----
function pickAlign(col: ColumnMeta): 'left' | 'right' | 'center' {
  switch (col.logicalType) {
    case LogicalType.TypeInt:
    case LogicalType.TypeBigInt:
    case LogicalType.TypeFloat:
    case LogicalType.TypeDecimal:
      return 'right'
    case LogicalType.TypeBool:
      return 'center'
    default:
      return 'left'
  }
}

// ---- 文本测量（表头最小宽度 / auto-fit） ----
let _measureCtx: CanvasRenderingContext2D | null = null
function measureCtx(): CanvasRenderingContext2D | null {
  if (_measureCtx) return _measureCtx
  const ctx = document.createElement('canvas').getContext('2d')
  if (ctx) _measureCtx = ctx
  return ctx
}
function measureText(text: string, font: string): number {
  const ctx = measureCtx()
  if (!ctx) return text.length * 8
  ctx.font = font
  return ctx.measureText(text).width
}

// 表头类型行文案：统一小写；nativeType 不带括号时（查询结果集走 wire
// protocol，只有裸类型名）从 length/precision/scale 补上长度/精度。
// 字符类型不补：协议报告的 length 是字节数（utf8mb4 下 varchar(255) 报
// 1020），显示会误导；元数据路径的 COLUMN_TYPE 本身就带准确括号。
function headerTypeText(c: ColumnMeta): string {
  const t = c.nativeType
  if (!t) return ''
  const base = t.toLowerCase()
  if (base.includes('(')) return base
  const upper = t.toUpperCase()
  if ((upper === 'DECIMAL' || upper === 'NUMERIC') && c.precision && c.precision > 0 && c.precision <= 65) {
    return `${base}(${c.precision},${c.scale ?? 0})`
  }
  if ((upper === 'BINARY' || upper === 'VARBINARY' || upper === 'BIT') && c.length && c.length > 0) {
    return `${base}(${c.length})`
  }
  return base
}

function headerMinWidth(col: ColumnMeta): number {
  let textW = measureText(col.name, `500 ${FONT_SIZE}px ${FONT_FAMILY}`)
  if (props.showTypes && col.nativeType) {
    textW = Math.max(textW, measureText(headerTypeText(col), `${FONT_SIZE - 2}px ${FONT_FAMILY}`))
  }
  const iconW = props.sortable ? SORT_ZONE_WIDTH : 0
  return Math.ceil(textW + iconW + 16 + 1)
}

// ---- 几何 ----
const offsets = computed(() => buildOffsets(colWidths.value))
const totalWidth = computed(() => ROWNUM_W + (offsets.value[offsets.value.length - 1] ?? 0))
const totalHeight = computed(() => headerH.value + props.rows.length * props.rowHeight)

function geoOpts() {
  return {
    rowNumberWidth: ROWNUM_W,
    headerHeight: headerH.value,
    rowHeight: props.rowHeight,
    colOffsets: offsets.value,
  }
}

// ---- 选区 ----
const normSel = computed<NormRange | null>(() => {
  const a = selAnchor.value
  const h = selHead.value
  if (!a || !h || !props.rows.length || !props.columns.length) return null
  return {
    r0: Math.max(0, Math.min(a.row, h.row)),
    r1: Math.min(props.rows.length - 1, Math.max(a.row, h.row)),
    c0: Math.max(0, Math.min(a.col, h.col)),
    c1: Math.min(props.columns.length - 1, Math.max(a.col, h.col)),
  }
})

function emitSelection() {
  const s = normSel.value
  emit('selection-change', {
    range: s ? { startRow: s.r0, startCol: s.c0, endRow: s.r1, endCol: s.c1 } : null,
  })
}

function setSelection(anchor: { row: number; col: number } | null, head?: { row: number; col: number }) {
  selAnchor.value = anchor
  selHead.value = anchor ? (head ?? anchor) : null
  emitSelection()
  requestRender()
}

function selectAll() {
  if (!props.rows.length || !props.columns.length) return
  setSelection({ row: 0, col: 0 }, { row: props.rows.length - 1, col: props.columns.length - 1 })
}

// ---- 主题 ----
const gridTheme = computed<GridTheme>(() => {
  const vars = themeVars.value
  const dark = theme.mode === 'dark'
  const surface = dark ? editorSurface.dark : editorSurface.light
  return {
    surface,
    headerBg: vars.tableHeaderColor,
    text: vars.textColor1,
    textMuted: dark ? '#777' : '#aaa',
    border: vars.borderColor,
    divider: vars.dividerColor,
    hoverFill: vars.hoverColor,
    selectionFill: 'rgba(32,128,240,0.14)',
    selectionBorder: vars.primaryColor,
    deletedFill: 'rgba(200,200,200,0.1)',
    deletedText: '#999',
    dirtyText: '#999',
    rowNumText: vars.textColor3,
  }
})

// ---- 渲染调度 ----
let renderQueued = false
function requestRender() {
  if (renderQueued) return
  renderQueued = true
  requestAnimationFrame(() => {
    renderQueued = false
    draw()
  })
}

function draw() {
  const canvas = canvasRef.value
  if (!canvas || viewW.value <= 0 || viewH.value <= 0) return
  const dirtySet = props.dirtyCells
  const deletedSet = props.deletedRows
  const dirtyRowSet = props.dirtyRows
  const sortIndicator = props.sortRemote
    ? props.sortState
      ? { col: props.sortState.field, order: props.sortState.order }
      : null
    : clientSort.value
  drawGrid({
    canvas,
    width: viewW.value,
    height: viewH.value,
    theme: gridTheme.value,
    fonts: { family: FONT_FAMILY, size: FONT_SIZE },
    rowNumberWidth: ROWNUM_W,
    headerHeight: headerH.value,
    rowHeight: props.rowHeight,
    colWidths: colWidths.value,
    colOffsets: offsets.value,
    scrollTop: scrollTop.value,
    scrollLeft: scrollLeft.value,
    rowCount: props.rows.length,
    columns: props.columns.map((c) => ({
      title: c.name,
      align: pickAlign(c),
      subtitle: props.showTypes ? headerTypeText(c) : undefined,
    })),
    cellText,
    cellIsNull: (r, c) => props.rows[r]?.[c] == null,
    isDirtyCell: (r, c) => dirtySet.has(`${r}:${c}`),
    isDeletedRow: (r) => deletedSet.has(r),
    isDirtyRow: (r) => dirtyRowSet.has(r),
    selection: normSel.value,
    hover: hover.value,
    editing: editing.value,
    sortable: props.sortable,
    sortState: sortIndicator,
  })
}

// ---- 滚动 ----
function onScroll() {
  const el = scrollerRef.value
  if (!el) return
  scrollTop.value = el.scrollTop
  scrollLeft.value = el.scrollLeft
  requestRender()
  // 滚近底部触发 load-more，一次「进入底部区域」只发一次
  const remain = el.scrollHeight - el.scrollTop - el.clientHeight
  if (remain < props.rowHeight * 3) {
    if (loadMoreArmed && props.rows.length) {
      loadMoreArmed = false
      emit('load-more')
    }
  } else {
    loadMoreArmed = true
  }
}

function scrollCellIntoView(row: number, col: number) {
  const el = scrollerRef.value
  if (!el) return
  const cellTop = row * props.rowHeight
  const cellBottom = cellTop + props.rowHeight
  const viewTop = el.scrollTop
  const viewBottom = viewTop + el.clientHeight - headerH.value
  if (cellTop < viewTop) el.scrollTop = cellTop
  else if (cellBottom > viewBottom) el.scrollTop = cellBottom - (el.clientHeight - headerH.value)
  const cellLeft = offsets.value[col] ?? 0
  const cellRight = cellLeft + (colWidths.value[col] ?? 0)
  const viewLeft = el.scrollLeft
  const viewRight = viewLeft + el.clientWidth - ROWNUM_W
  if (cellLeft < viewLeft) el.scrollLeft = cellLeft
  else if (cellRight > viewRight) el.scrollLeft = cellRight - (el.clientWidth - ROWNUM_W)
}

// ---- 命中测试 ----
function hitFromEvent(e: MouseEvent): GridHit {
  const rect = canvasRef.value!.getBoundingClientRect()
  return hitTest(e.clientX - rect.left, e.clientY - rect.top, scrollLeft.value, scrollTop.value, geoOpts())
}

function inRowRange(row: number): boolean {
  return row >= 0 && row < props.rows.length
}

// ---- 鼠标：选区拖拽 / 列宽拖拽 / 排序点击 ----
type DragMode = 'cells' | 'rows' | 'cols' | 'resize'
let drag: {
  mode: DragMode
  moved: boolean
  clickCell: { row: number; col: number } | null
  resizeCol: number
  resizeStartX: number
  resizeStartW: number
  pointer: { x: number; y: number }
  start: { x: number; y: number }
} | null = null
let autoScrollRaf = 0

function onMouseDown(e: MouseEvent) {
  if (e.button !== 0) return
  wrapRef.value?.focus()
  const hit = hitFromEvent(e)
  const cols = props.columns.length
  const rows = props.rows.length

  if (hit.region === 'corner') {
    selectAll()
    return
  }

  if (hit.region === 'header') {
    if (hit.col >= 0) {
      const w = colWidths.value[hit.col]
      // 列边界 → 拖拽列宽
      if (hit.xInCol >= w - RESIZE_ZONE || (hit.xInCol < RESIZE_ZONE && hit.col > 0)) {
        const target = hit.xInCol < RESIZE_ZONE ? hit.col - 1 : hit.col
        drag = {
          mode: 'resize', moved: false, clickCell: null,
          resizeCol: target, resizeStartX: e.clientX, resizeStartW: colWidths.value[target],
          pointer: { x: e.clientX, y: e.clientY },
          start: { x: e.clientX, y: e.clientY },
        }
        attachWindowDrag()
        return
      }
      // 表头：点击=排序，拖拽=选列。选区在首次真实移动时才建立（见
      // updateDragSelection），松开时未移动则视为排序点击。
      drag = {
        mode: 'cols', moved: false, clickCell: { row: -1, col: hit.col },
        resizeCol: -1, resizeStartX: 0, resizeStartW: 0,
        pointer: { x: e.clientX, y: e.clientY },
        start: { x: e.clientX, y: e.clientY },
      }
      attachWindowDrag()
    }
    return
  }

  if (hit.region === 'rownum') {
    if (inRowRange(hit.row) && cols > 0) {
      setSelection({ row: hit.row, col: 0 }, { row: hit.row, col: cols - 1 })
      drag = {
        mode: 'rows', moved: false, clickCell: null,
        resizeCol: -1, resizeStartX: 0, resizeStartW: 0,
        pointer: { x: e.clientX, y: e.clientY },
        start: { x: e.clientX, y: e.clientY },
      }
      attachWindowDrag()
    }
    return
  }

  // body 区域
  if (!inRowRange(hit.row) || hit.col < 0) {
    setSelection(null)
    return
  }
  if (e.shiftKey && selAnchor.value) {
    selHead.value = { row: hit.row, col: hit.col }
    emitSelection()
    requestRender()
  } else {
    setSelection({ row: hit.row, col: hit.col })
  }
  drag = {
    mode: 'cells', moved: false,
    clickCell: e.shiftKey ? null : { row: hit.row, col: hit.col },
    resizeCol: -1, resizeStartX: 0, resizeStartW: 0,
    pointer: { x: e.clientX, y: e.clientY },
    start: { x: e.clientX, y: e.clientY },
  }
  attachWindowDrag()
}

function attachWindowDrag() {
  window.addEventListener('mousemove', onWindowDragMove)
  window.addEventListener('mouseup', onWindowDragUp)
}

function bodyCellAtPointer(clientX: number, clientY: number): { row: number; col: number } {
  const rect = canvasRef.value!.getBoundingClientRect()
  const x = clientX - rect.left
  const y = clientY - rect.top
  const contentY = y - headerH.value + scrollTop.value
  const row = Math.max(0, Math.min(props.rows.length - 1, Math.floor(contentY / props.rowHeight)))
  const contentX = x - ROWNUM_W + scrollLeft.value
  const off = offsets.value
  let col: number
  if (contentX <= 0) col = 0
  else if (contentX >= off[off.length - 1]) col = props.columns.length - 1
  else {
    col = 0
    let lo = 0
    let hi = off.length - 2
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1
      if (off[mid] <= contentX) lo = mid
      else hi = mid - 1
    }
    col = lo
  }
  return { row, col }
}

function updateDragSelection() {
  if (!drag || !props.rows.length) return
  // 表头 cols 拖拽：首次移动超过阈值才建立选区（未移动的点击留给排序）
  if (!selAnchor.value) {
    if (drag.mode !== 'cols' || !drag.clickCell) return
    const dx = Math.abs(drag.pointer.x - drag.start.x)
    const dy = Math.abs(drag.pointer.y - drag.start.y)
    if (dx + dy < 4) return
    selAnchor.value = { row: 0, col: drag.clickCell.col }
  }
  const { row, col } = bodyCellAtPointer(drag.pointer.x, drag.pointer.y)
  let head: { row: number; col: number }
  if (drag.mode === 'rows') head = { row, col: props.columns.length - 1 }
  else if (drag.mode === 'cols') head = { row: props.rows.length - 1, col }
  else head = { row, col }
  if (selHead.value?.row !== head.row || selHead.value?.col !== head.col) {
    drag.moved = true
    selHead.value = head
    emitSelection()
    requestRender()
  }
}

function onWindowDragMove(e: MouseEvent) {
  if (!drag) return
  drag.pointer = { x: e.clientX, y: e.clientY }
  if (drag.mode === 'resize') {
    const meta = props.columns[drag.resizeCol]
    const minW = meta ? headerMinWidth(meta) : 40
    const w = Math.max(minW, drag.resizeStartW + (e.clientX - drag.resizeStartX))
    if (colWidths.value[drag.resizeCol] !== w) {
      colWidths.value[drag.resizeCol] = w
      drag.moved = true
      requestRender()
    }
    return
  }
  updateDragSelection()
  maybeAutoScroll()
}

// 拖出 body 可视区边缘时按距离持续滚动
function maybeAutoScroll() {
  if (autoScrollRaf) return
  const step = () => {
    autoScrollRaf = 0
    if (!drag || drag.mode === 'resize') return
    const el = scrollerRef.value
    const canvas = canvasRef.value
    if (!el || !canvas) return
    const rect = canvas.getBoundingClientRect()
    const px = drag.pointer.x
    const py = drag.pointer.y
    let dx = 0
    let dy = 0
    if (py < rect.top + headerH.value) dy = -Math.min(40, (rect.top + headerH.value - py) / 2)
    else if (py > rect.bottom) dy = Math.min(40, (py - rect.bottom) / 2)
    if (px < rect.left + ROWNUM_W) dx = -Math.min(40, (rect.left + ROWNUM_W - px) / 2)
    else if (px > rect.right) dx = Math.min(40, (px - rect.right) / 2)
    if (dx || dy) {
      el.scrollTop += dy
      el.scrollLeft += dx
      updateDragSelection()
      autoScrollRaf = requestAnimationFrame(step)
    }
  }
  autoScrollRaf = requestAnimationFrame(step)
}

function onWindowDragUp() {
  window.removeEventListener('mousemove', onWindowDragMove)
  window.removeEventListener('mouseup', onWindowDragUp)
  if (autoScrollRaf) {
    cancelAnimationFrame(autoScrollRaf)
    autoScrollRaf = 0
  }
  const d = drag
  drag = null
  if (!d) return
  // 单击（未拖动）→ 进入编辑（VTable editCellTrigger:'click' 的等价行为）
  if (d.mode === 'cells' && !d.moved && d.clickCell) {
    startEdit(d.clickCell.row, d.clickCell.col)
    return
  }
  // 表头单击（未拖出选区）：排序；不可排序时退化为整列选择
  if (d.mode === 'cols' && !d.moved && d.clickCell) {
    if (props.sortable) {
      sortCycle(d.clickCell.col)
    } else if (props.rows.length) {
      setSelection({ row: 0, col: d.clickCell.col }, { row: props.rows.length - 1, col: d.clickCell.col })
    }
  }
}

// ---- hover / cursor / 表头 tooltip ----
function onCanvasMouseMove(e: MouseEvent) {
  if (drag) return
  const canvas = canvasRef.value
  if (!canvas) return
  const hit = hitFromEvent(e)
  let cursor = 'default'
  let title = ''
  let nextHover: { row: number; col: number } | null = null
  if (hit.region === 'header' && hit.col >= 0) {
    const w = colWidths.value[hit.col]
    if (hit.xInCol >= w - RESIZE_ZONE || (hit.xInCol < RESIZE_ZONE && hit.col > 0)) cursor = 'col-resize'
    else if (props.sortable) cursor = 'pointer'
    const meta = props.columns[hit.col]
    title = meta?.comment || (meta ? headerTypeText(meta) : '')
  } else if (hit.region === 'cell' && inRowRange(hit.row) && hit.col >= 0) {
    nextHover = { row: hit.row, col: hit.col }
  }
  if (canvas.style.cursor !== cursor) canvas.style.cursor = cursor
  if (canvas.title !== title) canvas.title = title
  if (hover.value?.row !== nextHover?.row || hover.value?.col !== nextHover?.col) {
    hover.value = nextHover
    requestRender()
  }
}

function onMouseLeave() {
  if (hover.value) {
    hover.value = null
    requestRender()
  }
}

// ---- 双击：emit + 列宽 auto-fit ----
function onDblClick(e: MouseEvent) {
  const hit = hitFromEvent(e)
  if (hit.region === 'header' && hit.col >= 0) {
    const w = colWidths.value[hit.col]
    if (hit.xInCol >= w - RESIZE_ZONE || (hit.xInCol < RESIZE_ZONE && hit.col > 0)) {
      autoFitColumn(hit.xInCol < RESIZE_ZONE ? hit.col - 1 : hit.col)
    }
    return
  }
  if (hit.region === 'cell' && inRowRange(hit.row) && hit.col >= 0) {
    emit('cell-dblclick', { row: hit.row, col: hit.col, value: props.rows[hit.row]?.[hit.col] })
  }
}

// 按可视行 + 上下各一屏采样测量（大结果集不全量扫描）
function autoFitColumn(col: number) {
  const meta = props.columns[col]
  if (!meta) return
  const font = `${FONT_SIZE}px ${FONT_FAMILY}`
  const first = Math.max(0, Math.floor(scrollTop.value / props.rowHeight) - 100)
  const last = Math.min(props.rows.length - 1, first + 300)
  let maxW = 0
  for (let r = first; r <= last; r++) {
    const w = measureText(cellText(r, col), font)
    if (w > maxW) maxW = w
  }
  colWidths.value[col] = Math.min(600, Math.max(headerMinWidth(meta), Math.ceil(maxW) + 20))
  requestRender()
}

// ---- 右键：先修选区、定菜单名，再 emit ----
function onContextMenu(e: MouseEvent) {
  if (!props.rows.length || !props.columns.length) return
  const hit = hitFromEvent(e)
  const sel = normSel.value
  const cols = props.columns.length
  const rows = props.rows.length

  // 选区修正：右键落点在选区外 → 按区域重选（VTable rightdown 的等价行为）
  if (hit.region === 'corner') {
    selectAll()
  } else if (hit.region === 'header' && hit.col >= 0) {
    const covered = sel && hit.col >= sel.c0 && hit.col <= sel.c1 && sel.r0 === 0 && sel.r1 === rows - 1
    if (!covered) setSelection({ row: 0, col: hit.col }, { row: rows - 1, col: hit.col })
  } else if (hit.region === 'rownum' && inRowRange(hit.row)) {
    const covered = sel && hit.row >= sel.r0 && hit.row <= sel.r1 && sel.c0 === 0 && sel.c1 === cols - 1
    if (!covered) setSelection({ row: hit.row, col: 0 }, { row: hit.row, col: cols - 1 })
  } else if (hit.region === 'cell' && inRowRange(hit.row) && hit.col >= 0) {
    const covered = sel && hit.row >= sel.r0 && hit.row <= sel.r1 && hit.col >= sel.c0 && hit.col <= sel.c1
    if (!covered) setSelection({ row: hit.row, col: hit.col })
  }

  // 菜单名判定：
  //   - props.contextMenuName 非空 → 直接采用（如 catdb-tables-overview）。
  //   - 否则：有 PK 且选中列不含 PK 列 → catdb-grid-cell-edit（含 Set to NULL），
  //     否则 catdb-grid-cell（仅复制项）。
  let menuName: string
  if (props.contextMenuName) {
    menuName = props.contextMenuName
  } else {
    let showSetNull = props.pkColumns.length > 0
    const s = normSel.value
    if (showSetNull && s) {
      for (let c = s.c0; c <= s.c1 && showSetNull; c++) {
        if (props.pkColumns.includes(props.columns[c]?.name)) showSetNull = false
      }
    }
    menuName = showSetNull ? 'catdb-grid-cell-edit' : 'catdb-grid-cell'
  }
  wrapRef.value?.style.setProperty('--custom-contextmenu', menuName)

  const bodyRow = Math.max(0, Math.min(rows - 1, hit.row))
  const bodyCol = Math.max(0, hit.col)
  emit('cell-context-menu', {
    row: bodyRow,
    col: bodyCol,
    x: e.pageX,
    y: e.pageY,
    value: props.rows[bodyRow]?.[bodyCol],
  })
}

// ---- 排序 ----
function sortCycle(col: number) {
  const cur = props.sortRemote
    ? props.sortState && props.sortState.field === col
      ? props.sortState.order
      : null
    : clientSort.value?.col === col
      ? clientSort.value.order
      : null
  const next: 'asc' | 'desc' | null = cur === null ? 'asc' : cur === 'asc' ? 'desc' : null

  if (props.sortRemote) {
    emit('sort-change', next ? { field: col, order: next } : null)
    return
  }
  clientSort.value = next ? { col, order: next } : null
  applyClientSort()
}

function toNum(v: any): number {
  if (typeof v === 'number') return v
  if (v && typeof v === 'object' && v.__type__ === 'bigint') return Number(v.value)
  return Number(v)
}

function makeComparator(col: number, order: 'asc' | 'desc') {
  const lt = props.columns[col]?.logicalType
  const numeric =
    lt === LogicalType.TypeInt ||
    lt === LogicalType.TypeBigInt ||
    lt === LogicalType.TypeFloat ||
    lt === LogicalType.TypeDecimal
  const dir = order === 'asc' ? 1 : -1
  return (a: any[], b: any[]): number => {
    const va = a?.[col]
    const vb = b?.[col]
    if (va == null && vb == null) return 0
    if (va == null) return 1 // NULL 恒排末尾
    if (vb == null) return -1
    let cmp: number
    if (numeric) {
      const na = toNum(va)
      const nb = toNum(vb)
      cmp = na < nb ? -1 : na > nb ? 1 : 0
    } else {
      cmp = renderCellValue(va).localeCompare(renderCellValue(vb))
    }
    return dir * cmp
  }
}

// 客户端排序：原地稳定排序 props.rows；取消排序时从快照恢复原序。
function applyClientSort() {
  const rows = props.rows
  const s = clientSort.value
  if (!s) {
    if (originalOrder) {
      rows.splice(0, rows.length, ...originalOrder)
      originalOrder = null
    }
  } else {
    if (!originalOrder) originalOrder = rows.slice()
    rows.sort(makeComparator(s.col, s.order))
  }
  requestRender()
}

// ---- 编辑 ----
function startEdit(row: number, col: number) {
  if (!props.editable || props.fetching) return
  if (props.deletedRows.has(row)) return
  const meta = props.columns[col]
  const kind = pickEditorKind(meta)
  if (!kind) return
  const raw = props.rows[row]?.[col]
  editOriginal = raw
  if (kind === 'datetime' || kind === 'time' || kind === 'date') {
    editValue.value = toInputValue(kind, raw)
  } else {
    editValue.value = raw == null ? '' : renderCellValue(raw)
  }
  editing.value = { row, col, kind }
  requestRender()
  nextTick(() => {
    const el = editorEl.value
    if (!el) return
    el.focus()
    if (el instanceof HTMLInputElement && (kind === 'input')) el.select()
  })
}

function commitEdit(): boolean {
  const ed = editing.value
  if (!ed || commitGuard) return false
  commitGuard = true
  const { row, col, kind } = ed
  const meta = props.columns[col]
  let v: string = editValue.value
  if (kind === 'datetime' || kind === 'time') v = fromInputValue(kind, v)
  editing.value = null
  commitGuard = false
  requestRender()

  // null 保持：原值为 NULL 且用户未输入 → 视为无变化
  if (editOriginal == null && v === '') return false
  // 值未变 → 不发射
  if (editOriginal != null && v === renderCellValue(editOriginal)) return false

  const oldValue = editOriginal
  if (props.rows[row]) props.rows[row][col] = v
  emit('edit-commit', { row, col, oldValue, newValue: v, column: meta })
  return true
}

function cancelEdit() {
  editing.value = null
  requestRender()
  wrapRef.value?.focus()
}

function onEditorBlur() {
  commitEdit()
}

function onEditorKeydown(e: KeyboardEvent) {
  e.stopPropagation()
  if (e.isComposing) return // IME 组字中的按键不触发提交/取消
  if (e.key === 'Escape') {
    e.preventDefault()
    cancelEdit()
    return
  }
  if (e.key === 'Tab') {
    e.preventDefault()
    const ed = editing.value
    commitEdit()
    if (ed) moveTabFrom(ed.row, ed.col, e.shiftKey)
    return
  }
  if (e.key === 'Enter') {
    const isTextarea = editing.value?.kind === 'textarea'
    if (isTextarea && e.shiftKey) return // Shift+Enter 换行
    e.preventDefault()
    commitEdit()
    wrapRef.value?.focus()
  }
}

const editorStyle = computed(() => {
  const ed = editing.value
  if (!ed) return {}
  const left = ROWNUM_W + (offsets.value[ed.col] ?? 0)
  const top = headerH.value + ed.row * props.rowHeight
  const width = colWidths.value[ed.col] ?? props.defaultColumnWidth
  const height = ed.kind === 'textarea' ? Math.max(props.rowHeight * 4, 96) : props.rowHeight
  return {
    left: `${left}px`,
    top: `${top}px`,
    width: `${width}px`,
    height: `${height}px`,
  }
})

const editorInputType = computed(() => {
  switch (editing.value?.kind) {
    case 'date': return 'date'
    case 'time': return 'time'
    case 'datetime': return 'datetime-local'
    default: return 'text'
  }
})

// ---- 键盘 ----
function activeCell(): { row: number; col: number } | null {
  return selHead.value
}

function moveTabFrom(row: number, col: number, shift: boolean) {
  if (!props.editable) return
  const maxCol = props.columns.length - 1
  const maxRow = props.rows.length - 1
  let r = row
  let c = col
  if (shift) {
    if (c > 0) c--
    else if (r > 0) { r--; c = maxCol }
    else return
  } else {
    if (c < maxCol) c++
    else if (r < maxRow) { r++; c = 0 }
    else return
  }
  setSelection({ row: r, col: c })
  scrollCellIntoView(r, c)
  if (pickEditorKind(props.columns[c])) startEdit(r, c)
}

function onKeydown(e: KeyboardEvent) {
  if (editing.value) return
  if (e.key === 'Escape') {
    setSelection(null)
    return
  }
  if ((e.metaKey || e.ctrlKey) && (e.key === 'a' || e.key === 'A')) {
    e.preventDefault()
    selectAll()
    return
  }
  if (e.key === 'Tab') {
    if (!props.editable) return
    e.preventDefault()
    const cur = activeCell()
    if (cur) moveTabFrom(cur.row, cur.col, e.shiftKey)
    return
  }
  if (e.key === 'Enter') {
    const cur = activeCell()
    if (cur && props.editable) {
      e.preventDefault()
      startEdit(cur.row, cur.col)
    }
    return
  }
  const arrows: Record<string, [number, number]> = {
    ArrowUp: [-1, 0],
    ArrowDown: [1, 0],
    ArrowLeft: [0, -1],
    ArrowRight: [0, 1],
  }
  const delta = arrows[e.key]
  if (delta && props.rows.length && props.columns.length) {
    e.preventDefault()
    const base = selHead.value ?? { row: 0, col: 0 }
    const r = Math.max(0, Math.min(props.rows.length - 1, base.row + delta[0]))
    const c = Math.max(0, Math.min(props.columns.length - 1, base.col + delta[1]))
    if (e.shiftKey && selAnchor.value) {
      selHead.value = { row: r, col: c }
      emitSelection()
      requestRender()
    } else {
      setSelection({ row: r, col: c })
    }
    scrollCellIntoView(r, c)
  }
}

// ---- 粘贴 TSV（Ctrl+V / Cmd+V）：DataGrip 语义 ----
// 单值填满整个选区（整行/整列），多值按块平铺/展开。分布逻辑在纯函数 planPaste。
function onPaste(e: ClipboardEvent) {
  if (!props.editable || editing.value) return
  e.preventDefault()
  const text = e.clipboardData?.getData('text/plain')
  if (!text) return
  const s = normSel.value
  if (!s) return

  const pastedGrid = parseTSV(text)
  const selBounds = { row0: s.r0, row1: s.r1, col0: s.c0, col1: s.c1 }

  for (const { row, col, value } of planPaste(pastedGrid, selBounds)) {
    if (row < 0 || col < 0 || row >= props.rows.length || col >= props.columns.length) continue
    // 标记删除的行在保存前不可编辑
    if (props.deletedRows.has(row)) continue
    const colMeta = props.columns[col]
    // 跳过不可编辑列
    if (!pickEditorKind(colMeta)) continue
    const oldValue = props.rows[row]?.[col]
    if (value === oldValue) continue
    props.rows[row][col] = value
    emit('edit-commit', { row, col, oldValue, newValue: value, column: colMeta })
  }
  requestRender()
}

// ---- 列宽初始化 / props 监听 ----
function resetColumnWidths() {
  colWidths.value = props.columns.map((c) => Math.max(props.defaultColumnWidth, headerMinWidth(c)))
}

// 列「签名」不变（同表刷新重建 columns 数组）时保留列宽/选区/排序，
// 等价 VTable 的 keep-column-width-change；签名变了才整体重置。
let columnsSig = ''
function columnsSignature(): string {
  return props.columns.map((c) => `${c.name} ${c.logicalType}`).join('')
}

watch(
  () => props.columns,
  () => {
    const sig = columnsSignature()
    if (sig === columnsSig && colWidths.value.length === props.columns.length) {
      requestRender()
      return
    }
    columnsSig = sig
    resetColumnWidths()
    clientSort.value = null
    originalOrder = null
    selAnchor.value = null
    selHead.value = null
    editing.value = null
    hover.value = null
    loadMoreArmed = true
    const el = scrollerRef.value
    if (el) {
      el.scrollTop = 0
      el.scrollLeft = 0
    }
    requestRender()
  },
  { immediate: true },
)

// rows 数组身份变化（computed 重算 / 新查询 / 追加批次产生新数组）
watch(
  () => props.rows,
  () => {
    loadMoreArmed = true
    // 客户端排序保持生效：新数组重新快照原序并排序
    if (!props.sortRemote && clientSort.value) {
      originalOrder = props.rows.slice()
      props.rows.sort(makeComparator(clientSort.value.col, clientSort.value.order))
    } else {
      originalOrder = null
    }
    // 选区/编辑态钳到新边界
    if (editing.value && editing.value.row >= props.rows.length) editing.value = null
    if (selHead.value && (selHead.value.row >= props.rows.length || !props.rows.length)) {
      selAnchor.value = null
      selHead.value = null
      emitSelection()
    }
    requestRender()
  },
)

// 渲染依赖的其余 props / 主题变化 → 重绘。rows.length 覆盖流式追加
// （同一数组 in-place push，身份不变）的场景。
watch(
  [
    () => props.rows.length,
    () => props.dirtyCells,
    () => props.deletedRows,
    () => props.dirtyRows,
    () => props.sortState,
    () => props.rowHeight,
    gridTheme,
  ],
  () => requestRender(),
  { deep: false },
)

// ---- 对外方法 ----
function scrollToBottom() {
  const el = scrollerRef.value
  if (!el) return
  el.scrollTop = el.scrollHeight
}

// 横向滚动并选中指定数据列首个单元格（body 列下标）。只动水平位置，
// 不打断用户当前的垂直浏览位置。
function scrollToColumn(bodyCol: number) {
  if (bodyCol < 0 || bodyCol >= props.columns.length) return
  const el = scrollerRef.value
  if (el) {
    const cellLeft = offsets.value[bodyCol] ?? 0
    const cellRight = cellLeft + (colWidths.value[bodyCol] ?? 0)
    const viewLeft = el.scrollLeft
    const viewRight = viewLeft + el.clientWidth - ROWNUM_W
    if (cellLeft < viewLeft) el.scrollLeft = cellLeft
    else if (cellRight > viewRight) el.scrollLeft = cellRight - (el.clientWidth - ROWNUM_W)
  }
  if (props.rows.length) setSelection({ row: 0, col: bodyCol })
}

function resize() {
  syncViewport()
  requestRender()
}

defineExpose({ scrollToBottom, scrollToColumn, resize })

// ---- 视口尺寸 ----
function syncViewport() {
  const el = scrollerRef.value
  if (!el) return
  viewW.value = el.clientWidth
  viewH.value = el.clientHeight
}

let ro: ResizeObserver | null = null
onMounted(() => {
  syncViewport()
  requestRender()
  if (typeof ResizeObserver !== 'undefined' && scrollerRef.value) {
    ro = new ResizeObserver(() => {
      syncViewport()
      requestRender()
    })
    ro.observe(scrollerRef.value)
  }
})

onBeforeUnmount(() => {
  ro?.disconnect()
  ro = null
  window.removeEventListener('mousemove', onWindowDragMove)
  window.removeEventListener('mouseup', onWindowDragUp)
  if (autoScrollRaf) cancelAnimationFrame(autoScrollRaf)
})
</script>

<template>
  <!-- --custom-contextmenu 触发 Wails 原生上下文菜单（wailsbridge/contextmenu.go 中注册）。
       默认 catdb-grid-cell；contextMenuName prop 非空时改用该名字（如 catdb-tables-overview）。 -->
  <div
    ref="wrapRef"
    class="datagrid-wrap"
    :style="{ '--custom-contextmenu': props.contextMenuName || 'catdb-grid-cell' }"
    tabindex="0"
    @keydown="onKeydown"
    @paste="onPaste"
  >
    <div ref="scrollerRef" class="dg-scroller" @scroll="onScroll">
      <div class="dg-spacer" :style="{ width: totalWidth + 'px', height: totalHeight + 'px' }">
        <canvas
          ref="canvasRef"
          class="dg-canvas"
          :style="{ width: viewW + 'px', height: viewH + 'px' }"
          @mousedown="onMouseDown"
          @mousemove="onCanvasMouseMove"
          @mouseleave="onMouseLeave"
          @dblclick="onDblClick"
          @contextmenu="onContextMenu"
        />
        <div v-if="editing" class="dg-editor" :style="editorStyle" @mousedown.stop>
          <textarea
            v-if="editing.kind === 'textarea'"
            ref="editorEl"
            v-model="editValue"
            spellcheck="false"
            @keydown="onEditorKeydown"
            @blur="onEditorBlur"
          />
          <input
            v-else
            ref="editorEl"
            v-model="editValue"
            :type="editorInputType"
            :step="editing.kind === 'datetime' || editing.kind === 'time' ? 1 : undefined"
            spellcheck="false"
            @keydown="onEditorKeydown"
            @blur="onEditorBlur"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.datagrid-wrap {
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  position: relative;
  overflow: hidden;
  border-radius: 3px;
  background: var(--app-content-bg);
  outline: none;
}

.dg-scroller {
  width: 100%;
  height: 100%;
  overflow: auto;
  overscroll-behavior: none;
}

.dg-spacer {
  position: relative;
}

.dg-canvas {
  position: sticky;
  top: 0;
  left: 0;
  z-index: 1;
  display: block;
}

.dg-editor {
  position: absolute;
  z-index: 2;
}

.dg-editor input,
.dg-editor textarea {
  width: 100%;
  height: 100%;
  box-sizing: border-box;
  padding: 2px 6px;
  font: 12px -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Hiragino Sans GB',
    'Microsoft YaHei', sans-serif;
  color: var(--n-text-color, inherit);
  background: v-bind('gridTheme.surface');
  border: 2px solid v-bind('gridTheme.selectionBorder');
  border-radius: 0;
  outline: none;
  resize: none;
}
</style>
