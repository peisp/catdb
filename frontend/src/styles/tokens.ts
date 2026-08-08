// Design tokens — 单一来源是 DESIGN.md 的 frontmatter,本文件与之逐项对应,
// 两处漂移视为 bug。消费链(见 DESIGN.md「实现映射」):
//   1. applyThemeTokens() 把当前 mode 的 token 注入 :root 的 --catdb-* CSS 变量
//      (main.ts 挂载前引导,theme store 在 mode 变化时调用),自研控件库与
//      组件 scoped 样式用 var(--catdb-*);
//   2. canvas 数据网格与 CodeMirror 主题读不了 CSS 变量,直接 import 这里的常量。

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
    'accent-soft': 'rgba(0, 122, 255, 0.1)',
    'text-primary': 'rgba(0, 0, 0, 0.88)',
    'text-secondary': 'rgba(0, 0, 0, 0.52)',
    'text-tertiary': 'rgba(0, 0, 0, 0.28)',
    'text-on-accent': '#ffffff',
    'surface-chrome': '#f6f6f7',
    'surface-sidebar': '#eeeef0',
    'surface-content': '#ffffff',
    'surface-raised': '#ffffff',
    'row-alternate': '#f7f8fa',
    'selection-focused': '#007aff',
    'selection-unfocused': '#e1e1e4',
    'separator': 'rgba(0, 0, 0, 0.1)',
    'control-border': 'rgba(0, 0, 0, 0.15)',
    'control-bg': '#ffffff',
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
    'accent-soft': 'rgba(10, 132, 255, 0.2)',
    'text-primary': 'rgba(255, 255, 255, 0.9)',
    'text-secondary': 'rgba(255, 255, 255, 0.55)',
    'text-tertiary': 'rgba(255, 255, 255, 0.28)',
    'text-on-accent': '#ffffff',
    'surface-chrome': '#2c2c2e',
    'surface-sidebar': '#242428',
    'surface-content': '#1d1d20',
    'surface-raised': '#323236',
    'row-alternate': 'rgba(255, 255, 255, 0.03)',
    'selection-focused': '#0a84ff',
    'selection-unfocused': '#3a3a3f',
    'separator': 'rgba(255, 255, 255, 0.12)',
    'control-border': 'rgba(255, 255, 255, 0.16)',
    'control-bg': 'rgba(255, 255, 255, 0.09)',
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
  xs: '4px',
  sm: '6px',
  md: '10px',
  lg: '12px',
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
  'toolbar-height': '55px',
  'tabbar-height': '36px',
  'statusbar-height': '24px',
  'viewbar-height': '36px',
  'tree-row-height': '24px',
  'grid-row-height': '24px',
  'grid-header-height': '26px',
  'sidebar-default-width': '210px',
} as const

export const focusRing = {
  light: '0 0 0 3px rgba(0, 122, 255, 0.35)',
  dark: '0 0 0 3px rgba(10, 132, 255, 0.4)',
} as const satisfies Record<ThemeMode, string>

// 按钮描边:1px 描边环 + 0.5px 微投影,替代 border(DESIGN.md「按钮」)。
// 这是控件的"描边"实现,不算阴影层级。primary 为顶部内高光,light/dark 同值。
export const controlShadow = {
  light: '0 0 0 1px rgba(0, 0, 0, 0.1), 0 0.5px 1.5px rgba(0, 0, 0, 0.1)',
  dark: '0 0 0 1px rgba(255, 255, 255, 0.13), 0 0.5px 1.5px rgba(0, 0, 0, 0.25)',
} as const satisfies Record<ThemeMode, string>

export const controlShadowPrimary =
  'inset 0 1px 0 rgba(255, 255, 255, 0.16), 0 0.5px 1.5px rgba(0, 0, 0, 0.18)'

// 浮层阴影:light/dark 双值,第一段是 0.5px 描边环(暗色下阴影不够,由描边
// 承担分离感),消费方不再额外叠 separator 描边(DESIGN.md「形状与深度」)。
export const shadow = {
  light: {
    menu: '0 0 0 0.5px rgba(0, 0, 0, 0.08), 0 10px 32px rgba(0, 0, 0, 0.16)',
    modal: '0 0 0 0.5px rgba(0, 0, 0, 0.1), 0 24px 64px rgba(0, 0, 0, 0.28)',
  },
  dark: {
    menu: '0 0 0 0.5px rgba(0, 0, 0, 0.55), 0 10px 32px rgba(0, 0, 0, 0.5)',
    modal: '0 0 0 0.5px rgba(0, 0, 0, 0.6), 0 24px 64px rgba(0, 0, 0, 0.6)',
  },
} as const satisfies Record<ThemeMode, { menu: string; modal: string }>

// 数据网格列分隔线:separator 的 50% 强度(DESIGN.md 数据网格规格)。派生值,
// canvas 无法用 color-mix,故在此落成常量。
export const gridColumnLine = {
  light: 'rgba(0, 0, 0, 0.05)',
  dark: 'rgba(255, 255, 255, 0.06)',
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
  s.setProperty('--catdb-control-shadow', controlShadow[mode])
  s.setProperty('--catdb-control-shadow-primary', controlShadowPrimary)
  s.setProperty('--catdb-shadow-menu', shadow[mode].menu)
  s.setProperty('--catdb-shadow-modal', shadow[mode].modal)
  s.setProperty('--catdb-font-family', fontFamily)
  s.setProperty('--catdb-font-family-mono', fontFamilyMono)
}
