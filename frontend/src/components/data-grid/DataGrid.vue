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
//
// VTable 的 row 索引把 header 算在内（row=0 是表头），所以对外发出的 row
// 一律减去 columnHeaderLevelCount 得到 body 行号。
import { computed, ref, shallowRef, watch } from 'vue'
import { ListTable, register } from '@visactor/vue-vtable'
import { InputEditor, TextAreaEditor, DateInputEditor } from '@visactor/vtable-editors'
import { useThemeVars } from 'naive-ui'
import { ColumnMeta, LogicalType } from '../../../bindings/catdb/internal/dbdriver/models'
import type { SelectionRange } from '../../composables/useTableSelection'

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
}

const props = withDefaults(defineProps<Props>(), {
  editable: false,
  pkColumns: () => [],
  fetching: false,
  rowHeight: 24,
  defaultColumnWidth: 160,
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
}>()

const themeVars = useThemeVars()
const tableRef = ref<any>(null)
const vTableInstance = shallowRef<any>(null)

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
  const cols = props.columns.map((c, idx) => ({
    field: idx,
    title: c.name,
    width: props.defaultColumnWidth,
    minWidth: 60,
    editor: props.editable ? pickEditor(c) : undefined,
    style: { textAlign: pickAlign(c) },
    description: c.comment || c.nativeType,
    fieldFormat: (record: any) => renderCellValue(record?.[idx]),
  }))
  return {
    columns: cols,
    defaultRowHeight: props.rowHeight,
    defaultHeaderRowHeight: 28,
    defaultColWidth: props.defaultColumnWidth,
    rowSeriesNumber: {
      title: '#',
      width: 50,
      style: { textAlign: 'right' },
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

function bodyRowOf(row: number): number {
  const headerLevels = vTableInstance.value?.columnHeaderLevelCount ?? 1
  return row - headerLevels
}

function onChangeCellValue(args: any) {
  // args: { col, row, changedValue, originValue, ... }
  if (!args) return
  const bodyRow = bodyRowOf(args.row)
  if (bodyRow < 0) return
  const meta = props.columns[args.col]
  if (!meta) return
  emit('edit-commit', {
    row: bodyRow,
    col: args.col,
    oldValue: args.originValue,
    newValue: args.changedValue,
    column: meta,
  })
}

function onContextMenuCell(args: any) {
  if (!args || args.col == null || args.row == null) return
  const bodyRow = bodyRowOf(args.row)
  if (bodyRow < 0) return
  const ev: MouseEvent | undefined = args.federatedEvent?.nativeEvent ?? args.event
  emit('cell-context-menu', {
    row: bodyRow,
    col: args.col,
    x: ev?.pageX ?? args.x ?? 0,
    y: ev?.pageY ?? args.y ?? 0,
    value: props.rows[bodyRow]?.[args.col],
  })
}

function onSelectedCell(args: any) {
  if (!args) {
    emit('selection-change', { range: null })
    return
  }
  // 单击选中：{ col, row }
  // 拖拽 / shift+click：{ ranges: [{ start: {col,row}, end: {col,row} }] } 或类似
  // VTable 不同版本 args 形状可能略有差异，做兼容
  const cr = args.cellRange ?? args.range ?? null
  if (cr && cr.start && cr.end) {
    const sr = bodyRowOf(cr.start.row)
    const er = bodyRowOf(cr.end.row)
    if (sr < 0 || er < 0) {
      emit('selection-change', { range: null })
      return
    }
    emit('selection-change', {
      range: {
        startRow: sr,
        startCol: cr.start.col,
        endRow: er,
        endCol: cr.end.col,
      },
    })
    return
  }
  // 单 cell
  if (args.col != null && args.row != null) {
    const r = bodyRowOf(args.row)
    if (r < 0) {
      emit('selection-change', { range: null })
      return
    }
    emit('selection-change', {
      range: { startRow: r, startCol: args.col, endRow: r, endCol: args.col },
    })
  }
}

function onDragSelectEnd(args: any) {
  // VTable 在拖拽结束时给出最终范围，比 selected_cell 更可靠
  onSelectedCell(args)
}

function onScrollVerticalEnd() {
  emit('load-more')
}

function onReady(instance: any) {
  vTableInstance.value = instance
}

// 暴露给 parent 访问底层实例（高级用法，正常不会用到）
defineExpose({
  getVTableInstance: () => vTableInstance.value,
})

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
</script>

<template>
  <!-- --custom-contextmenu: catdb-grid-cell 触发 Wails 原生上下文菜单
       （wailsbridge/contextmenu.go 中注册）。CSS 变量在画布子节点也生效。 -->
  <div class="datagrid-wrap" style="--custom-contextmenu: catdb-grid-cell">
    <ListTable
      ref="tableRef"
      :options="tableOptions"
      :records="rows"
      width="100%"
      height="100%"
      :keep-column-width-change="true"
      :on-ready="onReady"
      :on-change-cell-value="onChangeCellValue"
      :on-contextmenu-cell="onContextMenuCell"
      :on-selected-cell="onSelectedCell"
      :on-drag-select-end="onDragSelectEnd"
      :on-scroll-vertical-end="onScrollVerticalEnd"
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
