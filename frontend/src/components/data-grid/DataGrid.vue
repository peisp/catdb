<script setup lang="ts">
// DataGrid —— 整个项目唯一的 VTable 进出口（防腐层，CLAUDE.md 规则 1）。
//
// 设计要点：
//   - IPC 形状不动：rows 保持 any[][]，列元数据单传一次（CLAUDE.md 规则 5）
//   - 利用 VTable 的 FieldDef = string | number 特性，直接用列下标作为 field
//     —— 无需把 any[][] 转 records，省内存
//   - NULL / BLOB / JSON / bigint 在 fieldFormat 里统一渲染
//   - 主题从 Naive 的 useThemeVars() 派生，light/dark 切换走同一通道
//   - 选区翻译成现有 SelectionRange 形状，formatters（useTableSelection）100% 复用
//   - 编辑：VTable 的 InputEditor/TextAreaEditor/DateInputEditor 按列类型分发
//   - 排序：sortRemote=true 时不启用 VTable 客户端排序（用 () => 0 替代），
//     排序交互通过 sort_click 事件发射给父组件处理（服务端 ORDER BY）；
//     sortRemote=false 时 VTable 原生处理客户端排序。
//
// VTable 的 row 索引把 header 算在内（row=0 是表头），所以对外发出的 row
// 一律减去 columnHeaderLevelCount 得到 body 行号。
import { computed, ref, shallowRef, watch } from 'vue'
import { ListTable, register } from '@visactor/vue-vtable'
import { InputEditor, TextAreaEditor, DateInputEditor } from '@visactor/vtable-editors'
import { useThemeVars } from 'naive-ui'
import { ColumnMeta, LogicalType } from '../../../bindings/catdb/internal/dbdriver/models'
import type { SelectionRange } from '../../composables/useTableSelection'

/** Sort state for indicator sync (server-side sort). field = column index. */
export interface SortState {
  field: number
  order: 'asc' | 'desc'
}

// ---- editor 注册：模块级单次 ----
let editorsRegistered = false
function ensureEditorsRegistered() {
  if (editorsRegistered) return
  // string 单行编辑
  register.editor('catdb-input', new InputEditor({}))
  // 长文本 / JSON
  register.editor('catdb-textarea', new TextAreaEditor({}))
  // 日期 / 时间
  register.editor('catdb-date', new DateInputEditor({}))
  editorsRegistered = true
}
ensureEditorsRegistered()

interface Props {
  columns: ColumnMeta[]
  rows: any[][]
  /** 是否允许双击进入编辑态（read-only 模式传 false） */
  editable?: boolean
  /** PK 列名 —— editable 为 true 时这些列仍然只读，避免误改行身份 */
  pkColumns?: string[]
  /** 提示性 fetching，用于禁用编辑触发等 */
  fetching?: boolean
  /** 行高，默认 24px 桌面风格 */
  rowHeight?: number
  /** 单列默认宽度 */
  defaultColumnWidth?: number
  /** 是否启用手动列排序; 默认 true */
  sortable?: boolean
  /** true=服务端排序（发射 sort-change，VTable 用无操作排序函数禁掉客户端重拍），
   *  false=VTable 原生处理客户端排序（默认）。 */
  sortRemote?: boolean
  /** 当前服务端排序状态，用于同步 VTable 排序指示器。sortRemote=true 时使用。 */
  sortState?: SortState | null
}

const props = withDefaults(defineProps<Props>(), {
  editable: false,
  pkColumns: () => [],
  fetching: false,
  rowHeight: 24,
  defaultColumnWidth: 160,
  sortable: true,
  sortRemote: false,
  sortState: null,
})

const emit = defineEmits<{
  /** 编辑提交：VTable 的 row/col 已转换为 body 坐标系（row 从 0 起） */
  (e: 'edit-commit', p: { row: number; col: number; oldValue: any; newValue: any; column: ColumnMeta }): void
  /** 右键单元格：x/y 为屏幕坐标（pageX/pageY） */
  (e: 'cell-context-menu', p: { row: number; col: number; x: number; y: number; value: any }): void
  /** 选区变化：range 为 null 表示清空 */
  (e: 'selection-change', p: { range: SelectionRange | null }): void
  /** 滚到底部，触发分页/流式追加 */
  (e: 'load-more'): void
  /** 排序变化（sortRemote=true 时发射）：field 为列下标，order 为 'asc'/'desc'；
   *  null 表示清除排序 */
  (e: 'sort-change', p: { field: number; order: 'asc' | 'desc' } | null): void
  /** 双击单元格：row 为 body 行号（0 起），col 为 body 列号 */
  (e: 'cell-dblclick', p: { row: number; col: number; value: any }): void
}>()

const themeVars = useThemeVars()
const vTableInstance = shallowRef<any>(null)
const gridWrapRef = ref<HTMLElement | null>(null)

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

// ---- 列类型 → 编辑器名 ----
function pickEditor(col: ColumnMeta): string | undefined {
  if (col.isAutoIncrement) return undefined
  if (col.isPrimaryKey) return undefined
  if (props.pkColumns.includes(col.name)) return undefined
  switch (col.logicalType) {
    case LogicalType.TypeDate:
    case LogicalType.TypeTime:
    case LogicalType.TypeDateTime:
    case LogicalType.TypeTimestamp:
      return 'catdb-date'
    case LogicalType.TypeJSON:
    case LogicalType.TypeText:
      return 'catdb-textarea'
    case LogicalType.TypeBytes:
      // 二进制不在表格内编辑
      return undefined
    default:
      return 'catdb-input'
  }
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

// ---- VTable 主题：从 Naive themeVars 派生 ----
const tableTheme = computed(() => {
  const vars = themeVars.value
  return {
    underlayBackgroundColor: vars.cardColor,
    defaultStyle: {
      borderColor: vars.dividerColor,
      borderLineWidth: 1,
      fontSize: 12,
      fontFamily:
        '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif',
      color: vars.textColor1,
      bgColor: vars.cardColor,
      hover: {
        cellBgColor: vars.hoverColor,
      },
    },
    headerStyle: {
      bgColor: vars.tableHeaderColor,
      color: vars.textColor1,
      borderColor: vars.borderColor,
      fontSize: 12,
      fontWeight: '500',
      hover: { cellBgColor: vars.hoverColor },
    },
    bodyStyle: {
      bgColor: vars.cardColor,
      color: vars.textColor1,
      borderColor: vars.dividerColor,
      fontSize: 12,
      hover: { cellBgColor: vars.hoverColor },
    },
    frameStyle: {
      borderColor: vars.borderColor,
      borderLineWidth: 1,
      cornerRadius: 3,
    },
    columnResize: {
      lineColor: vars.primaryColorHover,
      bgColor: vars.primaryColorSuppl,
      width: 3,
    },
    selectionStyle: {
      cellBgColor: 'rgba(32,128,240,0.14)',
      cellBorderColor: vars.primaryColor,
      cellBorderLineWidth: 1,
    },
    rowSeriesNumberStyle: {
      bgColor: vars.tableHeaderColor,
      color: vars.textColor3,
      fontSize: 11,
      textAlign: 'right',
    },
  } as any
})

// ---- VTable options ----
const tableOptions = computed<any>(() => {
  const sortFn = props.sortRemote ? () => 0 : true
  const cols = props.columns.map((c, idx) => ({
    field: idx,
    title: c.name,
    width: props.defaultColumnWidth,
    minWidth: 60,
    editor: props.editable ? pickEditor(c) : undefined,
    style: (args: any) => {
      const align = pickAlign(c)
      // dataValue 是 fieldFormat 前的原始值，value 是格式化后的
      if (args?.dataValue == null) return { textAlign: align, color: '#aaa', fontStyle: 'italic' }
      return { textAlign: align }
    },

    sort: props.sortable ? sortFn : undefined,
    description: c.comment || c.nativeType,
    fieldFormat: (record: any) => {
      const v = record?.[idx]
      if (v == null) return 'NULL'
      return renderCellValue(v)
    },
  }))
  return {
    columns: cols,
    defaultRowHeight: props.rowHeight,
    defaultHeaderRowHeight: 28,
    defaultColWidth: props.defaultColumnWidth,
    rowSeriesNumber: {
      title: '#',
      width: 50,
      style: { textAlign: 'right', color: '#aaa', fontSize: 10 },
    },
    select: {
      disableSelect: false,
      disableHeaderSelect: false,
    },
    hover: {
      highlightMode: 'cell',
    },
    editCellTrigger: props.editable ? 'doubleclick' : undefined,
    keyboardOptions: {
      // 让 parent 控制 copy 行为（保留 useTableSelection 的 TSV/INSERT/UPDATE）
      copySelected: false,
      pasteValueToCell: false,
      moveEditCellOnArrowKeys: false,
      selectAllOnCtrlA: true,
    },
    menu: { defaultHeaderMenuItems: [] },
    autoFillWidth: false,
    autoWrapText: false,
    theme: tableTheme.value,
  }
})

// ---- VTable 事件 → 对外发射 ----
//
// 早期版本走 `:on-xxx` prop 路线踩了三个坑：
//   1. VTable 的 SELECTED_CELL payload 是 `{ ranges, col, row }`，不是
//      cellRange/range —— 之前的代码永远落到 fallback；
//   2. 当 rowSeriesNumber 打开时，事件中的 col 还包含 leftRowSeriesNumberCount
//      偏移（通常是 1），不扣会复制到错位的列；
//   3. `:on-contextmenu-cell` 这种 kebab 命名跟 Vue 的 emit 名 `onContextMenuCell`
//      存在大小写映射歧义，listener 经常根本没接上。
//
// 改成在 onReady 里直接 instance.on(eventName, handler) 订阅原生事件 —— 既能
// 拿到 SELECTED_CHANGED（右键自动选中走的是它，vue-vtable wrapper 没暴露），
// 又彻底绕开 prop / emit 名映射问题。

function offsets(): { col: number; row: number } {
  const inst = vTableInstance.value
  return {
    col: (inst?.rowHeaderLevelCount ?? 0) + (inst?.leftRowSeriesNumberCount ?? 0),
    row: inst?.columnHeaderLevelCount ?? 1,
  }
}

function toBody(rawCol: number, rawRow: number): { col: number; row: number } | null {
  const off = offsets()
  const col = rawCol - off.col
  const row = rawRow - off.row
  if (col < 0 || row < 0) return null
  return { col, row }
}

function rangeFromArgs(args: any): SelectionRange | null {
  const ranges: any[] = Array.isArray(args?.ranges) ? args.ranges : []
  if (!ranges.length) return null
  // 用最近一次的 range（用户最后拖出来的那块）
  const r = ranges[ranges.length - 1]
  if (!r?.start || !r?.end) return null

  const off = offsets()
  const sRow = r.start.row - off.row
  const eRow = r.end.row - off.row
  const sCol = r.start.col - off.col
  const eCol = r.end.col - off.col
  const colCount = props.columns.length
  const rowCount = props.rows.length
  if (colCount === 0 || rowCount === 0) return null

  // 序号列：任意端在行表头 / 序号区 → 整行选（所有数据列）
  // 列表头：任意端在列表头 → 整列选（所有数据行）
  // 两者交叉（顶左角的 # 表头）→ 全选
  const inSeriesNumber = sCol < 0 || eCol < 0
  const inColHeader = sRow < 0 || eRow < 0

  let startRow: number
  let endRow: number
  if (inColHeader) {
    startRow = 0
    endRow = rowCount - 1
  } else {
    startRow = Math.min(sRow, eRow)
    endRow = Math.max(sRow, eRow)
  }

  let startCol: number
  let endCol: number
  if (inSeriesNumber) {
    startCol = 0
    endCol = colCount - 1
  } else {
    startCol = Math.min(sCol, eCol)
    endCol = Math.max(sCol, eCol)
  }

  return { startRow, startCol, endRow, endCol }
}

function onReady(instance: any) {
  vTableInstance.value = instance

  // 选区变化：拖拽中 + 单击 + 右键自动选中都走 SELECTED_CHANGED；mouseup 之后
  // 还会再补一次 SELECTED_CELL。两个都接，让 parent 拿到最新状态。
  instance.on('selected_changed', (args: any) => {
    emit('selection-change', { range: rangeFromArgs(args) })
  })
  instance.on('selected_cell', (args: any) => {
    emit('selection-change', { range: rangeFromArgs(args) })
  })
  instance.on('selected_clear', () => {
    emit('selection-change', { range: null })
  })

  // 右键单元格：VTable 已经在 rightdown 里把选区调整好了；这里把屏幕坐标 +
  // body 坐标透传出去，parent 据此推送 setActiveGridContext。
  // 边角情况：
  //   - 序号列右键 → col 归零（数据列首列）
  //   - 列表头右键 → row 归零（数据行首行）
  //   - 顶左角 → (0, 0)
  // parent 的 isSelected 检查会命中已被 selected_changed 扩成整行/整列/全选
  // 的选区，不会触发 fallback 单元格选中。
  instance.on('contextmenu_cell', (args: any) => {
    if (args?.col == null || args?.row == null) return
    const off = offsets()
    const bodyRow = Math.max(0, args.row - off.row)
    const bodyCol = Math.max(0, args.col - off.col)
    if (!props.rows.length || !props.columns.length) return

    // Decide which native context menu to show:
    //   catdb-grid-cell-edit (includes "Set to NULL") when table has a PK
    //   and NO selected column is a PK column.
    //   Otherwise catdb-grid-cell (copy items only).
    let showSetNull = props.pkColumns.length > 0
    if (showSetNull) {
      const ranges = instance.getSelectedCellRanges?.() ?? []
      for (const r of ranges) {
        const sCol = Math.max(0, r.start.col - off.col)
        const eCol = Math.max(0, r.end.col - off.col)
        for (let c = sCol; c <= eCol && showSetNull; c++) {
          if (props.pkColumns.includes(props.columns[c]?.name)) {
            showSetNull = false
          }
        }
      }
    }
    gridWrapRef.value?.style.setProperty(
      '--custom-contextmenu',
      showSetNull ? 'catdb-grid-cell-edit' : 'catdb-grid-cell',
    )

    const ev: MouseEvent | undefined = args.event ?? args.federatedEvent?.nativeEvent
    emit('cell-context-menu', {
      row: bodyRow,
      col: bodyCol,
      x: ev?.pageX ?? 0,
      y: ev?.pageY ?? 0,
      value: props.rows[bodyRow]?.[bodyCol],
    })
  })

  // 双击单元格：发射 body 坐标 + 原始值，parent 可用于导航等用途。
  instance.on('dblclick_cell', (args: any) => {
    if (args?.col == null || args?.row == null) return
    const body = toBody(args.col, args.row)
    if (!body) return
    emit('cell-dblclick', {
      row: body.row,
      col: body.col,
      value: props.rows[body.row]?.[body.col],
    })
  })

  // 单元格编辑提交：VTable 内部已乐观更新 record；parent 决定是否回滚。
  instance.on('change_cell_value', (args: any) => {
    if (args?.col == null || args?.row == null) return
    const body = toBody(args.col, args.row)
    if (!body) return
    const meta = props.columns[body.col]
    if (!meta) return
    emit('edit-commit', {
      row: body.row,
      col: body.col,
      oldValue: args.originValue,
      newValue: args.changedValue,
      column: meta,
    })
  })

  // 滚到底部：触发分页/流式追加
  instance.on('scroll_vertical_end', () => emit('load-more'))

  // 排序点击：sortRemote=true 时发射给父组件做服务端排序
  instance.on('sort_click', (args: any) => {
    if (!props.sortRemote) return
    const field = args?.field
    const order = args?.order
    if (field == null || order == null) return
    if (order === 'normal' || order === 'NORMAL') {
      emit('sort-change', null)
    } else {
      emit('sort-change', { field: Number(field), order: order.toLowerCase() as 'asc' | 'desc' })
    }
  })
}

// 监听 rows 变化时滚到顶（避免新列保留旧滚动位置）
watch(
  () => props.columns,
  () => {
    const inst = vTableInstance.value
    if (inst?.scrollTo) {
      try { inst.scrollTo({ scrollTop: 0, scrollLeft: 0 }) } catch { /* ignore */ }
    }
  },
)

// 同步服务端排序状态到 VTable 指示器
watch(
  () => props.sortState,
  (state) => {
    const inst = vTableInstance.value
    if (!inst?.updateSortState) return
    if (!props.sortRemote) return
    if (!state) {
      inst.updateSortState(null, false)
    } else {
      inst.updateSortState({ field: state.field, order: state.order }, false)
    }
  },
  { deep: true },
)

// 服务端排序时，rows 变化会清掉 VTable 的排序指示器，需要等重渲染完成后恢复。
watch(
  () => props.rows,
  () => {
    if (!props.sortRemote || !props.sortState) return
    const inst = vTableInstance.value
    if (!inst?.updateSortState) return
    requestAnimationFrame(() => {
      inst.updateSortState({ field: props.sortState!.field, order: props.sortState!.order }, false)
    })
  },
  { deep: false },
)
</script>

<template>
  <!-- --custom-contextmenu: catdb-grid-cell 触发 Wails 原生上下文菜单
       （wailsbridge/contextmenu.go 中注册）。CSS 变量在画布子节点也生效。 -->
  <div ref="gridWrapRef" class="datagrid-wrap" style="--custom-contextmenu: catdb-grid-cell">
    <ListTable
      :options="tableOptions"
      :records="rows"
      width="100%"
      height="100%"
      :keep-column-width-change="true"
      :on-ready="onReady"
    />
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
  border: 1px solid var(--n-border-color);
  border-radius: 3px;
  background: var(--n-card-color, transparent);
}
</style>
