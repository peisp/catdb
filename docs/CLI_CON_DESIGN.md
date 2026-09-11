# CLI `-con` 设计：DBeaver 命令行连接参数的逐字符兼容

> 目标：用户把发给 DBeaver 的 `-con` 命令行参数**原样**（不改一个字符）传给 catdb，只要驱动受支持，连接就能建立。
> 本文是实现底稿。DBeaver 侧规格全部核对自 devel 分支源码（`DataSourceUtils.java`、`DBeaverTopLevelCommand.java`、`ApplicationInstanceServer.java`、`SystemVariablesResolver.java`）与官方 Command-Line 文档，非凭记忆。

## 1. 验收标准

- 从 DBeaver 文档与源码测试（`DataSourceUtilsTest`）中取真实 `-con` 串作 fixture，凡驱动可映射（MySQL/MariaDB/PostgreSQL/SQLite/DM）的，逐条原样喂给 catdb 必须连接成功。
- 无对应概念的参数**只产生警告日志，绝不导致失败**（宽进原则，这是"不改一个字符也能跑"的关键）。
- catdb 已在运行时，第二次带 `-con` 启动 → 参数转发给已运行实例执行并提窗，第二进程退出。

## 2. DBeaver `-con` 规格（调研结论）

### 2.1 CLI 层

| 参数 | 说明 |
|---|---|
| `-con <spec>`，别名 `-connect` / `-ds-spec` / `--datasource-specification` | **可重复出现**，依次处理 |
| `-vars <file>`，别名 `-variablesFile` | Java properties 文件，注入变量供 `${...}` 展开 |
| `-newInstance` | 强制新实例（不转发给已运行实例） |
| `-bringToFront` | 提窗 |
| `-stop` / `-quit` | 停止已运行实例 |
| `-f <file>` / 裸位置参数 | 打开文件；含 `-con` 时把 SQL 文件绑到该数据源 |
| `-closeTabs` / `-disconnectAll` / `-reuseWorkspace` | 编辑器/连接/工作区管理 |

### 2.2 spec 语法

- `key=value` 用 `|` 分隔；值内 `|` 写 `\|`，字面反斜杠写 `\\`（见 `splitConnectionSpec`）。
- **速记形式**：整串无 `=`（如 `-con "MyConn"`）→ 按「连接 id 或连接名」查已有连接，查不到直接报错（不新建）。
- 值先做 `${variable}` 展开（见 2.5），再参与解析。

### 2.3 参数字典（源码白名单全集）

| 类别 | 参数 | 默认 |
|---|---|---|
| 定位 | `id`、`name`、`driver`、`folder` | — |
| 地址 | `url`（JDBC 串）、`host`、`port`、`server`、`database` | — |
| 认证 | `user`、`password`、`auth`（认证模型 id）、`authProp.<k>`、`savePassword` | savePassword=**true** |
| 属性前缀 | `prop.<k>`（JDBC 连接属性）、`advProp.<k>`（provider 属性）、`handler.<id>.<k>`（按 handler 属性搜索）、`netHandler.<prefix>.<k>`（设置网络 handler，自动启用） | — |
| 导航器 | `showSystemObjects`、`showUtilityObjects`、`showOnlyEntities`、`hideFolders`、`hideSchemas`、`mergeEntities` | 均 false |
| 会话 | `autoCommit` | 驱动默认 |
| 行为 | `create`、`save`、`connect`、`openConsole` | true / **false** / true / false |

### 2.4 执行语义

1. **先查已有**：`id` 精确查 → `name` 按名查 → 速记 token 两者都试。
2. **查到 → 打补丁**：用 spec 中的 `database/user/password/prop.*/authProp.*/advProp.*/auth/netHandler.*` 覆盖已有连接的配置（容易忽略的语义）。
3. **查不到且 `create=true` → 新建**：必须有 `driver`，否则报错放弃；`name` 缺省生成 `"Ext: <驱动名> - <database|server>"`，重名追加 ` (2)`；`save=false` → temporary（注册进 registry 但不持久化，退出即弃）；`savePassword=true` → 保存密码。
4. `connect=true` → 立即建立物理连接；`openConsole=true` → 打开 SQL 控制台并提窗（隐含连接）。
5. **单实例**：已有实例在跑时，第二进程经本地 RPC 把 spec 转给它执行后退出。

### 2.5 变量展开（`GeneralUtils.replaceVariables` + `SystemVariablesResolver`）

解析顺序：**内置变量 → `-vars` 文件 → Java system properties → 环境变量**。
内置变量：`${home}`（用户主目录）、`${workspace}`、`${dbeaver_home}` / `${application.path}`（安装目录）、`${application.name}`、`${application.version}`、`${local.ip}`。

## 3. catdb 兼容矩阵

### 3.1 直接映射（语义一致）

`name` `host` `port` `database` `user` `password` `savePassword` `create` `save` `connect` `openConsole` `folder`（→ 连接分组，按名查/建）+ 全部语法规则（转义、速记、打补丁语义、多 `-con`）。

### 3.2 需翻译层（经 `dbdriver.ExternalCompat` 接口，实现在插件内）

| DBeaver 写法 | catdb 方案 |
|---|---|
| `driver=mysql8` 等 | 插件声明**别名表**（大小写不敏感）：mysqldrv←`mysql/mysql5/mysql8`；mariadbdrv←`mariadb/mariaDB`；postgresdrv←`postgresql/postgres/postgres-jdbc`；sqlitedrv←`sqlite/sqlite_jdbc`；dmdrv←`dm/dameng` |
| `url=jdbc:mysql://h:3306/db?k=v` | 插件解析各自 JDBC URL 形态（`jdbc:mysql://`、`jdbc:mariadb://`、`jdbc:postgresql://`、`jdbc:sqlite:<path>`），拆出 host/port/database，query 参数再走 prop 翻译 |
| `prop.<k>` | 插件属性翻译表：JDBC 属性名 → catdb schema key。MySQL（Connector/J）示例：`connectTimeout`(ms)→`params.timeout`(Go duration，需换算)、`sslMode`(DISABLED/PREFERRED/REQUIRED/VERIFY_CA/VERIFY_IDENTITY)→`ssl.mode`(disable/prefer/require/verify-ca/verify-full)、`useSSL`(false→disable/true→prefer)、`maxAllowedPacket`→`params.maxAllowedPacket`、`connectionCollation`→`params.collation`。PG（pgJDBC）示例：`connectTimeout`(s)→`params.timeout`、`sslmode`→`ssl.mode`、`sslrootcert`→`ssl.caCert`、`sslcert`/`sslkey`→`ssl.clientCert`/`ssl.clientKey`。翻不了的 → 警告忽略 |
| `netHandler.ssh.*` / `handler.ssh.*` | 通用层固定映射表（SSH 隧道是 catdb 通用能力，映射放 `internal/cli`）：`host/port/user(userName)/password` → `sshTunnel.*` 同名；`authType`(PASSWORD/PUBLIC_KEY/AGENT) → 决定走密码/`privateKey`/`useAgent`；`keyPath` → `sshTunnel.privateKey`。⚠️ 实现时核对 `internal/tunnel`：catdb 的 `privateKey` 字段是 PEM 内容还是路径——若是内容，`keyPath` 需读文件 |

sqlite 的 `database` 即文件路径，与 catdb sqlitedrv 的 `database` key 天然一致，无需特殊处理；相对路径按调用方工作目录解析（二次实例场景用 `SecondInstanceData.WorkingDir`）。

### 3.3 接受但语义忽略（警告日志，连接照建）

`showSystemObjects` 等导航器 6 参数、`autoCommit`、`server`、`auth` / `authProp.*`（DBeaver 认证模型体系）、`advProp.*` 中翻不了的项。

### 3.4 原理上不可逐字符兼容（边界）

- `id=<dbeaver-workspace-id>`：DBeaver 的连接 id 是其 workspace 生成的，catdb 不可能命中。行为：查不到 → 有 `driver` 且 `create=true` 则照常新建；仅有 `id` 无其他信息才报错。此项连两台 DBeaver 之间都不通用，不算兼容缺口。
- `-newInstance`：Wails 单实例是全局锁，一期识别但警告忽略。
- `-f`（绑定 SQL 文件）、`-closeTabs`、`-disconnectAll`、`-stop`、`-reuseWorkspace`：一期识别不实现（警告），避免解析报错；`-stop` 可低成本顺带做（转发退出指令）。

### 3.5 变量展开的 catdb 映射（一期）

顺序照抄 DBeaver：**内置 → `-vars` 文件 → 环境变量**（catdb 无 Java system properties 层，跳过）。
内置变量映射：`${home}`→用户主目录；`${workspace}`→catdb 数据目录（`storage.Open` 所在目录）；`${application.name}`→`catdb`；`${application.version}`→构建版本；`${dbeaver_home}`/`${application.path}`→catdb 可执行文件目录；`${local.ip}`→本机非环回 IP。
`-vars` 文件按 Java properties 格式解析（`key=value`，`#` 注释，`\` 续行与转义——注意 properties 的反斜杠转义规则，别用 Go 的 ini 库想当然）。

## 4. 架构分层

### 4.1 `internal/cli`（新包，纯解析，不 import `application`）

```go
type LaunchRequest struct {
    Specs     []ConSpec  // 每个 -con 一条
    VarsFile  string
    // 识别但忽略的 flag 记录在 Warnings
}
type ConSpec struct {
    Draft    services.ConnectionDraft // 翻译后的 catdb 连接草稿
    ID, Name string                   // 查找定位用
    Create, Save, Connect, OpenConsole, SavePassword bool
    FolderName string
    Warnings []string                 // 被忽略的参数,供日志/toast
}
func ParseArgs(args []string, resolve VarResolver) (LaunchRequest, error)
```

- 字典、默认值、转义、速记、打补丁语义全部对齐 DBeaver 源码。
- 驱动相关翻译通过 `registry` 取 `ExternalCompat` 接口完成；未实现该接口的驱动只支持 catdb 原生 key 直通（`params.*`/`ssl.*`/`sshTunnel.*` 本来就认）。

### 4.2 `dbdriver` 可选扩展接口（铁律 #12 的落点）

```go
type ExternalCompat interface {
    ExternalDriverAliases() []string
    ParseExternalURL(jdbcURL string) (ExternalURLParts, error) // host/port/database + query props
    TranslateExternalProp(key, value string) (dotKey, val string, ok bool)
}
```

实现全在各插件内部；通用层零方言。改接口前过一遍 ARCHITECTURE.md 契约说明，并补契约测试。

### 4.3 `main.go` + `wailsbridge`

- `main()` 早期 `cli.ParseArgs(os.Args[1:], …)`；解析失败打印 usage 到 stderr 后正常启动 GUI（不 os.Exit 阻断双击启动）。
- `application.Options.SingleInstance = &SingleInstanceOptions{UniqueID, OnSecondInstanceLaunch}`；UniqueID 按 build-tag 区分 dev/prod（复用 `appname_dev/prod` 常量，避免 dev 实例劫持 prod 的 `-con`）。
- `OnSecondInstanceLaunch(data)`：解析 `data.Args` → 同一条处理函数 → 提窗。回调中触达 `application.*` 的部分（提窗）放 `wailsbridge`。

### 4.4 services 层：launch 处理流程

1. id/name 查 profile → 查到则按打补丁语义覆盖字段（含 keyring 密码更新）。
2. 查不到且 `create` → 组 `ConnectionDraft`：
   - `save=true` → 现有 `SaveConnection`（密码进 keyring）；
   - `save=false` → **ephemeral profile**（已定案）：照常落 SQLite 但打临时标记。`connection` 表加 `ephemeral INTEGER NOT NULL DEFAULT 0` 列（开发期不做旧格式兼容，直接改 `migrate()`）。清理时机：`ServiceShutdown` 删除 + 启动时清扫上次异常退出的残留（连带 keyring 条目）。ephemeral 连接在侧栏正常显示（可加临时徽标，UI 细节遵 DESIGN.md）。
3. `connect=true` → 复用 `ConnectionService.Connect(ctx, id)`（SSH host-key 信任对话框等逻辑直接继承）。
4. `openConsole=true` → 连接后为该连接打开一个查询 tab。
5. `Warnings` 逐条打日志；二次实例场景可另发 toast。

### 4.5 前端通知（处理启动竞态）

- `wailsbridge.Emit` 在窗口 ready 前静默丢弃 → **冷启动**走"后端暂存 pending + 前端 mounted 后主动拉取"：新增 `SystemService.ConsumePendingLaunch()`；**二次实例**（窗口已 ready）走新事件 `connection:open-request`（`main.go` `init()` 里 `RegisterEvent`，`api/system.ts` 加 `onOpenConnection`）。
- 前端收到后：刷新连接列表（`connection:saved` 已有）、sidebar 选中、`markLive`、按需开查询 tab。

### 4.6 i18n / 安全

- 用户可见错误走稳定 slug → `error.*`（中英 locale 同步加）；解析警告属技术日志，保持英文。
- 命令行密码有 `ps` 泄露风险：文档引导用 `-vars` 文件或环境变量（`password=${MY_DB_PASS}`），一期即可用。

## 5. 分期

- **一期（MVP）**：spec 解析全字典 + 翻译层（别名/URL/prop/ssh 映射）+ `${...}` 展开与 `-vars` + 单实例转发 + create/save(ephemeral)/connect + 打补丁语义 + 前端聚焦。忽略类参数警告直通。
- **二期**：`openConsole` 完整体验、`folder` 建组交互打磨、`-stop`、ephemeral UI 徽标。
- **三期（可选）**：`catdb://` deep link（复用同一解析器）、`-f` 绑定 SQL 文件、`-disconnectAll`/`-closeTabs`。

## 6. 测试策略

- `internal/cli` 单测：转义/速记/默认值/打补丁/变量展开顺序/properties 文件解析，fixture 直接取 DBeaver 文档示例与 `DataSourceUtilsTest` 用例。
- 各插件 `ExternalCompat` 表驱动测试（别名、URL 解析、prop 换算含单位转换）。
- 集成：testcontainers MySQL 起库后，用 DBeaver 风格 spec 走完整 launch 流程建连；ephemeral 清理验证。
