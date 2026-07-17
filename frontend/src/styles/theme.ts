// Naive UI 主题覆写 — 全部数值由 styles/tokens.ts(即 DESIGN.md)生成,
// 本文件只做 token → GlobalThemeOverrides 的映射,不得出现独立 hex。
import type { GlobalThemeOverrides } from 'naive-ui'
import {
  fontFamily,
  fontFamilyMono,
  metrics,
  palette,
  rounded,
  shadow,
  typography,
  type ThemeMode,
} from './tokens'

// 有边框控件 hover/pressed 只做边框微加深(DESIGN.md「选中与焦点」)。
const controlBorder = {
  light: { base: 'rgba(0, 0, 0, 0.18)', hover: 'rgba(0, 0, 0, 0.24)', pressed: 'rgba(0, 0, 0, 0.3)' },
  dark: { base: 'rgba(255, 255, 255, 0.2)', hover: 'rgba(255, 255, 255, 0.26)', pressed: 'rgba(255, 255, 255, 0.32)' },
} as const satisfies Record<ThemeMode, Record<string, string>>

function makeOverrides(mode: ThemeMode): GlobalThemeOverrides {
  const c = palette[mode]
  const border = controlBorder[mode]
  return {
    common: {
      fontFamily,
      fontFamilyMono,
      fontSize: typography.body.fontSize,
      fontSizeSmall: typography.small.fontSize,
      fontSizeMedium: typography.body.fontSize,
      fontSizeLarge: '14px',
      borderRadius: rounded.sm,
      borderRadiusSmall: rounded.xs,
      heightTiny: metrics['control-height-mini'],
      heightSmall: metrics['control-height'],
      heightMedium: metrics['control-height-medium'],
      heightLarge: '32px',
      heightHuge: '36px',
      // 单一强调色:系统蓝。macOS 控件 hover 不变色,只有按下加深。
      primaryColor: c.accent,
      primaryColorHover: c.accent,
      primaryColorPressed: c['accent-pressed'],
      primaryColorSuppl: c.accent,
      infoColor: c.accent,
      infoColorHover: c.accent,
      infoColorPressed: c['accent-pressed'],
      infoColorSuppl: c.accent,
      successColor: c.success,
      successColorHover: c.success,
      successColorPressed: c.success,
      successColorSuppl: c.success,
      warningColor: c.warning,
      warningColorHover: c.warning,
      warningColorPressed: c.warning,
      warningColorSuppl: c.warning,
      errorColor: c.error,
      errorColorHover: c.error,
      errorColorPressed: c.error,
      errorColorSuppl: c.error,
      textColor1: c['text-primary'],
      textColor2: c['text-primary'],
      textColor3: c['text-secondary'],
      placeholderColor: c['text-tertiary'],
      textColorDisabled: c['text-tertiary'],
      dividerColor: c.separator,
      borderColor: border.base,
      popoverColor: c['surface-raised'],
      modalColor: c['surface-raised'],
      cardColor: c['surface-content'],
      bodyColor: c['surface-content'],
      tableColor: c['surface-content'],
      tableHeaderColor: c['surface-chrome'],
      // 浮层阴影 + 1px hairline 描边(暗色下阴影不够,描边承担分离感)。
      boxShadow2: `0 0 0 1px ${c.separator}, ${shadow.menu}`,
      boxShadow3: `0 0 0 1px ${c.separator}, ${shadow.modal}`,
    },
    Button: {
      paddingMedium: '0 12px',
      paddingSmall: '0 8px',
      border: `1px solid ${border.base}`,
      borderHover: `1px solid ${border.hover}`,
      borderPressed: `1px solid ${border.pressed}`,
      borderFocus: `1px solid ${border.hover}`,
    },
    Input: {
      heightMedium: metrics['control-height-medium'],
      heightSmall: metrics['control-height'],
    },
    DataTable: {
      fontSizeSmall: typography.small.fontSize,
      fontSizeMedium: typography.body.fontSize,
      thPaddingSmall: '4px 8px',
      tdPaddingSmall: '3px 8px',
      thPaddingMedium: '6px 10px',
      tdPaddingMedium: '4px 10px',
    },
    Tree: {
      nodeHeight: metrics['tree-row-height'],
      fontSize: typography.body.fontSize,
    },
    Tabs: {
      tabFontSizeMedium: typography.body.fontSize,
      tabPaddingMediumLine: '6px 14px',
      tabBorderColor: c.separator,
    },
    Menu: {
      itemHeight: metrics['control-height-medium'],
    },
    Popover: {
      borderRadius: rounded.md,
    },
    Dialog: {
      borderRadius: rounded.lg,
    },
    Layout: {
      siderToggleButtonColor: 'transparent',
    },
  }
}

export const themeOverrides: GlobalThemeOverrides = makeOverrides('light')
export const darkThemeOverrides: GlobalThemeOverrides = makeOverrides('dark')
