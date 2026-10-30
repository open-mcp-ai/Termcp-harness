# ADR-011：MCP 原生 Agent Runtime 与终端工作台定位

- 状态：Accepted / 方向确认（2026-10-28）
- 日期：2026-10-28
- 责任范围：Termcp Harness 产品、运行时、终端工作台与公开边界
- 关联：`docs/product/VISION_v0.9.md`、`docs/architecture/DESIGN_v0.4.md`、`docs/architecture/UPSTREAM.md`、`docs/adr/ADR-008-desktop-technology-stack.md`、`docs/adr/ADR-010-core-integration-boundary.md`

## 1. 背景

v0.8 将 Harness 定义为“Termcp Core 的独立 Desktop 客户端与终端工作台”，并明确“不承担 Agent Runtime”。在实际对比 Termcp-Desktop、Codex、Claude Code、OpenHands 与 DeepSeek Harness 后，这一定位存在两个问题：

1. Termcp-Desktop 已经覆盖连接、终端、多窗格、文件、转发、审批与 Claude/Codex 等 Agent 共享会话的人类终端工作台能力。继续复制会形成直接重复。
2. 只做 UI 或观察器，无法形成“Harness”应有的执行循环、工具策略、上下文、会话和恢复边界。

与此同时，Termcp 的核心资产不是另一个终端 UI，而是：

- Core 作为真实执行平面和 MCP Server。
- MCP 提供可替换工具能力。
- 终端是 Agent 执行高风险操作的主要现场。
- 人和 Agent 需要在同一现场协作。

因此需要确认 Harness 的运行时位置，而不是仅调整功能清单。

### 外部产品对照

本决策参考了以下公开产品与文档，但只用于理解 Agent Harness 的职责边界，不把外部实现当成 Termcp 契约：

- [OpenAI Agents API 架构](https://developers.openai.com/api/docs/guides/agents-api/architecture)将 harness、environment 和 application server 分开。
- [Claude Code 术语表](https://code.claude.com/docs/en/glossary#agentic-harness)将 agentic harness 描述为工具、上下文管理和执行环境。
- [OpenHands 架构](https://docs.openhands.dev/overview/introduction)将客户端、Agent SDK/Server、Sandbox 和自动化拆成独立组件。
- [DeepSeek Harness](https://www.deepseek.com/harness/)采用“一切皆插件”的通用 Agent Runtime 方向，可作为开放性与复杂度的对照。

## 2. 决策

Termcp Harness 定位为：

> **MCP 原生的 Agent Runtime 与人机共享的终端工作台。**

产品由三个平面组成：

1. **Execution Plane：Termcp Core**
   - 负责 SSH、PTY、Shell、会话、审批执行和真实终端状态。
   - 是执行事实和 Core 数据的权威来源。
2. **Control / Runtime Plane：Termcp Harness Runtime**
   - 负责 Agent Loop、MCP Client/Host、工具流水线、策略、上下文、Session、事件日志和恢复。
   - 必须能够以 headless 方式独立运行。
3. **Experience Plane：Terminal Workspace**
   - 负责终端、工具调用时间线、审批、接管、MCP 状态和恢复交互。
   - 是第一方体验层，不是 Runtime 本体，也不是唯一入口。

## 3. 产品边界

### 3.1 Harness 做的事情

- 运行或协调 Agent。
- 连接 Termcp Core 和外部 MCP Server。
- 在工具调用前后执行验证、策略和审批。
- 管理 Session、上下文、事件、来源和操作轨迹。
- 区分成功、失败、取消、过期和结果未知。
- 在安全依据下执行重试和恢复。
- 向 Terminal Workspace 或第三方 UI 提供稳定控制契约。
- 支持 headless、daemon、容器或桌面等开放部署形态。

### 3.2 Harness 不做的事情

- 不复制 Core 的 SSH、PTY、Shell、文件、转发或审批执行引擎。
- 不成为第二个 Core。
- 不绑定单一模型、厂商或固定 Agent 规划范式。
- 不把完整 Termcp-Desktop 的 SSH 管理器作为第一版范围。
- 不把通用办公、文档制作或表格处理作为首要定位。
- 不采用“一切皆插件”作为稳定内核原则。
- 不在首版建设完整 MCP/插件商城、模块市场或团队平台。

## 4. 开放性原则

“开源”不等于“开放”。Harness 的外部开放至少包括：

- **部署开放**：Runtime 不依赖某一种 UI 或桌面壳。
- **接入开放**：Agent、UI 和工具使用版本化公开协议。
- **工具开放**：符合 MCP 的服务可以接入，不要求私有 SDK。
- **模型开放**：不绑定单一模型厂商。
- **扩展开放**：插件 Manifest、权限和生命周期明确。
- **状态开放**：Session、事件和操作轨迹可以导出。
- **组合开放**：所有功能模块使用同一复选框目录，推荐项可取消，L0-L3 只用于分组。

稳定内核与开放边缘分离：

- 内核保留 Agent Loop、工具管线、策略、审批、事件、恢复和基础控制协议。
- 模型、MCP、工具、UI、存储、通知、主题和桌宠通过边界接入。
- 扩展不能修改安全语义，也不能绕过日志与恢复。

## 5. 终端工作台的作用

Terminal Workspace 不是“终端旁边放一个 Agent 日志”，而是：

- 人和 Agent 共享同一可验证 Session/Shell。
- 工具调用、审批、输出和异常进入同一时间线。
- 人能观察、干预和接管，但不伪造跨客户端互斥。
- UI 可以关闭，Runtime 继续运行。
- 终端传输是直连 Core 还是经 Runtime 转发，留待 PoC 决定。

第一版终端工作台只验证一条窄闭环：

> 一个终端 + 一个 Agent Session + 工具调用/审批时间线 + 人工接管 + 结果未知恢复。

不在第一版复制多窗格、SFTP、端口转发、主题和完整工作区。

## 6. 结果与影响

### 正面影响

- 与 Termcp-Desktop 形成职责分工，而不是功能复制。
- 让 MCP 工具、Agent 执行和人机终端协作进入同一产品叙事。
- 允许未来成为真正可部署、可嵌入、可替换的开放 Runtime。
- 为第三方 Agent、UI、MCP 与插件提供外部开放边界。

### 代价与风险

- 产品范围从 UI 扩展到 Agent Runtime、MCP Host、策略引擎和事件系统。
- 必须定义自己的公开控制 API、Session 模型、工具状态和恢复语义。
- Terminal Workspace 的终端传输路径和性能需要 PoC。
- 若无真实用户需求，Runtime 可能变成复杂度过高的中间层。
- Core 缺少事件关联、实例身份或恢复契约时，必须降级而不能补造事实。

### 强制止损条件

满足以下任一情况时，必须重新评估独立 Runtime 的必要性，或收缩产品范围：

- 没有真实 Agent/MCP 使用场景需要 Harness 位于 Core 与 Agent 之间。
- 现有 Core 与 Desktop 已完全满足目标用户，且没有开放部署或外部接入需求。
- 无法定义可信的工具调用状态、策略执行和恢复边界。
- 第一版只能通过复制 Desktop 功能来证明价值。

## 7. 后续决策

仍需通过 PoC 或后续 ADR 确认：

1. Harness Runtime 的语言、进程模型、启动与控制协议。
2. Agent Loop 由 Harness 内置、外部 Agent 提供，还是两者共存。
3. Terminal Workspace 直连 Core 还是经 Runtime 转发终端流。
4. MCP Host 的传输支持、能力刷新和插件隔离。
5. 控制 API、事件流、Session 与操作轨迹格式。
6. Headless、daemon、容器和远程部署的首发范围。
7. 第三方 Agent/UI/工具的接入契约。
8. 稳定内核、官方模块和第三方插件的信任边界。

## 8. 结论

**Harness 的独立价值来自运行时位置、开放边界和人机共享执行现场，而不是来自复制 Desktop 的终端功能。**

**Termcp Core 负责执行，Harness Runtime 负责 Agent 执行秩序、MCP 工具治理和安全恢复，Terminal Workspace 负责人与 Agent 的共同操作体验。**
