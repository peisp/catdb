// Design tokens — 单一来源是 DESIGN.md 的 frontmatter,本文件与之逐项对应,
// 两处漂移视为 bug。消费链(见 DESIGN.md「实现映射」):
//   1. applyThemeTokens() 把当前 mode 的 token 注入 :root 的 --catdb-* CSS 变量
//      (theme store 在 mode 变化时调用),组件 scoped 样式用 var(--catdb-*);
//   2. styles/theme.ts 由这里的常量生成 Naive UI GlobalThemeOverrides;
//   3. canvas 数据网格与 CodeMirror 主题读不了 CSS 变量,直接 import 这里的常量。

export type ThemeMode = 'light' | 'dark'

export const fontFamily =
  'system-ui, -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", "Helvetica Neue", sans-serif'

export const fontFamilyMono =
  'ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace'

// 颜色:key 与 DESIGN.md colors 完全一致(kebab-case),注入后即 --catdb-<key>。
export const palette = {
  light: {
    'accent': '#007aff',
    'accent-pressed': '#0062cc',
    'accent-soft': 'rgba(0, 122, 255, 0.12)',
    'text-primary': 'rgba(0, 0, 0, 0.85)',
    'text-secondary': 'rgba(0, 0, 0, 0.5)',
    'text-tertiary': 'rgba(0, 0, 0, 0.26)',
    'text-on-accent': '#ffffff',
    'surface-chrome': '#f3f3f3',
    'surface-sidebar': '#ececec',
    'surface-content': '#ffffff',
    'surface-raised': '#ffffff',
    'row-alternate': '#f7f7f7',
    'selection-focused': '#007aff',
    'selection-unfocused': '#dcdcdc',
    'separator': 'rgba(0, 0, 0, 0.12)',
    'control-border': 'rgba(0, 0, 0, 0.18)',
    'hover-fill': 'rgba(0, 0, 0, 0.05)',
    'pressed-fill': 'rgba(0, 0, 0, 0.1)',
    'scrim': 'rgba(0, 0, 0, 0.25)',
    'success': '#28cd41',
    'warning': '#ff9500',
    'error': '#ff3b30',
  },
  dark: {
    'accent': '#0a84ff',
    'accent-pressed': '#409cff',
    'accent-soft': 'rgba(10, 132, 255, 0.22)',
    'text-primary': 'rgba(255, 255, 255, 0.85)',
    'text-secondary': 'rgba(255, 255, 255, 0.55)',
    'text-tertiary': 'rgba(255, 255, 255, 0.25)',
    'text-on-accent': '#ffffff',
    'surface-chrome': '#3d3d3d',
    'surface-sidebar': '#373737',
    'surface-content': '#333333',
    'surface-raised': '#404040',
    'row-alternate': 'rgba(255, 255, 255, 0.03)',
    'selection-focused': '#0a84ff',
    'selection-unfocused': '#464646',
    'separator': 'rgba(255, 255, 255, 0.14)',
    'control-border': 'rgba(255, 255, 255, 0.2)',
    'hover-fill': 'rgba(255, 255, 255, 0.07)',
    'pressed-fill': 'rgba(255, 255, 255, 0.12)',
    'scrim': 'rgba(0, 0, 0, 0.45)',
    'success': '#32d74b',
    'warning': '#ff9f0a',
    'error': '#ff453a',
  },
} as const satisfies Record<ThemeMode, Record<string, string>>

export type ColorToken = keyof (typeof palette)['light']

export const rounded = {
  xs: '3px',
  sm: '5px',
  md: '8px',
  lg: '10px',
  pill: '9999px',
} as const

export const spacing = {
  xxs: '2px',
  xs: '4px',
  sm: '8px',
  md: '12px',
  lg: '16px',
  xl: '20px',
  xxl: '32px',
} as const

export const typography = {
  'body': { fontSize: '13px', fontWeight: 400, lineHeight: 1.4 },
  'body-strong': { fontSize: '13px', fontWeight: 600, lineHeight: 1.4 },
  'small': { fontSize: '12px', fontWeight: 400, lineHeight: 1.35 },
  'small-strong': { fontSize: '12px', fontWeight: 600, lineHeight: 1.35 },
  'mini': { fontSize: '11px', fontWeight: 400, lineHeight: 1.3 },
  'micro': { fontSize: '10px', fontWeight: 400, lineHeight: 1.2 },
  'title': { fontSize: '15px', fontWeight: 600, lineHeight: 1.3 },
  'large-title': { fontSize: '26px', fontWeight: 600, lineHeight: 1.2 },
  'mono': { fontSize: '12px', fontWeight: 400, lineHeight: 1.5 },
  'mono-small': { fontSize: '11px', fontWeight: 400, lineHeight: 1.4 },
} as const

export const metrics = {
  'control-height-mini': '20px',
  'control-height': '24px',
  'control-height-medium': '28px',
  'toolbar-height': '38px',
  'tabbar-height': '30px',
  'statusbar-height': '24px',
  'tree-row-height': '24px',
  'grid-row-height': '24px',
  'grid-header-height': '26px',
  'sidebar-default-width': '240px',
} as const

export const focusRing = {
  light: '0 0 0 3px rgba(0, 122, 255, 0.35)',
  dark: '0 0 0 3px rgba(10, 132, 255, 0.4)',
} as const satisfies Record<ThemeMode, string>

export const shadow = {
  menu: '0 4px 16px rgba(0, 0, 0, 0.18)',
  modal: '0 12px 40px rgba(0, 0, 0, 0.25)',
} as const

// 数据网格列分隔线:separator 的 50% 强度(DESIGN.md 数据网格规格)。派生值,
// canvas 无法用 color-mix,故在此落成常量。
export const gridColumnLine = {
  light: 'rgba(0, 0, 0, 0.06)',
  dark: 'rgba(255, 255, 255, 0.07)',
} as const satisfies Record<ThemeMode, string>

/** 当前 mode 的调色板(canvas 网格 / CodeMirror 等 TS 消费方使用)。 */
export function colors(mode: ThemeMode) {
  return palette[mode]
}

/**
 * 把 token 注入 :root 内联样式(--catdb-*)。theme store 在启动与 mode 切换时
 * 调用;内联样式优先级高于 global.css 里的静态兜底值,二者由此保持一致。
 */
export function applyThemeTokens(mode: ThemeMode): void {
  const s = document.documentElement.style
  for (const [key, value] of Object.entries(palette[mode])) {
    s.setProperty(`--catdb-${key}`, value)
  }
  for (const [key, value] of Object.entries(rounded)) {
    s.setProperty(`--catdb-rounded-${key}`, value)
  }
  for (const [key, value] of Object.entries(spacing)) {
    s.setProperty(`--catdb-space-${key}`, value)
  }
  for (const [key, value] of Object.entries(metrics)) {
    s.setProperty(`--catdb-${key}`, value)
  }
  for (const [key, t] of Object.entries(typography)) {
    s.setProperty(`--catdb-fs-${key}`, t.fontSize)
  }
  s.setProperty('--catdb-focus-ring', focusRing[mode])
  s.setProperty('--catdb-shadow-menu', shadow.menu)
  s.setProperty('--catdb-shadow-modal', shadow.modal)
  s.setProperty('--catdb-font-family', fontFamily)
  s.setProperty('--catdb-font-family-mono', fontFamilyMono)
  // 迁移期兼容别名:旧样式里的 --n-border-color / --n-divider-color 在 Naive
  // 组件子树之外没有注入源,由这里对齐 separator token。阶段 2 消费方迁完后删。
  s.setProperty('--n-border-color', palette[mode].separator)
  s.setProperty('--n-divider-color', palette[mode].separator)
}
