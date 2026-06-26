# 架构设计文档（ARCHITECTURE.md）

> 本文档说明系统**为什么这样分层、接口契约是什么、数据如何流动、关键取舍的理由**。Claude Code 在动接口、写核心层、加新驱动前应先读本文。操作规约见 `CLAUDE.md`，任务范围见 `MVP.md`。

---

## 1. 设计目标与约束

| 目标 | 设计回应 |
|---|---|
| MySQL 先行，多库可扩展 | 统一 `dbdriver` 抽象接口 + 编译期注册插件 |
| 大结果集不卡死 | 后端分批 fetch + 前端虚拟滚动 + 流式导出 |
| 长查询可取消/可超时 | 全链路 `context.Context` |
| Wails v3 alpha 风险可控 | 防腐层隔离框架 API + 版本锁定 |
| 跨平台单二进制分发 | 纯 Go 依赖优先（modernc sqlite），避免 CGO |
| 多窗口并发安全 | 会话管理器 + 窗口级事务隔离 |

---

## 2. 分层架构

```
┌─────────────────────────────────────────────────────────────┐
│  前端 (WebView, embed.FS)  Vue 3 + TS + Vite + Naive UI + Pinia │
│  连接管理 / SQL编辑器(CodeMirror6) / 结果表格(TanStack)/对象树 │
│  frontend/src/api  ← 防腐层：封装绑定 + 事件，组件只调这层      │
└───────────────────────────┬───────────────────────────────────┘
        Wails v3 IPC（内存内，无 HTTP；自动分块绕过 2MB body 上限）
        方法调用 Promise(+cancel) ┃ 事件 Emit/On
┌───────────────────────────┴───────────────────────────────────┐
│  Wails 绑定层  internal/services/*Service                      │
│  Connection / Query / Metadata / Edit / Transfer / Settings    │
│  仅做参数校验 + 调核心层 + Emit 事件，不放业务逻辑              │
└───────────────────────────┬───────────────────────────────────┘
┌───────────────────────────┴───────────────────────────────────┐
│  Go 核心层  internal/core, storage, tunnel                     │
│  会话/连接管理器 · 查询引擎(ctx) · 动态扫描器 · 元数据 · DDL    │
│  配置存储(SQLite) · 凭据(keyring) · SSH隧道                     │
└───────────────────────────┬───────────────────────────────────┘
┌───────────────────────────┴───────────────────────────────────┐
│  驱动插件层  plugins/*  +  internal/dbdriver(接口) + registry   │
│  [MySQL✓] [PostgreSQL] [SQLite] [...]  各实现 Driver 接口       │
└─────────────────────────────────────────────────────────────────┘
```

**每层职责边界（重要，决定代码放哪）：**

- **前端**：UI + 交互，数据访问只走 `api/`。不感知 Wails 绑定细节。
- **api/ 防腐层（前端）**：把生成的 `bindings/` 与事件封装成稳定的前端 API。框架升级只改这层。
- **Service 层**：Wails 绑定入口。**薄**——只做入参校验、调用核心层、Emit 进度事件。不写 SQL、不写连接逻辑。
- **核心层**：真正的业务。连接生命周期、查询执行、结果扫描、元数据组装、DDL 生成、存储、隧道。**不依赖任何具体数据库**，只依赖 `dbdriver` 接口。
- **驱动插件层**：实现 `dbdriver` 接口，封装具体数据库的 SQL/协议/方言。

---

## 3. 驱动抽象契约（系统承重墙）

> 这是整个可扩展性的核心。改这里之前务必想清楚：**任何改动都要让所有驱动同步实现，并通过契约测试套件。**

### 3.1 关键原则

1. **接口不绑定 `database/sql` 类型。** 用自定义 `ResultSet`/`ColumnMeta`/`ExecResult`。这样 MySQL 插件内部可用 `*sql.DB`，未来 PG 插件可用 pgx 原生 `*pgxpool.Pool`，对核心层完全透明。**抽象层定义"做什么"，插件自选"怎么做"。**
2. **能力声明驱动 UI。** `Capabilities()` 让前端按库的能力显隐功能（如 ClickHouse 无事务则隐藏事务开关）。
3. **连接参数自描述。** `ConnectionSchema()` 返回字段列表，前端据此**动态渲染连接表单**，新增数据库无需改前端表单代码。

### 3.2 接口定义

```go
package dbdriver

import (
	"context"
	"database/sql"
)

// Driver —— 一个数据库类型的插件入口
type Driver interface {
	Name() string                       // 唯一标识 "mysql"
	Version() string
	ConnectionSchema() []ConnParamField // 前端动态渲染连接表单
	Capabilities() Capabilities         // 前端据此显隐功能
	Dialect() Dialect
	Open(ctx context.Context, cfg ConnConfig) (Connection, error)
}

type ConnParamField struct {
	Key, Label, Type, Default string // Type: text|number|password|select|bool
	Required                  bool
	Options                   []string
	Group                     string // "常规"|"SSL"|"SSH"
}

type Capabilities struct {
	Schemas, StoredProcedures, Triggers, Views bool
	Transactions, ExplainPlan                  bool
}

type ConnConfig struct {
	Host, Port, User, Password, Database string
	Params    map[string]string // charset/loc/tls 等驱动特定参数
	SSL       *SSLConfig
	SSHTunnel *SSHConfig
}

// Connection —— 已建立的连接（封装连接池）
type Connection interface {
	Ping(ctx context.Context) error
	Close() error
	Querier() Querier
	Metadata() Metadata
	Editor() Editor
	Begin(ctx context.Context, opts *sql.TxOptions) (Tx, error)
}

type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (ExecResult, error)
	Query(ctx context.Context, sql string, args ...any) (ResultSet, error)
	Explain(ctx context.Context, sql string) (ResultSet, error)
}

// ResultSet —— 流式分批读取，绝不一次性载入全部
type ResultSet interface {
	Columns() []ColumnMeta
	Next(batch int) (rows [][]any, done bool, err error)
	Close() error
}

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

type Dialect interface {
	QuoteIdentifier(name string) string                // MySQL `x` / PG "x"
	Paginate(baseSQL string, limit, offset int) string // LIMIT/OFFSET vs OFFSET FETCH
	MapType(nativeType string) LogicalType
	GenerateCreateTable(t TableSchema) (string, error)
}

// Editor —— 基于主键生成安全的增删改
type Editor interface {
	PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error)
	BuildInsert(table string, row map[string]any) (string, []any, error)
	BuildUpdate(table string, pk, changes map[string]any) (string, []any, error)
	BuildDelete(table string, pk map[string]any) (string, []any, error)
}

type Tx interface {
	Querier
	Commit() error
	Rollback() error
}

type ExecResult struct{ RowsAffected, LastInsertID int64 }
```

### 3.3 编译期注册机制

```go
// internal/registry
var (
	mu      sync.RWMutex
	drivers = make(map[string]dbdriver.Driver)
)

func Register(d dbdriver.Driver) { /* 加锁写 map，重复名 panic */ }
func Get(name string) (dbdriver.Driver, error) { /* 加锁读 */ }
func List() []dbdriver.Driver { /* 供前端"新建连接"下拉 */ }
```

驱动侧：

```go
// plugins/mysqldrv
func init() { registry.Register(mysqlDriver{}) }
```

聚合导入（build tag 可裁剪）：

```go
// plugins/plugins_all.go
//go:build !no_mysql
import _ "yourapp/plugins/mysqldrv"
```

`go build -tags "no_oracle"` 即可不编进 Oracle/CGO 依赖。

### 3.4 新增一个数据库驱动的标准步骤

1. 新建 `plugins/xxxdrv/` 包，实现 `Driver`/`Connection`/`Querier`/`Metadata`/`Dialect`/`Editor`。
2. `init()` 里 `registry.Register(...)`。
3. `plugins/plugins_all.go` 加匿名导入（按需加 build tag）。
4. **跑统一契约测试套件**（见 §7），全绿才算接入完成。
5. 前端无需改动——连接表单由 `ConnectionSchema()` 自动渲染，功能按 `Capabilities()` 自动显隐。

### 3.5 各库扩展要点

| 库 | 驱动 | 内部实现 | 关键差异 |
|---|---|---|---|
| MySQL | go-sql-driver/mysql | `*sql.DB` | 元数据走 information_schema；DDL 用 `SHOW CREATE TABLE` |
| PostgreSQL | jackc/pgx/v5 | **pgx 原生 + pgxpool**（性能更优） | SSH 隧道需重写 `LookupFunc`（见 §6.2） |
| SQLite | modernc.org/sqlite | `*sql.DB` | 纯 Go，无 CGO |
| SQL Server | microsoft/go-mssqldb | `*sql.DB` | 分页 `OFFSET..FETCH` |
| Redis/Mongo | go-redis / mongo-driver | 独立 `KVDriver`/`DocDriver` | 不套 SQL 接口，前端独立 UI |

---

## 4. Go↔前端通信

### 4.1 Service 绑定

```go
type QueryService struct {
	app *application.App
	mgr *core.SessionManager
}

// v3 生命周期方法（注意不是 v2 的 OnStartup/OnShutdown）
func (s *QueryService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

// 公共方法 → 自动生成 TS 绑定；首参 ctx 由运行时注入，支持前端取消
func (s *QueryService) RunQuery(ctx context.Context, connID, sql string) (*QueryResult, error) { ... }
```

注册：`application.New(Options{Services: []application.Service{application.NewService(&QueryService{})}})`。

### 4.2 取消与进度

- **取消**：前端 cancel 该方法的 promise（或 `cancelOn` 绑 AbortSignal）→ Go 侧 `ctx` 取消 → `QueryContext` 中断。
- **进度**：核心层 `app.Event.Emit("export-progress", {done,total})`，前端 `On("export-progress", cb)`。

### 4.3 类型映射

Go→TS：`map[K]V`→`Record<K,V>`，slice→数组，`[]byte`→`Uint8Array`。生成命令带 `-names` 保留字段名与位置参数，降低重构成本。

---

## 5. 数据流：一次查询的完整路径

```
前端编辑器执行
  → api/query.run(connID, sql, signal)          // 防腐层，绑定 AbortSignal
  → QueryService.RunQuery(ctx, connID, sql)      // Service 层，薄
  → core.QueryEngine.Run(ctx, conn, sql)         // 取连接、QueryContext
  → driver.Querier.Query(ctx, sql)               // 插件，返回 ResultSet
  → core.Scanner 分批 Next(batch=500)            // 动态扫描，[][]any
  → 首批 + 列元数据经 IPC 返回；后续批次按需拉取/Emit
  → 前端 TanStack Virtual 仅渲染视口行 + LRU 预读缓存
```

**列元数据只传一次**（列名 + 类型），行数据用 `[][]any`。前端虚拟滚动到底/跳转未加载区时，经事件请求下一批。

---

## 6. 关键技术机制

### 6.1 动态结果集扫描（核心层 core/scanner）

动态 SQL 的列名/列数/类型编译期未知，必须运行时反射扫描：

```go
func scanBatch(rows *sql.Rows, colTypes []*sql.ColumnType, batch int) (data [][]any, done bool, err error) {
	n := len(colTypes)
	for i := 0; i < batch; i++ {
		if !rows.Next() { done = true; break }
		holders := make([]any, n)
		raw := make([]sql.RawBytes, n)
		for j := range holders { holders[j] = &raw[j] }
		if err = rows.Scan(holders...); err != nil { return }
		row := make([]any, n)
		for j := range raw {
			row[j] = convert(raw[j], colTypes[j]) // 按 DatabaseTypeName 做 Type Switch
		}
		data = append(data, row)
	}
	return data, done, rows.Err()
}
```

- 用 `rows.ColumnTypes()` 的 `DatabaseTypeName`/`Nullable`/`DecimalSize` 做精确转换（BIGINT→int64、VARCHAR→string、DATETIME→格式化时间、NULL→nil）。
- **行数据用 `[]any` 不用 `map[string]any`**（避免 hashing + 内存碎片，减小 IPC 载荷）。
- 高吞吐优化：`sync.Pool` 复用扫描缓冲；极致场景改一维平铺 `[]any` + 列数步长还原。

### 6.2 SSH 隧道（core/tunnel）

MySQL：建立 `*ssh.Client` 后用 `mysql.RegisterDialContext("mysql+ssh", dialer)`，dialer 内 `sshClient.Dial("tcp", addr)`。DSN：`user:pass@mysql+ssh(dbhost:3306)/db`。

**PostgreSQL 的 DNS 陷阱（务必注意）**：pgx 默认在客户端本地做 DNS 解析，内网私有域名会超时失败。必须在 `pgxpool.Config` 同时设置 `DialFunc`（透传 SSH）**和** `LookupFunc`（把域名解析也透传给跳板机）。只设 DialFunc 会"隧道通但连接挂"。

主机密钥用 `ssh.FixedHostKey` 校验，**禁止 `InsecureIgnoreHostKey`**。

### 6.3 连接生命周期与多窗口并发隔离（core/session）

- `*sql.DB` 连接池按连接 ID 封装在管理器，Service 启动钩子初始化，应用退出钩子优雅 `Close()`。
- **多窗口并发风险**：多窗口在同一连接池上跑未提交事务，无序抢占会导致事务在同一物理连接上交叉/死锁。
- **解决**：开启事务/独占操作时，会话管理器 `BeginTx` 分离独立会话并绑定窗口 ID，事务结束前该物理连接不被其他窗口借调。普通自动提交查询仍走共享池。

### 6.4 表数据在线编辑（core + Editor）

- UPDATE/DELETE 必须 `WHERE pk=?` 参数化，pk 来自 `Editor.PrimaryKeys` 探测。
- 乐观锁可选 `WHERE pk=? AND col=?old`，受影响 0 行 → "数据已被他人修改"。
- **无主键/唯一键的表 → 标记只读**，不生成写语句（最稳妥）。

### 6.5 原生 UI 集成层（部分 UI 活在 Go 侧）

为达到 `UI_SPEC.md` 的"去 Web 感"要求，以下 UI 不在 Vue 里实现，而是用 Wails 原生能力，封装在 `wailsbridge/`：

- **窗口外壳**：Frameless + 自绘标题栏（CSS `--wails-draggable`）；平台分叉——macOS `Mac.InvisibleTitleBarHeight` + 交通灯 + `Mac.Backdrop` 毛玻璃，Windows `WindowsWindow{BackdropType: Mica}` + caption 按钮；标题栏配色用 `CustomTheme` 双套色。
- **应用菜单**：`app.NewMenu()` 建 File/Edit/View/Query/Window/Help，`SetAccelerator` 注册快捷键；macOS 进系统菜单栏，Win/Linux 用 `UseApplicationMenu`。
- **上下文菜单**：Wails 原生（前端 CSS `--custom-contextmenu: <id>` + `--custom-contextmenu-data`，Go 侧 `OnClick`）。对象树/结果集/标签页的右键都走这条，不用 HTML 浮层。**注意**：context data 来自前端 CSS，属不可信输入，Go 侧须校验。
- **系统对话框**：文件打开/保存/选目录、确认/警告 → Wails 原生 Dialog，不用 HTML 模拟。

**架构含义**：这些 Go 侧 UI 通过 `wailsbridge` 暴露给前端调用/联动（如菜单项触发前端动作用事件；右键菜单点击在 Go 侧处理后 Emit 给前端）。前端只负责应用内的密集内容区（树/编辑器/表格/表单），系统级外壳交给 Go。

> **已知缺口**：Wails v3 暂无统一主题（明暗）读取/设置/订阅 API（官方 issue #4665 未实现）。`wailsbridge` 需自封装：前端 `matchMedia('(prefers-color-scheme: dark)')` 驱动 Naive UI 暗色主题，Go 侧标题栏用 `CustomTheme` 双套色同步。

---

## 7. 测试架构

- **单元测试**：Dialect 类型映射、Editor 语句生成、分页 SQL、DSN 构建、scanner 类型转换——纯逻辑，无需数据库。
- **契约测试套件（关键）**：对 `dbdriver.Driver` 写一套通用测试（连接/Ping/查询/元数据/编辑/取消），**任何驱动都跑同一套**，保证接口语义一致。新驱动接入的验收标准 = 契约测试全绿。
- **集成测试**：`testcontainers-go/modules/mysql` 起真实 MySQL（`mysql.Run(ctx,"mysql:8.0")` + `WithScripts` + `ConnectionString()`）。

---

## 8. 已知约束与缓解（影响设计的现实因素）

| 约束 | 缓解 |
|---|---|
| Wails v3 仍 alpha，API 可能 break | 锁 `v3.0.0-alpha2.106`；wailsbridge/api 防腐层隔离 |
| Service 方法命名 v2→v3 已变更 | 统一用 `ServiceStartup/ServiceShutdown` |
| WebView2 IPC body ~2MB 上限 | v3 已自动分块；但仍靠分页+虚拟滚动防卡死 |
| Linux GTK3/GTK4 过渡期不稳 | MVP 锁 GTK3；优先 Win/Mac |
| CGO 复杂化交叉编译 | SQLite 用 modernc 纯 Go；避免 CGO 驱动 |

---

## 9. 设计决策速查（FAQ）

- **为什么编译期注册而非动态加载？** 零运行时开销、类型安全、易交叉编译、天然支持 cgo 驱动。go plugin 仅 Linux/macOS 且版本脆弱，Goja 有性能/调试成本——都不是本项目主线。
- **为什么接口不直接用 `database/sql`？** 为了让 pgx 等能用原生高性能接口；抽象层只定义语义。
- **为什么 CodeMirror 不用 Monaco？** 体积小一个数量级、多实例独立、补全可组合（适合基于元数据的自定义补全）。
- **为什么行数据用数组不用 map？** 减少 hashing/内存碎片/IPC 载荷；列名单独传一次即可。
