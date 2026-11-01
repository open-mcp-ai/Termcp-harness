# Changelog

Termcp Harness 的重要功能与架构变更记录。

当前版本在 `dev` 分支持续迭代，不承诺跨版本兼容。版本号采用语义化版本的基础形式，每次可独立验收的功能里程碑递增补丁号。

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
