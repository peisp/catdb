# 使用 Wails v3 开发 MySQL 数据库管理工具：可行性报告与详细技术方案

## 执行摘要（可行性结论先行）

**结论：可行，但应将 Wails v3 视为"功能稳定但仍处 alpha"的框架，采用版本锁定 + 增量升级策略推进。** 截至 2026 年 6 月，Wails v3 仍处于 ALPHA 阶段，最新正式 alpha 为 v3.0.0-alpha.96（2026-05-25 发布，后续有 alpha.97/alpha.98 的 nightly 预发布），官方明确"目标是进入 Beta"，但**没有给出确定的 Beta/Stable 发布日期**。官方首页原文声明："Wails v3 is in ALPHA. The API is reasonably stable, and applications are running in production. We're refining documentation and tooling before the final release."（API 已相当稳定，已有应用在生产环境运行，发布前正在打磨文档与工具）。核心 API（应用、窗口、菜单、事件、文件对话框、Service 绑定）被官方标记为"Stable ✅ 可用于生产"，而部分高级窗口选项、平台特定特性、实验性特性被标记为"Unstable ⚠️"。

对一个数据库管理工具而言，Wails v3 的能力匹配度很高：它提供原生多窗口（适合多连接/多查询窗口）、系统托盘、基于 Service 的后端架构、类型安全的 Go↔前端绑定、内置事件系统、context 驱动的长任务取消、以及内置自动更新器（含 bsdiff 增量更新）。Go 后端天然适配数据库工具——database/sql 生态成熟，goroutine + context 模型完美匹配查询取消/超时/连接池需求。

**关键风险与缓解：**(1) alpha 期 breaking changes——锁定具体 alpha 版本到 go.mod，关注 changelog，集中封装 Wails API；(2) 大结果集性能——后端分页 + 前端虚拟滚动，避免一次性序列化巨型 JSON；(3) 跨平台 webview 差异（WebView2/WebKitGTK），尤其 Linux GTK3/GTK4 双栈过渡期。

用户已确定的**编译期注册插件架构**对此场景是最优选：所有数据库驱动编译进单一二进制，通过 Go interface 统一抽象，各驱动在 init()/显式 Register() 注册——这与 database/sql 的 driver 注册模式一致，零运行时加载开销、类型安全、易于交叉编译，避免了 go plugin（仅 Linux/macOS、版本脆弱）和 WASM（性能与系统调用受限）的复杂性。

**MVP 工作量估算：1-2 名全栈开发者约 16-22 人周**（详见路线图章节）。

---

## 一、Wails v3 现状与可行性评估

### 1.1 版本与发布状态

- **当前状态：ALPHA。** 最新正式 alpha 为 **v3.0.0-alpha.96（2026-05-25）**，引入 Garble 混淆支持（#4563）；其后 alpha.97（2026-05-31）、alpha.98（2026-06-03）为基于 master 的自动 nightly 预发布。pkg.go.dev 上 `github.com/wailsapp/wails/v3` 模块最新版本发布于 2026-06-03。安装：`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.96`。
- **版本号语义：** 官方采用语义化版本，Minor 为向后兼容新特性，Patch 为向后兼容修复，当前状态标注为"Alpha（API 稳定，持续打磨中）"。
- **路线图：** 官方 Roadmap 仅声明"Our goal is to reach Beta status"，是一份"living document（活文档）"，**未给出确定日期**。GitHub 讨论中维护者 leaanthony 表态"准备好就发布""已接近 Beta，只剩几个 issue 要解决，但不知道还要多久"。
- **API 稳定性分级（官方文档）：**
  - **Stable ✅（可生产使用）：** 核心应用 API、窗口管理、菜单系统、事件系统、文件对话框、Service 绑定。
  - **Unstable ⚠️（最终版前可能变化）：** 部分高级窗口选项、平台特定特性、实验性特性。
  - **弃用策略：** 弃用 API 在文档标注，提供迁移指南，保留 1 个 major 版本后移除。

### 1.2 v3 相对 v2 的关键新特性及对本项目的价值

| v3 新特性 | 说明 | 对数据库工具的价值 |
|---|---|---|
| **多窗口** | 每个窗口是一等对象，可动态创建/销毁，独立生命周期与事件 | 多连接/多查询独立窗口、独立的表结构设计器窗口 |
| **Services 架构** | 后端逻辑组织为独立 Go struct（"Service"），自动发现公共方法生成类型安全绑定，无需 v2 的 context 字段 | 将"连接服务/查询服务/元数据服务/导出服务"清晰分层，可单测 |
| **静态分析绑定生成** | `wails3 generate bindings` 用 AST 静态分析生成 TS 绑定，保留注释与参数名，速度快 | 强类型前端 API，重构安全 |
| **事件系统** | 类型化事件对象，分 user/application/window 三类，`app.Event.Emit/On` | 查询进度、长查询取消通知、导出进度推送 |
| **context 取消** | Service 方法首参可为 `context.Context`，前端可调用 promise 的 cancel 方法（或 `cancelOn` 绑定 AbortSignal）触发 Go context 取消 | 长查询取消的核心机制 |
| **系统托盘** | 富菜单、窗口附着、明暗图标自适应 | 后台常驻、快捷连接 |
| **内置自动更新器** | 支持自动检查/下载/安装，含 bsdiff 增量更新 | 客户端分发与升级 |
| **菜单/键绑定/对话框** | 原生应用菜单、上下文菜单、快捷键、文件/消息对话框 | SQL 文件打开保存、快捷键执行查询 |
| **透明构建系统** | 基于 Taskfile 的 CLI，所有步骤（图标、manifest、打包）可定制 | CI/CD 可控 |

通信性能特性（官方）：IPC 为**内存内通信，无网络端口、无 HTTP 开销**。官方 Changelog「v3.0.0-alpha.55 - 2026-01-02」记载：运行时 JSON 处理（方法绑定、事件、webview 请求、通知、kvstore）切换到 goccy/go-json，"improving performance by 21-63% and reducing memory allocations by 40-60%"。**注意：** 同期 changelog 另有一条 revert goccy/go-json 以修复 Windows 上的 panic 的记录——采用前应核实所锁定的 alpha 版本是否仍启用该优化。

### 1.3 已知限制与风险

- **平台 webview 依赖：** Windows 用 WebView2（Go 原生 loader，无需嵌入 dll）；macOS 用 WKWebView；Linux 用 WebKitGTK。**Linux 处于双栈过渡期：** 官方 Changelog 的明确措辞是"WIP: Add experimental WebKitGTK 6.0 / GTK4 support for Linux, available via -tags gtk4 (GTK3/WebKit2GTK 4.1 remains the default)"——即 **GTK3/WebKit2GTK 4.1 仍为默认**，GTK4 为实验性（`-tags gtk4`）。但另有 changelog 条目提到"after the GTK4 + WebKitGTK 6.0 stack was promoted to the default in alpha.93"，存在版本间表述不一致；2026 年多个版本仍在修复 GTK4 相关的事件、AppImage 打包、Wayland 崩溃问题。无论默认与否，**Linux 是目前最不稳定的平台面，建议显式锁定 GTK3 栈**。
- **远程 URL + IPC 限制：** 出于 WebKit/WebView2 安全边界，远程跨域 URL 默认无法调用 Go 方法；对桌面数据库工具应采用 `//go:embed` 嵌入编译期前端资源（标准做法）。
- **文档完整度：** 官方承认正在"打磨文档与工具"；部分指南存在不一致（如 Gin 指南中 `ServiceShutdown` 签名带 `ctx` 参数，与权威 godoc 的 `ServiceShutdown() error` 不符）。
- **打包：** Windows 支持 NSIS/MSI/MSIX；macOS 支持 .app/dmg、签名公证；Linux 支持 AppImage/deb/rpm/archlinux。2026 年 changelog 中 AppImage 打包仍有较多修复，说明 Linux 打包成熟度落后于 Win/Mac。

### 1.4 可行性结论与风险缓解建议

**可行。** 缓解措施：
1. **锁定版本：** go.mod 钉死到某个具体 alpha（如 alpha.96），CLI 用 `@v3.0.0-alpha.96`，避免 nightly 漂移。升级前先读 changelog。
2. **封装隔离层：** 在 Go 端建一个 `wailsbridge` 包封装所有 `application.*` 调用；前端建一个 `api/` 层封装生成的绑定。breaking change 时改一处。
3. **平台策略：** MVP 优先 Windows + macOS（webview 最成熟），Linux 作为次级目标并锁定 GTK3 栈（`-tags gtk3`）直到 GTK4 稳定。
4. **升级时机：** 进入 Beta 后再考虑大版本跟进；生产发布前做完整跨平台回归。
5. **关注 breaking changes：** 订阅 GitHub releases 与 v3 changelog；Service 生命周期方法签名在 alpha 期曾从 `Name`/`OnStartup`/`OnShutdown` 重命名为 `ServiceName`/`ServiceStartup`/`ServiceShutdown`，需警惕类似变动。

---

## 二、整体架构设计

### 2.1 分层架构（ASCII 图）

```
┌─────────────────────────────────────────────────────────────┐
│                     前端 (WebView, embed.FS)                   │
│  React 18 + TypeScript + Vite                                 │
│  ┌──────────────┬───────────────┬──────────────────────────┐ │
│  │ 连接管理 UI   │ SQL 编辑器     │ 结果集表格 / 对象树        │ │
│  │ (动态表单)    │ CodeMirror 6   │ TanStack Table+Virtual    │ │
│  └──────────────┴───────────────┴──────────────────────────┘ │
│  api/ 层：封装 wails3 生成的类型安全绑定 + 事件监听            │
└───────────────────────────┬───────────────────────────────────┘
                 Wails v3 IPC（内存内，无 HTTP；JSON 处理 via goccy/go-json）
                 方法调用 (Promise + cancel) / 事件推送 (Emit/On)
┌───────────────────────────┴───────────────────────────────────┐
│            Wails v3 绑定层 (application.Service)                │
│  ConnectionService / QueryService / MetadataService /          │
│  EditService / TransferService (导入导出) / SettingsService     │
└───────────────────────────┬───────────────────────────────────┘
┌───────────────────────────┴───────────────────────────────────┐
│                        Go 核心层                                │
│  连接池管理器 │ 查询执行引擎(ctx) │ 元数据服务 │ DDL生成器       │
│  本地存储(连接配置/SQLite/JSON) │ 凭据管理(go-keyring) │ SSH隧道  │
└───────────────────────────┬───────────────────────────────────┘
┌───────────────────────────┴───────────────────────────────────┐
│              数据库驱动插件层（编译期注册）                      │
│  registry ← Driver 接口实现                                    │
│  [MySQL插件✓MVP] [PostgreSQL插件] [SQLite插件] [...]            │
│  基于 database/sql（MySQL）或原生(pgx native 可选)              │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 前端技术选型建议

- **框架：React 18 + TypeScript。** 理由：生态最大、AG Grid/TanStack/CodeMirror 等关键组件 React 适配最佳、Wails 模板支持。Vue 3 亦可（对标项目 Tiny RDM 即 Vue3 + Naive UI）；若团队熟 Vue 可选 Vue。
- **SQL 编辑器：CodeMirror 6（强烈推荐，优于 Monaco）。** 关键依据：
  - **包体积：** CodeMirror 6 核心约 150-300KB，基础+常用扩展仍是 Monaco 的零头；Monaco 约 2-5MB（gzipped ~5MB）。Sourcegraph 官方博客《Migrating from Monaco Editor to CodeMirror》原文："just Monaco itself still amounted to a 2.4 MB download — which is 40% of all the JavaScript for our search page"，移除 Monaco 后"we've reduced our JavaScript download to 3.4MB: a 43% improvement just by swapping out a single dependency"。Replit 官方博客《Betting on CodeMirror》原文："monaco-editor and related libraries contributed a whopping 51.17 MB to our bundle size (5.01 MB when parsed + gzipped)"，而 CodeMirror"contributed a mere 8.23 MB (or 1.26 MB when parsed + gzipped) to our bundle"。
  - **多实例：** Monaco 有全局引用模型，同页多实例不同配置困难；CodeMirror 实例完全独立——对多标签查询编辑器很重要。
  - **业界实践：** 数据库管理工具（Prisma Studio、多个 SQL 客户端）普遍用 CodeMirror。
  - **取舍：** Monaco 开箱即用 IntelliSense 更强，但需 worker 配置、bundle 大；本项目自动补全需基于 DB 元数据自定义，CodeMirror 的可组合补全（CompletionSource）更合适。
- **结果集表格：TanStack Table + TanStack Virtual（推荐）或 AG Grid Community（备选）。**
  - **TanStack Table**：headless（仅逻辑无 UI），MIT，需自建 UI + 配合 TanStack Virtual 实现虚拟滚动；体积小（实际约 30KB），灵活。适合需要深度定制单元格编辑的数据库工具。
  - **AG Grid Community**：功能最全、虚拟滚动经实战检验（百万行客户端数据流畅），但体积大；部分高级特性（如范围复制粘贴、server-side row model）在 Enterprise 付费版。
  - **建议：** MVP 用 TanStack Table + Virtual（许可清晰、体积小、可控）；若后续需要 Excel 级交互可评估 AG Grid。虚拟滚动阈值：>1000 行建议启用，>5000 行必须启用。

### 2.3 Go↔前端通信机制（含 Wails v3 Service 绑定示例）

Wails v3 中后端逻辑组织为"Service"（普通 Go struct），在 `application.New` 时通过 `Options.Services` 注册，并用 `application.NewService` 包裹：

```go
package main

import (
	"context"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	app := application.New(application.Options{
		Name:        "DBTool",
		Description: "MySQL 数据库管理工具",
		Services: []application.Service{
			application.NewService(&QueryService{}),
			application.NewService(&ConnectionService{}),
			application.NewService(&MetadataService{}),
		},
	})
	// 也可在创建后用 app.RegisterService(...) 延迟注册（需注入 app 引用时常用）
	// app.RegisterService(application.NewService(NewExportService(app)))

	app.NewWebviewWindow() // 创建主窗口
	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

Service 可选实现生命周期方法（**经官方 godoc 确认的权威签名**）：

```go
// ServiceName() string                                                 —— 自定义服务名（默认用 struct 名）
// ServiceStartup(ctx context.Context, options application.ServiceOptions) error  —— 服务加载时调用（App.Run 时触发，非 New 时）
// ServiceShutdown() error                                              —— 服务卸载时调用（无 ctx 参数）

type QueryService struct {
	app *application.App
}

func (s *QueryService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get() // 获取 App 引用（也可用构造函数注入 *application.Application）
	return nil
}

// 公共方法自动生成类型安全 TS 绑定；首参 context.Context 由运行时注入，支持前端取消
func (s *QueryService) RunQuery(ctx context.Context, connID, sql string) (*QueryResult, error) {
	// ... 用 ctx 执行 QueryContext，前端取消 promise 即取消此 ctx
}
```

- **方法绑定：** 公共方法（首字母大写）自动生成 TS 绑定，调用返回 Promise；Go 端 `error` 返回值自动转为 JS 异常。
- **长任务取消：** 方法首参声明 `context.Context`，运行时自动注入；前端调用 promise 的特殊 `cancel` 方法（或绑定 `AbortSignal` via `cancelOn`）即可触发 Go context 取消，丢弃结果。这是查询取消的核心。
- **事件推送：** 后端 `func (em *EventManager) Emit(name string, data ...interface{})`，调用形如 `s.app.Event.Emit("query-progress", payload)`；前端 `On(name, cb)` 监听。`ServiceShutdown` 在 alpha 期由 `OnShutdown` 重命名而来——升级需留意。
- **大结果集传输策略（关键）：**
  1. **后端分页/分批 fetch：** 不要一次性把百万行序列化成 JSON。用 `LIMIT/OFFSET` 或 keyset 分页，或服务端游标按批（如每批 200-1000 行）`Emit` 给前端。
  2. **前端虚拟滚动：** 只渲染视口内的行。
  3. **避免巨型 JSON：** 大导出走流式写文件（见 §4.4），而非经 IPC 传给前端再保存。
  4. **类型化与紧凑化：** 列元数据单独传一次；行数据用 `[]any` 数组（而非 map）减少 JSON 体积。
  5. 利用 v3 内存内 IPC（无 HTTP 开销）。

### 2.4 本地数据存储方案

- **连接配置存储：推荐 SQLite（modernc.org/sqlite 纯 Go）**，存于 OS 标准用户目录（如 `os.UserConfigDir()`）。理由：结构化查询连接/分组、纯 Go 无 CGO 易交叉编译。轻量项目也可用 JSON/YAML 文件（Tiny RDM 用 yaml + `adrg/xdg` 定位目录）。**连接配置（host/port/user/options/分组）存 SQLite/文件，密码绝不明文落盘。**
- **密码安全存储：分层策略。**
  - **首选 OS 钥匙串：`github.com/zalando/go-keyring`**——macOS Keychain（经 `/usr/bin/security`）、Windows Credential Manager、Linux Secret Service（D-Bus，需 GNOME Keyring/libsecret）。API 极简：`keyring.Set(service, user, password)` / `Get` / `Delete`；测试可 `MockInit()`。
  - **降级方案（Linux 无 Secret Service 时）：** 用主密码派生密钥（如 Argon2id）+ AES-GCM 加密存本地文件。提供"主密码"开关。
  - **注意：** go-keyring 在 Linux 依赖 D-Bus Secret Service，headless/服务器环境可能不可用，需优雅降级。

---

## 三、插件化数据库驱动设计（编译期注册，核心章节）

### 3.1 设计理念与对标

本架构借鉴三个成熟实践：
- **database/sql 的 driver 注册模式：** 驱动在 `init()` 中 `sql.Register(name, driver)`，主程序匿名导入 `_ "github.com/go-sql-driver/mysql"` 即注册。
- **usql/dburl 的 dialect 抽象：** usql 用统一 `Driver` 结构描述各库的词法/注释风格/元数据读取器，通过 build tag（`most`/`all`/`no_<driver>`）控制编译哪些驱动。
- **DBeaver 的 Generic + 扩展继承模型：** Generic JDBC 框架（`GenericDataSourceProvider`/`GenericMetaModel`/`GenericSQLDialect`）提供元数据读取、SQL dialect 抽象、连接管理基类，各库扩展（`parent="generic"`）只覆盖差异化行为。

我们的 Go 版本：定义一组接口，各库实现并编译期注册，主程序通过 build tag 选择编译哪些插件。

### 3.2 核心接口定义（Go 代码示例）

```go
// package dbdriver —— 统一抽象层
package dbdriver

import (
	"context"
	"database/sql"
)

// Driver 是一个数据库类型的插件入口（如 mysql、postgres）。
type Driver interface {
	Name() string                          // 唯一标识，如 "mysql"
	Version() string                       // 插件版本
	ConnectionSchema() []ConnParamField    // 连接参数 schema，供前端动态渲染连接表单
	Capabilities() Capabilities            // 方言能力声明
	Dialect() Dialect                      // 方言对象
	Open(ctx context.Context, cfg ConnConfig) (Connection, error) // 建立连接
}

// ConnParamField 描述一个连接参数字段（前端据此渲染表单）。
type ConnParamField struct {
	Key      string   `json:"key"`      // 如 "host"
	Label    string   `json:"label"`    // 如 "主机"
	Type     string   `json:"type"`     // text|number|password|select|bool
	Default  string   `json:"default"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"` // select 类型可选值
	Group    string   `json:"group,omitempty"`   // "常规"|"SSL"|"SSH"
}

// Capabilities 声明方言能力，前端据此显隐功能。
type Capabilities struct {
	Schemas          bool `json:"schemas"`
	StoredProcedures bool `json:"storedProcedures"`
	Triggers         bool `json:"triggers"`
	Views            bool `json:"views"`
	Transactions     bool `json:"transactions"`
	ExplainPlan      bool `json:"explainPlan"`
}

// ConnConfig 连接配置（密码运行时从 keyring 注入，不持久化于此）。
type ConnConfig struct {
	Host, Port, User, Password, Database string
	Params    map[string]string // 驱动特定参数（charset、loc、tls 等）
	SSL       *SSLConfig
	SSHTunnel *SSHConfig
}

// Connection 表示一个已建立的连接（封装连接池）。
type Connection interface {
	Ping(ctx context.Context) error
	Close() error
	Querier() Querier
	Metadata() Metadata
	Editor() Editor
	Begin(ctx context.Context, opts *sql.TxOptions) (Tx, error) // 手动事务模式
}

// Querier 查询执行。
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (ExecResult, error) // 非查询语句
	Query(ctx context.Context, sql string, args ...any) (ResultSet, error) // 流式结果集
	Explain(ctx context.Context, sql string) (ResultSet, error)            // 执行计划
}

// ResultSet 支持流式分批读取，避免一次性载入内存。
type ResultSet interface {
	Columns() []ColumnMeta
	Next(batch int) (rows [][]any, done bool, err error) // 按批读取
	Close() error
}

// Metadata 元数据读取（库/schema/表/视图/列/索引/外键/存储过程）。
type Metadata interface {
	ListDatabases(ctx context.Context) ([]string, error)
	ListSchemas(ctx context.Context, db string) ([]string, error)
	ListTables(ctx context.Context, db, schema string) ([]TableInfo, error)
	ListViews(ctx context.Context, db, schema string) ([]ViewInfo, error)
	ListColumns(ctx context.Context, db, schema, table string) ([]ColumnMeta, error)
	ListIndexes(ctx context.Context, db, schema, table string) ([]IndexInfo, error)
	ListForeignKeys(ctx context.Context, db, schema, table string) ([]ForeignKeyInfo, error)
	ListRoutines(ctx context.Context, db, schema string) ([]RoutineInfo, error)
}

// Dialect 方言差异（引用规则、类型映射、分页、DDL 生成）。
type Dialect interface {
	QuoteIdentifier(name string) string                // MySQL `name` / PG "name"
	Paginate(baseSQL string, limit, offset int) string // LIMIT/OFFSET vs TOP vs ROWNUM
	MapType(nativeType string) LogicalType             // 原生类型 → 统一逻辑类型
	GenerateCreateTable(t TableSchema) (string, error) // 由表结构生成建表 DDL
}

// Editor 生成安全的增删改语句（基于主键/唯一键）。
type Editor interface {
	PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error)
	BuildInsert(table string, row map[string]any) (string, []any, error)
	BuildUpdate(table string, pk map[string]any, changes map[string]any) (string, []any, error)
	BuildDelete(table string, pk map[string]any) (string, []any, error)
}

type Tx interface {
	Querier
	Commit() error
	Rollback() error
}

type ExecResult struct {
	RowsAffected int64
	LastInsertID int64
}
```

### 3.3 注册机制（registry + init/Register + build tags）

```go
// package registry
package registry

import (
	"fmt"
	"sync"
	"yourapp/dbdriver"
)

var (
	mu      sync.RWMutex
	drivers = make(map[string]dbdriver.Driver)
)

// Register 由各驱动插件在 init() 中调用（模仿 database/sql）。
func Register(d dbdriver.Driver) {
	mu.Lock()
	defer mu.Unlock()
	if d == nil {
		panic("registry: nil driver")
	}
	name := d.Name()
	if _, dup := drivers[name]; dup {
		panic("registry: duplicate driver " + name)
	}
	drivers[name] = d
}

func Get(name string) (dbdriver.Driver, error) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("registry: unknown driver %q", name)
	}
	return d, nil
}

// List 返回所有已注册驱动（供前端渲染"新建连接"下拉）。
func List() []dbdriver.Driver {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]dbdriver.Driver, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d)
	}
	return out
}
```

MySQL 插件注册（`init()` 自动注册）：

```go
// package mysqldrv
package mysqldrv

import (
	"yourapp/dbdriver"
	"yourapp/registry"
)

type mysqlDriver struct{}

func (mysqlDriver) Name() string    { return "mysql" }
func (mysqlDriver) Version() string { return "1.0.0" }
// ... 实现其余接口方法 ...

func init() {
	registry.Register(mysqlDriver{})
}
```

主程序通过匿名导入选择编译哪些插件（编译期注册的精髓）：

```go
// plugins/plugins_all.go
import (
	_ "yourapp/plugins/mysqldrv"   // MVP 仅此一个
	// _ "yourapp/plugins/pgdrv"
	// _ "yourapp/plugins/sqlitedrv"
)
```

**用 build tags 控制编译哪些插件（可选，借鉴 usql）：**

```go
//go:build !no_mysql
// +build !no_mysql

package plugins
import _ "yourapp/plugins/mysqldrv"
```

编译时 `go build -tags "no_pg no_oracle"` 即可裁剪驱动，控制二进制体积与依赖（如避免引入 Oracle/CGO 依赖）。

### 3.4 MySQL 插件 MVP 实现要点

- **驱动：`github.com/go-sql-driver/mysql`**（database/sql 标准实现，社区事实标准）。
- **DSN 与连接参数（关键）：**
  - `parseTime=true`：将 DATE/DATETIME 扫描为 `time.Time`（注意：会使 time.Time 成为唯一可扫描类型，破坏 sql.RawBytes）。
  - `loc=<location>`：设置 time.Time 时区；MySQL 会话时区另用 `time_zone` 系统变量 DSN 参数设置。
  - **字符集：** 1.5 起默认 `utf8mb4_general_ci`；优先用 `collation` 参数（不发额外查询），`charset` 会发 `SET NAMES` 额外往返但支持 fallback（如 `charset=utf8mb4,utf8`）。
  - **超时：** `timeout`（拨号）、`readTimeout`、`writeTimeout` 为每连接 I/O 超时。
  - **TLS：** `tls=true|skip-verify|preferred|<name>`；自定义证书用 `mysql.RegisterTLSConfig`。
  - `maxAllowedPacket`：官方 README 原文"The default value is 64 MiB and should be adjusted to match the server settings. maxAllowedPacket=0 can be used to automatically fetch the max_allowed_packet variable from server on every connection"。
- **连接池：** 由 database/sql 管理，配置 `SetMaxOpenConns`/`SetMaxIdleConns`/`SetConnMaxLifetime`。GUI 工具建议 MaxOpen 适中（如 5-10），避免占满服务器连接。
- **context 取消：** 驱动支持 Go 1.8+ 的 context 查询超时/取消（`QueryContext`/`ExecContext`），直接对接前端取消机制。
- **元数据查询：用 `information_schema`：**
  - 库：`SHOW DATABASES` 或 `SELECT schema_name FROM information_schema.schemata`。
  - 表/视图：`information_schema.tables`（`table_type` 区分 BASE TABLE/VIEW）。
  - 列：`information_schema.columns`（含类型、可空、默认值、注释、主键标记）。
  - 索引：`SHOW INDEX FROM` 或 `information_schema.statistics`。
  - 外键：`information_schema.key_column_usage` + `referential_constraints`。
  - 存储过程/函数：`information_schema.routines`；触发器：`information_schema.triggers`。
  - 建表 DDL：`SHOW CREATE TABLE`（MySQL 直接给出完整 DDL，最省事）。
- **SSH 隧道（见 §4.6）：** 用 `mysql.RegisterDialContext` 注册自定义拨号函数走 SSH 连接。

### 3.5 平滑扩展到其他数据库

| 数据库 | 推荐驱动 | 接入方式 | 注意事项 |
|---|---|---|---|
| **PostgreSQL** | `jackc/pgx/v5` | 既可作 database/sql 驱动（stdlib 模式），也可用原生接口 | **原生模式性能显著更高**：pgx 用二进制协议、自动 prepared statement 缓存（官方文档称某些负载近 3 倍 QPS）、支持约 70 种 PG 类型（数组/JSONB/uuid 等）。建议 PG 插件用 pgx 原生 + pgxpool，而非 database/sql。 |
| **SQLite** | `modernc.org/sqlite`（纯 Go，注册名 `sqlite`）vs `mattn/go-sqlite3`（CGO，注册名 `sqlite3`） | database/sql | **优先 modernc.org/sqlite**：无 CGO，交叉编译简单（Wails 跨平台分发的关键）。性能代价：DataStation 基准（2022，OVH 裸金属，10 轮平均）原文"INSERTs are still twice as slow but SELECTs are at worst twice as slow and at best 10% as slow"。对 GUI 工具的小数据集足够。需极致性能再考虑 mattn（但 CGO 会复杂化 Wails 交叉编译）。 |
| **SQL Server** | `microsoft/go-mssqldb` | database/sql | 分页语法 `OFFSET ... FETCH`（2012+）；Dialect.Paginate 需差异化处理。 |
| **Oracle** | `godror`（CGO）或 `sijms/go-ora`（纯 Go） | database/sql | 纯 Go 的 go-ora 避免 CGO；分页历史上用 `ROWNUM`/`ROW_NUMBER()`，12c+ 支持 `OFFSET FETCH`。 |
| **ClickHouse** | `ClickHouse/clickhouse-go` | database/sql | 列式、无标准事务；Capabilities 标注 Transactions=false。 |
| **Redis / MongoDB（非关系型）** | `redis/go-redis`、`mongodb/mongo-go-driver` | **单独抽象，不强塞进 SQL 接口** | 这类不适合 Querier(SQL) 抽象。建议在 Driver 之上再分一层：`SQLDriver`（实现本文 Querier/Dialect）与 `KVDriver`/`DocDriver`（自有命令模型）。前端按 Capabilities/驱动类别渲染不同 UI。Tiny RDM 即专为 Redis 设计的 Wails 应用，证明 KV 类需独立 UI 范式。 |

### 3.6 与 database/sql 的关系（取舍）

- **统一基于 database/sql 的优点：** 接口统一、生态广、连接池/context 内建、各库实现工作量小。MVP（MySQL）与 SQLite/SQL Server 均走此路。
- **各驱动原生 API 的优点：** 性能与特性更强。**pgx 原生**是典型——二进制协议、PG 专有类型、语句缓存使其比 database/sql 路径快约 50%~近 2 倍（pgx 作者基准与官方文档）。database/sql 接口仅允许底层驱动返回 int64/float64/bool/[]byte/string/time.Time/nil，PG 数组等高级类型走文本格式无法发挥二进制优势——这是 pgx 原生接口存在的核心理由。
- **本方案取舍：** 抽象层接口（Querier/Metadata/Dialect）**不绑定 database/sql 类型**（用自定义 ResultSet/ColumnMeta），因此 MySQL 插件内部用 `*sql.DB`，PG 插件内部可用 pgx 原生 `*pgxpool.Pool`，对上层透明。这是兼顾通用性与性能的关键设计——抽象层定义"做什么"，各插件自由选择"怎么做"。

---

## 四、关键功能的技术实现方案

### 4.1 SQL 编辑器

- **选型：CodeMirror 6**（见 §2.2）。包 `@codemirror/lang-sql` 提供 SQL 语法高亮与基础补全。
- **语法高亮：** `@codemirror/lang-sql` 支持按方言（MySQL/PostgreSQL 等）配置关键字。
- **基于元数据的自动补全：** 实现自定义 `CompletionSource`——从后端 MetadataService 拉取当前连接的库/表/列名缓存，在编辑器中根据上下文（FROM 后补表名、`table.` 后补列名）提供补全。CodeMirror 的补全是函数式可注入的，适合此场景。
- **SQL 格式化：** 前端用 `sql-formatter`（npm，支持多方言）即可；若想后端统一可在 Go 端集成，但前端库更简单、即时。
- **多标签页：** 每个标签一个独立 CodeMirror EditorView 实例（CodeMirror 实例独立，无 Monaco 全局模型问题）。

### 4.2 查询执行

- **goroutine + context：** QueryService 方法首参 `context.Context`，每次查询起独立执行；用 `QueryContext(ctx, ...)`。前端取消 → context 取消 → 驱动中断查询。
- **查询超时：** `context.WithTimeout` 包装；超时值可在设置中配置。
- **多结果集：** 对支持的语句（如多语句、存储过程），用 `rows.NextResultSet()` 遍历。需 DSN `multiStatements=true`（注意 SQL 注入风险，仅在用户显式多语句执行时启用）。
- **事务模式（自动提交开关）：** 提供 UI 开关。关闭自动提交时，QueryService 用 `Begin` 开启 Tx，后续查询走同一 Tx，用户手动 Commit/Rollback。
- **执行计划：** MySQL `EXPLAIN`/`EXPLAIN FORMAT=JSON`/`EXPLAIN ANALYZE`，结果以表格或树形展示。Capabilities.ExplainPlan 控制按钮显隐。

### 4.3 大结果集处理

- **服务端分批 fetch：** ResultSet.Next(batch) 按批读取（如每批 500 行），避免一次性 `rows.Scan` 全部。
- **两种范式：**
  1. **分页查询：** 用 Dialect.Paginate 加 LIMIT/OFFSET（或 keyset 分页避免大 OFFSET 性能问题），前端"下一页"。适合浏览。
  2. **流式 + 虚拟滚动：** 后端持有打开的 ResultSet，前端虚拟滚动滚到底时通过事件请求下一批，后端 Emit 增量行。适合"查看全部"。
- **内存控制：** 限制单次最大返回行数（可配置，如默认 10000 行预览）；超大结果引导用户用导出功能（流式到文件）。
- **前端虚拟滚动：** TanStack Virtual / AG Grid 虚拟化，DOM 只渲染视口行。

### 4.4 数据导入导出

- **CSV：** Go 标准库 `encoding/csv`，流式逐行写。
- **JSON：** `encoding/json` 的 `Encoder` 流式逐行/逐对象写（避免构建巨型内存对象）。
- **SQL dump：** 自行生成 `INSERT INTO ... VALUES (...)`（用 Dialect 引用标识符、转义值），可选含建表 DDL（`SHOW CREATE TABLE`）。
- **Excel：`github.com/qax-os/excelize`（导入名 `github.com/xuri/excelize/v2`）。** 关键：用其 **StreamWriter**（`NewStreamWriter` + `SetRow` + `Flush`）逐行写大数据集，不在内存构建整个工作表，显著降低内存。注意 excelize 非完全线程安全，并发需自行加锁。
- **大文件流式 + 进度事件：** 导出在 goroutine 中流式写文件，每写 N 行 `app.Event.Emit("export-progress", {done, total})`；前端进度条。导出走文件系统（配合 Wails 文件对话框选路径），**不经 IPC 传大数据**。

### 4.5 表数据在线编辑

- **安全 UPDATE/DELETE：** 必须基于主键/唯一键定位行（Editor.PrimaryKeys 探测）。BuildUpdate 用 `WHERE pk=?` 参数化，绝不字符串拼接值（防注入）。
- **乐观更新：** 前端先更新 UI，后端执行；失败则回滚 UI 并提示。可选乐观锁：UPDATE 的 WHERE 带原值（`WHERE pk=? AND col=?old`），受影响行数为 0 则提示"数据已被他人修改"。
- **无主键表处理：** 策略：(1) 用全列值匹配 `WHERE col1=? AND col2=? ...`（可能误伤多行，需 `LIMIT 1` 或警告）；(2) MySQL 特定存储引擎可用隐藏的 `_rowid`；(3) 最稳妥——检测到无唯一键时，将该表标记为只读并明确提示用户。

### 4.6 连接安全

- **SSH 隧道：`golang.org/x/crypto/ssh` + `mysql.RegisterDialContext`。** 模式：建立 `ssh.Dial("tcp", sshHost, config)` 得到 `*ssh.Client`，注册自定义网络：

```go
mysql.RegisterDialContext("mysql+ssh", func(ctx context.Context, addr string) (net.Conn, error) {
    return sshClient.Dial("tcp", addr) // 走 SSH 隧道
})
// DSN: user:pass@mysql+ssh(dbhost:3306)/dbname?parseTime=true
```

  SSH 认证支持密码（`ssh.Password`）、私钥（`ssh.PublicKeys(signer)`，`ssh.ParsePrivateKey`）、ssh-agent（`agent.NewClient`）。`HostKeyCallback` 生产应校验主机密钥（`ssh.FixedHostKey`），勿用 `InsecureIgnoreHostKey`。也可考虑现成库 `jfcote87/sshdb`（封装了 mysql/mssql/postgres 的隧道驱动，并提供 `DialContext`）。
- **SSL/TLS：** MySQL 用 `tls` DSN 参数 + `mysql.RegisterTLSConfig` 注册含 CA/客户端证书的 `*tls.Config`。连接表单 SSL 分组收集证书路径。

---

## 五、工程化与交付

### 5.1 项目目录结构（monorepo）

```
dbtool/
├── go.mod                     # 钉死 wails/v3 alpha 版本
├── Taskfile.yml               # wails3 构建编排
├── main.go                    # application.New + Services 注册 + 插件匿名导入
├── wailsbridge/               # 封装所有 application.* 调用（隔离 breaking change）
├── internal/
│   ├── dbdriver/              # 统一抽象接口（Driver/Connection/Querier/...）
│   ├── registry/              # 编译期注册表
│   ├── core/                  # 连接池管理、查询引擎、元数据服务、DDL 生成
│   ├── storage/               # 连接配置(SQLite)、go-keyring 凭据
│   ├── tunnel/                # SSH 隧道
│   └── services/              # Wails Service：Connection/Query/Metadata/Edit/Transfer
├── plugins/
│   ├── plugins_all.go         # build-tag 控制的匿名导入聚合
│   ├── mysqldrv/              # MVP
│   ├── pgdrv/                 # 后续
│   └── sqlitedrv/             # 后续
├── frontend/
│   ├── src/
│   │   ├── api/               # 封装生成的绑定 + 事件
│   │   ├── components/        # 编辑器/表格/对象树/连接表单
│   │   └── ...
│   ├── bindings/              # wails3 generate bindings 输出
│   └── package.json
└── build/                     # 图标、manifest、平台打包资源
```

### 5.2 构建与打包

- **CLI：** `wails3 dev`（热重载开发）、`wails3 build`（生产构建）、`wails3 generate bindings`（生成 TS 绑定）。默认用 **Taskfile** 编排（也可用 make）。
- **单一二进制：** Go + 前端资源（`//go:embed`）打包成单可执行文件，除系统 webview 外无外部依赖；支持交叉编译。
- **平台打包：**
  - **Windows：** NSIS 安装器（默认开启 HiDPI）、MSI、MSIX。
  - **macOS：** `.app` bundle → dmg；签名公证（文档有 Code Signing 指南）；dev 模式会 ad-hoc 签名以启用部分 macOS API。
  - **Linux：** `wails3 tool package` 生成 deb/rpm/archlinux；`wails3 generate appimage` 生成 AppImage（注意 2026 年仍在修复 GTK4 下的 AppImage 问题，建议锁 GTK3 栈）。
- **CI/CD（GitHub Actions）：** 官方有跨平台构建指南。建议矩阵构建 windows/macos/linux，缓存 Go module 与 node_modules（注意：曾有因 node_modules 被 go-task 全量校验导致 20-30 分钟卡顿的 bug，alpha.72 修复——确认 sources 配置排除 node_modules）。

### 5.3 自动更新方案

- **Wails v3 内置更新器：** 官方提供内置更新系统，支持自动检查/下载/安装，并支持 **bsdiff 增量更新（patch）** 以最小化下载体积。这是 v3 相对 v2 的重要补强（v2 时代靠社区方案如 minio/selfupdate 或 sidecar 进程）。
- **建议：** MVP 用内置更新器对接 GitHub Releases；发布时为近几个版本生成 bsdiff patch。

### 5.4 测试策略

- **Go 单元测试：** 核心层（Dialect 类型映射、Editor 语句生成、分页 SQL 生成、DSN 构建）纯逻辑可大量单测。
- **接口契约测试（重要）：** 对 `dbdriver.Driver` 接口编写**统一契约测试套件**，任何新驱动插件都跑同一套测试（连接/Ping/查询/元数据/编辑/取消），保证接口语义一致——这是插件架构质量的关键保障，借鉴 database/sql 的兼容性测试思路。
- **集成测试：`testcontainers-go/modules/mysql`** 起真实 MySQL 容器跑端到端测试：`mysql.Run(ctx, "mysql:8.0")`，支持 `WithDatabase/WithUsername/WithPassword`、`WithScripts` 注入初始化 SQL、`ConnectionString()` 获取 DSN。CI 中可用（注意 Ryuk 清理或设 `TESTCONTAINERS_RYUK_DISABLED`）。
- **前端：** 组件测试（Vitest）+ 关键交互 E2E。

---

## 六、MVP 路线图与工作量估算

假设 **1-2 名全栈开发者**，MySQL-only MVP。工作量为粗略人周估算（含联调与基本测试）。

| 里程碑 | 范围 | 估算（人周） |
|---|---|---|
| **M0 骨架** | Wails v3 项目脚手架、前端框架(React+Vite+TS)接入、CI 雏形、wailsbridge/api 隔离层、抽象接口与 registry 定义 | 2-3 |
| **M1 连接管理** | 连接 CRUD/分组、动态连接表单(ConnParamField 驱动)、go-keyring 凭据存储、SQLite 配置存储、MySQL 插件连接/Ping、SSL/SSH 隧道 | 3-4 |
| **M2 查询与结果集** | CodeMirror 6 编辑器(高亮/多标签)、查询执行(ctx 取消/超时)、结果集分批/分页、前端虚拟滚动表格、EXPLAIN | 4-5 |
| **M3 元数据浏览与表编辑** | 对象树(库/表/视图/存储过程/触发器/索引)、information_schema 元数据、SHOW CREATE TABLE/DDL 查看、表数据在线增删改(主键探测/安全语句)、自动补全(基于元数据) | 4-6 |
| **M4 导入导出与打磨** | CSV/JSON/SQL dump/Excel(excelize 流式) 导入导出 + 进度事件、用户与权限查看、打包(Win/Mac)、自动更新器接入、契约测试 + testcontainers 集成测试、UI 打磨 | 3-4 |
| **合计** | | **16-22 人周**（双人并行历时约 8-12 周；单人约 16-22 周） |

注：Linux 完整支持、PostgreSQL/SQLite 等第二/三个插件、AG Grid 升级等不计入 MVP，作为后续迭代。

---

## 七、风险与应对

| 风险 | 等级 | 应对 |
|---|---|---|
| **Wails v3 alpha breaking changes** | 中高 | 锁定 alpha 版本；wailsbridge 隔离层；订阅 changelog；Service 生命周期方法已有重命名先例（`OnStartup`→`ServiceStartup`），升级前回归 |
| **Beta/Stable 无确定日期** | 中 | 不依赖未发布特性；用已标 Stable 的 API；生产发布基于锁定的 alpha |
| **大结果集性能/内存** | 高 | 后端分批 fetch + 分页/keyset；前端虚拟滚动；限制预览行数；大数据走流式导出；行数据数组化减小 JSON |
| **跨平台 webview 差异** | 中 | MVP 优先 Win/Mac；Linux 锁 GTK3 栈；针对 WebKitGTK/WebView2 做兼容性测试 |
| **Linux 打包成熟度** | 中 | 锁 GTK3 的 AppImage；deb/rpm 为主；关注 AppImage 相关修复 |
| **密码存储跨平台** | 中 | go-keyring 优先；Linux 无 Secret Service 时降级到主密码 + AES-GCM 加密文件 |
| **CGO 交叉编译复杂度** | 中 | SQLite 用纯 Go modernc；尽量避免 CGO 驱动；必须用时单独 CI 任务 |
| **SQL 注入/数据误改** | 高 | 全程参数化；表编辑必须基于主键；无主键表只读；多语句执行需显式开启并警告 |
| **无主键表编辑** | 中 | 检测无唯一键则标记只读并提示 |
| **excelize 并发安全** | 低 | 导出加锁或单 goroutine 串行写 |

---

## 八、建议（决策导向）

**分阶段落地建议与触发再评估的阈值：**

1. **立即可做（第 0-1 周）：** 用 `wails3 init` 起 React+TS 模板，锁定 `v3.0.0-alpha.96`，先跑通一个 Service 方法 + 一个事件推送的最小闭环，验证团队对 v3 API 的掌握。**门槛：** 若热重载/绑定生成/打包在目标平台跑不通，先解决工具链再继续。
2. **优先级：** 先把 §3 的抽象接口 + registry + MySQL 插件 + 契约测试搭好（这是整个架构的承重墙），再做 UI。**理由：** 接口定型后前后端可并行。
3. **前端组件锁定：** CodeMirror 6 + TanStack Table/Virtual（而非 Monaco + AG Grid），除非出现"需要 Excel 级范围操作"的明确需求——届时再评估 AG Grid（注意 Enterprise 许可成本）。
4. **平台节奏：** Win/Mac 先行并发布内测；Linux 待 GTK4 栈稳定（关注 changelog 中 AppImage/Wayland 修复收敛）再投入完整精力。
5. **何时升级 Wails：** 仅在 (a) 进入 Beta，或 (b) 出现影响本项目的关键修复时升级，且每次升级走完整跨平台回归。
6. **何时引入第二个数据库：** MySQL MVP 上线且契约测试套件稳定后，再加 PostgreSQL（用 pgx 原生验证抽象层"插件自选实现"的设计成立）。

---

## 九、参考资料链接

**Wails v3 官方：**
- 首页与状态：https://v3.wails.io/ ；https://v3.wails.io/status/
- What's New / 架构：https://v3.wails.io/whats-new/ ；https://v3.wails.io/concepts/architecture/
- Bindings & Services：https://v3.wails.io/features/bindings/services/ ；事件 API：https://v3alpha.wails.io/reference/events/ ；Application 参考：https://v3alpha.wails.io/reference/application/
- 自动更新：https://v3alpha.wails.io/guides/distribution/auto-updates/
- Changelog：https://v3.wails.io/changelog/ ；GitHub releases：https://github.com/wailsapp/wails/releases
- 迁移指南 v2→v3：https://v3alpha.wails.io/migration/v2-to-v3/

**数据库驱动：**
- go-sql-driver/mysql：https://github.com/go-sql-driver/mysql ；DSN/RegisterDialContext：https://pkg.go.dev/github.com/go-sql-driver/mysql
- pgx：https://pkg.go.dev/github.com/jackc/pgx ；性能基准：https://github.com/jackc/go_db_bench
- modernc.org/sqlite vs mattn 基准：https://datastation.multiprocess.io/blog/2022-05-12-sqlite-in-go-with-and-without-cgo.html
- usql dialect 抽象：https://pkg.go.dev/github.com/xo/usql/drivers
- Go SQL 驱动 Wiki：https://go.dev/wiki/SQLDrivers

**前端组件：**
- CodeMirror vs Monaco：https://sourcegraph.com/blog/migrating-monaco-codemirror ；https://blog.replit.com/codemirror
- TanStack Table vs AG Grid：https://tanstack.com/table/v8/docs/enterprise/ag-grid

**支撑库：**
- go-keyring：https://github.com/zalando/go-keyring
- excelize（流式）：https://github.com/qax-os/excelize
- testcontainers-go MySQL：https://golang.testcontainers.org/modules/mysql/
- SSH 隧道：https://pkg.go.dev/golang.org/x/crypto/ssh ；https://github.com/jfcote87/sshdb

**对标项目：**
- Tiny RDM（Wails Redis GUI）：https://github.com/tiny-craft/tiny-rdm
- DBeaver 通用驱动架构：https://deepwiki.com/dbeaver/dbeaver/4.1-generic-jdbc-support

---

*报告基准日期：2026 年 6 月 12 日。Wails v3 仍处 alpha，版本与 API 细节请以采用时的官方 changelog 为准。*