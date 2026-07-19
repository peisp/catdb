// canvasGridRenderer — DataGrid 的纯 canvas 渲染器 + 几何/命中测试助手。
// 无 Vue 依赖、无状态（除文本截断缓存）：每帧按滚动位置全量重绘可视区。
// 主题色由 DataGrid.vue 从 Naive themeVars 派生后传入。
//
// 绘制顺序（后画的盖住先画的）：
//   1. body 单元格（随 scrollLeft/scrollTop 平移）
//   2. 行号列（水平钉在 x=0）
//   3. 表头行（垂直钉在 y=0）
//   4. 左上角 # 单元格
//   5. 选区边框

export type CellAlign = 'left' | 'right' | 'center'

export interface GridColumn {
  title: string
  align: CellAlign
  /** 表头第二行（字段类型），存在时表头为两行布局 */
  subtitle?: string
  /** 主键列 —— 表头字段名前画 🔑 标记（与对象树的主键标记一致） */
  pk?: boolean
}

export interface GridTheme {
  surface: string
  headerBg: string
  text: string
  textMuted: string
  border: string
  divider: string
  hoverFill: string
  selectionFill: string
  selectionBorder: string
  deletedFill: string
  deletedText: string
  dirtyText: string
  rowNumText: string
  sortActiveColor: string
  zebraFill: string
}

export interface GridFonts {
  family: string
  size: number
}

/** 归一化选区（含端点，body 坐标系）。 */
export interface NormRange {
  r0: number
  r1: number
  c0: number
  c1: number
}

export interface DrawGridOptions {
  canvas: HTMLCanvasElement
  width: number
  height: number
  theme: GridTheme
  fonts: GridFonts
  rowNumberWidth: number
  headerHeight: number
  rowHeight: number
  colWidths: number[]
  colOffsets: number[]
  scrollTop: number
  scrollLeft: number
  rowCount: number
  columns: GridColumn[]
  cellText: (row: number, col: number) => string
  cellIsNull: (row: number, col: number) => boolean
  isDirtyCell: (row: number, col: number) => boolean
  isDeletedRow: (row: number) => boolean
  isDirtyRow: (row: number) => boolean
  selection: NormRange | null
  hover: { row: number; col: number } | null
  editing: { row: number; col: number } | null
  sortable: boolean
  sortState: { col: number; order: 'asc' | 'desc' } | null
}

export const SORT_ZONE_WIDTH = 28
export const RESIZE_ZONE = 4
const CELL_PAD = 8

// ---- 文本截断缓存（同 font+text+width 复用测量结果） ----
const fitCacheMax = 8000
const fitCache = new Map<string, string>()

export function clearFitTextCache(): void {
  fitCache.clear()
}

function fitText(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (maxWidth <= 0) return ''
  const key = `${ctx.font}|${maxWidth}|${text}`
  const hit = fitCache.get(key)
  if (hit !== undefined) return hit
  let out: string
  if (ctx.measureText(text).width <= maxWidth) {
    out = text
  } else {
    const ell = '…'
    const ellW = ctx.measureText(ell).width
    let lo = 0
    let hi = text.length
    while (lo < hi) {
      const mid = Math.ceil((lo + hi) / 2)
      if (ctx.measureText(text.slice(0, mid)).width + ellW <= maxWidth) lo = mid
      else hi = mid - 1
    }
    out = text.slice(0, lo) + ell
  }
  if (fitCache.size >= fitCacheMax) fitCache.clear()
  fitCache.set(key, out)
  return out
}

// ---- 几何 ----

export function buildOffsets(widths: number[]): number[] {
  const offsets = new Array<number>(widths.length + 1)
  offsets[0] = 0
  for (let i = 0; i < widths.length; i++) offsets[i + 1] = offsets[i] + widths[i]
  return offsets
}

/** 内容坐标 x（不含行号列、已加 scrollLeft）→ 列下标；越过末列返回 -1。 */
export function colAtX(offsets: number[], x: number): number {
  if (x < 0 || offsets.length < 2 || x >= offsets[offsets.length - 1]) return -1
  let lo = 0
  let hi = offsets.length - 2
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1
    if (offsets[mid] <= x) lo = mid
    else hi = mid - 1
  }
  return lo
}

export type GridRegion = 'corner' | 'header' | 'rownum' | 'cell'

export interface GridHit {
  region: GridRegion
  /** body 行号；header/corner 为 -1 */
  row: number
  /** body 列号；rownum/corner 为 -1；header/cell 越界为 -1 */
  col: number
  /** header/cell 命中时：指针在该列内的 x 偏移 */
  xInCol: number
}

/** viewport 坐标（相对 canvas 左上角）→ 网格区域命中。row 可能 >= rowCount（点在空白处），调用方自行判断。 */
export function hitTest(
  x: number,
  y: number,
  scrollLeft: number,
  scrollTop: number,
  opts: { rowNumberWidth: number; headerHeight: number; rowHeight: number; colOffsets: number[] },
): GridHit {
  const inHeader = y < opts.headerHeight
  const inRowNum = x < opts.rowNumberWidth
  if (inHeader && inRowNum) return { region: 'corner', row: -1, col: -1, xInCol: 0 }
  const row = inHeader ? -1 : Math.floor((y - opts.headerHeight + scrollTop) / opts.rowHeight)
  if (inRowNum) return { region: 'rownum', row, col: -1, xInCol: 0 }
  const contentX = x - opts.rowNumberWidth + scrollLeft
  const col = colAtX(opts.colOffsets, contentX)
  const xInCol = col >= 0 ? contentX - opts.colOffsets[col] : 0
  return { region: inHeader ? 'header' : 'cell', row, col, xInCol }
}

// ---- 绘制 ----

function crisp(v: number, dpr: number): number {
  return Math.round(v * dpr) / dpr + 0.5 / dpr
}

function drawSortIndicator(
  ctx: CanvasRenderingContext2D,
  cx: number,
  cy: number,
  order: 'asc' | 'desc' | 'none',
  color: string,
  mutedColor?: string,
) {
  if (order === 'none') {
    // 灰色双向三角，同激活态大小，上下错开不重叠
    ctx.fillStyle = mutedColor || color
    ctx.beginPath()
    ctx.moveTo(cx - 4, cy - 0.5)
    ctx.lineTo(cx + 4, cy - 0.5)
    ctx.lineTo(cx, cy - 5)
    ctx.closePath()
    ctx.fill()
    ctx.beginPath()
    ctx.moveTo(cx - 4, cy + 0.5)
    ctx.lineTo(cx + 4, cy + 0.5)
    ctx.lineTo(cx, cy + 5)
    ctx.closePath()
    ctx.fill()
    return
  }
  ctx.fillStyle = color
  ctx.beginPath()
  if (order === 'asc') {
    ctx.moveTo(cx - 4, cy + 2.5)
    ctx.lineTo(cx + 4, cy + 2.5)
    ctx.lineTo(cx, cy - 3.5)
  } else {
    ctx.moveTo(cx - 4, cy - 2.5)
    ctx.lineTo(cx + 4, cy - 2.5)
    ctx.lineTo(cx, cy + 3.5)
  }
  ctx.closePath()
  ctx.fill()
}

export function drawGrid(o: DrawGridOptions): void {
  const dpr = Math.max(1, window.devicePixelRatio || 1)
  const pw = Math.max(1, Math.ceil(o.width * dpr))
  const ph = Math.max(1, Math.ceil(o.height * dpr))
  if (o.canvas.width !== pw) o.canvas.width = pw
  if (o.canvas.height !== ph) o.canvas.height = ph
  // CSS 尺寸与位图在同一帧原子更新——由 Vue 绑定异步改 style 会先绘出一帧
  // 「旧位图拉伸到新尺寸」的画面，容器连续 resize 时表现为抖动。
  const cssW = `${o.width}px`
  const cssH = `${o.height}px`
  if (o.canvas.style.width !== cssW) o.canvas.style.width = cssW
  if (o.canvas.style.height !== cssH) o.canvas.style.height = cssH
  const ctx = o.canvas.getContext('2d')
  if (!ctx) return
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)

  const { theme, fonts, rowNumberWidth: rnw, headerHeight: hh, rowHeight: rh } = o
  const normalFont = `${fonts.size}px ${fonts.family}`
  const headerFont = `500 ${fonts.size}px ${fonts.family}`
  const italicFont = `italic ${fonts.size}px ${fonts.family}`
  const rowNumFont = `${Math.max(9, fonts.size - 2)}px ${fonts.family}`

  ctx.fillStyle = theme.surface
  ctx.fillRect(0, 0, o.width, o.height)
  ctx.textBaseline = 'middle'
  ctx.lineWidth = 1

  const firstRow = Math.max(0, Math.floor(o.scrollTop / rh))
  const lastRow = Math.min(o.rowCount - 1, Math.floor((o.scrollTop + o.height - hh) / rh))
  const firstCol = Math.max(0, colAtX(o.colOffsets, o.scrollLeft))
  const colCount = o.columns.length

  const colX = (c: number) => rnw + o.colOffsets[c] - o.scrollLeft
  const rowY = (r: number) => hh + r * rh - o.scrollTop
  const sel = o.selection

  // 1. body 单元格
  for (let r = firstRow; r <= lastRow; r++) {
    const y = rowY(r)
    const deleted = o.isDeletedRow(r)
    for (let c = firstCol; c < colCount; c++) {
      const x = colX(c)
      if (x >= o.width) break
      const w = o.colWidths[c]
      const dirty = o.isDirtyCell(r, c)
      const selected = !!sel && r >= sel.r0 && r <= sel.r1 && c >= sel.c0 && c <= sel.c1

      // 底色（优先级：删除 > 选区 > hover > 斑马纹）
      if (deleted) {
        ctx.fillStyle = theme.deletedFill
        ctx.fillRect(x, y, w, rh)
      } else if (selected) {
        ctx.fillStyle = theme.selectionFill
        ctx.fillRect(x, y, w, rh)
      } else if (o.hover && o.hover.row === r && o.hover.col === c) {
        ctx.fillStyle = theme.hoverFill
        ctx.fillRect(x, y, w, rh)
      } else if (r % 2 === 1) {
        ctx.fillStyle = theme.zebraFill
        ctx.fillRect(x, y, w, rh)
      }

      // 网格线:只画纵向列分隔(行向靠斑马纹区分,DESIGN.md 数据网格规格)
      ctx.strokeStyle = theme.divider
      ctx.beginPath()
      const bx = crisp(x + w - 1, dpr)
      ctx.moveTo(bx, y)
      ctx.lineTo(bx, y + rh)
      ctx.stroke()

      // 文本（编辑中的单元格留白给 DOM 编辑器）
      if (o.editing && o.editing.row === r && o.editing.col === c) continue
      const isNull = o.cellIsNull(r, c)
      const text = o.cellText(r, c)
      if (!text) continue
      ctx.font = isNull ? italicFont : normalFont
      ctx.fillStyle = deleted ? theme.deletedText : isNull ? theme.textMuted : dirty ? theme.dirtyText : theme.text
      const maxW = w - CELL_PAD * 2
      const t = fitText(ctx, text, maxW)
      const align = o.columns[c].align
      if (align === 'right') {
        ctx.textAlign = 'right'
        ctx.fillText(t, x + w - CELL_PAD, y + rh / 2)
      } else if (align === 'center') {
        ctx.textAlign = 'center'
        ctx.fillText(t, x + w / 2, y + rh / 2)
      } else {
        ctx.textAlign = 'left'
        ctx.fillText(t, x + CELL_PAD, y + rh / 2)
      }
      // 删除行文本划线
      if (deleted && t) {
        const tw = Math.min(ctx.measureText(t).width, maxW)
        const lx = align === 'right' ? x + w - CELL_PAD - tw : align === 'center' ? x + (w - tw) / 2 : x + CELL_PAD
        ctx.strokeStyle = theme.deletedText
        ctx.beginPath()
        const ly = crisp(y + rh / 2, dpr)
        ctx.moveTo(lx, ly)
        ctx.lineTo(lx + tw, ly)
        ctx.stroke()
      }
    }
  }

  // 5.（先算好，最后画）选区边框——只围 body 可视部分
  const drawSelectionBorder = () => {
    if (!sel) return
    const x0 = Math.max(colX(sel.c0), rnw)
    const x1 = Math.min(colX(sel.c1) + o.colWidths[sel.c1] - 1, o.width)
    const y0 = Math.max(rowY(sel.r0), hh)
    const y1 = Math.min(rowY(sel.r1) + rh - 1, o.height)
    if (x1 <= x0 || y1 <= y0) return
    ctx.strokeStyle = theme.selectionBorder
    ctx.strokeRect(crisp(x0, dpr), crisp(y0, dpr), x1 - x0, y1 - y0)
  }

  // 2. 行号列（钉左）
  ctx.textAlign = 'right'
  for (let r = firstRow; r <= lastRow; r++) {
    const y = rowY(r)
    ctx.fillStyle = theme.headerBg
    ctx.fillRect(0, y, rnw, rh)
    const deleted = o.isDeletedRow(r)
    const dirtyRow = o.isDirtyRow(r)
    // 未提交脏行:行号槽左缘 accent 竖条(DESIGN.md 数据网格规格)
    if (dirtyRow && !deleted) {
      ctx.fillStyle = theme.dirtyText
      ctx.fillRect(0, y, 2, rh)
    }
    ctx.font = rowNumFont
    ctx.fillStyle = deleted ? theme.deletedText : dirtyRow ? theme.dirtyText : theme.rowNumText
    const label = String(r + 1)
    const tx = rnw - 8
    const ty = y + rh / 2
    ctx.fillText(label, tx, ty)
    if (deleted) {
      const tw = ctx.measureText(label).width
      ctx.strokeStyle = theme.deletedText
      ctx.beginPath()
      const ly = crisp(ty, dpr)
      ctx.moveTo(tx - tw, ly)
      ctx.lineTo(tx, ly)
      ctx.stroke()
    }
  }
  // 行号列右边界
  ctx.strokeStyle = theme.border
  ctx.beginPath()
  const rnb = crisp(rnw - 1, dpr)
  ctx.moveTo(rnb, 0)
  ctx.lineTo(rnb, o.height)
  ctx.stroke()

  // 3. 表头行（钉顶）
  ctx.fillStyle = theme.headerBg
  ctx.fillRect(0, 0, o.width, hh)
  const subtitleFont = `${Math.max(9, fonts.size - 2)}px ${fonts.family}`
  for (let c = firstCol; c < colCount; c++) {
    const x = colX(c)
    if (x >= o.width) break
    const w = o.colWidths[c]
    const sortHere = o.sortState?.col === c
    const reserve = o.sortable ? SORT_ZONE_WIDTH : 0
    const sub = o.columns[c].subtitle
    let textX = x + CELL_PAD
    let maxTextW = w - CELL_PAD * 2 - reserve
    ctx.textAlign = 'left'
    // 主键列：字段名前画 🔑（与对象树的主键标记一致）
    if (o.columns[c].pk) {
      ctx.font = subtitleFont
      const kw = ctx.measureText('🔑').width
      ctx.fillText('🔑', textX, sub ? hh * 0.32 : hh / 2)
      textX += kw + 4
      maxTextW -= kw + 4
    }
    ctx.font = headerFont
    ctx.fillStyle = theme.text
    if (sub) {
      // 两行：字段名 + 类型
      ctx.fillText(fitText(ctx, o.columns[c].title, maxTextW), textX, hh * 0.32)
      ctx.font = subtitleFont
      ctx.fillStyle = theme.textMuted
      ctx.fillText(fitText(ctx, sub, maxTextW), x + CELL_PAD, hh * 0.72)
    } else {
      ctx.fillText(fitText(ctx, o.columns[c].title, maxTextW), textX, hh / 2)
    }
    if (o.sortable) {
      const order = sortHere ? o.sortState!.order : 'none'
      const c = sortHere ? theme.textMuted : theme.divider
      const ac = sortHere ? theme.sortActiveColor : theme.textMuted
      drawSortIndicator(ctx, x + w - SORT_ZONE_WIDTH / 2 - 2, hh / 2, order, ac, c)
    }
    ctx.strokeStyle = theme.border
    ctx.beginPath()
    const bx = crisp(x + w - 1, dpr)
    ctx.moveTo(bx, 0)
    ctx.lineTo(bx, hh)
    ctx.stroke()
  }
  // 表头底边
  ctx.strokeStyle = theme.border
  ctx.beginPath()
  const hb = crisp(hh - 1, dpr)
  ctx.moveTo(0, hb)
  ctx.lineTo(o.width, hb)
  ctx.stroke()

  // 4. 左上角
  ctx.fillStyle = theme.headerBg
  ctx.fillRect(0, 0, rnw, hh)
  ctx.font = rowNumFont
  ctx.fillStyle = theme.rowNumText
  ctx.textAlign = 'right'
  ctx.fillText('#', rnw - 8, hh / 2)
  ctx.strokeStyle = theme.border
  ctx.beginPath()
  ctx.moveTo(rnb, 0)
  ctx.lineTo(rnb, hh)
  ctx.moveTo(0, hb)
  ctx.lineTo(rnw, hb)
  ctx.stroke()

  drawSelectionBorder()
}
