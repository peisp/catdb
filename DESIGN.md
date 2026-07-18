---
version: 1.0
name: catdb-macos-native
description: catdb 的 UI 设计规范 —— macOS 官方应用(Finder/备忘录/Xcode 检查器)的设计语言,适配数据库管理工具的高密度桌面形态。单一系统蓝强调色、系统字体 13px 桌面字阶、hairline 分隔、克制的圆角与阴影。所有 token 定义 light/dark 双值,是前端 styles/tokens.ts 的唯一来源。
colors:
  light:
    accent: "#007aff"              # macOS 系统蓝 controlAccentColor —— 唯一强调色
    accent-pressed: "#0062cc"      # 按下态(蓝加深)
    accent-soft: "rgba(0, 122, 255, 0.12)"   # 强调色弱底(选中 chip、active 侧栏项)
    text-primary: "rgba(0, 0, 0, 0.85)"      # labelColor —— 正文/控件文字
    text-secondary: "rgba(0, 0, 0, 0.5)"     # secondaryLabelColor —— 辅助说明
    text-tertiary: "rgba(0, 0, 0, 0.26)"     # tertiaryLabel —— 占位符/禁用/NULL
    text-on-accent: "#ffffff"
    surface-chrome: "#f3f3f3"      # 窗口铬:标题栏/工具栏/状态栏/tab 条
    surface-sidebar: "#ececec"     # 侧栏(连接列表 + 对象树)
    surface-content: "#ffffff"     # 内容面:SQL 编辑器/数据网格/表单
    surface-raised: "#ffffff"      # 浮层:菜单/popover/对话框
    row-alternate: "#f7f7f7"       # 表格斑马纹偶数行
    selection-focused: "#007aff"   # 列表/树/表格选中行(拥有焦点)
    selection-unfocused: "#dcdcdc" # 选中行(失焦)—— 灰,不抢焦点所在面板的戏
    separator: "rgba(0, 0, 0, 0.12)"   # NSColor.separatorColor —— 面板分隔 hairline
    control-border: "rgba(0, 0, 0, 0.18)"  # 按钮/输入框描边
    hover-fill: "rgba(0, 0, 0, 0.05)"      # 无边框控件 hover 底
    pressed-fill: "rgba(0, 0, 0, 0.1)"     # 无边框控件按下底
    scrim: "rgba(0, 0, 0, 0.25)"   # 模态遮罩
    success: "#28cd41"
    warning: "#ff9500"
    error: "#ff3b30"
  dark:
    accent: "#0a84ff"
    accent-pressed: "#409cff"
    accent-soft: "rgba(10, 132, 255, 0.22)"
    text-primary: "rgba(255, 255, 255, 0.85)"
    text-secondary: "rgba(255, 255, 255, 0.55)"
    text-tertiary: "rgba(255, 255, 255, 0.25)"
    text-on-accent: "#ffffff"
    surface-chrome: "#3d3d3d"
    surface-sidebar: "#373737"
    surface-content: "#333333"     # DataGrip 风格编辑面(沿用既有决策)
    surface-raised: "#404040"
    row-alternate: "rgba(255, 255, 255, 0.03)"
    selection-focused: "#0a84ff"
    selection-unfocused: "#464646"
    separator: "rgba(255, 255, 255, 0.14)"
    control-border: "rgba(255, 255, 255, 0.2)"
    hover-fill: "rgba(255, 255, 255, 0.07)"
    pressed-fill: "rgba(255, 255, 255, 0.12)"
    scrim: "rgba(0, 0, 0, 0.45)"
    success: "#32d74b"
    warning: "#ff9f0a"
    error: "#ff453a"
typography:
  body:            { fontSize: 13px, fontWeight: 400, lineHeight: 1.4 }   # 默认:控件/树/菜单/表单
  body-strong:     { fontSize: 13px, fontWeight: 600, lineHeight: 1.4 }   # 行内强调/选中 tab
  small:           { fontSize: 12px, fontWeight: 400, lineHeight: 1.35 }  # 状态栏/表头/次要说明
  small-strong:    { fontSize: 12px, fontWeight: 600, lineHeight: 1.35 }
  mini:            { fontSize: 11px, fontWeight: 400, lineHeight: 1.3 }   # 徽标/列类型注记/行计数
  micro:           { fontSize: 10px, fontWeight: 400, lineHeight: 1.2 }   # 极小注记:tab 库名行/muted 角标/微徽标
  title:           { fontSize: 15px, fontWeight: 600, lineHeight: 1.3 }   # 面板标题/对话框标题
  large-title:     { fontSize: 26px, fontWeight: 600, lineHeight: 1.2 }   # 欢迎页/空状态大标题
  mono:            { fontSize: 12px, fontWeight: 400, lineHeight: 1.5 }   # SQL 编辑器/数据单元格
  mono-small:      { fontSize: 11px, fontWeight: 400, lineHeight: 1.4 }   # 网格密集模式/行号
rounded:
  xs: 3px       # 行内徽标、类型 tag
  sm: 5px       # 按钮/输入框/segmented control —— macOS 控件标准圆角
  md: 8px       # 菜单/popover/卡片式分组
  lg: 10px      # 对话框/sheet/设置窗口分组卡片
  pill: 9999px  # 计数徽标、过滤 chip
spacing:
  xxs: 2px
  xs: 4px
  sm: 8px
  md: 12px
  lg: 16px
  xl: 20px
  xxl: 32px
metrics:
  control-height-mini: 20px      # 行内小按钮
  control-height: 24px           # small 控件(工具栏内按钮/输入框)
  control-height-medium: 28px    # 默认控件(表单按钮/输入框)
  toolbar-height: 38px
  tabbar-height: 30px
  statusbar-height: 24px
  tree-row-height: 24px
  grid-row-height: 24px
  grid-header-height: 26px
  sidebar-default-width: 240px
  focus-ring: "0 0 0 3px rgba(0, 122, 255, 0.35)"        # dark 用 rgba(10,132,255,0.4)
  shadow-menu: "0 4px 16px rgba(0, 0, 0, 0.18)"
  shadow-modal: "0 12px 40px rgba(0, 0, 0, 0.25)"
---

# catdb UI 设计规范(macOS 原生风格)

> 本文件是 catdb 全部 UI 外观与交互的**唯一来源**。frontmatter 中的 token 是机器可读的单一数据源,`frontend/src/styles/tokens.ts` 必须与之逐项对应;正文规定 token 的使用规则与组件形态。改 token 先改这里,再同步代码。

## 总纲

catdb 的外观目标是:**放在 macOS 上像一个 Apple 官方出品的专业工具**(参照 Finder、备忘录、Xcode 的检查器面板),而不是"套壳网页"。核心手法:

- **UI 铬后退,数据前置**。工具栏、侧栏、tab 条使用中性灰面,不抢戏;唯一的彩色是系统蓝强调色和语义色(成功/警告/错误)。数据网格与 SQL 编辑器是舞台中心。
- **单一强调色**。所有"可点/已选/焦点"信号都是 `accent`(系统蓝)。禁止引入第二强调色;绿色/橙色/红色只作语义反馈(测试连接成功、警告、错误),不作装饰。
- **原生桌面密度**。13px 基准字号、24px 行高的树与网格、28px 控件。这是专业数据库工具的信息密度,不做网页式的大留白。
- **hairline 分隔,而非阴影分隔**。面板之间用 1px `separator` 分界;阴影只出现在真正浮起的东西上(菜单、popover、对话框)。
- **深浅双模式同权**。每个颜色 token 都有 light/dark 双值,跟随系统 `prefers-color-scheme`,无独立开关。

**平台策略**:Windows 上不模拟 Windows 风格,统一走本规范(macOS 语言),仅字体回退到 Segoe UI。这保证跨平台品牌一致,也避免维护两套规范。

## 颜色使用规则

### 表面(Surface)层次

从外到内三层灰阶,亮色模式下**越靠近数据越亮**,暗色模式下 chrome 比 content 略亮(DataGrip 惯例,让编辑面下沉):

| 层 | token | 用于 |
|---|---|---|
| 窗口铬 | `surface-chrome` | 标题栏、工具栏、workspace tab 条、状态栏、过滤栏 |
| 侧栏 | `surface-sidebar` | 连接列表、对象树所在面板 |
| 内容面 | `surface-content` | SQL 编辑器、数据网格、结构编辑表格、表单主体 |
| 浮层 | `surface-raised` | 右键菜单、popover、对话框、下拉面板 |

相邻表面之间**必须**有 1px `separator` hairline;不要用阴影或颜色跳变代替。

### 文字三级灰

- `text-primary`:正文、控件标签、数据值。
- `text-secondary`:辅助说明、表头、状态栏信息、面板小标题。
- `text-tertiary`:占位符、禁用文字、网格中的 `NULL`(斜体 + tertiary 是 NULL 的标准渲染)。

**禁止**在这三级之外自造灰色。需要更弱的存在感就用 `text-tertiary`,需要强调就用 `body-strong` 字重而不是加深颜色。

### 选中与焦点(桌面应用的关键态)

- **拥有键盘焦点的列表/树/网格**:选中行底色 `selection-focused`(实心系统蓝),文字 `text-on-accent`。
- **失去焦点的面板**:选中行降为 `selection-unfocused`(中性灰),文字回 `text-primary`。这是 macOS 列表的标志性行为——焦点在哪一目了然,必须实现。
- **键盘焦点环**:可聚焦控件 focus-visible 时用 `metrics.focus-ring`(3px 半透明蓝晕),不用改边框色的方式表达焦点。
- **hover**:无边框控件(工具栏图标按钮、树行、tab)hover 时垫 `hover-fill`,按下垫 `pressed-fill`。有边框控件 hover 仅边框微加深。hover 不改变文字颜色。

### 语义色

`success`/`warning`/`error` 只用于状态反馈:连接测试结果、保存成败、危险操作确认、单元格校验错误。**规则**:语义色只染小面积(图标、一行提示文字、输入框错误描边),不整块铺底;需要铺底时用对应色 8%~12% 透明度。

## 字体排印

- **字族**:`system-ui, -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", "Helvetica Neue", sans-serif`(macOS 上即 SF Pro)。等宽:`ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace`。不打包字体文件。
- **字阶封闭**:只允许 frontmatter `typography` 里的 9 个档位。新场景先归档到现有档位,确实不够再修订规范。禁止在组件里手写 `font-size`。
- **字重阶梯 400 / 600**:正文 400,强调与标题 600。禁用 300、500、700(500 在 13px 下与 600 难分,徒增混乱)。
- **等宽的领地**:SQL 文本、数据单元格值、行号、DDL 预览一律 `mono`;UI 文案(按钮、菜单、表单标签)一律不用等宽。
- 数据网格与树**不用**负字距;13px 以下加字距同样禁止,系统默认即可。

## 形状与深度

- **圆角语法**(只有四档 + pill,不得混用中间值):`xs`(3px)行内徽标 → `sm`(5px)一切按钮/输入框 → `md`(8px)菜单与 popover → `lg`(10px)对话框。
- **阴影只给浮层**:菜单/popover 用 `shadow-menu`,对话框用 `shadow-modal`,且都同时带 1px `separator` 描边(暗色模式下阴影不够,描边承担分离感)。按钮、卡片、工具栏、tab **永远没有阴影**。
- **无装饰渐变**。任何表面都是纯色。**唯一例外是"玻璃材质"**(见下)。
- **玻璃材质(Liquid Glass)**:半透明渐变 + 内侧高光的磨砂玻璃质感,对齐当前 macOS 的 Liquid Glass 语言。**只允许**用于 chrome 层的小面积分段/开关控件(侧栏开关、分段 tab 轨),整面板、卡片、按钮一律禁止。该材质是组件局部实现(多段 rgba 渐变 + inset 高光,light/dark 各一套 + `@supports` 回退),**不 token 化**;现有实现见 AppShell 侧栏开关、ConnectionForm 分组 tab、TableStructure tab 轨,新增玻璃控件以它们为准。
- **模态遮罩**用 `scrim` token(light 黑 25% / dark 黑 45%),不自造遮罩灰。
- **按下微缩仅限图标按钮**:工具栏图标按钮按下时 `pressed-fill` 垫底即可,不做 scale 变换(桌面工具不要营销站的弹性动效)。动效原则:130ms ease-out 的透明度/背景过渡,不做位移动画。

## 组件规格

### 窗口结构

**titlebar** —— 与 `surface-chrome` 同色,通栏可拖拽(`--wails-draggable: drag`,内部控件 no-drag)。无独立底边线时与工具栏融为一体。

**toolbar** —— 高 `metrics.toolbar-height`(38px),底部 1px `separator`。内容为图标按钮(见 button-toolbar)与 24px 高的小控件。图标 16px,`text-secondary` 着色,active/开关态用 `accent`。

**sidebar** —— 右缘 1px `separator`,默认宽 240px、可拖拽(拖拽柄 hover 时显示 accent 高亮线)。内容为连接列表与对象树。**底色分平台**:macOS 上 CSS 透明,透出窗口的 `MacBackdropTranslucent` 原生毛玻璃;Windows 无原生毛玻璃,用 `surface-sidebar` 实底。不要在 macOS 上给侧栏及其子面板刷任何不透明底色,会杀掉毛玻璃。

**statusbar** —— 高 24px,`surface-chrome` 底 + 顶部 hairline,文字 `small` + `text-secondary`(行数、耗时、连接状态)。

### 树与列表(对象树 / 连接列表)

- 行高 24px,缩进每级 16px,图标 14~16px。
- 文字 `body`;库/表计数徽标 `mini` + `text-tertiary`。
- 选中/失焦/hover 行为严格按「选中与焦点」一节;整行选中(通栏高亮),圆角 `sm`、左右各留 4px 内边距(macOS 侧栏胶囊选中形态)。
- 加载中的节点用行内 14px spinner 替换展开箭头,不弹遮罩。
- 连接**在线状态点**用 `accent`(在线即"活跃"信号,与主题色统一),空闲点用 `text-tertiary`;`success` 绿只留给"测试连接成功"这类操作反馈。

### Workspace tab 条

- 高 30px,`surface-chrome` 底,底边 1px `separator`。
- 选中 tab:`surface-content` 底色(与内容面连成一体)+ `body-strong`;未选中:透明底 + `text-secondary`,hover 垫 `hover-fill`。
- 关闭按钮 hover 才显现;未保存态用 `accent` 圆点替代关闭钮(macOS 文档惯例)。
- 表对象 tab 的库名/表名分行显示(沿用现状),库名 `mini` + `text-secondary`。

### 按钮

| 变体 | 形态 | 用于 |
|---|---|---|
| button-primary | `accent` 实底、白字、`sm` 圆角、高 28px、内边距 0 12px;按下 `accent-pressed` | 对话框默认动作、表单主动作(每个视图最多一个) |
| button-standard | `surface-content` 底 + 1px `control-border`、`text-primary`、`sm` 圆角、高 28px | 普通动作(取消、次要操作) |
| button-toolbar | 无边框图标钮 24×24,hover `hover-fill`、按下 `pressed-fill`、`sm` 圆角;开关型 active 态图标染 `accent` + `accent-soft` 垫底 | 工具栏、面板角落的动作 |
| button-danger | 形同 standard,文字与图标 `error`;仅确认对话框中的破坏性动作可用 `error` 实底 | 删除连接/表/行 |

按钮文字一律 `body`(13px/400),不加粗。禁用态整体 40% 透明度,不改色相。

### 输入控件

- **input / select**:高 28px(表单)或 24px(工具栏/过滤栏内),`surface-content` 底 + 1px `control-border`,圆角 `sm`;focus 时边框转 `accent` + focus-ring;错误时边框 `error`。占位符 `text-tertiary`。
- **搜索框**:同 input,前置 14px 放大镜图标(`text-tertiary`);macOS 风格圆角仍为 `sm`,不做 pill。
- **checkbox / radio / switch**:交给 Naive UI,主题色映射 `accent`,尺寸 14px。
- **segmented control**:整体 `hover-fill` 底、`sm` 圆角,选中段 `surface-content` 底 + hairline 描边(macOS Big Sur 之后的形态),高 24px。

### 数据网格(canvas 自绘)

- 行高 24px,表头 26px;单元格文字 `mono`(12px),表头 `small-strong` + `text-secondary`。
- 表头底 `surface-chrome`,底边 1px `separator`;列分隔线用 `separator` 的 50% 透明度(比行分隔更弱,数据行只画横向 hairline 或斑马纹二选一——默认斑马纹 `row-alternate`,不画横线)。
- 选中规则同「选中与焦点」;当前编辑单元格 2px `accent` 描边。
- `NULL` 渲染:斜体 `text-tertiary` 的 "NULL" 字样。
- 脏行(未提交修改):行号槽 `accent` 竖条 + 修改单元格文字转 `accent`;删除待提交行整行删除线 + `text-tertiary`。
- canvas 读不了 CSS 变量:颜色一律从 `tokens.ts` 导入 TS 常量,禁止在网格代码里写 hex。

### SQL 编辑器(CodeMirror)

- 底 `surface-content`,文字 `mono`。行号 `mono-small` + `text-tertiary`,当前行行号 `text-secondary`。
- 语法高亮主题基于 token 派生(关键字 `accent`,字符串/数字用从语义色降饱和的固定辅助阶),light/dark 各一套,定义集中在一个 CodeMirror theme 文件,同样从 `tokens.ts` 取色。
- 选区用系统蓝 25% 透明度;当前行垫 `hover-fill`。

### 菜单 / 弹层 / 对话框

- **右键菜单**:原生菜单(Go 侧),不在此规范内;Web 内自绘下拉(select 面板、自动补全)用 `surface-raised` + `md` 圆角 + `shadow-menu` + hairline 描边,菜单项高 24px,hover 垫 `accent` 实底白字(菜单是唯一 hover 即蓝的地方,对齐 NSMenu)。
- **对话框/sheet**:`surface-raised` 底、`lg` 圆角、`shadow-modal`;标题 `title`,按钮右对齐、primary 在最右;危险确认对话框按 button-danger 规则。宽度按内容,常用 420~520px。
- **popover**(列筛选、快捷设置):同菜单面盘,内边距 `md`(12px)。

### 表单(连接表单 / 设置窗口)

- 标签右对齐冒号省略(macOS 表单惯例),标签列 `text-secondary`,控件列左对齐;行距 `sm`(8px),分组间距 `xl`(20px)。
- 分组用 `title`(15px/600)小标题 + 组内容,或设置窗口中用 `lg` 圆角卡片(`surface-content` 底 + hairline)包裹分组(系统设置 App 形态)。
- 校验错误:输入框 `error` 描边 + 下方 `small` 红色说明,不弹 toast。

### 空状态 / 欢迎页

唯一允许低密度的表面:图标(48px、`text-tertiary`)+ `large-title` 或 `title` 标题 + `body`/`text-secondary` 说明 + 一个 button-primary。垂直居中,最大宽 360px。

## 光标、滚动条与拖拽(桌面铬规则)

- **pointer 光标只属于超链接**。按钮、可点行、tab 一律默认箭头光标(`cursor: default`)——这是桌面应用与网页的分水岭,已有 `global.css` 规则,保持。
- **overlay 滚动条**:轨道透明、静止时全隐,容器 hover 渐显 10px 拇指(现行实现保持)。滚动条永不占布局宽度。
- **文本不可选**:UI 铬 `user-select: none`;数据单元格、SQL 编辑器、错误消息文本必须可选可复制。

## Do / Don't

**Do**
- 所有颜色/字号/间距/圆角从 token 取;组件样式里出现 hex 或裸 px 字号即违规(布局性 px 如 flex 尺寸除外)。
- 每个视图至多一个 button-primary;蓝色永远意味着"可交互/已选中/焦点"。
- 列表与树实现"聚焦蓝/失焦灰"的双态选中。
- 相邻面板之间画 1px `separator`。
- 暗色模式与亮色同步实现——新组件两套值一次到位。

**Don't**
- 不引入第二强调色;不拿语义绿当品牌色(历史遗留的 `#18a058` 一律清除)。
- 不给按钮/卡片/工具栏加阴影;不用渐变。
- 不在 UI 铬里用等宽字体;不在数据区用 UI 字体。
- 不做位移/弹性动画;过渡只有 130ms 的透明度与背景色。
- 不写 `cursor: pointer`(超链接除外)。
- 不为 Windows 单独做一套视觉。

## 实现映射(给 Claude Code 的落地指引)

- **单一来源链**:本文件 frontmatter → `frontend/src/styles/tokens.ts`(TS 常量,light/dark 双套)→ ① 启动时注入 `:root` CSS 变量(`--catdb-*`,theme store 切换时整组替换);② `styles/theme.ts` 由 token 生成 Naive `GlobalThemeOverrides`(`primaryColor` ← accent、`borderRadius` ← rounded.sm、字号/控件高 ← typography/metrics);③ canvas 网格与 CodeMirror 主题直接 import TS 常量。
- 组件 scoped 样式一律 `var(--catdb-*)`;Naive 注入的 `--n-*` 变量仅在覆写 Naive 内部样式时使用。
- 旧的 `--app-content-bg`、`editorSurface` 由 `surface-content` token 取代。
- token 有增改时:先改本文件 frontmatter,再同步 `tokens.ts`,两处漂移视为 bug。
