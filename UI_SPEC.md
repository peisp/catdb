# UI 规范：向原生靠拢，去 Web 感（UI_SPEC.md）

> 本规范是**强制性设计约束**。核心原则：这是一个**桌面原生应用**，不是网页。任何让人"一眼看出是网页套壳"的东西都要消除。Claude Code 写任何前端 UI 前必须遵守本文；与本文冲突的"好看的 Web 写法"一律服从本文。
>
> **窗口、菜单、事件、对话框——优先查 Wails v3 API，不默认用 Web 方案。** 这是本规范的最高优先级子原则，详见 `agent.md`。
>
> 技术底座：Wails v3（提供原生窗口/菜单/菜单栏/上下文菜单/对话框/事件系统）+ Vue 3 + Naive UI（JS 主题系统，便于贴合系统外观）。参照样板：Tiny RDM、TablePlus、Navicat、DBeaver、Finder/Xcode、Windows 设置/文件资源管理器。

---

## 0. 一句话判定标准

提交任何界面前自问："把它截图发出去，别人会以为这是网页还是桌面软件？" 答案必须是**桌面软件**。

典型"Web 感"信号（出现即扣分，需消除）：大圆角卡片 + 大留白 + 大号正文 + 彩色渐变大按钮 + 悬停放大动画 + 骨架屏闪烁 + 浏览器默认滚动条 + HTML 模态弹窗当系统对话框用 + **HTML 模拟菜单/右键菜单/文件选择/窗口管理——Wails v3 明明有原生 API 却不用**。

---

## 1. 窗口外壳（Window Chrome）—— 最影响"原生感"的部分

**用 Wails v3 Window API（`@wailsio/runtime` 的 `Window` 模块）管理窗口行为，用 Frameless + 自绘标题栏融合工具栏，按平台分叉。** 这是和"网页 + 浏览器边框"区分开的第一要素。

- **Frameless 窗口**：`WebviewWindowOptions{ Frameless: true }`，自绘标题栏区域，用 CSS `--wails-draggable: drag` 标记可拖拽区，按钮/输入框区域设 `--wails-draggable: no-drag`。
- **macOS**：用 `Mac.InvisibleTitleBarHeight`（透明标题栏 + 保留红绿灯交通灯按钮）而非纯 frameless，让红绿灯由系统绘制并正确内嵌；标题栏高度与工具栏合一（现代 macOS 应用的"统一标题栏 + 工具栏"观感）。可选 `Mac.Backdrop` 半透明材质（vibrancy）让侧边栏有原生毛玻璃质感。
- **Windows**：`WindowsWindow{ BackdropType: application.Mica }`（Win11 Mica 材质），自绘最小化/最大化/关闭按钮**靠右**，尺寸/间距对齐 Win11 规范；保留 Win11 贴靠布局（鼠标悬停最大化按钮的 Snap Layouts）。
- **标题栏配色**：用 `CustomTheme` 设定明暗双套标题栏/边框/文字色，跟随应用主题，避免标题栏与内容区割裂。
- **细节**：标题栏底部 1px 分隔线（非阴影）；窗口圆角交给系统（不要自己加大圆角）。

> ⚠️ Frameless 的代价：窗口控制、拖拽、双击标题栏最大化、各平台交通灯/caption 按钮位置都要自己处理且分平台。这是真实工作量。

**窗口相关的 Wails v3 API**：`import { Window } from '@wailsio/runtime'`。窗口生命周期、关闭拦截、缩放、置顶等操作都通过此模块，不用 `window.open()`、`window.close()`、`beforeunload` 等浏览器 API。

---

## 2. 排版与密度（Typography & Density）—— 第二影响要素

桌面专业工具是**信息密集**的，不是 SaaS 落地页。

- **字体：用系统字体栈，不加载 Web 字体。**
  ```
  font-family: system-ui, -apple-system, "Segoe UI", "PingFang SC",
               "Microsoft YaHei", "Helvetica Neue", sans-serif;
  ```
  代码/SQL/数据单元格用**系统等宽字体**：`ui-monospace, "SF Mono", "Cascadia Code", "JetBrains Mono", Menlo, Consolas, monospace`。
- **字号偏小（桌面尺度）**：正文 **12–13px**（不是 Web 的 14–16px）；次要信息 11–12px；标题克制，不要大号 hero 字。
- **行高紧凑**：1.3–1.45，不要 Web 常见的 1.6+。
- **高密度间距**：
  - 表格行高 **24–28px**（数据浏览态），紧凑模式可到 22px。
  - 工具栏高度 32–36px，按钮内边距小。
  - 树节点行高 24–26px。
  - 表单控件高度 28–30px（small/medium 之间）。
- **Naive UI 落地**：通过 `themeOverrides` 全局下调 `common.fontSize`、各组件 `height`/`padding`，把默认的"网页尺寸"压到桌面尺度。优先用各组件的 `size="small"`。

---

## 3. 控件与视觉（Controls & Visuals）

- **按钮**：克制、小尺寸、低饱和。**禁止**彩色渐变大 CTA、阴影按钮、悬停放大/位移动画。主操作用细微的实色/描边区分即可。
- **圆角小**：控件圆角 **3–4px**（Naive `common.borderRadius` 调小），不要 8px+ 的"卡片感"大圆角。
- **边框是发丝线**：1px（视网膜屏下视觉 0.5px）的 hairline 分隔，少用投影。面板之间用边框分隔而非大留白 + 阴影浮层。
- **颜色中性化**：以系统灰阶为主，强调色尽量贴合系统（macOS 可取系统强调色，Windows 取 accent color）。不要堆品牌色。语义色（成功/警告/错误）仅用于状态，不用于装饰。
- **图标**：用统一线性图标集（如 Tiny RDM 用的 IconPark，或 Lucide/Tabler），尺寸 14–16px，风格一致，不要 emoji 当功能图标。
- **布局范式**：左侧连接/对象树（可折叠侧栏）+ 主区标签页 + 底部状态栏。这是桌面 DB 工具的标准三段式，不要做成"网页 dashboard 卡片流"。
- **状态栏**：底部常驻细条（行数、执行耗时、当前连接、字符集、行列位置），高度 22–24px——这是桌面工具的标志性元素。

---

## 4. 交互（Interaction）—— 行为也要原生

- **右键上下文菜单**：用 **Wails 原生上下文菜单**（Go 侧 `application.NewContextMenu(id)` 注册，CSS `--custom-contextmenu: <id>` 绑定元素，Go 侧 `OnClick` Emit 事件，前端监听处理）——**禁止** HTML 浮层菜单模拟右键。对象树节点、结果集单元格/行、标签页都应有原生右键菜单。
- **原生应用菜单**：用 Go 侧 `app.NewMenu()` + `AddSubmenu()` 建顶层菜单（File/Edit/View/Query/Window/Help），macOS 显示在系统菜单栏，Windows/Linux 用 `UseApplicationMenu` 显示在窗口。用预定义 role 生成平台标准项（关于、退出、最小化、Cut/Copy/Paste 等）。**严禁** HTML/CSS 模拟菜单栏或菜单项。
- **键盘快捷键**：快捷键通过菜单项 `SetAccelerator()` 注册，使其同时出现在菜单标签右侧且全局生效。前端 `keydown` 监听**只做辅助/补充**，不作为快捷键的唯一注册方式。
- **菜单事件通信**：Go 菜单项 `OnClick` → `app.EmitEvent()` → 前端 `Events.On()` 接收。菜单业务逻辑在前端处理（靠近编辑器状态），Go 侧只负责菜单定义和加速器注册。

- **Go ↔ 前端事件走 Wails 事件系统**：
  - Go → 前端：`app.EmitEvent(name, data)` → 前端 `Events.On(name, cb)`（封装在 `api/events.ts`）。
  - 前端 → Go：通过 Wails 绑定方法调用（Service 方法），**不用事件代替 RPC**。
  - 事件名用 `ctx:` 前缀（如 `ctx:grid-data-changed`）避免与 Wails 内部事件冲突。
  - 禁止用 WebSocket、轮询、自定义 HTTP 端点替代 Wails 事件系统。
- **标准桌面手势**：双击对象树表名 = 打开数据；双击标签页空白 = 新建查询；表头拖拽调整列宽、点击排序；单元格双击进入编辑。
- **光标规范**：只有真正的超链接才用 `cursor: pointer`；按钮/可点区域保持默认箭头光标（Web 习惯里到处 pointer 是典型"网页味"）。
- **选择行为**：支持 Shift/Ctrl 多选行、Ctrl/Cmd+A 全选、复制选区（制表符分隔，可直接粘进 Excel）。

---

## 5. 系统对话框与文件操作

- **一切系统级交互走 Wails 原生对话框（`import { Dialogs } from '@wailsio/runtime'`），不用 HTML 模拟：**
  - 打开/保存文件（导入导出选路径）→ `Dialogs.OpenFile()` / `Dialogs.SaveFile()`（带 `Filters` 文件类型过滤）。
  - 选目录 → `Dialogs.OpenDirectory()`。
  - 确认/警告/错误（如关闭未保存、删除连接、清空表、应用 ALTER）→ `Dialogs.Info(title, msg)` / `Dialogs.Warning(title, msg, buttons)` / `Dialogs.Error(title, msg, buttons)`。
- **禁止使用：** HTML `<input type="file">`、`window.confirm()`/`alert()`、Naive UI 的 NModal/NDrawer 模拟确认弹窗。
- **应用内的轻量交互**（如连接编辑表单、字段属性）可用 Naive UI 的 modal/drawer，但要：尺寸紧凑、无大圆角、有标题栏式头部、按 ESC 关闭、按钮靠右且符合平台顺序（macOS 主按钮在右，Windows 在左——可按平台调整）。macOS 上连接配置等可用 Wails 的 `AttachModal` 呈现为原生 Sheet 抽屉。

---

## 6. 滚动、动画、反馈

- **滚动条**：用细的、悬停才明显的原生风格滚动条（macOS overlay 风格 / Windows 细条），**不要**浏览器默认粗滚动条，也不要重度自定义的"网页花式滚动条"。
- **动画极简**：过渡 ≤120ms，仅用于必要的状态变化（展开/折叠、菜单出现）。**禁止**入场动画、卡片悬停放大、视差、骨架屏 shimmer。本地操作要"即时"，桌面用户期待零延迟。
- **加载反馈**：本地快操作（<200ms）不显示任何 loading；长操作（查询/导出）用底部状态栏文字 + 进度，而非全屏遮罩 spinner。
- **无布局抖动**：用 Hidden→骨架就绪→`Show()`（见 ARCHITECTURE 多窗口）消除冷启动白屏；窗口背景色 `BackgroundColour` 设为主题色，避免加载前的白闪。

---

## 7. 主题与平台适配

- **跟随系统明暗**：监听 `prefers-color-scheme` 切换 Naive UI 的 `darkTheme`，同时用 `CustomTheme` 同步标题栏配色。
  - ⚠️ **已知缺口**：Wails v3 当前**没有**统一的主题读取/设置/订阅 API（官方 issue #4665 仍未实现）。需在 `wailsbridge` 自封装：前端用 `matchMedia('(prefers-color-scheme: dark)')`，Go 侧标题栏配色用 `CustomTheme` 双套色；系统主题变化的实时订阅在 v3 补齐前用 `matchMedia` change 事件兜底。
- **平台分叉而非统一一套**：不要追求三平台像素级一致。macOS 用交通灯 + vibrancy + 右侧主按钮；Windows 用 caption 按钮 + Mica + 左侧主按钮。一套"放之四海皆准的网页 UI"恰恰是 Web 感的根源。
- **强调色**：尽量取系统强调色，退化时用一个克制的中性蓝。

---

## 8. 可访问性（原生应用也要有）

- 键盘可达：Tab 顺序合理，所有操作可纯键盘完成。
- **保留焦点指示**：键盘聚焦时要有清晰 focus ring（原生应用同样有，别为了"干净"删掉）。
- 对比度达标；明暗两套都要测。

---

## 9. Naive UI themeOverrides 起步示例

```ts
// 仅示意方向：把默认 Web 尺寸压到桌面尺度，圆角调小，字体用系统栈
const themeOverrides = {
  common: {
    fontSize: '13px',
    fontFamily: 'system-ui, -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
    fontFamilyMono: 'ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace',
    borderRadius: '3px',
    heightSmall: '24px',
    heightMedium: '28px',
  },
  DataTable: { thPaddingSmall: '4px 8px', tdPaddingSmall: '3px 8px' },
  Tree:      { nodeHeight: '26px' },
  // ... 各组件按 §2 密度继续压
}
```

---

## 10. 自检清单（写每个界面前过一遍）

- [ ] 字号是不是桌面尺度（12–13px）、用系统字体栈？
- [ ] 密度够不够紧凑（表格行 24–28px、控件 28–30px）？
- [ ] 圆角是不是 3–4px、边框是不是发丝线、有没有多余阴影？
- [ ] 按钮是不是克制（无渐变大 CTA、无悬停放大动画）？
- [ ] **涉及窗口/菜单/事件/对话框吗？——Wails v3 有原生 API 吗？有就用，没有才考虑 Web 方案。**
- [ ] 右键有没有用 **Wails 原生上下文菜单**（`application.NewContextMenu` + `--custom-contextmenu`）？
- [ ] 确认/文件选择有没有用 **Wails 原生对话框**（`Dialogs.*`）？
- [ ] 顶层菜单 + 快捷键有没有走 **Wails 原生应用菜单**（`app.NewMenu` + `SetAccelerator`）？
- [ ] Go ↔ 前端通信有没有走 **Wails 事件系统**（`EmitEvent` / `Events.On`）？
- [ ] 滚动条/动画有没有"网页味"？本地操作是不是即时无 spinner？
- [ ] 明暗主题 + 标题栏配色有没有跟随系统、按平台分叉？
- [ ] 截图发出去，像桌面软件还是网页？
