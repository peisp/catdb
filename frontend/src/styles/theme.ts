// UI theme overrides — implements DESIGN.md (desktop scale, system
// font, hairline borders, 3–4px radius). DO NOT loosen these numbers without
// updating DESIGN.md — they're the difference between "looks native" and
// "looks like a web page".
import type { GlobalThemeOverrides } from 'naive-ui'

const fontFamily =
  'system-ui, -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", "Helvetica Neue", sans-serif'

const fontFamilyMono =
  'ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace'

const common = {
  fontFamily,
  fontFamilyMono,
  fontSize: '13px',
  fontSizeSmall: '12px',
  fontSizeMedium: '13px',
  fontSizeLarge: '14px',
  borderRadius: '3px',
  borderRadiusSmall: '2px',
  heightTiny: '20px',
  heightSmall: '24px',
  heightMedium: '28px',
  heightLarge: '32px',
  heightHuge: '36px',
}

// Shared content-surface background for the code/data editing areas (SQL editor
// + data grid). Light follows macOS textBackgroundColor (white); dark uses a
// DataGrip-style #333. Canvas-rendered VTable can't read CSS vars at paint time,
// so this stays a JS constant both consumers import (single source of truth).
export const editorSurface = { light: '#ffffff', dark: '#333333' } as const

export const themeOverrides: GlobalThemeOverrides = {
  common,
  Button: {
    paddingMedium: '0 10px',
    paddingSmall: '0 8px',
    border: '1px solid #c0c0c0',
    borderHover: '1px solid #b0b0b0',
    borderPressed: '1px solid #a0a0a0',
    borderFocus: '1px solid #b0b0b0',
  },
  Input: {
    heightMedium: '28px',
    heightSmall: '24px',
  },
  DataTable: {
    fontSizeSmall: '12px',
    fontSizeMedium: '13px',
    thPaddingSmall: '4px 8px',
    tdPaddingSmall: '3px 8px',
    thPaddingMedium: '6px 10px',
    tdPaddingMedium: '4px 10px',
  },
  Tree: {
    nodeHeight: '24px',
    fontSize: '13px',
  },
  Tabs: {
    tabFontSizeMedium: '13px',
    tabPaddingMediumLine: '6px 14px',
    // Card-tab outlines + the baseline extending from them default to Naive's
    // dividerColor, which is lighter than the app's hairlines. Point them at the
    // global --n-border-color (macOS separator, adapts light/dark) for consistency.
    tabBorderColor: 'var(--n-border-color)',
  },
  Menu: {
    itemHeight: '28px',
  },
  Layout: {
    siderToggleButtonColor: 'transparent',
  },
}

// Dark mode reuses everything but the button borders: the light-gray hairlines
// (#c0c0c0…) read as "too white" against the dark #333 toolbars. Swap in dim
// borders that sit just above the surface instead of glowing off it.
export const darkThemeOverrides: GlobalThemeOverrides = {
  ...themeOverrides,
  Button: {
    ...themeOverrides.Button,
    border: '1px solid #4a4a4a',
    borderHover: '1px solid #5a5a5a',
    borderPressed: '1px solid #666666',
    borderFocus: '1px solid #5a5a5a',
  },
}
