# Changelog

Termcp Harness 的重要功能与架构变更记录。

当前版本在 `dev` 分支持续迭代，不承诺跨版本兼容。版本号采用语义化版本的基础形式，每次可独立验收的功能里程碑递增补丁号。

## [v0.1.10] - 2026-11-05

`v0.1.10` 是 `v0.1.9` 之后的后向兼容能力更新；`v0.2.0` 继续保留给 MCP 注册表写入生命周期。

### Added

- 新增持久本地 Core profile 和 `core service install/start/status/stop`，Core 可由公开 daemon 命令独立运行；Harness 将其识别为 external，退出时不停止。
- 新增 `GET /v1/core/sessions` 和 Desktop Core Sessions 面板，可发现 Harness 启动前由其他 Termcp 客户端创建的 Session，并在验证 running Shell 后直接观察终端。
- 新增迟附着验证：外部创建 Session 后启动 Harness，清单可见；Harness 停止后 Core 与 Session 继续存在。
- NSIS 安装器增加“持久 Core”选项，首次安装可选择 managed 或 persistent Core；静默安装支持 `/PERSISTENT_CORE=1` 和 `/PERSISTENT_CORE=0`。
- 完整包与安装器包含 headless `termcp-harness.exe`，支持安装后 Core profile 查询、启停和维护。
- 新增可复现的 `desktop/scripts/build-installer.ps1` 和受版本控制的 NSIS 模板。

### Changed

- 退出确认框改为窗口居中模态，并在小窗口中限制高度和允许内部滚动；外部 Core 模式下提示退出只停止 Harness Runtime。
- 持久 Core 升级会停止旧 Core、替换文件、重新校验版本与 SHA-256，再启动新 Core。
- 已有 profile 默认保留 endpoint、数据目录和凭据引用；显式取消持久模式会把 profile 重命名为 `.disabled.*` 备份并保留数据。
- 卸载会停止持久 Core，但保留用户 profile 与 Core 数据目录。

### Verification

- `go test -race ./...`、`go vet ./...`、`go build ./...` 和 `go mod tidy -diff` 通过。
- 48 项前端测试、TypeScript 检查、Vite 构建和 Wails 生产构建通过。
- 安装器 E2E 覆盖 managed 安装、persistent 安装、默认升级保留、显式停用、Session 保留和卸载清理。

### Known limitations

- 尚无 Windows Service、登录自启动、托盘控制或 Core 崩溃自动重启。
- Core 重启后的 DEAD Session 可出现在清单中，但完整只读历史终端视图尚未实现。
- 外部 Agent 的模型 transcript、Harness 工具策略和审批历史不能从 Core 日志反推。
- 升级期间 live SSH/PTY 会断开；历史 Session 和输出保留，但不会恢复 live 连接。

## [v0.1.9] - 2026-11-04

`v0.1.9` 是 `v0.1.6` 之后首个计划实际发布的累积版本。`v0.1.7` 仅为未单独打 tag 的开发快照，`v0.1.8` 未形成单独快照；这两个版本号不得表述为已发布 tag。

### Added

- Desktop 默认内嵌 Harness Runtime，并从可执行文件旁的 `core/` 发现、校验和启动匹配的受管 Core；外部 Headless Runtime 仍可通过 `--runtime` 显式接入。
- 新增受管 Core bundle、Desktop 内嵌 Runtime 装配和发布打包流程；发布目录包含 Runtime 与匹配 Core 的版本、commit、SHA-256、逐文件校验和及 ZIP 校验值。
- 新增发布包冒烟检查：从包内入口启动 Desktop，确认相邻 Core 进程存在且身份为 `managed`，随后按精确包内路径回收测试进程。
- 新增 Codex global MCP 发现与受 Bearer 保护的显式注册，发现、注册、连接、能力读取、启用和可加载状态继续保持独立。

### Changed

- Runtime、Desktop 前端、Wails 产品和发布包统一使用 `v0.1.9`。
- 保留 `v0.1.7` 引入的恢复基线、MCP 发现、状态诚实性和 Windows 图标能力；其历史验证记录继续保留在下方未发布快照章节。

### Verification

- `go test -race ./... -count=1`、`go vet ./...` 和 `go build ./...` 通过。
- `npm run check`、47 项前端测试和 `npm run build` 通过。
- `wails build -m -nosyncgomod -clean -s -nocolour` 生产构建通过。
- Harness `adc63ca` 与 Core 本地集成提交 `d8261c7` 生成干净 `release-candidate` 包；Core bundle SHA-256 与 `release.json` 一致。
- 发布包内 Desktop 冒烟通过：相邻 Core 被识别为 `managed`，测试结束后未遗留 Desktop 或 Core 进程。

### Known limitations

- 当前交付包含便携 ZIP 和 NSIS 安装器，但不包含代码签名、自动更新或 Core 补丁回滚。
- Core 发布同步仍未关闭；本次候选固定到本地集成提交 `d8261c7`，不能表述为该环境已有包含全部修复的正式 Core 发布包。
- MCP 新增、编辑、移除、Registry 持久化和重启恢复仍属于 `v0.2.0`。

## [v0.1.7] - 2026-11-03 — 未单独发布的开发快照

### Added

- 新增 headless `recovery status`，可检查 Event、Session、Approval Store 与恢复清单的一致性，不启动 Runtime。
- 新增 `recovery rebaseline --confirm`，允许用户在 Runtime 停止后显式以当前可读 Store 建立新的恢复基线。
- 重建结果同时返回重建前后的恢复快照；不可读 Store、缺失清单参数或未确认操作会被拒绝。
- 新增原创“猫耳终端”图标，提供 SVG 源、1024px PNG、16–256px 多尺寸 ICO，并同时嵌入 Wails Desktop 与 Windows CLI。
- 新增本机 Codex global MCP 发现：返回来源、`global/codex` tags、transport、可加载性和受限原因，不暴露 command、args、env、header、token 或配置路径。
- 新增受 Bearer 保护的发现项显式注册；支持解析受约束的 Windows npm `.cmd` shim，并把注册、连接、能力读取和启用继续保持为独立状态。

### Fixed

- Runtime 不可达时不再使用默认绿色状态点；Runtime、Core 和 MCP Servers 统一显示灰色“状态未知”，不再把无法读取的 Core 误报为“未配置”。
- 自定义标题栏的最大化按钮读取真实窗口状态，最大化时显示叠加方框并改为“还原窗口”，恢复后重新显示单方框和“最大化”。
- Core 的 shell 输入 review 结果使用 `Submitted for review.` 时仍会收敛为 `approval_pending`，不再因只识别冒号形式而误记为 `succeeded`。
- SSE 进入重连状态时，Desktop 不再保留旧的“Runtime 运行中”绿色状态；Runtime、Core 和 MCP 统一降级为状态未知。

### Changed

- Runtime、Desktop 前端和 Wails 产品版本统一为 `v0.1.7`。
- 修复 Wails 构建使用 `-nopackage` 跳过 Windows 资源编译、导致 EXE 无项目图标的问题。
- 显式重建不会合并 Store、回放业务状态、修复损坏文件或自动恢复审批、工具调用和 Agent Loop。
- 重建输出不返回 Store 路径或业务正文；失败时保留原恢复清单。

### Verification

- 恢复 CLI 定向测试覆盖未验证状态只读检查、缺少确认、代次不一致重建、不可读 Store 拒绝和路径脱敏。
- `go test ./cmd/termcp-harness -run '^TestRecovery' -count=20` 通过。
- `go test -race ./internal/recovery -count=1 -v` 通过。
- `go test -race ./... -count=1`、`go vet ./...` 和 `go build ./...` 通过。
- `go mod tidy -diff` 无差异。
- `npm run check` 通过。
- `npm test` 通过，共 46 项；`npm run build` 通过。
- 在复用已独立构建的前端产物后，`wails build -m -nosyncgomod -clean -s -skipbindings` 成功生成带 Windows 图标资源的桌面二进制。
- 图标脚本连续运行两次生成相同 PNG/ICO SHA-256；amd64/arm64 CLI 资源重新生成成功，CLI 与 Wails 产物均提取到 32×32 图标。
- 真实本机 Codex global 发现识别 6 个 MCP，其中远程 HTTP 2 个被明确标记为当前 v1 不兼容；`termcp` npm stdio shim 经受保护注册后达到 `connected`、能力已读取、enabled 和 `ready=true`，工具数为 1。
- 真实 Core `v0.2.8-rc.4.0.20261030183716-938c53f4334d+dirty` 通过只读、12 路并发、幂等写入、副作用超时未知、审批批准/拒绝/并发决定/过期、Session 关闭后 `cancelled`、Terminal 历史/实时连续性、20,000 行洪峰和 Shell 正常结束测试。
- Kali 专用 Session 通过 POSIX 幂等写入、副作用超时 `unknown`、20,000 行输出洪峰、调用取消和 MCP 传输断连测试。
- 真实 Wails 窗口完成启动、重启、专用 Agent Session 选择、Core Session 关联、Terminal Shell 自动连接，以及在窗口中输入并观察到 `echo harness-wails-e2e` 输出。
- 真实 Kali → Debian 13 靶机链路完成审批断线时序验证：Core 接受决定后代理丢弃响应，Harness 收敛为 `unknown`，Wails 显示“结果未知”；断线窗口结束后只读核对收敛为 `approved/submitted`，Session 自动完成。重开 Wails 后恢复同一 Session、布局和截至 `sequence=27` 的事件轨迹；停止并重启 Runtime 后，Wails 从“状态未知”自动恢复为在线，并明确显示“部分恢复”警示。
- 从 CLI 与 Wails 两个 Windows EXE 提取到新图标；Wails 正常资源构建与 CLI 的 amd64/arm64 `.syso` 资源均通过验证。

### Known limitations

- 命令在检查与写入之间没有跨进程 Store 锁；必须在 Harness Runtime 完全停止后使用。活动锁硬化项登记为 `HARNESS-RECOVERY-LOCK-001`，当前 Deferred。
- `complete` 只证明当前文件集合与新建基线一致，不证明各 Store 的业务内容可以安全继续。
- 不提供跨文件原子事务、自动恢复或自动修复。
- Kali Session 关闭后的审批终止已由 Windows 专用 Session 严格契约测试覆盖；本轮未额外重跑 Kali 版本。
- 图标生成脚本不解析 SVG；矢量参考和脚本中的绘制步骤需要同步维护。
- Vite 主 JavaScript chunk 约 625 KiB（gzip 约 177 KiB），生产构建成功但保留大小告警；Terminal 按需加载登记为 `UX-BL-009`，当前 Deferred。
- 真实审批断线与 Wails 跨进程恢复已验证；重复/乱序/旧 revision 回执注入、决定提交期间 Runtime 重启及新事件到达时的 cursor 连续性仍待补测。
- MCP 发现当前只支持 Codex global；OpenCode、其他客户端、用户指定目录、已注册项卸载和配置变更检测尚未实现。
- MCP 新增、编辑、移除、Registry 持久化和重启恢复计划在 `v0.2.0`，不进入 `v0.1.9` 推送范围；见 `docs/development/MCP-MANAGEMENT-v0.2.0.md`。

## [v0.1.6] - 2026-11-02

### Added

- 新增可选 `--recovery-manifest-file`，记录 Event、Session、Approval Store 的存在性、大小和 SHA-256 指纹。
- 新增 `GET /v1/recovery`，返回 `not_configured`、`unverified`、`complete` 或 `partial` 恢复状态。
- Desktop 诊断面板显示恢复清单版本、基线时间、各 Store 状态、大小和指纹摘要。
- 不完整或未验证恢复会在 Desktop 主区域显示警示，不把部分数据表述为完整恢复。

### Changed

- Runtime 首次空恢复可建立清单基线；正常停止后更新基线。
- 启动时已判定为 `partial` 的恢复不会在退出时静默覆盖为新基线。
- 恢复清单不保存 Store 路径、事件正文、Session 内容、工具参数、审批详情或凭据。

### Verification

- 恢复清单测试覆盖未配置、未验证、匹配、Store 变更、Store 缺失、不可读、损坏清单和未知版本。
- Control API 测试确认恢复状态可见且不泄露 Store 路径。
- Control Client、Desktop Bootstrap、CLI 恢复参数和前端生产构建通过。
- `go test -race ./...`、`go vet ./...`、`go build ./...` 通过。

### Known limitations

- 不提供跨文件原子事务。
- `complete` 不等于审批队列、MCP 连接或活动工具执行已恢复。
- 不完整的恢复尚无自动修复命令；必须先核对或恢复原 Store。
- 自动 Agent 恢复仍未启用。

## [v0.1.5] - 2026-11-02

### Changed

- 顶部 Runtime、Core 和 MCP 状态改为紧凑仪表条，每个对象只显示一次状态值。
- 修复 `Core 未配置 Core 身份` 的重复拼接；已配置 Core 使用本地化状态，未配置时显示 `Core 未配置`。
- Session 状态汇总压缩为单行 `活动 / 等待 / 异常 / 未知` 指标，使用边框和状态色区分，不再为每项重复状态圆点。
- 完整状态口径继续保留在 `title` 和 `aria-label` 中：活动包含运行与停止中，等待包含审批与执行，异常包含失败/阻断/过期，未知保持独立。

### Verification

- `npm run check` 通过。
- `npm test` 通过，共 43 项。
- `npm run build` 通过。
- 固定桥可视回归确认顶部无重复文案，四项 Session 指标保持单行，MCP 未配置为灰色。
- Wails `v0.1.5` 生产可执行文件重新构建并启动。

## [v0.1.4] - 2026-11-02

### Fixed

- MCP 未配置或没有 Server 时显示中性灰，不再把 `0/0` 显示为绿色就绪。
- 本机 Space 目录、当前 Space、Canvas 布局或撤销历史写入失败时显示明确警示，不再继续宣称“已自动保存”。
- Session 汇总把 `unknown` 从“异常”中拆出；失败、阻断、过期与结果未知分别计数。
- 重新加载 Desktop 代码后恢复完整前端依赖，并重新生成生产静态资源。

### Changed

- Desktop 中的“添加模块”和“模块目录”统一改为“添加面板”和“面板目录”，把“模块”留给未来的 Manifest、安装、启用和能力就绪生命周期。
- Session 汇总显式写出包含状态：“运行/停止”“等待审批/执行”“失败/阻断/过期”“结果未知”。
- 增加 GitHub Actions `Verify` 工作流，覆盖 Runtime、前端和 Windows Wails 构建。

### Verification

- `npm run check` 通过。
- `npm test` 通过，共 43 项。
- `npm run build` 通过。
- `go test -race ./...`、`go vet ./...`、`go build ./...` 通过。
- Wails Windows 可执行文件使用生产前端重新构建并成功启动。
- 真实 Wails 自动视觉读取受 Computer Use/WebView2 调试通道限制，未冒充为完成；状态组件由类型检查、Node 回归和生产构建验证。

### Known limitations

- Wails 窗口中的三项目状态变更仍需人工目视或可用的 Windows UI 自动化复验。
- WebView2 内存基线未因本切片重新测量。
- Core v0.2.8 未关闭上游问题继续以 `docs/contracts/core/v0.2.8/known-issues.md` 为准。

## [v0.1.3] - 2026-11-01

### Added

- 支持创建多个个人 Space，并从空白、日常协作、安全测试、MCP 管理或诊断中心开始。
- Space 切换器优先展示个人 Space，固定场景模板收纳为次级入口；支持重命名、删除、重名检查和当前最多 20 个个人 Space。
- 每个 Space 独立保存布局、Session 引用和撤销/重做历史；已有唯一 `custom` Space 可直接继续使用。
- Agent Session 支持受保护停止任务与人工接管；Desktop 可在运行、等待审批或等待执行阶段发起停止。
- Terminal 断线后自动重连，按 200 ms–5 s 退避，并使用绝对字节游标分页补齐可证明连续的区间。
- 增加 `CHANGELOG.md`，开始记录可追溯的产品与工程里程碑。

### Changed

- Agent 停止使用 revision CAS；`running` 先进入 `cancelling`，派发前可安全取消，工具派发后无法证明结果时保持 `unknown`。
- Terminal 重连期间拒绝输入与 resize；无法证明缺口连续、游标倒退或恢复数据超过 4 MiB 时保持 `unknown`。
- Desktop 将低频布局操作收进“布局设置”，命令面板与模块目录分离。
- Desktop、前端包和 Runtime 默认版本统一为 `v0.1.3`。

### Verification

- `npm run check` 通过。
- `npm test` 通过，共 39 项。
- `npm run build` 通过。
- `go test ./desktop -count=1` 通过。
- 浏览器交互回归覆盖 Space 创建、刷新后恢复、重命名、删除及 1440/390 px 布局。

### Known limitations

- 多 Space 新界面尚未在真实 Wails 窗口重新执行完整回归。
- WebView2 内存基线尚未因多 Space 切片重新测量。
- Core v0.2.8 未关闭上游问题仍以 `docs/contracts/core/v0.2.8/known-issues.md` 为准。
- OAuth、跨 Store 原子恢复、自动 Agent 恢复和第三方模型 provider 兼容性仍未完成。

## [v0.1.2] - 2026-11-01

### Added

- OpenAI-compatible 模型 Adapter 和有界 Agent Loop。
- Agent Session、transcript、最终回复与活动 checkpoint 的本地持久化。
- `model_requesting`、`tools_ready` 安全检查点的人工恢复。
- SSE 实时事件流、Desktop cursor 续传、断线重连和权威快照合并。
- 固定 Wails Terminal 桥，接入真实 Core PTY、历史、输入、输出和 resize。
- Desktop 模型任务启动、审批时间线、审批历史恢复和 `waiting_execution` 核对恢复。
- Workspace Recipe v1 导入导出、跨 Workspace 复制和有界撤销/重做。

### Changed

- 工具执行、审批结果、Session 状态和 UI 视图分别建模。
- 事件日志支持有界保留、截断水位和可选文件持久化。
- 重启后无法确认的审批、工具执行和 Session checkpoint 保持 `unknown`。

### Verification

- 模型 Loop 通过脚本化 Adapter、本地 HTTP 桩，以及真实 Core 只读工具纵向闭环。
- Desktop Terminal 通过真实 Core 的 Shell 归属、历史、输入、输出、resize、关闭和 20,000 行洪峰测试。
- 真实 Wails 窗口验证模型回复、审批链、审批恢复、终端输入输出、历史恢复和 Shell 结束状态。

## [v0.1.1] - 2026-10-30

### Added

- Core `/api/version` 与 `/api/daemon` 身份识别、实例键和 external/managed/unverified 所有权状态。
- Streamable HTTP、SSE、stdio 多传输 MCP Host。
- MCP 健康检查、退避重连、能力刷新、工具命名空间和受管 stdio 子进程。
- 工具 allowlist、参数与结果上限、审批暂停、状态轨迹和事件记录。
- 多来源 MCP 配置与 Bearer token 文件引用。
- 受保护审批决定、审批 Execution 快照和确定性 Agent Session 基础。

### Changed

- 传输断开、超时和调用取消不再被误报为执行失败。
- 有副作用的结果未知默认不自动重试。
- 审批、工具调用、MCP 连接和 Session 生命周期分开建模。

### Verification

- 真实 Core v0.2.8 MCP 握手、能力读取和只读 `term` 调用通过。
- 真实 Core 写操作、审批挂起/批准/拒绝、传输断开和并发决定通过 opt-in 集成测试。
- 多来源 MCP、stdio 子进程和工具名冲突场景通过测试。

## [v0.1.0] - 2026-10-28

### Added

- Go Headless Runtime 与 `termcp-harness` CLI 基础结构。
- Runtime 生命周期、并发安全快照、优雅关闭、健康检查和本地回环控制 API。
- 工具调用与审批状态机基础。
- MCP Server Probe。
- Wails Desktop 初始工程和控制 API 客户端。
- 产品愿景、架构设计、ADR、Core 契约基线和开发代理规则。

### Security

- 控制 API 只能绑定回环地址。
- 审批与 Agent 执行入口默认关闭，启用后需要受限 Bearer token 文件。
- Token 不进入命令参数、普通日志、URL、模型上下文或前端持久化。
