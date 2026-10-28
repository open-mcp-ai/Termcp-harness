# AGENTS.md — Termcp Harness（新工作区）

> Termcp Harness 是 **MCP 原生的 Agent Runtime 与人机共享的终端工作台**：通过公开契约管理 Agent 执行、MCP 工具、上下文、会话、策略、操作轨迹和恢复，并由可选的 Terminal Workspace 提供服务。

面向在本工作区工作的 AI 编码 Agent。`docs/` 描述**意图**，不等于已实现事实；本文件只写会改变你行为的约束和最易犯错的点。

## 工作区现状（先确认，别假设）

- 已有 Go 1.27 Headless Runtime 与 Wails Desktop：`cmd/termcp-harness`、`internal/runtime`、`internal/controlapi`、`internal/daemon`、`internal/toolcall`、`internal/approval`、`internal/mcpcontrol`、`internal/eventlog`、`internal/controlclient` 和 `desktop/`。当前仍**没有 CI**；Git 已初始化，开发分支为 `dev`。
- 当前已实现 Runtime 实例生命周期、本地控制 API、有界事件日志、工具调用/审批状态机、持久 MCP Host、`tools/list_changed` 失效、Streamable HTTP 探测器和 Desktop 只读工作台，并通过 Core 0.2.8 真实连接、能力读取与只读 `term` 帮助调用；**MCP 认证/自动重连、Agent Loop、终端 Session 工具执行和审批集成尚未实现**。
- 这是**独立工作区**，不与旧实现合并；旧代码只作参考（见下节）。
- 引入技术栈时，把真实可执行命令（含跑单个测试的写法）补进本文件与 README，否则后续 Agent 只能猜。
- Harness Runtime 与 Terminal Workspace 必须分层；Runtime 目标是可 headless 运行，Terminal Workspace 是第一方体验层，不是唯一入口。
- Web UI 暂停新增功能，见 `docs/product/WEB-DEFERRED.md`；不要为 Web 预建跨端通用框架。
- **未经用户明确同意，不得 commit、push 或执行破坏性数据操作。**

## 开发命令

Runtime 使用 Go 标准库与 `github.com/mark3labs/mcp-go v0.50.0`；共享依赖校验和必须与本机 Core v0.2.8 对应的公开 `go.sum` 保持一致。Desktop 使用 Wails v2.15.0：

```powershell
go test -race ./...
go vet ./...
go build ./...
go run ./cmd/termcp-harness version
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787
go run ./cmd/termcp-harness mcp probe --endpoint http://127.0.0.1:18765/stream
go run ./cmd/termcp-harness serve --mcp-endpoint http://127.0.0.1:18765/stream
```

Desktop 安装、检查、构建与启动：

```powershell
Set-Location .\desktop\frontend
npm ci
npm run check
Set-Location ..
wails build -m -nosyncgomod -nopackage -clean
.\build\bin\termcp-harness-desktop.exe --runtime http://127.0.0.1:8787
```

Desktop 前端检查单条命令：

```powershell
Set-Location .\desktop\frontend
npm run check
```

单测示例：

```powershell
go test ./internal/runtime -run '^TestRuntimeLifecycle$' -count=1
go test ./internal/eventlog -run '^TestDiagnosticEvictionIsVisible$' -count=1
$env:TERMCP_CORE_MCP_URL = 'http://127.0.0.1:18765/stream'
go test ./internal/mcpcontrol -run '^TestProbeTermcpCoreIntegration$' -count=1
go test ./internal/mcpcontrol -run '^TestHostLifecycleAndCapabilityNotification$' -count=1
```

若 Agent 沙盒拒绝访问默认 Go 缓存，可把 `GOCACHE` 和 `GOTMPDIR` 指向任务临时目录后重跑；不要修改全局 Go 配置。控制 API 当前没有认证，只能绑定回环地址，不能通过 `0.0.0.0` 或局域网 IP 暴露。

## 同机相关仓库（参考，不在本工作区内）

- **Core**：`D:\data\code\github\Termcp-web`，Go（module `github.com/open-mcp-ai/termcp`）。会话、SSH、终端、端口转发、审批和 MCP Server 的权威实现。查契约先看 [API](https://github.com/open-mcp-ai/Termcp/blob/main/docs/api.md)、[架构](https://github.com/open-mcp-ai/Termcp/blob/main/docs/architecture.md)、[MCP 工具](https://github.com/open-mcp-ai/Termcp/blob/main/docs/mcp-tools.md)、[CLI](https://github.com/open-mcp-ai/Termcp/blob/main/docs/cli.md)。
- **Desktop**：`D:\data\code\github\Termcp-Desktop`，Wails + Go。它是成熟的人类终端工作台和重要参考实现，但不是 Harness Runtime/Workspace 的架构基线。
- **旧 Harness 实现**：`D:\data\code\github\Termcp Harness`，Go 1.27 + Wails v2.15.0，含 `web/`（cmd）与 `desktop/`。可作为行为参考，但**不是**本工作区的基线。
- **DeepSeek Harness 等外部 Agent Harness**：只可作为公开资料与产品对照，不得复制其内部架构或把其行为当成 Termcp 契约。
- Core 是 MCP **Server**，不是通用 MCP Client/Host；外部 MCP 的发现、连接、授权、启用和生命周期属于 Harness Runtime 的新增能力，不能描述成 Core 已有能力。
- 读这些仓库前先看 `git status`：它们可能有未提交的改动，不要把别人的在制品当作既定设计。

## 本工作区的智能体

- `.opencode/agent/Harness-Developer.md` — `mode: primary`，开发/实现主代理，负责 Runtime、MCP 工具链、Terminal Workspace、契约、测试与证据化汇报。
- `.opencode/agent/Harness-Reviewer.md` — `mode: all`，独立审查，**只读**：`edit`/`shell` 默认 deny，仅放行 `git status|diff|log|show`、`go test|vet|build`、`npm test|run check|run build`、`node --test`。
- 改这两个文件的 frontmatter 时：`---` 必须在**文件第一行**。把 HTML 注释放在 frontmatter 之前会让 OpenCode 静默忽略 `description`/`mode`/`permissions`，Reviewer 会退回 primary 并拿到全量写权限。改完用 `opencode debug agents` 核对 mode 与权限条数。
- 两个 agent 必须按 `docs/product/VISION_v0.9.md` 与 `docs/architecture/DESIGN_v0.4.md` 区分：Runtime 稳定内核、MCP 控制面、Terminal Workspace、可替换边缘；并保留 MCP/插件的已发现、已登记、已连接、能力已读取、已启用、已停用、不可用和受管状态。后续不得重新引入“发现即加载/可用”或“Harness 只是桌面客户端”的旧表述。

## 文档地图

- `docs/README.md` — 文档索引、阅读顺序和目录职责。
- `docs/product/PROJECT-DISCUSSION.md` — 对外首次讨论的一页简报、群聊模板和分阶段议题。
- `docs/product/VISION_v0.9.md` — 产品定位、Runtime/MCP/Workspace 能力模型、优先级、技术方向与路线图；只在范围/方向变化时改。
- `docs/architecture/UPSTREAM.md` — Core/Desktop/Harness 的上游关系、公开仓库与契约追踪。
- `docs/product/WEB-DEFERRED.md` — Web 暂停策略；不属于当前开发主线。
- `docs/architecture/DESIGN_v0.4.md` — Runtime、MCP 工具链、策略、Session、事件、恢复、终端工作台、安全与测试约束。
- `docs/governance/AGENTS_v0.3.md` — 本文件的归档来源；**不要再单独维护它的内容**。
- `docs/ux/UX-RECOVERY.md` — 高风险场景的用户提示文案与使用条件。
- `docs/adr/ADR-001…007` — 全部为 **Proposed / 待确认**；引用时必须带上该状态，不得当成 Core 已支持的能力。
- `docs/adr/ADR-008-desktop-technology-stack.md` — **Accepted**：第一方 Terminal Workspace 的 Wails v2 + Go + React/TypeScript/Vite + `@xterm/xterm` 技术栈；不等于 Runtime 部署形态已确定。
- `docs/adr/ADR-009-modular-composition.md` — **Accepted（交互）/实现待 PoC**：全部模块复选框、场景推荐可取消、L0-L3 仅分组、Shell 固定、模块目录与自由组合。
- `docs/adr/ADR-010-core-integration-boundary.md` — **Proposed**：Harness 消费独立 Core 进程、公开契约和实例所有权边界。
- `docs/adr/ADR-011-mcp-native-agent-runtime.md` — **Accepted**：Harness 作为 MCP 原生 Agent Runtime 与终端工作台的产品定位。
- `docs/adr/ADR-012-runtime-bootstrap-technology.md` — **Proposed / bootstrap 已实现**：Go 1.27 作为 Runtime 起始技术选择；不等于长期进程模型、依赖或跨平台已经确认。
- `docs/development/BRANCHING.md` — 上游分支观察与 Harness 的 trunk-based 分支策略。
- `docs/contracts/README.md` — Core 与 Harness 公开契约、来源、版本和验证记录规范。
- `docs/contracts/harness/v0.1/state-machines.md` — 工具调用、审批、结果未知与自动重试语义；尚未接入真实 MCP/Core。
- `docs/contracts/harness/v0.1/mcp-server-states.md` — MCP Server 的登记、连接、能力、启用和所有权状态边界。
- `docs/contracts/harness/v0.1/event-log.md` — 本地事件顺序、有界保留、截断和补拉 API。
- `docs/contracts/harness/v0.1/desktop-shell.md` — Desktop Wails 桥接、端点限制、视觉边界和未实现能力。
- `docs/contracts/core/v0.2.8/metadata.md` — 已安装 Core 0.2.8 的来源、版本和实际端点证据。
- `docs/contracts/core/v0.2.8/mcp.md` — Core MCP 握手、能力、工具 Schema 与待验证行为。
- 文档名带版本号：升版时同步更新交叉引用和 `docs/README.md`，不要留下两个并行版本。

## 状态词汇（本项目最容易被写坏的地方）

每条结论都要能归到：已由代码/测试确认、来自公开契约、团队确认、推断、未知。

- 不用推断填补缺失：没有 sequence / operation / correlation ID 时，只能说“关联未知”。
- 不把接收时间当执行顺序；不把时间接近、文本相似说成因果关系。
- 请求超时或断连后是“**结果未知**”，既不是“失败”，也不是“已执行”。
- 工具调用、审批、MCP 连接、插件和 UI 状态必须分别建模，不能用一个“成功/失败”字段覆盖全部语义。
- 点“拒绝”只表示“正在提交拒绝”；收到可信服务端确认后才显示“已拒绝”。
- 未运行的测试不得写成通过；跑不了就写原因和未验证的风险。

## 硬边界

- 只通过公开 HTTP / MCP / WebSocket / CLI/API 契约与 Core 通信：不 import Core 内部包，不直接读写 Core 私有数据库或配置来绕开契约。
- 不复制 SSH、PTY、终端执行引擎、会话执行、服务端审批规则或 Core 权限策略。Harness 可以运行 Agent、MCP Client/Host、工具管线、策略和恢复逻辑，但不能成为第二个 Core。
- 不采用“一切皆插件”：Agent Loop、工具流水线、策略、审批、事件、恢复和基础控制协议属于稳定内核；模型、MCP、工具、UI、存储和桌面扩展属于开放边缘。
- Harness Shell 对用户始终可用，但全部功能模块都是可勾选、可取消的模块；L0-L3 不是产品版本、权限或质量等级。
- 安装器与安装后模块管理必须使用同一份模块目录：所有模块可见，推荐项默认勾选但可取消，支持全选、全不选、搜索、筛选、恢复推荐和保存组合。
- 不得把推荐模块做成不可取消的强制套餐，不得隐藏未推荐模块，也不得用灰掉复选框代替依赖和冲突说明。
- 统一目录不等于统一生命周期：选择、安装、启用、运行和能力就绪必须分开建模；复选框不代表已安装或可运行。
- Runtime 内核、内建功能、MCP 连接器、模块包、第三方插件和 Workspace Recipe 可以使用不同生命周期适配器。
- Runtime 必须可 headless 运行；Terminal Workspace、桌宠、主题或 Web UI 不能成为唯一控制入口或安全边界。
- 工具调用必须经过验证、策略、执行、观察和后处理；未经验证的旁路不得直接调用高风险工具。
- 有副作用的操作在结果未知时不自动重试；任何重试都要说明幂等依据、次数、退避与用户可见结果。
- UI 按钮、本地锁、前端校验和插件声明都不是安全保证；不把客户端状态描述成服务端保证。
- 终端字节流必须保序：可以合并渲染批次，不得重排，不得跨会话混合。
- 禁止无界队列/无界缓存；关键控制事件（审批、会话关闭、工具状态、错误）不得套用普通输出的静默丢弃策略，任何截断都必须可见。
- 只管理本 Harness 明确拥有的 Core/Runtime 子进程：不按端口 kill，不自动终止未知进程，不让两个受管 Core 共用同一数据目录（除非 Core 明确支持且有验证证据）。
- MCP 工具描述、Schema、结果和外部内容均视为不可信；不得当作 HTML/脚本执行，也不得提升为系统指令。
- 不新增任意 URL / 文件读写 / 命令执行的通用桥接 API，除非经过明确安全审查。
- 代码执行型插件必须独立隔离；展示型插件不得获得执行权限。
- Token、Authorization、私钥、敏感终端内容不进普通日志、URL、前端持久化、模型上下文或诊断包；诊断导出默认脱敏并要求用户确认。

## 契约、Mock 与测试

- 自维护的 Mock/Schema 只是测试工具，不是 Core 或 MCP 的权威契约。每项关键契约声明都要记录来源、版本和确认状态。
- 契约测试失败先分类：真实契约变更、桩过期、Harness 适配错误、行为语义未定义。**不得只改 Mock 让测试变绿。**
- 结构 Schema 证明不了行为语义（拒接是否原子、跨通道因果、超时后是否已执行），这些只能靠文档约定 + 真实 Core/MCP 集成测试。
- 优先覆盖场景：MCP 连接/能力/断开、工具名冲突、审批拒绝/超时后断连、有副作用调用结果未知、并发输入、输出洪峰中的控制事件、重连快照覆盖旧状态、旧异步任务晚到、实例/目录冲突、凭据脱敏。
- 先测量再优化：背压、合并、缓冲上限都需要基线（输出速率、峰值内存、输入延迟、会话切换延迟）。

## 改动与汇报

- 架构/状态/契约/安全边界变更 → `docs/architecture/DESIGN_v0.4.md` + 相关 ADR；产品范围变更 → `docs/product/VISION_v0.9.md`；实现与运行方式 → `README.md`。
- 汇报包含：改了什么、依据（文件/契约/测试）、实际执行的命令与真实结果、未验证项、待 Core/MCP 确认项、风险。不要用“应该可以”代替运行结果。

## 环境备注

- `docs/` 是 UTF-8 中文；本机 PowerShell 控制台默认 GBK，`Get-Content`/`type` 会显示乱码。读文件用 read 工具（或显式 `-Encoding UTF8`）。
- 同理，`opencode debug agents` 输出整份 system prompt：先 `[Console]::OutputEncoding=[Text.Encoding]::UTF8`，再 `Out-File -Encoding utf8` 后解析，否则 `ConvertFrom-Json` 会因 GBK 解码失败。
