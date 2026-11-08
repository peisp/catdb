<script setup lang="ts">
// CTree — 通用虚拟化懒加载树(DESIGN.md「树与列表」规格:行高 24px、缩进
// 16px/级、聚焦蓝/失焦灰双态整行胶囊选中、加载中行内 spinner 替换展开箭头)。
//
// 数据受控:expanded-keys / selected-keys 均为 v-model(不传则内部托管)。
// 异步加载:onLoad(node) 返回 Promise;成功须给 node.children 赋值(未赋值
// 视为空并写入 []),返回 false 或 reject 视为失败——节点保持未加载、从
// expandedKeys 移除,下次展开自动重试。加载的触发点只有一个:下方的
// watchEffect 扫描「已展开但 children === undefined」的节点,并用
// loadingKeys/failedKeys 保证同一节点并发只触发一次、失败不无限重试
// (n-tree/treemate 的老坑)。消费方把 children 置回 undefined 即可让已
// 展开的节点自动重载。
//
// 过滤:pattern 非空时按 label 不区分大小写 includes 匹配,只显示命中节点
// 及其祖先(等价 n-tree show-irrelevant-nodes=false),含命中后代的节点强制
// 展开;懒加载的未展开层不参与匹配。
//
// TODO: 键盘导航(上下移动选中、左右收起/展开)暂未实现。
import { computed, reactive, ref, watchEffect } from 'vue'
import CSpin from './CSpin.vue'

export interface CTreeNode {
  key: string
  label: string
  isLeaf?: boolean
  // undefined = 未加载(onLoad 惰性拉取);[] = 已加载且为空。
  children?: CTreeNode[]
  // 消费方自定义载荷,CTree 不读取。
  extra?: unknown
}

interface Props {
  data: CTreeNode[]
  pattern?: string
  itemHeight?: number
  indent?: number
  onLoad?: (node: CTreeNode) => Promise<boolean | void>
}

const props = withDefaults(defineProps<Props>(), {
  pattern: '',
  itemHeight: 24,
  indent: 16,
})

const expandedKeys = defineModel<string[]>('expandedKeys', { default: () => [] })
const selectedKeys = defineModel<string[]>('selectedKeys', { default: () => [] })

const emit = defineEmits<{
  'node-click': [node: CTreeNode, event: MouseEvent]
  'node-dblclick': [node: CTreeNode, event: MouseEvent]
  'node-contextmenu': [node: CTreeNode, event: MouseEvent]
}>()

defineSlots<{
  prefix?: (props: { node: CTreeNode }) => unknown
  label?: (props: { node: CTreeNode }) => unknown
}>()

// --- 异步加载 ---

const loadingKeys = reactive(new Set<string>())
// 失败标记:阻止 watchEffect 在「失败 → 移出 expandedKeys」生效前的窗口期
// 重复触发;节点收起后清除,重新展开即重试。
const failedKeys = new Set<string>()

async function runLoad(node: CTreeNode) {
  loadingKeys.add(node.key)
  let ok = false
  try {
    ok = (await props.onLoad!(node)) !== false
  } catch {
    ok = false
  }
  loadingKeys.delete(node.key)
  if (ok) {
    // 成功但没写 children → 视为空,防止扫描器无限重触发。
    if (node.children === undefined) node.children = []
  } else {
    failedKeys.add(node.key)
    if (expandedKeys.value.includes(node.key)) {
      expandedKeys.value = expandedKeys.value.filter((k) => k !== node.key)
    }
  }
}

watchEffect(() => {
  if (!props.onLoad) return
  const expanded = new Set(expandedKeys.value)
  for (const k of [...failedKeys]) if (!expanded.has(k)) failedKeys.delete(k)
  const stack = [...props.data]
  while (stack.length) {
    const n = stack.pop()!
    if (n.children) stack.push(...n.children)
    if (n.isLeaf || !expanded.has(n.key)) continue
    if (n.children !== undefined) continue
    if (loadingKeys.has(n.key) || failedKeys.has(n.key)) continue
    void runLoad(n)
  }
})

// --- 过滤 + 拍平 ---

interface Row {
  node: CTreeNode
  depth: number
  expanded: boolean
  leaf: boolean
}

const filterSets = computed(() => {
  const q = props.pattern.trim().toLowerCase()
  const visible = new Set<string>()
  const forced = new Set<string>()
  if (!q) return { active: false, visible, forced }
  const walk = (n: CTreeNode): boolean => {
    const self = n.label.toLowerCase().includes(q)
    let desc = false
    for (const c of n.children ?? []) if (walk(c)) desc = true
    if (desc) forced.add(n.key)
    if (self || desc) {
      visible.add(n.key)
      return true
    }
    return false
  }
  for (const n of props.data) walk(n)
  return { active: true, visible, forced }
})

const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const { active, visible, forced } = filterSets.value
  const expanded = new Set(expandedKeys.value)
  const push = (n: CTreeNode, depth: number) => {
    if (active && !visible.has(n.key)) return
    const leaf = !!n.isLeaf
    const exp = !leaf && (expanded.has(n.key) || (active && forced.has(n.key)))
    out.push({ node: n, depth, expanded: exp, leaf })
    if (exp) for (const c of n.children ?? []) push(c, depth + 1)
  }
  for (const n of props.data) push(n, 0)
  return out
})

// --- 窗口化渲染(同 CVirtualList 的定高行方案,行内嵌树排版) ---

const scroller = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewportH = ref(0)
const OVERSCAN = 6

function onScroll() {
  const el = scroller.value
  if (!el) return
  scrollTop.value = el.scrollTop
  viewportH.value = el.clientHeight
}

const range = computed(() => {
  const start = Math.max(0, Math.floor(scrollTop.value / props.itemHeight) - OVERSCAN)
  const count = Math.ceil((viewportH.value || 600) / props.itemHeight) + OVERSCAN * 2
  const end = Math.min(rows.value.length, start + count)
  return { start, end }
})

const visibleRows = computed(() => rows.value.slice(range.value.start, range.value.end))

// --- 交互 ---

const selectedSet = computed(() => new Set(selectedKeys.value))

function toggleExpand(node: CTreeNode) {
  if (node.isLeaf) return
  const keys = expandedKeys.value
  expandedKeys.value = keys.includes(node.key)
    ? keys.filter((k) => k !== node.key)
    : [...keys, node.key]
}

function onRowClick(node: CTreeNode, e: MouseEvent) {
  selectedKeys.value = [node.key]
  emit('node-click', node, e)
}
</script>

<template>
  <div ref="scroller" class="c-tree" tabindex="0" @scroll.passive="onScroll">
    <div class="spacer" :style="{ height: rows.length * itemHeight + 'px' }">
      <div class="window" :style="{ transform: `translateY(${range.start * itemHeight}px)` }">
        <div
          v-for="r in visibleRows"
          :key="r.node.key"
          class="row"
          :class="{ selected: selectedSet.has(r.node.key) }"
          :style="{ height: itemHeight + 'px', paddingLeft: 4 + r.depth * indent + 'px' }"
          @click="onRowClick(r.node, $event)"
          @dblclick="emit('node-dblclick', r.node, $event)"
          @contextmenu="emit('node-contextmenu', r.node, $event)"
        >
          <span class="switcher" @click.stop="toggleExpand(r.node)" @dblclick.stop>
            <CSpin v-if="loadingKeys.has(r.node.key)" :size="14" />
            <svg
              v-else-if="!r.leaf"
              class="arrow"
              :class="{ open: r.expanded }"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M6 4l4 4-4 4" />
            </svg>
          </span>
          <slot name="prefix" :node="r.node" />
          <span class="lbl">
            <slot name="label" :node="r.node">{{ r.node.label }}</slot>
          </span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.c-tree {
  height: 100%;
  min-height: 0;
  overflow-y: auto;
  outline: none;
  user-select: none;
}
.spacer {
  position: relative;
}
.window {
  position: absolute;
  left: 0;
  right: 0;
  top: 0;
}
.row {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 0 4px;
  padding-right: 4px;
  border-radius: var(--catdb-rounded-sm);
  color: var(--catdb-text-primary);
}
.row:hover:not(.selected) {
  background: var(--catdb-hover-fill);
}
/* 失焦灰 / 聚焦蓝 双态选中(DESIGN.md「选中与焦点」)。 */
.row.selected {
  background: var(--catdb-selection-unfocused);
}
.c-tree:focus-within .row.selected {
  background: var(--catdb-selection-focused);
  color: var(--catdb-text-on-accent);
}
.switcher {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  flex: 0 0 auto;
}
.arrow {
  width: 12px;
  height: 12px;
  display: block;
  opacity: 0.6;
  transition: transform 130ms ease-out;
}
.arrow.open {
  transform: rotate(90deg);
}
.lbl {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--catdb-fs-body);
}
</style>
