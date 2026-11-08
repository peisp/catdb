// 自研 toast(替代 Naive useMessage/NMessageProvider)。CToastHost 挂在
// App.vue 根部,一个 SPA 实例一个 host(多窗口各自是独立 SPA 路由,天然隔离)。
// API 与原 useMessage 对齐(success/error/warning/info),迁移零改调用处。
import { reactive } from 'vue'

export type ToastKind = 'success' | 'error' | 'warning' | 'info'

export interface ToastItem {
  id: number
  kind: ToastKind
  text: string
}

export const toasts = reactive<ToastItem[]>([])

let nextId = 1

const DURATION: Record<ToastKind, number> = {
  success: 3000,
  info: 3000,
  warning: 4500,
  error: 6000,
}

function push(kind: ToastKind, text: string) {
  const id = nextId++
  toasts.push({ id, kind, text })
  window.setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id)
    if (i >= 0) toasts.splice(i, 1)
  }, DURATION[kind])
}

export const message = {
  success: (text: string) => push('success', text),
  error: (text: string) => push('error', text),
  warning: (text: string) => push('warning', text),
  info: (text: string) => push('info', text),
}

/** 迁移期同名 API:原 `const message = useMessage()` 调用处不用改。 */
export function useMessage() {
  return message
}
