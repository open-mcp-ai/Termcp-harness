# Termcp Harness 契约追踪

> 状态：目录规范（2026-10-28）
>
> 当前尚未保存正式契约快照。实际接口以实现前重新核对的 Core、MCP 和 Harness 版本为准。

## 目的

这里用于记录 Harness 依赖或对外暴露的公开契约，而不是复制 Core 内部实现或把 Harness 内部 API 当成稳定协议。

适用内容：

- Core HTTP API 端点与错误语义。
- Core WebSocket 事件类型、ID、关联和快照语义。
- MCP 传输、工具、资源、提示、能力刷新和错误语义。
- Core 与 Harness 的认证、审批、实例信息和审计边界。
- Harness Runtime 控制 API、事件流、Session 查询和状态导出。
- Terminal Workspace 与 Runtime 之间的公开交互契约。
- 模块目录、安装分组、推荐规则、依赖、权限、生命周期和版本兼容。
- 版本兼容与迁移说明。

## 当前基线

Core 计划基线：

- Core 仓库：<https://github.com/open-mcp-ai/Termcp>
- 版本：v0.2.8
- 核验日期：2026-10-28
- 官方 `main` 快照：`0595cb5c9ce2181de3f8049b06dc4c0ef0c2a6db`

注意：

- 2026-10-28 已通过本机安装的 `termcp v0.2.8 (commit 0595cb5)` 和运行中实例完成 MCP 握手/能力读取，证据见 `core/v0.2.8/`。
- Core v0.2.8 的未关闭上游问题和严格契约测试统一登记在 `core/v0.2.8/known-issues.md`；Core 相关开发和汇报必须先检查该文件。
- 本机 Core 源码 clone 仍落后于该快照，不能直接作为 v0.2.8 的证明。
- 公开文档、实际代码、集成测试和团队确认的证据强度不同，必须分别记录。
- Harness Runtime 已有 Headless 骨架、本地控制 API v1 草案、工具/审批状态机、MCP Server 状态模型和首个 OpenAI-compatible 模型 Adapter/Agent Loop，见 `harness/v0.1/`；兼容性尚未承诺。
- 外部 MCP Server 必须记录其服务器版本、协议版本和能力快照。

## 建议目录结构

```text
docs/contracts/
├── README.md
├── core/
│   └── v0.2.8/
│       ├── metadata.md
│       ├── endpoints.md
│       ├── events.md
│       ├── fixtures/
│       └── notes.md
├── mcp/
│   └── <server-or-protocol-version>/
│       ├── metadata.md
│       ├── tools.md
│       ├── events.md
│       └── fixtures/
└── harness/
    └── v0.x/
        ├── control-api.md
        ├── event-stream.md
        ├── session-and-tool-states.md
        ├── module-catalog.md
        ├── module-recommendation.md
        ├── workspace-recipe.md
        ├── plugin-manifest.md
        └── fixtures/
```

具体文件名可以调整，但必须保留来源、版本、确认状态和验证证据。

## 每个契约记录至少包含

- 来源 URL 或仓库文件路径。
- Core、MCP Server 或 Harness 的版本/commit SHA。
- 核验日期。
- 接口、事件、工具、字段和状态定义。
- 已确认与待确认的行为语义。
- 对应 Harness 适配层或 Terminal Workspace 测试。
- 真实集成验证结果。
- 兼容性变化和降级方案。
- 凭据、权限和数据暴露边界。

## 必须单独记录的行为语义

结构 Schema 不足以证明：

- MCP 工具调用是否幂等、可取消、可重试。
- 工具调用在超时、断连、进程退出或 Session 关闭后的状态。
- 审批决定是否原子、拒绝是否阻止已排队操作。
- Agent 输入、工具调用、审批和终端输出之间是否存在可信关联 ID。
- WebSocket 快照与实时事件的接管顺序。
- MCP 能力变化后旧工具引用是否仍有效。
- UI 关闭是否影响 Runtime 或工具执行。
- L0-L3 是否只表示安装分组，推荐项是否可以取消。
- 模块依赖、冲突、Capability 和未验证组合如何表达。
- 选择、安装、启用、运行和能力就绪是否分开记录。
- 不同模块对象类型是否允许不同生命周期适配器。

这些必须通过文档约定、状态模型、真实集成测试和故障测试验证。

## 禁止事项

- 不把 Harness 自维护的 Mock/Schema 当作 Core 或 MCP 权威契约。
- 不只改 Mock 让测试变绿。
- 不把结构 Schema 当作审批原子性、事件因果或超时后执行状态的证明。
- 不复制 Core 内部包来绕过公开契约。
- 不把 Harness 内部 API、内部目录或私有 RPC 暴露为第三方稳定契约。
- 不把未确认字段或行为写成确定事实。
- 不把“发现 MCP Server”写成“已连接、已启用或工具可安全调用”。

## 当前状态

Harness 本地控制 API v1 草案已有代码、单元测试和进程冒烟测试覆盖；工具调用与审批状态机已有独立单元/竞态测试。审批协调器现已支持可选历史持久化，重启时未确认决定 fail-closed 为 `unknown`，并可经只读 Core Lookup 显式收敛。MCP Host 已支持 Streamable HTTP、SSE、stdio、多来源配置、Bearer 凭据引用、健康检查、自动重连和能力刷新，并对 Core v0.2.8 完成真实 `initialize`/`tools/list`。非 Core stdio MCP 已通过 Agent Session 工具调用闭环。Desktop Terminal 固定桥 PoC 已通过真实 Core 的 Shell 查询、历史恢复、WebSocket watch、输入、输出、resize、20,000 行性能和关闭测试，契约见 `harness/v0.1/terminal-bridge.md`。事件日志已提供 SSE、cursor 重连、Desktop 有界桥接和可选有界文件持久化，契约见 `harness/v0.1/event-log.md`。这些仍不等于完整兼容性或行为语义验证；OAuth、跨 Store 原子提交、事件归档/压缩和每个第三方 MCP 的正式契约快照尚未完成。
