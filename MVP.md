# MVP 开发计划（MVP.md）

> 本文档是**任务驱动的开发清单**：按里程碑列出要做什么、做到什么程度算完成（验收标准）、什么不做（范围边界）。Claude Code 按此推进，每完成一个任务勾选并自测。设计依据见 `ARCHITECTURE.md`，工作规约见 `CLAUDE.md`。

## MVP 范围一句话

**只支持 MySQL** 的、功能完整的桌面数据库管理工具，跑在 **Windows + macOS** 上，架构上已为多库扩展预留好编译期注册插件接口。

## 总体验收（MVP 完成的定义）

用户能：新建/保存/分组 MySQL 连接（含 SSL/SSH）→ 浏览库表对象树 → 写 SQL（高亮+补全+多标签）并执行（可取消/超时）→ 看大结果集（虚拟滚动流畅）→ 在线编辑表数据（基于主键）→ 看/导出表结构 DDL → 导入导出 CSV/JSON/SQL/Excel。全程密码不明文落盘，Win/Mac 能打包出可分发安装包。

## 范围边界（MVP 明确不做）

- ❌ 第二个数据库驱动（PostgreSQL/SQLite 等）——接口预留但不实现
- ❌ Linux 完整支持（可跑但不保证，锁 GTK3）
- ❌ AG Grid、Monaco
- ❌ 运行时动态插件（Goja/go plugin）
- ❌ GC 极致优化（一维平铺传输）——先用 `[][]any`
- ❌ ER 图、数据同步/比对、可视化建表设计器（后续迭代）

---

## M0 · 项目骨架（2–3 人周）

**目标**：能跑起来的空壳 + 架构地基。

- [ ] `wails3 init` Vue+TS 模板，go.mod 锁 `v3.0.0-alpha2.106`；接入 Naive UI + Pinia
- [ ] 目录结构按 ARCHITECTURE.md §2 建好（wailsbridge / internal/* / plugins / frontend/src/api）
- [ ] `wailsbridge/` 防腐层骨架：封装 app/window/event/dialog 调用
- [ ] `frontend/src/api/` 防腐层骨架
- [ ] `internal/dbdriver/` 全部接口定义（Driver/Connection/Querier/ResultSet/Metadata/Dialect/Editor/Tx 及相关 struct）
- [ ] `internal/registry/` 注册表（Register/Get/List）
- [ ] 一个 demo Service（公共方法 + Emit 事件）跑通端到端，验证绑定生成与取消机制
- [ ] Taskfile：dev/build/test/test:integration/generate-bindings
- [ ] GitHub Actions CI 雏形（Win+Mac 矩阵构建，缓存 module/node_modules 且排除 node_modules 校验）
- [ ] **UI 原生化基调**（按 UI_SPEC.md）：Naive UI `themeOverrides` 定好桌面尺度（系统字体栈、13px、小圆角、紧凑密度）；明暗主题跟随系统（`prefers-color-scheme`）；三段式布局骨架（侧栏 + 标签页主区 + 底部状态栏）

**验收**：`wails3 dev` 起得来；前端按钮调 demo Service 能拿到返回 + 收到事件 + 能取消；CI 绿；UI 已是桌面尺度（非默认 Web 尺寸），明暗跟随系统。

---

## M1 · 连接管理（3–4 人周）

**目标**：完整的连接生命周期 + 安全存储 + MySQL 实连。

- [ ] `internal/storage/`：SQLite 存连接配置（host/port/user/db/options/分组），keyring 存密码
- [ ] `ConnectionService`：连接 CRUD、分组、测试连接、连接/断开
- [ ] 前端：连接列表/分组树、连接表单**由 `ConnectionSchema()` 动态渲染**
- [ ] `plugins/mysqldrv/`：实现 `Driver.Open` + `Connection.Ping/Close`，DSN 构建（parseTime/loc/collation/timeout/maxAllowedPacket）
- [ ] 连接池参数（MaxOpen 5–10、ConnMaxLifetime）
- [ ] `internal/tunnel/`：SSH 隧道（`RegisterDialContext`，密码/私钥/agent 认证，FixedHostKey 校验）
- [ ] SSL/TLS：`RegisterTLSConfig` + 表单 SSL 分组
- [ ] `init()` 注册 mysqldrv，plugins_all.go 匿名导入

**验收**：能新建一个 MySQL 连接（直连/SSL/SSH 三种各测通），保存重启后还在，密码不在 SQLite 里（在 keyring）；测试连接成功/失败有正确反馈。

---

## M2 · 查询与结果集（4–5 人周）

**目标**：能写 SQL、能执行、能看大结果。

- [ ] 前端 CodeMirror 6 编辑器（`vue-codemirror` 封装）：SQL 高亮、多标签页（每标签独立编辑器实例）
- [ ] `QueryService.RunQuery(ctx, ...)`：`QueryContext`、前端取消 promise → 中断、`context.WithTimeout` 超时
- [ ] `internal/core/scanner`：动态结果集扫描（ColumnTypes + RawBytes + Type Switch，输出 `[][]any` + 列元数据）
- [ ] 结果集分批：`ResultSet.Next(batch=500)`；分页（Dialect.Paginate）与流式两种范式
- [ ] 前端 `@tanstack/vue-table` + `@tanstack/vue-virtual`：虚拟滚动（>1000 行启用）、LRU 预读缓存、滚动触边拉下一批
- [ ] 列元数据只传一次，行数据数组化
- [ ] EXPLAIN 执行计划展示（按钮受 Capabilities.ExplainPlan 控制）
- [ ] 单次最大返回行数限制（默认 10000 预览），超出引导用导出
- [ ] 错误展示（SQL 报错、超时、取消的区分提示）

**验收**：执行 `SELECT` 百万行表不卡死（虚拟滚动流畅）；执行慢查询能点取消并真正中断；多标签互不干扰；EXPLAIN 出结果。

---

## M3 · 元数据浏览与表编辑（4–6 人周）

**目标**：对象树 + 看结构 + 改数据。

- [ ] `MetadataService` + mysqldrv 的 `Metadata`：库/schema/表/视图/列/索引/外键/存储过程/触发器（走 information_schema）
- [ ] 前端对象树（Naive UI `n-tree`，虚拟化）：库→表/视图/存储过程/触发器，懒加载、**Wails 原生右键菜单**（`--custom-contextmenu`，按节点类型给不同菜单项）
- [ ] 表结构查看：列/索引/外键面板 + `SHOW CREATE TABLE` 的 DDL 文本
- [ ] 基于元数据的自动补全：MetadataService 拉库/表/列缓存，CodeMirror `CompletionSource`（FROM 后补表、`table.` 后补列）
- [ ] 表数据浏览（带分页/虚拟滚动，复用 M2）
- [ ] `EditService` + mysqldrv 的 `Editor`：主键探测、BuildInsert/Update/Delete（参数化、基于主键）
- [ ] 在线编辑：单元格编辑 → 乐观更新 → 失败回滚提示；可选乐观锁
- [ ] **无主键表标记只读**并明确提示
- [ ] `internal/core/session`：多窗口会话管理器，事务/独占操作绑定窗口 ID 隔离

**验收**：对象树能展开到列级；改一个有主键表的单元格能落库且并发安全；无主键表无法编辑且有提示；补全能基于当前库的真实表名/列名工作。

---

## M4 · 导入导出与打磨（3–4 人周）

**目标**：数据进出 + 多窗口体验 + 可分发。

- [ ] `TransferService`：导出 CSV / JSON / SQL dump（含可选 DDL）/ Excel（excelize StreamWriter 流式）
- [ ] 导入 CSV / SQL；大文件流式 + `Emit("progress")` 进度条；走文件对话框选路径，不经 IPC 传大数据
- [ ] 用户与权限查看（只读列出）
- [ ] **原生窗口外壳**（UI_SPEC §1）：Frameless + 自绘标题栏（`--wails-draggable`）；macOS 交通灯 + InvisibleTitleBarHeight + 可选 Backdrop 毛玻璃；Windows Mica + caption 按钮；标题栏配色 `CustomTheme` 跟随主题
- [ ] **原生应用菜单 + 快捷键**：`app.NewMenu()` File/Edit/View/Query/Window/Help，`SetAccelerator`（执行查询/新标签/保存/查找等）
- [ ] 多窗口交互：AttachModal 连接配置窗（macOS Sheet）、Hidden 防白屏、WindowClosing 钩子保护未保存 SQL
- [ ] 系统级交互全部走 Wails 原生对话框（文件/确认/警告）；结果集/标签页原生右键菜单
- [ ] 底部状态栏（行数/耗时/连接/字符集/光标位置）
- [ ] 契约测试套件覆盖 mysqldrv（连接/查询/元数据/编辑/取消全过）
- [ ] testcontainers MySQL 集成测试接入 CI
- [ ] 打包：Windows NSIS/MSI、macOS .app→dmg（+ 签名公证流程文档）
- [ ] 自动更新器接入 GitHub Releases（含 bsdiff patch）
- [ ] UI 打磨、空状态、加载态、快捷键（执行查询等）

**验收**：导出 50 万行 Excel 不 OOM 且有进度；关窗有未保存 SQL 会拦截确认；Win/Mac 各打出一个可安装包并能正常启动；契约 + 集成测试在 CI 绿。

---

## 里程碑汇总

| 里程碑 | 人周 | 关键产出 |
|---|---|---|
| M0 骨架 | 2–3 | 接口/注册表/防腐层/CI |
| M1 连接管理 | 3–4 | 连接 CRUD + keyring + MySQL 实连 + SSH/SSL |
| M2 查询结果集 | 4–5 | 编辑器 + 执行取消 + 动态扫描 + 虚拟滚动 |
| M3 元数据表编辑 | 4–6 | 对象树 + DDL + 在线编辑 + 补全 + 会话隔离 |
| M4 导入导出打磨 | 3–4 | 导入导出 + 多窗口 + 打包 + 自动更新 + 测试 |
| **合计** | **16–22** | 双人并行约 8–12 周 |

---

## 跨里程碑的持续要求（每个任务都适用）

- 每个 Service 公共方法改动后跑 `wails3 generate bindings -names`
- 所有查询带 ctx、参数化
- 涉及 Wails API 只走防腐层
- 新增核心逻辑配单元测试，`task test` 保持绿
- 密码只进 keyring
- 任何前端 UI 遵循 `UI_SPEC.md`（桌面尺度、原生菜单/对话框、去 Web 感）

## 建议的实现顺序提示

承重墙优先：**M0 的 dbdriver 接口 + registry + M1 的 mysqldrv.Open/Ping + M2 的 scanner** 是后续一切的地基，应最先稳定下来并配上契约测试雏形。接口定型后前端（编辑器/表格/树）与后端（核心层/插件）可并行推进。
