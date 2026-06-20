# CLAUDE.md

> Claude Code 在本仓库工作时**必须先读本文件**。这里是工作规约（约束 + 约定 + 命令），不是功能说明。功能与设计见 `ARCHITECTURE.md`，任务与范围见 `MVP.md`。

## 项目一句话

基于 **Wails v3（Go 后端 + Vue 3/TS 前端 + 原生 WebView）** 的跨平台数据库管理工具（类 Navicat/DBeaver/TablePlus）。MVP 只做 **MySQL**；其他数据库通过**编译期注册的 Go 接口插件**扩展。

## 技术栈（不要擅自替换）

- **后端**：Go 1.22+，Wails v3 **锁定 `v3.0.0-alpha.96`**（go.mod 与 CLI 都钉死，禁止 `@latest`）
- **前端**：Vue 3（Composition API + `<script setup>`）+ TypeScript + Vite，状态用 Pinia
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
  mysqldrv/         # MVP 唯一插件
frontend/src/
  api/              # 前端防腐层：封装生成的绑定 + 事件，组件只调 api/ 不直接 import bindings/
  components/       # 编辑器/表格/对象树/连接表单
```

## 铁律（违反会埋雷，务必遵守）

1. **隔离 Wails API**：Go 侧所有 `application.*` 调用只允许出现在 `wailsbridge/`；前端组件只能调 `frontend/src/api/`，禁止直接 import 生成的 `bindings/`。理由：alpha 期 breaking change 时只改一处。
2. **Service 生命周期方法用 v3 命名**：`ServiceName() / ServiceStartup(ctx, opts) error / ServiceShutdown() error`。**绝不要用 v2 的 `OnStartup`/`OnShutdown`**（训练数据里大量 v2 示例是错的）。
3. **长任务必须吃 ctx**：Service 方法首参 `context.Context`，查询一律 `QueryContext/ExecContext(ctx, ...)`。前端取消 promise → ctx 取消 → 查询中断。禁止写不可取消的阻塞查询。
4. **SQL 一律参数化**：永远用占位符 + args，**禁止字符串拼接用户值**。表数据的 UPDATE/DELETE **必须基于主键/唯一键**；探测不到唯一键的表 → 标记只读，不生成写语句。
5. **大结果集不许一次性序列化**：后端分批 fetch（`ResultSet.Next(batch)`），行数据用 `[][]any` 数组（不是 `map[string]any`），列元数据单独传一次。大导出走**流式写文件**，不经 IPC 传给前端。
6. **抽象层不绑定 `database/sql` 类型**：`dbdriver` 接口用自定义 `ResultSet/ColumnMeta`，这样 MySQL 插件内部可用 `*sql.DB`、未来 PG 插件可用 pgx 原生。改接口前先读 ARCHITECTURE.md 的契约说明。
7. **驱动靠 `init()` 注册**：新驱动在自己包的 `init()` 里 `registry.Register(...)`，并在 `plugins/plugins_all.go` 匿名导入。不要写运行时动态加载（go plugin / Goja），那不是本项目主线。
8. **密码绝不明文落盘**：连接配置（host/port/user/options）存 SQLite，密码只进 keyring。代码评审看到明文密码持久化直接拒绝。
9. **多窗口并发隔离**：事务/独占操作走会话管理器分离的独立连接并绑定窗口 ID，事务期间该物理连接不被其他窗口借调。不要把未提交事务放在共享连接池上裸跑。
10. **平台优先级**：MVP 只保证 Windows + macOS。Linux 锁 GTK3 栈（`-tags gtk3`），不为 GTK4 实验特性写代码。
11. **UI 必须向原生靠拢、去 Web 感**：这是桌面应用不是网页。写任何前端 UI 前先读 `UI_SPEC.md`。硬性要求：系统字体栈 + 桌面字号（12–13px）、高密度布局、小圆角发丝线、克制按钮无花哨动画；右键用 **Wails 原生上下文菜单**、文件/确认用 **Wails 原生对话框**、顶层菜单走 **原生应用菜单**，不要用 HTML 浮层模拟系统级交互。判定标准：截图发出去要像桌面软件，不像网页。

## 代码风格

- Go：标准 `gofmt`/`go vet`；错误用 `fmt.Errorf("...: %w", err)` 包装并传递；公共接口写文档注释。
- TS：严格模式；前端不放业务逻辑到组件里，数据访问统一走 `api/`。
- 提交前：`task test` 必须绿；改了驱动接口要让契约测试套件全过。

## 写代码前的自检清单

- [ ] 这段逻辑该放哪一层？（看上面目录结构）
- [ ] 涉及查询吗？带 ctx 了吗？参数化了吗？
- [ ] 涉及 Wails API 吗？是不是该走 wailsbridge / api 层？
- [ ] 改了 Service 公共方法吗？要重新生成 bindings 吗？
- [ ] 涉及新驱动吗？接口实现全了吗？契约测试过吗？

## 不确定时

- 接口语义、数据流、设计取舍 → 读 `ARCHITECTURE.md`
- 当前该做什么、做到什么程度算完成 → 读 `MVP.md`
- 任何前端外观/交互怎么做才"像原生" → 读 `UI_SPEC.md`
- Wails v3 API 细节 → 查 https://v3.wails.io/ ，**不要凭训练记忆**（v3 与 v2 差异大，记忆易错）
