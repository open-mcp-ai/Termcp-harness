# Termcp Harness 上游关系与契约追踪

> 状态：已确认（2026-10-28）
>
> 适用范围：Harness Runtime、Terminal Workspace、Termcp Core、Termcp-Desktop、MCP Server 与公开契约升级之间的关系。

## 1. 官方仓库

2026-10-28 通过 GitHub API 核验：

| 角色 | 官方仓库 | 默认分支 | 核验快照 |
|---|---|---|---|
| Core | <https://github.com/open-mcp-ai/Termcp> | `main` | `0595cb5c9ce2181de3f8049b06dc4c0ef0c2a6db`，发布 v0.2.8 |
| Desktop | <https://github.com/open-mcp-ai/Termcp-Desktop> | `main` | `d4386ecfa8e62110ab0351827a212e81bfab6c79` |
| Harness | 本工作区 | 尚未 `git init` | 未建立远端 |

补充事实：

- `termcp/termcp-desktop` 和 `termcp/termcp` 在核验时返回 404。
- 本机 Desktop clone 的 `origin` 是 `Sud0g-BlackZer0/Termcp-Desktop`，属于 fork；官方地址以上表为准。
- 本机 Core clone 当前 `main` 为 `34a1cb533959c90f8d4b8ca51fb709f83ceb70b6`，落后于官方 `main` 的 v0.2.8 快照。
- 本机 Desktop clone 当前 `main` 与官方核验快照一致。

这些是本机与 GitHub 在 2026-10-28 的观察结果，不代表未来版本。

## 2. 三个平面与角色

### Core：执行平面与契约上游

Core 负责：

- SSH、PTY、Shell、终端 I/O、文件与端口转发。
- 会话、消息、审批执行和审计。
- MCP Server 能力。
- HTTP、WebSocket、MCP 等公开契约。
- 服务端认证、权限和执行约束。

Harness 可以读取 Core 的公开文档、调用公开接口和记录契约版本，但不得：

- import Core 内部包。
- 读取或修改 Core 私有数据库和内部配置。
- 复制终端执行引擎、审批规则或权限策略。
- 修改 Core 来让 Harness 测试变绿。

### Harness Runtime：控制平面的核心

Harness Runtime 负责：

- Agent Loop 与 Session 生命周期。
- MCP Client/Host、外部 MCP 注册、连接和工具能力状态。
- 工具调用前验证/策略、调用后验证和操作记录。
- 上下文、事件日志、诊断和恢复。
- 对外控制 API、事件流与第三方接入。
- Headless/daemon/容器等部署边界。
- Harness 自有配置、日志和脱敏。

Harness 不是第二个 Core，也不是固定模型或固定规划范式的单一自主 Agent。它可以承载和协调 Agent，但不得复制 Core 的 SSH、PTY、会话执行和审批执行能力。

### Terminal Workspace：第一方体验平面

Terminal Workspace 负责：

- 终端展示、输入、输出和 Session/Shell 切换。
- Agent 工具调用、审批、输出和异常时间线。
- 人工接管、继续和恢复操作。
- MCP 服务、工具能力和健康状态展示。

Terminal Workspace 不是 Runtime 本体，也不是唯一控制入口。Runtime 必须能在 Workspace 关闭时继续工作。

### Desktop：成熟的人类终端工作台与参考实现

Termcp-Desktop 的公开文档与代码显示：

- 它采用 Go + Wails v2 + 系统 WebView。
- 它在同一模块树内复用 Core 的 Go 内部组件。
- 某些运行模式把 Core 放在 Wails 进程内。
- 它已经覆盖连接、多会话、多窗格、文件、转发、审批、历史和多平台发布。

Desktop 可作为以下方面的参考：

- Wails 工程与平台经验。
- 终端 UI、窗口、托盘和生命周期行为。
- Core 契约的实际使用方式。
- 性能、测试和发布场景。

但 Desktop 的进程内 Core 集成方式、内部桥接和完整 SSH 管理器模型不能自动成为 Harness 的架构基线。Harness 的差异化是 Runtime、MCP 工具控制面和人机共享执行现场，而不是复制 Desktop。

## 3. MCP 关系

- Core 是 MCP **Server**，不是通用 MCP Client/Host。
- Harness Runtime 是 MCP **Client/Host**，可以连接 Core 和其他外部 MCP Server。
- Core 不提供“发现、连接、聚合外部 MCP Server”的通用生命周期；该能力属于 Harness Runtime。
- Terminal Workspace 应通过 Harness Runtime 的公开契约获得 MCP 状态和工具能力；不应假设 Core 具备外部 MCP 管理能力。
- Harness 自身对 Agent/UI 暴露的控制 API、事件流和插件接口仍需设计，不得把内部 API 当成公开契约。

## 4. 消费方式

Harness 不复制 Core 源码，也不 import Core 内部包。目标消费方式是：

```text
Terminal Workspace
  -> Harness Runtime 控制 API / 事件流
Harness Runtime
  -> Termcp Core 的公开 MCP / HTTP / WebSocket 契约
  -> 外部 MCP Server 的公开协议
```

终端字节流是由 Terminal Workspace 直连 Core，还是经 Harness Runtime 转发，仍是待 PoC 决策。选择时必须评估字节保序、延迟、策略覆盖、凭据暴露和恢复语义。

Core 的 Web UI 与 Core 服务当前在同一仓库和可执行程序中，Web UI 资源可以不被 Harness 使用，但这不等于已经有独立的 headless Core 发布物。

需要 Core 团队确认或提供：

- 适合父进程管理的 standalone 启动方式。
- 实例 ID、健康检查、动态端口和优雅关闭语义。
- 可验证的进程/数据目录所有权。
- 可禁用的 Web UI 资源或不影响服务的 headless 模式。
- 明确的版本和兼容范围。
- MCP 工具调用、审批、事件和终端输出的可信关联字段。

在这些契约确认前，Harness 优先管理自己明确启动的子进程；不得仅凭端口复用或停止未知实例。详细方案见 `docs/adr/ADR-010-core-integration-boundary.md`。

多客户端冲突策略：先探测并验证现有 Core；兼容则作为外部实例附着，不取得所有权；不兼容或身份未知时停止启动并诊断；没有可用实例时才用独立端口和独立数据目录启动受管 Core。

## 5. 契约基线

开始任何真实 Core/MCP 集成前，必须记录：

1. Core 或 MCP Server 的版本/commit SHA。
2. HTTP、WebSocket、MCP 文档路径。
3. 实际使用的接口、事件和工具类型。
4. 行为语义的确认状态。
5. 测试环境、平台和复现步骤。
6. Harness Runtime/Terminal Workspace 自身的接口版本。

建议的初始 Core 基线：

- Core v0.2.8。
- 官方仓库 `main` 在 2026-10-28 的快照。
- 契约文档：[API](https://github.com/open-mcp-ai/Termcp/blob/main/docs/api.md)、[架构](https://github.com/open-mcp-ai/Termcp/blob/main/docs/architecture.md)、[MCP 工具](https://github.com/open-mcp-ai/Termcp/blob/main/docs/mcp-tools.md)、[CLI](https://github.com/open-mcp-ai/Termcp/blob/main/docs/cli.md)。

初始基线仍需在脚手架阶段重新拉取和核对；本机 Core clone 当前落后，不能直接当成 v0.2.8 契约。

2026-10-28 已通过安装的 Core v0.2.8 release 完成运行时版本、MCP initialize 和 tools/list 核验，证据记录在 `docs/contracts/core/v0.2.8/`。源码 clone 的落后状态不变，因此不能用它替代已验证 release。

## 6. 升级与变更规则

- Core/MCP 契约升级前先阅读 changelog、API 文档和实际实现。
- 契约测试失败先分类：真实变更、桩过期、Harness 适配错误、语义未定义。
- 不通过放宽 Mock 或删除断言解决契约变更。
- 不直接修改官方仓库作为本地捷径；需要 Core 变更时单独提出 issue/PR。
- Harness 与 Core/MCP 的版本对应关系写入 README 和测试记录。
- 未验证的行为标记为待确认，不写成服务端或协议保证。
- Harness 自身的控制 API、事件流和插件契约也必须版本化，不让第三方依赖内部实现。

## 7. 本机相关仓库使用规则

- 读 Core/Desktop 前先运行 `git status`。
- 不把未提交改动当既定设计。
- 不在未授权情况下切换、清理或提交其他仓库。
- 可以只读查看参考实现；如要复制代码，必须确认许可证、所有权和架构适用性，并记录来源。
- 旧 Harness 实现含未提交改动，只能作为行为参考，不作为本工作区基线。

## 8. 当前结论

**Core 是执行权威与契约上游；Desktop 是成熟的人类终端工作台和参考实现；Harness Runtime 是 MCP 原生的 Agent Runtime 与工具控制面；Terminal Workspace 是第一方体验层。**

**Harness 与 Core 通过公开 MCP/HTTP/WebSocket 契约协作，不复制执行引擎；Harness 与外部 MCP 通过公开 MCP 协议协作，不要求私有 SDK。**
