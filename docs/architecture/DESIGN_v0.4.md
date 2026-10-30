# Termcp Harness — DESIGN

> 状态：draft v0.4（2026-10-28）
>
> 配套：`docs/product/VISION_v0.9.md`、`docs/architecture/UPSTREAM.md`、`docs/product/WEB-DEFERRED.md`、`docs/governance/AGENTS_v0.3.md`、`docs/adr/`、`docs/ux/UX-RECOVERY.md`
>
> 本文约束 Harness 的运行时、MCP 工具链、终端工作台、开放边界与验证方式。凡涉及 Core 服务端语义、公开接口或数据目录兼容性的内容，均不得仅凭 Harness 文档视为既定事实。

## 1. 文档状态标记

- **已知基线**：来自现有项目文档/设计记录；仍须与当前代码和公开契约核对。
- **Harness 可实现**：Harness Runtime 或 Terminal Workspace 可以独立负责的行为。
- **待 Core 确认**：必须查看公开契约、实现或由 Core 团队明确答复。
- **待 PoC 确认**：产品方向明确，但技术实现尚无可复现证据。
- **未验证**：尚无测试证据，不能宣称已实现或已保证。

本版的核心变化：

1. Harness 是 MCP 原生 Agent Runtime，不只是 Core 的桌面 UI。
2. Runtime 与 Terminal Workspace 分层，Runtime 必须可 headless 运行。
3. Runtime 稳定内核与可替换边缘分离，不采用“一切皆插件”。
4. MCP 工具调用、策略、会话、事件和恢复是核心能力。
5. Core 仍是 SSH、PTY、Shell、会话和审批执行的权威来源。

## 2. 总体架构与三个平面

```text
┌─────────────────────────────────────────────┐
│ Experience Plane                            │
│ Terminal Workspace                          │
│ 终端 / 操作时间线 / 审批 / 接管 / MCP 状态    │
└──────────────────────┬──────────────────────┘
                       │ 控制 API / 事件流
                       │ 终端传输路径待 PoC
┌──────────────────────▼──────────────────────┐
│ Control / Runtime Plane                     │
│ Termcp Harness Runtime                      │
│ Agent Loop / MCP Host / Tool Pipeline       │
│ Policy / Session / Context / Event Log      │
│ Recovery / Diagnostics / Public API         │
└──────────────────────┬──────────────────────┘
                       │ MCP / HTTP / WebSocket
┌──────────────────────▼──────────────────────┐
│ Execution Plane                             │
│ Termcp Core                                 │
│ PTY / SSH / Shell / Files / Forwards        │
│ Server-side approval and execution policy   │
└─────────────────────────────────────────────┘
```

### 2.1 Execution Plane：Core

Core 负责：

- SSH、PTY、Shell、文件、转发。
- 会话、Shell 通道和真实执行状态。
- 服务端权限、已有审批约束与审计语义。
- 作为 MCP Server 暴露能力。
- HTTP、WebSocket、MCP 等公开契约。

Harness 不得：

- import Core 内部包。
- 读取或修改 Core 私有数据库和内部配置。
- 复制终端执行引擎、审批规则或权限策略。
- 通过修改 Core 来让 Harness 测试变绿。

### 2.2 Control / Runtime Plane：Harness Runtime

Harness Runtime 负责：

- Agent 循环与 Session 生命周期。
- 模型适配器和会话上下文。
- MCP Client/Host、工具发现、能力状态和连接生命周期。
- 工具调用前策略、人工审批与调用后验证。
- 有界上下文、事件日志和操作轨迹。
- 超时、取消、重试、断连和恢复。
- 对第一方或第三方 UI 暴露稳定控制 API 与事件流。
- 自身配置、诊断、凭据引用和脱敏。

Runtime 必须可以脱离 Terminal Workspace 运行。UI 关闭不等于 Agent、Session 或工具调用自动停止。

### 2.3 Experience Plane：Terminal Workspace

Terminal Workspace 负责：

- 展示终端、Session、Shell 和 Runtime 状态。
- 展示 Agent 输入、工具调用、审批、输出和异常。
- 提交审批、人工接管、继续和恢复操作。
- 展示 MCP 服务和工具能力状态。
- 通过公开控制契约操作 Runtime。

Terminal Workspace 不是：

- 第二个 Runtime。
- Core 执行引擎。
- 权限授予来源。
- 唯一控制入口。
- 完整 SSH 管理器的自动复制品。

当前已建立 Wails + React/TypeScript Desktop Shell，可读取 Runtime、Agent Session、审批、Core 身份、MCP 注册和事件窗口，并可在 token 仅驻留 Go 内存时提交审批决定；终端流和安装包尚未接入。Core 面板展示 external/managed/unverified 所有权、PID、启动时间和实例键，详见 `docs/contracts/harness/v0.1/core-instance.md` 与 `docs/contracts/harness/v0.1/desktop-shell.md`。

Harness 当前通过 `/api/version` 和 `/api/daemon` 识别 Core，以端点、PID、启动时间和版本生成实例键。端点可达不等于取得进程所有权；受管绑定必须与 Harness 启动时记录的 PID 和启动时间匹配。端口、PID 或版本中的单项都不能作为长期身份。

## 3. 稳定内核与开放边缘

### 3.1 稳定内核

以下边界不能以普通插件方式绕过或替换：

- Agent Loop 与 Session 状态机。
- 工具调用流水线。
- 策略、审批和凭据边界。
- 事件日志与操作关联。
- 取消、超时、重试和恢复语义。
- MCP 连接与能力状态。
- Core 实例身份、所有权与重启检测。
- 控制 API、兼容性和安全入口。

稳定内核可以有可测试的扩展点，但扩展不能成为修改安全语义的隐藏通道。

### 3.2 开放边缘

以下能力应通过版本化、可替换的边界接入：

- 模型与模型适配器。
- MCP Servers 和工具。
- 终端工作台与第三方 UI。
- 存储后端和状态导出。
- 通知、主题、桌宠和展示型插件。
- headless、daemon、容器和远程部署。
- 自动化与工作流模板。

### 3.3 不做“一切皆插件”

“一切皆插件”会增加配置空间、故障模式和诊断成本。Harness 的取舍是：

- 稳定内核只暴露经过验证的扩展点。
- 能力型扩展通过独立进程或受控协议隔离。
- 展示型扩展不能获得执行权限。
- 插件失败不能破坏 Agent Loop、事件日志、审批或会话恢复。
- 菜单、按钮和 UI 状态不能作为权限边界。

## 4. Agent Runtime 与模型边界

### 4.1 Runtime 不绑定单一 Agent

Harness Runtime 可以：

- 内置并运行一个 Agent Loop。
- 接入外部 Agent 或 Agent SDK。
- 将某个模型或 Agent 作为可替换执行器。

Harness 不规定唯一的规划范式。模型选择、提示策略、子 Agent 编排等属于 Agent/Adapter 层，但必须遵守 Runtime 的工具流水线、策略和事件模型。

### 4.2 模型不可信

模型输出、工具参数、MCP Schema、工具结果和外部内容一律按不可信输入处理：

- 工具参数执行前验证。
- 高风险调用进入策略或人工审批。
- 工具结果中的提示文本不能扩大权限。
- 模型不能直接修改 Runtime 安全配置。
- 系统级规则与项目指令冲突时，以宿主策略为准。

### 4.3 Agent 与 Session

至少区分：

- Runtime 实例。
- Agent 定义或执行器。
- Session。
- Shell / 终端通道。
- 工具调用。
- 审批请求。
- 输出区间与操作轨迹。

Session 必须记录足以重建模型可见上下文的事件。若实现使用压缩、投影或摘要，必须保证原始事实、摘要来源和不可重建部分可区分。

## 5. MCP Client / Host 边界

### 5.1 基本职责

Harness 作为 MCP Client/Host 时负责：

- 建立 stdio、SSE、Streamable HTTP 等传输连接。
- 初始化协议和记录协议版本。
- 读取工具、资源和提示能力。
- 处理工具名冲突、能力变化、超时和断连。
- 按 Session/Agent 授权能力。
- 运行或连接受管 MCP 子进程。
- 记录来源、版本、健康和失败原因。

Termcp Core 是其中一个 MCP Server，不是唯一的工具来源。Harness 必须避免把“连接 Core”等同于“完成 MCP 管理”。

### 5.2 MCP 状态机

至少区分：

1. **已发现**：扫描到配置、Manifest、安装目录或宿主清单。
2. **已登记**：导入 Harness 注册表，记录来源、版本和配置位置。
3. **已连接**：协议初始化完成。
4. **能力已读取**：已取得工具列表、Schema 或资源/提示声明。
5. **已启用**：能力被加入当前 Session、Agent 或工作区。
6. **已停用**：保留记录，但不提供能力。
7. **不可用**：配置错误、进程退出、版本不兼容、权限不足或连接失败。
8. **受管**：Harness 明确负责启动、停止、更新、卸载和清理。

关键规则：

- 发现不等于信任、连接、启用或可执行。
- 协议初始化不等于工具已获授权。
- 读取 Schema 不等于参数安全。
- 连接成功不等于调用成功。
- 没有执行证据时不得显示“已加载成功”或“运行正常”。

当前本地 Host 已把真实 Streamable HTTP 连接、能力读取、启用/停用、断连和 `tools/list_changed` 失效接入 Runtime，并保持登记、连接、能力、启用和所有权分开建模，见 `docs/contracts/harness/v0.1/mcp-server-states.md`。认证、自动重连、SSE/stdio 和受管子进程仍未实现。

### 5.3 工具命名与隔离

- 工具必须绑定稳定的来源身份和版本。
- 同名工具需要显式命名空间或冲突策略。
- 不同 MCP Server 的凭据、环境变量和工作目录必须隔离。
- 外部 MCP 描述和结果不得直接作为 HTML、脚本或桥接命令执行。
- 连接断开后，之前读取的能力不能自动被视为仍可用。

## 6. 工具调用流水线

### 6.1 阶段

每次工具调用至少经过：

1. **提议**：Agent 产生工具调用意图。
2. **验证**：检查名称、Schema、参数、目标、权限和预算。
3. **策略**：允许、拒绝、要求审批或降级。
4. **派发**：在明确执行上下文中发送给工具或 Core。
5. **观察**：接收结果、输出、错误和进度。
6. **后处理**：验证结果、脱敏、归一化并写入事件日志。
7. **收敛**：标记成功、失败、取消、过期或结果未知。

任何跳过验证、策略或日志的旁路都必须经过显式安全审查。

### 6.2 工具调用状态

至少区分：

- `proposed`
- `blocked`
- `approval_pending`
- `ready`
- `dispatched`
- `accepted`
- `running`
- `succeeded`
- `failed`
- `cancelled`
- `expired`
- `unknown`

这些状态是否可被可信确认，最终取决于 Core/MCP 契约。客户端和 Runtime 不得用自己的本地状态伪造服务端终态。

当前本地状态机已通过 `internal/toolpipeline` 接入 MCP Host，支持显式 Server/工具/精确参数 allowlist、参数与结果上限、控制事件轨迹和同步调用收敛。成功、工具级失败、传输结果未知、真实 Core 的幂等覆盖写入、副作用超时未知和调用侧 MCP Host 断开后的 `unknown` 已由测试覆盖。调用取消同样只形成本地 `unknown`，远端命令可能继续；20,000 行输出和 12 路并发只读调用已通过真实 Core 测试。远端 `review_pending` 现已映射为 `approval_pending`，审批决定、结构化执行结果、Step 完成级别和后续步骤自动恢复已接入 Runtime。当前 Termcp 0.2.8 开发基线已完成审批过期和 session 关闭后取消状态修复及严格契约验证，后续 Termcp 迭代继续沿用同一测试链。该测试不证明远端 idempotency key、真实网络分区或可信未执行查询。Agent Loop、持久化 Session 和跨重启恢复仍未接入，不得由本地时间或终端文本伪造。边界见 `docs/contracts/harness/v0.1/state-machines.md`。

### 6.3 重试规则

- 默认不对未知结果自动重试。
- 只读或明确幂等的调用可以按策略重试。
- 有副作用的调用必须提供幂等键、去重语义或可验证的查询机制，才能自动重试。
- 重试次数、退避、预算和最终状态必须可见。
- 取消请求超时后，不推断工具已停止。
- 失败与未知必须分开显示和记录。

## 7. 策略与人工审批

### 7.1 两层边界

- **Core 服务端**：已有权限限制、审批执行和审计语义。
- **Harness Runtime**：在 Agent/模型与工具之间提供调用前策略、调用后验证和统一操作记录。

Harness 的策略不能替代 Core 的服务端权限。UI 点击、按钮禁用或本地锁不能描述为跨客户端安全保证。

### 7.2 策略输入

策略可以基于：

- Agent 与 Session 身份。
- MCP Server 与工具来源。
- 工具风险与 Schema。
- 目标 Session/Shell、路径、网络和参数。
- 审批模式、时间和预算。
- 是否只读、是否幂等、是否可能产生副作用。

策略配置必须可审计、可导出，并明确版本和适用范围。

### 7.3 审批状态

至少区分：

- 待处理。
- 正在提交决定。
- 服务端已接受批准/拒绝。
- 已过期。
- 已取消。
- 结果未知/未确认。

拒绝请求已提交不等于服务端已阻止执行。只有可信确认后才能显示“已拒绝”或“已阻止”。

当前实现把审批状态与工具调用状态分开建模，具体转换与限制见 `docs/contracts/harness/v0.1/state-machines.md`。

## 8. 会话、上下文与事件日志

### 8.1 权威与边界

- Core 的 Session、Shell、终端和执行状态以 Core 为准。
- Harness 的 Agent Session、工具调用、策略和操作轨迹以 Harness Runtime 为准。
- 两者关联必须使用可信 ID；缺失时显示未知，不按时间推测因果。
- 不得在 Harness 复制一份 Core 会话数据库并双写为主要状态来源。

### 8.2 模型可见即可记录

影响模型后续行为的输入必须可记录或可重建：

- 用户消息。
- 系统/项目指令。
- 工具调用与结果。
- 注入的上下文。
- 压缩/摘要事件。
- 审批结果。
- 重试和取消。

事件日志必须：

- 有界或可归档。
- 可导出。
- 明确记录的完整性和未知区段。
- 保护凭据和敏感终端内容。
- 不因 UI 重渲染或崩溃而丢失已提交事实。

当前本地事件日志只提供内存内补拉窗口，顺序、容量和截断语义见 `docs/contracts/harness/v0.1/event-log.md`。持久化、实时订阅和跨重启恢复仍未实现。

### 8.3 上下文管理

- 不假设无限上下文。
- 压缩策略必须保留原始事件引用和摘要来源。
- 工具结果按需加载或截断，截断必须可见。
- 外部 MCP 返回的提示注入文本不得被提升为系统指令。
- 性能优化基于测量，不因“高级架构”引入无证据的复杂引擎。

## 9. Terminal Workspace

### 9.0 Harness Canvas 初版

Terminal Workspace 的初版界面方向采用 `docs/adr/ADR-013-harness-canvas-layout.md` 定义的 Harness Canvas：

- Shell 保持轻量，提供 Workspace、模块、命令、状态和恢复入口。
- Canvas 使用受约束的停靠式自由布局。
- 工作台、审批中心和任务时间线是 Workspace 模板，不是割裂的固定页面。
- Activity 统一入口并保留实时事件、历史记录和诊断视图。
- 面板、模块和任务生命周期必须分开。

该布局目前是 **Accepted / Canvas 初版 PoC 已实现**；标签合并、Workspace 导入导出、真实 Terminal 和第三方模块贡献仍未完成。

### 9.1 共享执行现场

Terminal Workspace 的目标不是“把 Agent 日志放在终端旁边”，而是：

- 终端、Agent Session、工具调用和审批属于同一可验证执行上下文。
- 人可以看到 Agent 正在操作哪个 Session/Shell。
- Agent 可以看到人类已有上下文，但不能假定独占控制权。
- 人可以干预、接管、停止或继续，但界面必须说明实际保证边界。

### 9.2 终端传输路径

以下方案仍需 PoC 决定：

- Terminal Workspace 直接连接 Core 的终端 WebSocket，Harness 只观察控制事件。
- Terminal Workspace 通过 Harness 转发终端字节和控制事件。
- 混合方式：控制经 Harness，终端数据直连 Core。

选择时必须评估：

- 字节保序与跨会话隔离。
- 输入延迟和输出吞吐。
- 策略覆盖范围。
- 凭据与网络暴露。
- 断线恢复与事件完整性。
- UI 关闭后 Runtime 和会话生命周期。

不得为了“所有流量经过 Harness”而引入无证据的代理复杂度。

### 9.3 终端性能与状态

- 终端字节流必须保序，可以合并渲染批次，不得重排或跨会话混合。
- 禁止无界队列/缓存。
- 审批、会话关闭、执行状态、错误等关键控制事件不得静默丢弃。
- 高频输出下的内存、输入延迟、会话切换延迟必须有基线。
- 关闭或重连 UI 不得被描述为停止远端任务。

## 10. 人工与 Agent 并发

### 10.1 不可混淆的状态

- 连接状态：连接中、已连接、重连中、已断开、不兼容。
- Agent 状态：空闲、运行、等待工具、等待审批、暂停、失败、未知。
- Shell/执行状态：以 Core 提供的状态为准。
- 工具调用状态：见第 6 节。
- 审批状态：见第 7.3 节。
- 输入来源：人工、Agent 或未知。
- 本地交互状态：焦点、正在发送、请求失败等。

Agent 正在运行，不等于 Harness 可以假设输入独占；审批待处理，也不等于 Shell 中所有进程已暂停。

### 10.2 行为约束

1. 同一客户端应防止明显重复提交，但不能把本地去重说成跨客户端互斥。
2. 观察到 Agent 正在同一 Shell 操作时，应显示冲突风险；是否阻止输入需依据产品决定和 Core 能力。
3. 发送操作后超时或断连，显示“结果未知/需核对”，不自动重复有副作用的操作。
4. 关闭 UI 时不得推断 Agent 已停止或远端操作已取消。
5. 若 Core 不提供暂停、取消、互斥或操作查询能力，Harness 只提供有边界的提示和降级行为。

## 11. 事件顺序、关联与恢复

### 11.1 正确的问题定义

同一条 WebSocket/TCP 连接内通常按序交付；主要风险来自不同事件源缺少共同顺序、跨连接无统一序号、异步处理改变完成顺序、缺少操作 ID、重连漏事件或旧快照覆盖新状态。

### 11.2 可实现

- 为每个连接、Session 和工具调用维护隔离顺序。
- 使用可信 ID 关联 Agent、MCP、Core 和 UI 状态。
- 保存本地处理序号时明确它不是服务端顺序。
- 旧异步任务晚到时不得覆盖更新状态。
- 无法关联时显示未知，不推断因果。

### 11.3 恢复目标

- 权威状态来源明确。
- 旧快照不能覆盖新确认状态。
- 快照期间到达的实时事件有协调策略。
- 没有游标或补拉语义时，不承诺无遗漏恢复。
- 优先恢复必要 Session、未决审批、工具调用和终端状态，历史按需加载。
- 恢复失败或部分成功时保留现状并标记不完整。

## 12. 开放部署与公开边界

### 12.1 部署形态

产品方向至少支持：

- CLI。
- Headless daemon。
- 容器化运行。
- 桌面 Terminal Workspace。
- 未来远程控制面。

实际支持范围和优先级待 PoC。任何形态都必须能说明：

- 启动、停止和健康状态。
- 配置与数据目录。
- 认证与网络暴露。
- 升级、回滚和迁移。
- 关闭 UI 后的 Runtime 生命周期。

### 12.2 对外契约

Harness 自身需要稳定、版本化的公开边界：

- Runtime 控制 API。
- 事件流。
- Session 和工具调用查询。
- MCP 服务管理。
- 策略配置。
- 状态导出。
- 插件 Manifest 和权限。

第三方 UI、Agent 和工具不得依赖 Harness 内部包、内部目录或未版本化 RPC。

### 12.3 状态开放

- Session、事件和操作轨迹可以导出。
- 导出默认脱敏。
- 导出格式记录版本和完整性。
- 用户不被锁在某个内部 UI 或数据库格式里。
- 插件卸载和迁移必须区分保留、删除与转移数据。

### 12.4 模块目录、安装与自由组合

Harness Shell 是固定展示底座，负责：

- 启动、展示和恢复工作台。
- 模块目录、复选框、搜索、分类和组合保存。
- 缺失依赖、冲突、模块错误和未验证组合提示。
- 全部功能模块取消时的空工作台与恢复入口。

所有功能模块使用同一目录和同一选择模型：

- 每个功能模块都显示复选框，包括 L0 模块。
- 推荐模块默认勾选，但任何推荐项都可以取消。
- L0-L3 仅作为安装必要程度和场景分组，不代表产品版本、权限或质量。
- 支持全选、全不选、搜索、按功能类型筛选和恢复推荐。
- 安装器与安装后的模块管理复用同一清单、状态和组合格式。

统一目录不要求所有对象使用同一种底层生命周期。至少区分：

- Runtime 内核组件：运行语义明确，但不一定独立安装。
- 内建功能模块：可随主程序构建，通过配置启停。
- MCP/外部服务连接器：独立发现、连接、授权和能力刷新。
- 官方模块包：独立下载、校验、安装、更新和卸载。
- 第三方插件：独立隔离、权限、兼容和审计。
- Workspace / Recipe：只保存布局和组合，不获得执行权限。

必须分开的三条关系：

1. **安装关系：** 是否选择、可下载、已安装、更新或卸载。
2. **运行关系：** 是否启用、运行中、停下或运行失败。
3. **能力关系：** 依赖、授权、模型、MCP 和目标服务是否足以完成当前任务。

复选框只表达用户选择，不代表已经安装、启用、运行成功或能力就绪。硬依赖缺失时允许保留选择但不得启用或运行；软依赖缺失时允许运行并显示能力受限；版本或安全冲突必须阻止受影响操作。

模块选择模型至少记录：

- `module_id` 与版本。
- `install_tier`：L0-L3。
- `object_type` 与 `lifecycle_adapter`。
- `functional_category`。
- `use_case_tags`。
- 依赖与冲突。
- 提供的 Capabilities 与需要的 Capabilities。
- 权限、体积、平台、下载来源和离线可用性。
- 是否推荐、推荐理由和验证状态。
- 是否需要重启。
- 数据保留、迁移和卸载策略。

推荐规则必须透明、可解释且可覆盖。场景选择只能改变默认勾选，不能隐藏模块或静默覆盖用户的手动选择。依赖和冲突通过说明、联动勾选和状态提示处理，不使用不可操作的灰掉控件代替解释。

安装与模块管理必须覆盖：

- 恢复推荐。
- 保存、导入和导出组合 Recipe。
- Workspace 布局与模块组合分别保存。
- 安装中断、部分成功、重试和回滚。
- 未验证组合的明确支持边界。

如果实现仍将模块编译在单一二进制中，可以先用功能开关验证交互，但不得把“配置隐藏”描述成“安装裁剪”。真正的模块包、签名、下载、更新和卸载仍以 ADR-009 的 PoC 为准。

## 13. 安全边界

- Token、Authorization、私钥、密码和敏感终端内容不进普通日志、URL、前端持久化或模型上下文。
- MCP 工具描述、Schema 和返回值均视为不可信。
- 外部工具安装、启动和执行必须明确来源、版本、权限、工作目录和环境。
- 代码执行型插件必须独立进程或沙箱，不能直接进入 Runtime 主进程。
- 展示型插件不得获得操作权限。
- 不新增任意 URL、文件读写或命令执行的通用桥接 API。
- 高风险操作需要确认、失败处理、清理和必要回滚。
- 诊断导出默认脱敏并要求用户确认。
- 审计和控制事件不能套用普通输出的静默丢弃策略。

## 14. 测试与验收

最低场景：

- 一个 Agent Session 连接多个 MCP Server。
- MCP 发现、连接、能力读取、启用、停用和断开状态转换。
- 工具名冲突、Schema 变化、错误响应和恶意返回。
- 工具调用等待审批后断连、超时和会话关闭。
- 工具已派发但结果未知，不自动重试。
- 只读工具重试与有副作用工具不重试的差异。
- 人工输入与 Agent 工具调用在同一 Shell 上重叠。
- Terminal Workspace 与 Runtime 重连后，旧快照不覆盖新状态。
- 高频输出下审批和取消等控制事件仍可见。
- UI 关闭后 Headless Runtime 继续运行。
- 第三方 MCP 不依赖 Harness 私有 SDK 即可接入。
- 安装页显示全部功能模块，推荐项默认勾选且可以逐项取消。
- 场景推荐变化不静默覆盖用户的手动勾选。
- 依赖模块自动联动勾选，取消依赖时给出联动取消提示。
- 已选择、已安装、已启用、运行中和能力就绪分别显示，不能互相冒充。
- 硬依赖缺失时不能启用；软依赖缺失时显示能力受限；安全冲突阻止受影响执行。
- 全部功能模块取消后，Shell 仍能展示恢复入口。
- 安装后补装模块与安装器使用同一清单和状态。
- 模块组合、Workspace 和 Recipe 可以保存、导入和导出。
- 插件崩溃、超时、资源泄漏和权限越界。
- 两个 Harness Runtime 或多个客户端连接同一 Core。
- 两个受管 Core 尝试共用数据目录。
- 凭据、工具结果和诊断导出脱敏。

测试分层：

1. 单元/纯逻辑测试。
2. MCP/API 契约测试。
3. Runtime 集成测试。
4. 真实 Core/MCP Server 端到端测试。
5. Terminal Workspace 端到端与平台回归。
6. 压力、故障和安全测试。

每个结果记录 Core 版本、MCP Server 版本、Harness 版本、平台、复现步骤和未覆盖内容。未执行测试不得写成通过。

## 15. 需要 Core 或其他上游确认的契约

- Core MCP 工具参数、结果、错误和事件语义。
- Core 工具调用、审批、输入和终端输出的关联字段。
- 审批终态、拒绝原子性、取消和过期语义。
- Core 启动、实例身份、健康检查、数据目录和 Web UI 禁用方式。
- WebSocket 快照、游标、补拉和重连完整性。
- 认证、授权、限制会话和审计的服务端保证。
- MCP 协议版本、能力刷新和工具 Schema 变化处理方式。
- Terminal Workspace 的终端传输路径、限流和背压能力。

核查结果记录来源、日期、版本和结论。无法核实的内容标记为未知。

## 16. ADR 索引

- `docs/adr/ADR-001-input-control-ownership.md`
- `docs/adr/ADR-002-event-ordering-correlation.md`
- `docs/adr/ADR-003-terminal-backpressure.md`
- `docs/adr/ADR-004-core-instance-data-directory.md`
- `docs/adr/ADR-005-approval-security-boundary.md`
- `docs/adr/ADR-006-disconnect-timeout-cleanup.md`
- `docs/adr/ADR-007-contract-baseline-and-testing.md`
- `docs/adr/ADR-008-desktop-technology-stack.md`
- `docs/adr/ADR-009-modular-composition.md`
- `docs/adr/ADR-010-core-integration-boundary.md`
- `docs/adr/ADR-011-mcp-native-agent-runtime.md`
- `docs/adr/ADR-012-runtime-bootstrap-technology.md`（Proposed / bootstrap 已实现）

ADR 状态以各文件为准。记录决策不等于代码、Core 或第三方 MCP 已支持相应能力。
