// CTabs/CTabPane 的 provide key 与注册表类型。
import type { Ref } from 'vue'

export interface TabPaneInfo {
  name: string
  tab: string
}

export interface TabsContext {
  active: Ref<string | null>
  register: (info: TabPaneInfo) => void
  unregister: (name: string) => void
}

export const C_TABS = Symbol('c-tabs')
