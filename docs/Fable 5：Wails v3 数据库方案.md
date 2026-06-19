# **基于Wails v3的多数据库连接工具可行性评估与技术方案研究报告**

## **一、 可行性评估与行业先验验证**

在现代多异构数据库环境的桌面客户端开发领域，寻找一个兼顾轻量化体积、低内存占用与高开发效率的底层架构，是技术团队的核心诉求。长期以来，Electron 凭借完整的 Chromium 浏览器内核与 Node.js 运行时，几乎统治了该领域的开源及商业软件市场1。然而，其安装包动辄超过 150MB、冷启动耗时数秒以及 baseline 运行内存动辄数百兆的臃肿特性，也给终端用户带来了严重的系统负担1。  
Wails v3 的出现为打破这一技术局限提供了全新且完全可行的路径。Wails v3 摒弃了 Electron 的打包浏览器内核方案，转而利用各操作系统内置的原生 WebView 引擎进行前端 UI 渲染（Windows 平台采用 WebView2，macOS 采用 WebKit，Linux 平台采用 WebKitGTK）1。这种“Go 语言编译的核心后端 \+ 原生 WebView 渲染前端”的混合应用架构，将打包生成的单二进制可执行文件体积压缩至 10MB 至 15MB 级别，冷启动时间缩短到 0.5 秒以下，基础常驻内存也仅仅在 10MB 左右1。  
尽管目前 Wails v3 仍处于 Alpha 活跃开发阶段，底层的部分接口或特性依然存在变更风险并带有预发布警告3，但大量来自生产环境的真实应用已经充分佐证了其在稳定性方面的可靠表现5。在多窗口生命周期管理、系统托盘深度集成、以及基于静态分析的自动类型安全绑定等核心基础设施上，Wails v3 已经构建了极其完备的工业级支撑能力6。  
在先验行业实践中，已经涌现出若干基于 Wails 构建的优秀多数据库客户端案例。例如，完全基于 Go 语言和 Wails \+ Vue3 技术栈开发的现代多数据库可视化工具 keeper7，以及采用 Go 1.24、Wails v2、React 18、Zustand、Ant Design 5 和 Monaco 核心编辑器构建的跨平台、轻量级高性能数据库客户端 GoNavi8。特别是 GoNavi，其利用 Wails 后端与前端虚拟化数据网格的紧密结合，成功克服了 Electron 客户端在渲染高吞吐、高容量表数据集时的性能泥潭，充分证明了 Wails 架构在开发此类专业开发者工具时的天然优势与工程可行性1。  
下表详细对比了 Wails v3 与传统 Electron 平台及原生 Fyne 等技术在构建数据库工具场景下的关键数据指标：

| 评估指标 | Wails v3 混合架构 | Electron 容器平台 | Native 原生框架 (如 Fyne) |
| :---- | :---- | :---- | :---- |
| **可执行文件体积** | 约 10MB \- 15MB1 | 约 150MB+1 | 约 5MB \- 12Offset |
| **常驻内存占用** | 约 10MB1 | 100MB+1 | 约 10MB |
| **冷启动时长** | \< 0.5s1 | 2.0s \- 3.0s1 | 即时启动 (\< 0.1s) |
| **窗口集成模式** | 独立多窗口，进程级内存共享6 | 多窗口，每个窗口对应独立渲染进程 | 纯原生窗口树 |
| **IPC 通讯延迟** | \< 1ms (基于原生内存网桥)10 | 5ms \- 15ms (基于本地回环套接字) | 无 (同进程直接函数调用) |
| **系统托盘能力** | 支持模版图标、窗口依附、富菜单6 | 完整支持 | 基础支持 |
| **类型桥接校验** | 编译期静态分析，自动生成 TS 绑定6 | 需手动维护 schema 或依赖协议生成器 | 静态强类型，无需跨语言桥接 |
| **生态复用程度** | 完美复用主流 Web 前端框架与包生态1 | 完美复用全部 Web 生态 | 极其受限，需手动实现底层绘制 |

## **二、 容易实现的“插件化”异构数据库扩展方案**

在完成 MVP 阶段的 MySQL 连接支持后，工具需要具备横向扩展至 PostgreSQL、SQL Server、SQLite 等其他异构数据库的能力。为了在 Go 语言这一静态强类型编译语言中，以最轻量、最容易实现且符合开发直觉的方式构建“插件系统”，本方案摒弃了配置极端复杂、受编译器及系统依赖约束严格的 Go 标准库 .so/.dll 动态链接库插件模式12，提出了两种可自由选择的易用插件实现路径。

### **方案 A：编译期适配器静态注册（最轻量、最容易实现的“伪插件”方式）**

这是效仿 Go 语言标准库 database/sql 驱动注册机制的经典设计，也是最符合 Go 语言惯例、最容易实施的技术路径13。开发团队定义一个统一的 DatabaseAdapter 通用行为接口，放置于主程序的核心抽象包内。随后，每一种异构数据库的适配逻辑作为一个独立的子包进行开发（如 plugins/postgres、plugins/mssql）。这些子包在各自的 init() 初始化函数中，调用核心注册中心提供的全局方法，将当前的适配器实例注册到一个全局的 map\[string\]DatabaseAdapter 中13。  
当应用启动时，主程序只需通过副作用导出的方式，在启动入口处空导入这些驱动插件包（例如 import \_ "myapp/plugins/postgres"）14。这种设计将插件的边界在代码组织结构上完全划清，各插件包可独立进行模块化 CI 测试与维护，而核心框架在运行时只需简单地根据用户选择的数据库类型，从全局注册 Map 中检索出对应的适配器，便能透明地调度底层连接与元数据解析逻辑13。由于 Wails v3 具备极其优异的编译剪裁能力，集成 5-8 种常用关系型数据库的原生驱动对最终可执行文件的体积影响微乎其微17。

### **方案 B：运行时动态 JavaScript 脚本插件（基于 Goja 虚拟机，最容易实现的热插拔插件方式）**

若项目明确要求最终用户能够在不重新编译主程序的前提下，通过拖拽、下发脚本等方式动态添加对某些特定私有协议或新型数据库的支持，那么基于 **Goja**（一个纯 Go 编写、无需 cgo 编译依赖的 ECMAScript 5.1 兼容 JavaScript 解释器）构建动态脚本插件系统，是桌面端最容易实现的运行时热插拔方案18。  
在此方案下，Goja 虚拟机直接嵌入主应用中19。主程序提供一组底层的 Go 网络通讯与数据解析接口作为 SDK，并利用 Goja 的反射与类型推断能力将其挂载为 JavaScript 环境下的全局函数或全局对象19。用户编写的数据库驱动插件仅仅是一个 .js 格式的文本文档，存放在应用的 plugins/ 目录下19。应用在启动或点击刷新时，动态读取该目录下的脚本文件，利用 Goja 进行编译和解析，便能无缝调用脚本中实现的数据库连接与查询方法19。由于 Goja 拥有极高的跨平台连通性与极低的对象转换损耗18，这种在 Go 宿主环境中执行动态 JavaScript 脚本的方式，极大降低了运行时插件开发的门槛，特别适合用于非常规数据库驱动的轻快接入。  
下表对上述两种“容易实现”的插件化方案进行了核心维度对比：

| 评估维度 | 方案 A：编译期适配器静态注册 | 方案 B：运行时动态 JS 脚本插件 (Goja) |
| :---- | :---- | :---- |
| **实现代码复杂度** | 极低 (仅需基础接口实现与 init 机制)13 | 中等 (需要设计 Go ↔ JS 的 SDK 桥接层)20 |
| **热插拔与动态加载** | 不支持 (新增驱动需重新打包编译)12 | 完美支持 (随时放置/修改 .js 文件即时生效)19 |
| **外部编译依赖** | 无任何外部编译要求，天然支持 cgo 驱动12 | 极低 (纯 Go 虚拟机实现，无 cgo 困扰)18 |
| **运行期性能损耗** | 无 (纯原生 Go 语言方法级调用，性能极致) | 中等 (虚拟机解释执行网络 IO 与数据序列化损耗) |
| **调试与维护成本** | 极低 (完全融入标准 Go IDE 的断点与静态检查) | 中等 (需要捕获并格式化 JS VM 运行期异常)19 |
| **适用数据库场景** | 主流、稳定的通用关系型数据库 (MySQL, Postgres 等) | 定制化、演进频繁的私有协议或轻量级时序库 |

## **三、 MVP 阶段核心技术方案**

### **1\. 数据库连接生命周期管理**

在 Wails v3 桌面应用生命周期内，严禁将数据库连接池定义为多线程环境下的全局连接变量，以防止引发严重的并发竞争死锁、网络阻塞以及内存泄露隐患22。推荐的最佳实践方案是将代表数据库物理连接池的 \*sql.DB 实例，作为字段封装在全局 App 结构体内，实现底层连接生命周期与桌面应用生命周期钩子的深度契合22。  
当应用启动时，Wails 会自动触发 OnStartup 钩子函数，在此钩子内完成配置读取、数据源参数校验，并调用 sql.Open 初始化数据库连接池22。为了保证连接池的稳定运转，必须合理调用 SetConnMaxLifetime、SetMaxOpenConns 以及 SetMaxIdleConns 方法限制物理套接字的资源消耗，确保连接在被数据库服务器或防火墙强制切断前得到优雅释放23。当用户主动关闭应用或主窗体退出触发 OnShutdown 生命周期钩子时，应用将安全地阻断所有新发起的 SQL 请求，优雅调用关闭方法释放全部物理套接字连接，锁死内存资源不发生漂移22。

### **2\. 内嵌式安全 SSH 隧道**

在真实的企业网络拓扑中，生产或测试数据库往往被安全隔离在内网 VPC 中，仅开放 SSH 端口通过跳板机对外进行受限代理24。为了向用户提供原生的免配置安全直连体验，本工具在 Go 后端直接内嵌了 golang.org/x/crypto/ssh 纯 Go 客户端实现26，相比于让用户在本地配置 PuTTY 软件或命令行映射临时 localhost 端口的繁琐流程29，内嵌式拨号方案具有不可比拟的高连通率与安全性。  
通过调用标准 SSH 加密配置，工具与跳板机安全建立物理层 TCP 套接字通道26。此时，利用 MySQL 驱动原生提供的自定义 Dial 注册机制，调用 mysql.RegisterDialContext 注册一个绑定特定网络协议标识（如 mysql+ssh）的自定义拨号器26。在该拨号器内，通过 SSH 客户端直接与 VPC 内部的目标数据库网络地址进行拨号，从而将数据库驱动底层的网络传输完全引流至 SSH 安全加密隧道中，省去了本地多开监听端口的安全隐患26。  
需要注意的是，若后续利用此机制集成 PostgreSQL 的 pgx 驱动27，必须规避 pgx 默认在客户端进行域名 DNS 物理校验的冲突风险（若目标库地址是内网私有 DNS，在本端解析必然报错超时）32。应对策略是在配置 pgxpool.Config 时，除了指定 DialFunc 透传至 SSH 客户端外，还必须手动重写 LookupFunc，将主机域名解析直接透传给 SSH 远程跳板机代为执行，从而保障异构环境下的平稳连接32。

### **3\. 大数据集传输架构**

高频 SQL 往往单次返回多达数万条数据集。在 Wails 架构下，这些大批量数据必须穿过底层的 IPC 网桥进入前端渲染树中1。虽然 Wails v3 已经针对 WebView2 底层 2MB 的 IPC 数据传输上限在后端内置了自动分块（Chunking）机制34，但一次性向前端推送几万个复杂的嵌套 JSON 仍会引发严重的序列化耗时，并直接因 DOM 节点数突破极限而导致整个前端界面彻底卡死35。  
为了保障高连通吞吐下的界面响应，本方案引入了“后端动态物理分页 \+ 前端可视区虚拟滚动网格”的协同架构。前端采用 TanStack Virtual 引擎，基于屏幕当前的滚动距离、容器高度与行间距，计算出当前可视区域内仅需渲染的有限 DOM 行数（通常仅 30 至 50 行）35，从而将渲染开销由全量的 ![][image1] 降到常数级的 ![][image2]35。  
尽管虚拟滚动大幅降低了前端 DOM 树压力，但对于在大数据集中点击字母定位、拖动滑块跳转等非连续跳变场景，未加载的数据区域在拉取时会产生明显的白屏等待卡顿38。为了提供无缝的滚动体验，系统在滚动触发边界时，利用 Cursor 机制向后端异步拉取对应页码的数据38，并在前端开辟一个带有上下缓冲区的 LRU 内存缓存池（Memory Cache），提前异步预读取（Pre-fetching）上下相邻的数据页并缓存于内存切片中35。这种软硬结合的物理分页与网格虚拟化架构，完美兼顾了检索完备性与界面滚动流畅性35。

### **4\. 动态 SQL 结果集反射扫描**

面对动态 SQL 查询语句，由于各列字段名、返回列数、底层数据类型在编译期均是不可知的，传统基于结构体的静态 ORM 映射模式在此场景下完全失效，必须在后端构建高性能的运行时动态反射扫描器。  
在通过连接池执行 Query 查询后，通过调用 rows.Columns() 动态截取当前投影的所有返回列名40；调用 rows.ColumnTypes() 深度解析包含底层数据物理类型名（如 VARCHAR、BIGINT）、可空性、精度等列级别元数据信息40。在数据提取时，构建等长于列数的 \[\]any 空接口切片以及专门用于接收字节的 \[\]sql.RawBytes 指针切片传入 rows.Scan 函数中进行底层物理数据的反射写入41。扫描完成后，类型推断逻辑通过类型开关（Type Switch）和数据库类型映射，将底层的原始字节切片安全格式化为对应的 Go 基本类型（数字、布尔、格式化时间字符串等）41。每行数据最终被动态拼装成 map\[string\]any 键值对，汇聚为二维切片通过 IPC 返回10。  
在类型序列化跨界传递方面，Wails v3 的静态分析器发挥了极大的便利。底层的 Go 基本类型、嵌套切片和 Map 数据结构会被精准、无缝地映射为 TypeScript 的类型定义（Go 的 map\[K\]V 自适应转译为前端的 Record\<K, V\>，切片转译为数组，\[\]byte 转译为 JS 的 Uint8Array）10。为了在复杂多窗口环境下提升前端开发和接口调用的可维护性，可以通过执行带 \-names 指令的编译命令 wails3 generate bindings \-names，使得自动生成的 TypeScript 绑定文件强制保留完整的 Go 字段命名与位置参数11，这极大降低了大型重构场景下的接口差错成本。

## **四、 多窗口管理与高级交互方案**

Wails v3 引入了极其灵活的命令式多窗口管理 API，彻底告别了先前版本中单一、声明式主窗体的设计缺陷，这为构建拥有复杂独立面板的现代数据库客户端提供了强大的支撑6。各子窗体不仅拥有独立、完整的生命周期，更能实现低延迟的窗体间通讯，从而能够原生承载诸如“连接设置面板”、“主数据网格窗”、“SQL 执行轨迹监视器”等多视口交互任务6。  
在窗口创建与属性配置中，开发团队可以调用 app.Window.NewWithOptions 动态拉起新的 WebviewWindow 实例9。通过细粒度地调配 WebviewWindowOptions 配置实体，可以实现复杂的窗口定制44：

* **AlwaysOnTop**：在进行 SQL 编辑器编写时，可将元数据表结构查看器窗口锁定在最上层，防止多屏操作时焦点丢失引发频繁遮挡44。  
* **Frameless**：为应用定制无边框的、高度美观的现代暗色系顶部标题栏导航44。  
* **Hidden**：在应用加载数据库长事务或高负荷物理表时，采用先隐藏再显示（即创建后暂不展现，等前端静态资产与骨架屏完全载入后再执行 Show()）的策略，优雅消除冷启动时 WebView 渲染引擎无可避免产生的瞬间白屏闪烁现象6。  
* **BackgroundColour**：支持直接设定底层的原生色值背景，保证在前端 DOM 加载出来之前，窗口颜色与应用 UI 主题浑然一体45。

在交互控制流程中，多窗口之间的树形父子层级挂载同样至关重要。例如，当用户在配置列表点击新建连接时，可以基于当前主活动窗调用 parentWindow.AttachModal(childWindow) 方法强行挂载一个连接配置窗9。这不仅在 UI 逻辑上建立起高聚合的模态级联层级，在 macOS 平台上更能无缝呈现为优雅的原生 Sheet 抽屉下拉动画，极大提升了应用的交互质感9。  
此外，为了防止由于用户频繁意外点击、导致未保存的复杂长 SQL 脚本直接丢失，系统注册了底层的窗体关闭拦截钩子（RegisterHook）44。当用户关闭窗口触发 events.Common.WindowClosing 事件时，Go 后端会拦截此控制流，通过 IPC 反向查验前端编辑器的脏数据标记6。若存在未保存的修改，则弹出优雅的原生确认框阻断销毁流，赋予用户取消关闭的权利，切实锁定了核心业务数据的安全性6。

## **五、 系统风险、挑战与规避方案**

尽管 Wails v3 拥有极其耀眼的技术指标，但作为一款立足于服务开发人员与 DBA 的专业级数据库工具，在复杂的生产环境落地过程中，仍需面对一些特定的系统风险：

* **框架 Alpha 版本更迭不向下兼容的风险**：Wails v3 仍在快速演进中5。为了使项目具备坚实的稳定性，推荐规避直接使用 github.com/wailsapp/wails/v3@latest 这种不稳定指向，而是在应用的 go.mod 文件中强制锁死到经过团队内部充分测试的特定 Alpha Commit 节点版本（例如已具备高稳定性的 v3.0.0-alpha.73）3。在主程序代码设计中，利用防腐层（Corruption-Free Layer）隔离 Wails 底层 API，保证底层框架大幅更迭时主业务逻辑无感5。  
* **高吞吐数据查询带来的 GC 性能滑坡**：高频执行大批量查询会产生大量频繁创建、销毁的临时的 \[\]byte 和 map 容器，这会令 Go 运行时（Runtime）的垃圾回收器出现 GC 活动骤增和明显的 STW（Stop-The-World）微小卡顿，进而也会导致前端 WebView 的渲染帧率出现瞬间抖动。规避策略是引入 sync.Pool 建立复用扫描器缓冲区池17，同时在前端表格组件中引入 Debounce 防抖逻辑，避免同一毫秒内频繁触发多窗口的重新渲染，抚平 GC 曲线17。  
* **高频哈希碰撞与大结果集映射耗时**：动态扫描将每行数据封装为 map\[string\]any41。在 Go 语言底层，哈希 Map 虽具备 ![][image2] 的时间复杂度查找，但在极大数据集循环中，高频的 Hashing 计算在性能敏感场景下明显落后于连续的 Slice 连续寻址操作48。因此，若针对极大规模数据检索进行极速优化，后端可以重构为双切片（Slice）传输机制（一个 \[\]string 用于记录有序的列名，一个 \[\]any 一维平铺存储连续行数据，前端通过一维指针按步长偏移还原）48。这既保留了数组物理存储的高度连续性，又有效消除了 Map 带来的内存碎片与碰撞损耗，大幅降低了跨平台 IPC 的传输载荷。  
* **多窗口并发下的物理连接池死锁与事务交叉**：Wails v3 的所有服务接口在底层默认被多个并发窗口和系统线程共享调度10。如果用户通过多个独立的 Wails 窗口并发执行多个极其繁重的长耗时 SQL 查询，由于底层连接池默认的无序抢占机制23，极易导致不同窗口的查询、未提交事务在同一个物理套接字连接上发生逻辑交叉或直接遭遇物理死锁23。规避方案是设计一个带有“独占标记”的物理连接池会话管理器（Session Manager）。当用户在窗口 A 开启事务或执行独占式复杂操作时，管理器在 Go 后端通过 db.BeginTx(ctx, ...) 分离出一个隔离的事务会话并与该窗口的唯一 ID 进行生命周期锁定，保证在事务提交或回滚前，其底层的物理连接连接套接字决不被其他并发窗口借调复用，从而物理隔离各种并发时序冲突。

#### **引用的著作**

1. Why Wails?, [https://v3.wails.io/quick-start/why-wails/](https://v3.wails.io/quick-start/why-wails/)  
2. wailsapp/wails: Create beautiful applications using Go \- GitHub, [https://github.com/wailsapp/wails](https://github.com/wailsapp/wails)  
3. Wails v3.0.0-alpha.73 release notes (2026-02-27) \- Awesome Go, [https://go.libhunt.com/wails-changelog/3.0.0-alpha.73](https://go.libhunt.com/wails-changelog/3.0.0-alpha.73)  
4. Roadmap \- Wails v3, [https://v3.wails.io/status/](https://v3.wails.io/status/)  
5. Anyone using Wails v3? How's the stability? : r/golang \- Reddit, [https://www.reddit.com/r/golang/comments/1q8afx1/anyone\_using\_wails\_v3\_hows\_the\_stability/](https://www.reddit.com/r/golang/comments/1q8afx1/anyone_using_wails_v3_hows_the_stability/)  
6. What's New in Wails v3, [https://v3.wails.io/whats-new/](https://v3.wails.io/whats-new/)  
7. wails · GitHub Topics, [https://github.com/topics/wails?l=typescript\&o=asc\&s=updated](https://github.com/topics/wails?l=typescript&o=asc&s=updated)  
8. GitHub \- Syngnat/GoNavi: 现代化、原生体验的数据库管理工具，支持 MySQL, [https://github.com/Syngnat/GoNavi](https://github.com/Syngnat/GoNavi)  
9. Multiple Windows \- Wails v3, [https://v3.wails.io/features/windows/multiple/](https://v3.wails.io/features/windows/multiple/)  
10. Method Bindings \- Wails v3, [https://v3.wails.io/features/bindings/methods/](https://v3.wails.io/features/bindings/methods/)  
11. Advanced Binding \- Wails v3, [https://v3.wails.io/features/bindings/advanced/](https://v3.wails.io/features/bindings/advanced/)  
12. Clean Architecture: A Practical Example of Dependency Inversion in Go using Plugins, [https://cekrem.github.io/posts/clean-architecture-and-plugins-in-go/](https://cekrem.github.io/posts/clean-architecture-and-plugins-in-go/)  
13. Ask r/golang: How best to dynamically load/run code based on a string value? \- Reddit, [https://www.reddit.com/r/golang/comments/aky84h/ask\_rgolang\_how\_best\_to\_dynamically\_loadrun\_code/](https://www.reddit.com/r/golang/comments/aky84h/ask_rgolang_how_best_to_dynamically_loadrun_code/)  
14. Idiomatic approach to a Go plugin-based system \- Stack Overflow, [https://stackoverflow.com/questions/35708608/idiomatic-approach-to-a-go-plugin-based-system](https://stackoverflow.com/questions/35708608/idiomatic-approach-to-a-go-plugin-based-system)  
15. Writing a Go SQL driver | DoltHub Blog, [https://www.dolthub.com/blog/2026-01-23-golang-sql-drivers/](https://www.dolthub.com/blog/2026-01-23-golang-sql-drivers/)  
16. Design patterns in Go's database/sql package \- Eli Bendersky's website, [https://eli.thegreenplace.net/2019/design-patterns-in-gos-databasesql-package/](https://eli.thegreenplace.net/2019/design-patterns-in-gos-databasesql-package/)  
17. Performance Optimisation \- Wails v3, [https://v3.wails.io/guides/performance/](https://v3.wails.io/guides/performance/)  
18. goja package \- github.com/dop251/goja \- Go Packages, [https://pkg.go.dev/github.com/dop251/goja](https://pkg.go.dev/github.com/dop251/goja)  
19. Exploring Goja: A Golang JavaScript Runtime \- JT Archie, [https://jtarchie.com/posts/2024-08-30-exploring-goja-a-golang-javascript-runtime](https://jtarchie.com/posts/2024-08-30-exploring-goja-a-golang-javascript-runtime)  
20. How to support custom Javascript scripting in Go Applications \- Prasanth Janardhanan, [https://prasanthmj.github.io/go/javascript-parser-in-go/](https://prasanthmj.github.io/go/javascript-parser-in-go/)  
21. Goja: A Golang JavaScript Runtime \- Hacker News, [https://news.ycombinator.com/item?id=41445803](https://news.ycombinator.com/item?id=41445803)  
22. Best practices when managing db connections in a Wails application? \#4343 \- GitHub, [https://github.com/wailsapp/wails/discussions/4343](https://github.com/wailsapp/wails/discussions/4343)  
23. go-sql-driver/mysql \- GitHub, [https://github.com/go-sql-driver/mysql](https://github.com/go-sql-driver/mysql)  
24. How to Set Up an SSH Tunnel for IPv4 Database Access (MySQL/PostgreSQL) \- OneUptime, [https://oneuptime.com/blog/post/2026-03-20-ssh-tunnel-database-mysql-postgres/view](https://oneuptime.com/blog/post/2026-03-20-ssh-tunnel-database-mysql-postgres/view)  
25. Is it normal to use an SSH tunnel to access a production database? : r/learnrust \- Reddit, [https://www.reddit.com/r/learnrust/comments/11poo5h/is\_it\_normal\_to\_use\_an\_ssh\_tunnel\_to\_access\_a/](https://www.reddit.com/r/learnrust/comments/11poo5h/is_it_normal_to_use_an_ssh_tunnel_to_access_a/)  
26. Run a SQL Query Through SSH \- Gopher Coding, [https://gophercoding.com/sql-query-through-ssh/](https://gophercoding.com/sql-query-through-ssh/)  
27. pgxpool.ConnectConfig is not able to connect over ssh tunnel · Issue \#1661 · jackc/pgx, [https://github.com/jackc/pgx/issues/1661](https://github.com/jackc/pgx/issues/1661)  
28. Using MySQL / MariaDB via SSH in Golang \- GitHub Gist, [https://gist.github.com/vinzenz/d8e6834d9e25bbd422c14326f357cce0](https://gist.github.com/vinzenz/d8e6834d9e25bbd422c14326f357cce0)  
29. Article: How can I connect to a Database through an SSH tunnel? \- Boomi Community, [https://community.boomi.com/s/article/How-can-I-connect-to-a-Database-through-an-SSH-tunnel](https://community.boomi.com/s/article/How-can-I-connect-to-a-Database-through-an-SSH-tunnel)  
30. MySQL connection over SSH tunnel \- how to specify other MySQL server? \- Stack Overflow, [https://stackoverflow.com/questions/18373366/mysql-connection-over-ssh-tunnel-how-to-specify-other-mysql-server](https://stackoverflow.com/questions/18373366/mysql-connection-over-ssh-tunnel-how-to-specify-other-mysql-server)  
31. Connect pgx to a PostgreSQL-dialect database | Spanner \- Google Cloud Documentation, [https://docs.cloud.google.com/spanner/docs/pg-pgx-connect](https://docs.cloud.google.com/spanner/docs/pg-pgx-connect)  
32. Connecting via SSH fails to resolve host · Issue \#1724 · jackc/pgx \- GitHub, [https://github.com/jackc/pgx/issues/1724](https://github.com/jackc/pgx/issues/1724)  
33. Setting a DialFunc for pgxpool.Config \- Stack Overflow, [https://stackoverflow.com/questions/70774969/setting-a-dialfunc-for-pgxpool-config](https://stackoverflow.com/questions/70774969/setting-a-dialfunc-for-pgxpool-config)  
34. fix(v3): chunk large IPC payloads to bypass WebView2 2 MB body limit \#1778 \- GitHub, [https://github.com/wailsapp/wails/actions/runs/25533475205](https://github.com/wailsapp/wails/actions/runs/25533475205)  
35. Frontend Performance Optimization: List Virtualization \- ExplainThis, [https://www.explainthis.io/en/swe/list-virtualization-performance](https://www.explainthis.io/en/swe/list-virtualization-performance)  
36. Optimizing Large Lists in React : Virtualization vs. Pagination \- Ignek, [https://www.ignek.com/blog/optimizing-large-lists-in-react-virtualization-vs-pagination](https://www.ignek.com/blog/optimizing-large-lists-in-react-virtualization-vs-pagination)  
37. Optimizing Large Datasets with Virtualized Lists | by Eva Matova | Medium, [https://medium.com/@eva.matova6/optimizing-large-datasets-with-virtualized-lists-70920e10da54](https://medium.com/@eva.matova6/optimizing-large-datasets-with-virtualized-lists-70920e10da54)  
38. Paginated Virtualized List with ability to jump to specific sections of the list, how to smoothly handle jumping to data not yet collected : r/reactnative \- Reddit, [https://www.reddit.com/r/reactnative/comments/15bbltn/paginated\_virtualized\_list\_with\_ability\_to\_jump/](https://www.reddit.com/r/reactnative/comments/15bbltn/paginated_virtualized_list_with_ability_to_jump/)  
39. How to power a windowed virtual list with cursor based pagination? \- Stack Overflow, [https://stackoverflow.com/questions/68498501/how-to-power-a-windowed-virtual-list-with-cursor-based-pagination](https://stackoverflow.com/questions/68498501/how-to-power-a-windowed-virtual-list-with-cursor-based-pagination)  
40. database/sql \- Go Packages, [https://pkg.go.dev/database/sql](https://pkg.go.dev/database/sql)  
41. Dynamic scan you query DB to struct golang \- GitHub Gist, [https://gist.github.com/thiagozs/772fd1246ef7f6cbca06f6a1fbf6dc4e](https://gist.github.com/thiagozs/772fd1246ef7f6cbca06f6a1fbf6dc4e)  
42. Trying to scan into a struct dynamically from SQL \- Google Groups, [https://groups.google.com/g/golang-nuts/c/BjTKfsd8ZKQ](https://groups.google.com/g/golang-nuts/c/BjTKfsd8ZKQ)  
43. Combine row.Scan and rows.Scan interfaces in go? \- Stack Overflow, [https://stackoverflow.com/questions/21095630/combine-row-scan-and-rows-scan-interfaces-in-go](https://stackoverflow.com/questions/21095630/combine-row-scan-and-rows-scan-interfaces-in-go)  
44. Window Basics \- Wails v3, [https://v3.wails.io/features/windows/basics/](https://v3.wails.io/features/windows/basics/)  
45. Window Options \- Wails v3, [https://v3.wails.io/features/windows/options/](https://v3.wails.io/features/windows/options/)  
46. Window API \- Wails v3, [https://v3alpha.wails.io/reference/window/](https://v3alpha.wails.io/reference/window/)  
47. Changelog \- Wails v3, [https://v3.wails.io/changelog/](https://v3.wails.io/changelog/)  
48. My friend is saying using maps in inefficient. : r/golang \- Reddit, [https://www.reddit.com/r/golang/comments/ueydyg/my\_friend\_is\_saying\_using\_maps\_in\_inefficient/](https://www.reddit.com/r/golang/comments/ueydyg/my_friend_is_saying_using_maps_in_inefficient/)  
49. How about performance of map against slice? \- Getting Help \- Go Forum, [https://forum.golangbridge.org/t/how-about-performance-of-map-against-slice/4415](https://forum.golangbridge.org/t/how-about-performance-of-map-against-slice/4415)

[image1]: <data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAADQAAAAaCAYAAAD43n+tAAACYklEQVR4Xu2WuYtUQRDGS0VFEBUUNtlAQTQQLzDxAPHEwGBVTMwUQVjRxVQEEwNFNPEP2DUxMDEwELxAjTRbBEHwAlEMvBdUvOujq3dqvqmefe7MrMHODz7o/qr6eK9f92uRLpOTDWy0yG42xsNW1QIrT1GdVx1XzR/NiPkiKb+d7FENsvkvvFNdtPJn1W/VH6djFmPuqjayaWBCI1LrY6gumvgl9eNscbEHqn2uXol5kjpabnWUt9fCctA8iFkksc/4CUfcVi1m0yi1KYIGfVZ+pnroYpm8Wj3kf1DtIo/Bp3hNdUVSH9HeaDbpF6oLbJZ4o/po5RVS7hifImJ7yS/lewZUa6xcWqWfbDh2StymgYWSEuda/aqqdzRaD/YPctc574B5Y4G9mcHLQ5s5zluiOuPqEVXGkU9SMVG5Lo252LCvyIvw7bBPUH/svEuq2a4egTZr2WRKyx+R9xB798hjsH+w8h4el/uNQM5+NhkkYVJVQO7JwMvHfAm/fzJHJbU9a/XvLlYC+afZZPhNlTghcR68ITaJ92wYeexlqlMUi0DuOTaZKg80XVJOvj14vqnusEmU+r8hKfZcNYtiEcjFyjblqaTEJxww8sOs54AxrHrJpgPtb7FpTJVqLzSDPH/ChvhOv6qWmj9T0skDv9npc0jKE5qmequ6zwEH7n9Y5SqUxmlghuqH1B4sy9+nmhENdFnSLwH/H/x3cFeLWKk6zGYArmHROB0BA21js808Uh1hs1OslupH/3iZsNXJvFatYrNN3FTtYHMiKO2TVtgs6XD6b2xio0X62ejSpUtn+AuVm54gXWJPmAAAAABJRU5ErkJggg==>

[image2]: <data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACsAAAAZCAYAAACo79dmAAACAUlEQVR4Xu2VP0gcQRjFPwVNYVCDghYWamOlmDYGRPxTayGkDoIkoijYKCFWdkFIp50hoIVoY2dnbEIgRUiTQtNEQ0REDUTFEM332Jm7uefM7RyoINwPHrf3m7e7c3PLjkiR+8VTFik8Y1EoPZpac1yimdNMa2oyDT+nkvQLoVnzhWUsh5p35vi35lJz5WTCjDEfNJ0sHSY1L1ga3moWWOajWpLJtJrvOO7LDsuQcQjTJH6/rLmQ7Hkvc4dz8J0fBOV+c/xd89UZs9hVriN/pBkgx6RN9r1EPg6/NMfmuE3CvxKPB8YGyYf6LmmTrZKI6zRKUkIZrGsaMqO54HlF94njnhuXRtpkATqVLF1OJO5mYEOudz9p9sj5iJ3sFEsXFHgCIewzy26LnA+cN8KSQMe+ibyggBvGgO6Mx+W9gQG9UZbEmeYjS5fYlX0l/h7cIksP6I2xJP5oPrN0iZlsmSQdu6u5nGs2WXrA+eMsCXTWWLrsSFLa5gGDnWgHDxjwbvzB0gOuEdr9LOhgWw9SKtnVxTPTYvwDzZLxD43zMSzp/wz+EXTe8ACBziOWTLnmr2QnbdPtlvIQmuyK5kCzK8nq43Nfki2YqZDwdW4U3KSXZYHMa1ZZ3gaPJf71F+JOVtXyU9POMpLXmlmWt80/FhHUa76xvCu6WKSQtlEUKWL5DyFmg+Bwxzo/AAAAAElFTkSuQmCC>