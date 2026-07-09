# CLAUDE.md

> Claude Code 在本仓库工作时**必须先读本文件**。这里是工作规约（约束 + 约定 + 命令），不是功能说明。功能与设计见 `ARCHITECTURE.md`。

## 项目一句话

基于 **Wails v3（Go 后端 + Vue 3/TS 前端 + 原生 WebView）** 的跨平台数据库管理工具（类 Navicat/DBeaver/TablePlus）。当前只支持 **MySQL**；其他数据库通过**编译期注册的 Go 接口插件**扩展。

## 技术栈（不要擅自替换）

- **后端**：Go 1.22+，Wails v3 **锁定 `v3.0.0-alpha2.106`**（go.mod 与 CLI 都钉死，禁止 `@latest`）
- **前端**：Vue 3（Composition API + `<script setup>`）+ TypeScript + Vite，状态用 Pinia
- **多语言**：`vue-i18n` v9（前端）+ `internal/i18n` 消息目录（Go 原生菜单/对话框）—— 规约见下方「多语言（i18n）」
- **UI 框架**：Naive UI（TS-first、JS 主题系统、自带 `n-tree` 虚拟化树与表单）—— 同类对标 Tiny RDM（Wails+Vue3 数据库 GUI）即用此栈
- **SQL 编辑器**：CodeMirror 6（`@codemirror/lang-sql`，Vue 下用 `vue-codemirror` 薄封装）—— **不用 Monaco**
- **结果表格**：`@tanstack/vue-table` + `@tanstack/vue-virtual` —— **不用 AG Grid**（除非任务单明确要求）
- **MySQL 驱动**：`github.com/go-sql-driver/mysql`
- **本地配置存储**：`modernc.org/sqlite`（纯 Go，**禁止引入 CGO SQLite 驱动**）
- **凭据存储**：`github.com/zalando/go-keyring`
- **Excel 导出**：`github.com/xuri/excelize/v2`（用 StreamWriter）
- **SSH 隧道**：`golang.org/x/crypto/ssh`

## 常用命令

```bash
wails3 dev                 # 热重载开发
wails3 build               # 生产构建（单二进制）
wails3 generate bindings -ts -names   # 生成 TS 绑定（改了 Service 公共方法后必跑；-ts 输出 .ts 文件、-names 保留字段名）
task test                  # 跑 Go 单元测试 + 契约测试
task test:integration      # testcontainers 起真实 MySQL 的集成测试（需 Docker）
```

> 改动任何 Service 的**公共方法签名**后，必须运行 `wails3 generate bindings -ts -names` 重新生成前端绑定，否则前端类型会过期。

## 目录结构（新增代码放对位置）

```
wailsbridge/        # 防腐层：所有 application.* 调用只能在这里出现
internal/
  dbdriver/         # 统一抽象接口（Driver/Connection/Querier/Metadata/Dialect/Editor）—— 接口是承重墙，改前先看 ARCHITECTURE.md
  registry/         # 编译期驱动注册表
  core/             # 连接/会话管理器、查询引擎、动态扫描器、元数据、DDL 生成
  storage/          # 连接配置(SQLite)、keyring 凭据
  tunnel/           # SSH 隧道（含 pgx LookupFunc 处理）
  services/         # Wails Service：Connection/Query/Metadata/Edit/Transfer/Settings
plugins/
  plugins_all.go    # build-tag 控制的匿名导入聚合
  mysqldrv/         # 目前唯一插件
frontend/src/
  api/              # 前端防腐层：封装生成的绑定 + 事件，组件只调 api/ 不直接 import bindings/
  components/       # 编辑器/表格/对象树/连接表单
```

## 铁律（违反会埋雷，务必遵守）

1. **隔离 Wails API**：Go 侧所有 `application.*` 调用只允许出现在 `wailsbridge/`；前端组件只能调 `frontend/src/api/`，禁止直接 import 生成的 `bindings/`。理由：alpha 期 breaking change 时只改一处。
2. **Service 生命周期方法用 v3 命名**：`ServiceName() / ServiceStartup(ctx, opts) error / ServiceShutdown() error`。**绝不要用 v2 的 `OnStartup`/`OnShutdown`**（训练数据里大量 v2 示例是错的）。
3. **长任务必须吃 ctx**：Service 方法首参 `context.Context`，查询一律 `QueryContext/ExecContext(ctx, ...)`。前端取消 promise → ctx 取消 → 查询中断。禁止写不可取消的阻塞查询。
4. **SQL 一律参数化**：永远用占位符 + args，**禁止字符串拼接用户值**。表数据的 UPDATE/DELETE 优先基于主键/唯一键；探测不到唯一键时降级为**整行匹配**（WHERE 用全部列原值，NULL 用 `IS NULL`，对齐 dbx），仅当列数 ≥2 才启用、否则标记只读。整行匹配的 WHERE 列由前端（`BrowseResult.KeylessEditable` / 各表格组件的 `idCols`）决定；驱动的 `Build*` 只按传入的标识列 map 构造 SQL，**空 map 仍要拒绝**（守住最后一道防线）。注意：整行完全相同的两行会被一并命中（不加 LIMIT，与 dbx 一致），UI 需向用户提示。
5. **大结果集不许一次性序列化**：后端分批 fetch（`ResultSet.Next(batch)`），行数据用 `[][]any` 数组（不是 `map[string]any`），列元数据单独传一次。大导出走**流式写文件**，不经 IPC 传给前端。
6. **抽象层不绑定 `database/sql` 类型**：`dbdriver` 接口用自定义 `ResultSet/ColumnMeta`，这样 MySQL 插件内部可用 `*sql.DB`、未来 PG 插件可用 pgx 原生。改接口前先读 ARCHITECTURE.md 的契约说明。
7. **驱动靠 `init()` 注册**：新驱动在自己包的 `init()` 里 `registry.Register(...)`，并在 `plugins/plugins_all.go` 匿名导入。不要写运行时动态加载（go plugin / Goja），那不是本项目主线。
8. **密码绝不明文落盘**：连接配置（host/port/user/options）存 SQLite，密码只进 keyring。代码评审看到明文密码持久化直接拒绝。
9. **多窗口并发隔离**：事务/独占操作走会话管理器分离的独立连接并绑定窗口 ID，事务期间该物理连接不被其他窗口借调。不要把未提交事务放在共享连接池上裸跑。
10. **平台优先级**：只保证 Windows + macOS。Linux 锁 GTK3 栈（`-tags gtk3`），不为 GTK4 实验特性写代码。
11. **UI 必须向原生靠拢、去 Web 感**：这是桌面应用不是网页。写任何前端 UI 前先读 `UI_SPEC.md`。硬性要求：系统字体栈 + 桌面字号（12–13px）、高密度布局、小圆角发丝线、克制按钮无花哨动画；右键用 **Wails 原生上下文菜单**、文件/确认用 **Wails 原生对话框**、顶层菜单走 **原生应用菜单**，不要用 HTML 浮层模拟系统级交互。

## 多语言（i18n）
> 基线语言 **en-US**，首批双语 **en-US / zh-CN**，回退 en-US。用户偏好持久化在 `app_settings["ui.locale"]`，支持运行时切换、无需重启。加一门语言 = 新增一个前端 locale 文件 + Go 目录条目，别的不用动。

**第一原则：任何用户可见文案都必须走 i18n，禁止硬编码——中文和英文都不行。** 英文硬编码"看着没问题"，但切到中文不会变，等同漏译。

### 前端（vue-i18n）
- locale 文件：`frontend/src/i18n/locales/{en-US,zh-CN}.ts`。**两个文件的 namespace 结构必须完全一致**，新增 key 两边都加（CI 心智：en/zh key 数相等、无单边键）。
- 调用：组件模板用 `$t('ns.key')`（已开 `globalInjection`，无需引入）；`<script setup>` 与纯 `.ts`/store 用 `import { t } from '../i18n'`（相对路径按层级）。
- 命名空间：通用原子（确定/取消/保存/各类失败提示…）进 `common.*`；模块特有措辞进各自 namespace（`queryTab.*`/`tableBrowser.*`/`structure.*`…）。后端错误码映射进 `error.*`。
- 插值用**命名参数**：`t('ns.key', { name })`，文案写 `{name}` / `{error}` / `{n}`。
- **不译**（保持原样）：SQL 关键字与技术 token（EXPLAIN、CSV/Excel/JSON/SQL、ON UPDATE/ON DELETE、RESTRICT/CASCADE、BTREE/HASH/ASC/DESC）、品牌名（catdb）、键盘修饰符（Cmd/Ctrl）、Language 菜单里的本族名（English / 中文（简体））。

四个**必踩的坑**：
1. **原生对话框判定点哪个按钮**：Wails 的 `Dialogs.Warning/Error/Info` 只返回**被点按钮的 label 文本**（无 id/index），直接比对译文很脆。**统一走 `api/dialogs.ts` 的 `confirm()` helper**——给按钮 `{ value, label }`，helper 把返回的 label 映回稳定 `value`，调用处只比 `value`（`if (choice !== 'delete') …`），永不碰译文。别再裸调 `Dialogs.*` 做判定（`SaveFile`/`OpenFile` 返回路径的除外）。
2. **模块级 `const` 数组/对象含文案**（列定义、options、tooltip 表…）：必须改 `computed(() => …)` 才能随语言切换刷新；script 内引用 computed 用 `.value`，模板自动解包。纯函数（每次渲染被调用，如 `typeFormatFor`）里内联 `t()` 即响应式。
3. **文案内嵌 HTML 标记**（`<b>{{n}}</b>`）：用 `<i18n-t keypath="ns.key" tag="span">` + 具名 slot（`<template #foo>` 对应文案里的 `{foo}`），`<i18n-t>` 由插件全局注册。
4. **文件里已有局部变量名 `t`**（如 `const t = tab.value`、`for (const t of …)`）：i18n 导入要么别名 `import { t as tr }`，要么让 computed **返回 key**、模板 `$t(key)` 解析——别让两个 `t` 撞上。
- 已持久化/已渲染的字符串（tab 标题、已展开的树节点）按创建时的 locale 定型，不强求实时回译——可接受。

### Go 原生层
- 目录在 `internal/i18n`（纯 Go、locale 无关、en 基线 + zh，`T(loc,key)` 带回退）。新增条目两个 locale 都加，key 与菜单/对话框构建处对应。
- `wailsbridge` 用 `tr(key)`（内部）/ `Tr(key)`（导出给 services）按当前 locale 译；启动 `InitMenuLocale(stored)` 后再建菜单。
- 切换：`SettingsService.SetLocale` 持久化后调 `wailsbridge.SetMenuLocale` —— **右键菜单**直接 `RegisterContextMenus`（`ContextMenuManager.Add` 是带锁 map 写，任意 goroutine 安全）；**应用菜单**走 `application.InvokeAsync` 在主线程 `SetApplicationMenu`（原生 C 调用须主线程）。
- **Go 侧不要塞本地化文案**：用户可见的错误/状态从 service 返回**稳定 slug**（如 `fetch-failed`），前端映射 `error.*`；纯技术/日志错误保持英文。
- **驱动 `ConnectionSchema` 保持 locale 无关**：`Group` 用稳定 key（`general/advanced/ssl/ssh`），`Label`/`Help` 是英文基线；前端按字段 key 翻译并**回退驱动英文**（未来驱动无 i18n 条目也能显示）。

## 代码风格

- Go：标准 `gofmt`/`go vet`；错误用 `fmt.Errorf("...: %w", err)` 包装并传递；公共接口写文档注释。
- TS：严格模式；前端不放业务逻辑到组件里，数据访问统一走 `api/`。
- 提交前：`task test` 必须绿；改了驱动接口要让契约测试套件全过。
- 代码尽量简洁易读，不写多余的抽象和注释。
- **无关代码不要动**：只改问题涉及的代码，不顺手重构或格式化无关部分。

## 写代码前的自检清单

- [ ] 这段逻辑该放哪一层？（看上面目录结构）
- [ ] 涉及查询吗？带 ctx 了吗？参数化了吗？
- [ ] 涉及 Wails API 吗？是不是该走 wailsbridge / api 层？
- [ ] 改了 Service 公共方法吗？要重新生成 bindings 吗？
- [ ] 涉及新驱动吗？接口实现全了吗？契约测试过吗？
- [ ] 有新增/改动用户可见文案吗？走 i18n 了吗（中英都别硬编码）？en/zh 两个 locale 都加了吗？（看「多语言（i18n）」）

## 不确定时

- 有不确定的问题就问用户，不要自己猜
- 接口语义、数据流、设计取舍 → 读 `ARCHITECTURE.md`
- 任何前端外观/交互怎么做才"像原生" → 读 `UI_SPEC.md`
- Wails v3 API 细节 → 查 https://v3.wails.io/ ，**不要凭训练记忆**（v3 与 v2 差异大，记忆易错）