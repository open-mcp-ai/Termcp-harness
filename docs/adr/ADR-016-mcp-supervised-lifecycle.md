# ADR-016：MCP 监督生命周期与凭据引用

- 状态：Accepted
- 日期：2026-10-31
- 责任范围：Harness MCP Host 的连接健康、恢复和认证输入
- 关联：`ADR-005`、`ADR-007`、`ADR-011`、`docs/contracts/harness/v0.1/mcp-server-states.md`

## 1. 背景

MCP Host 已有持久 Streamable HTTP 客户端、能力读取、启用偏好和 `tools/list_changed` 失效，但连接失败后不会恢复。外部认证也只能通过进程内字段注入，缺少安全的启动入口。

## 2. 决策

Host 运行统一 supervisor：

- 注册后按显式 desired connection/enable 状态推进生命周期。
- 已连接时周期调用 MCP `ping`。
- 健康失败或非预期断连进入 `unavailable`，使用 1 秒到 30 秒指数退避重连。
- 每次重连后重新 `initialize` 和 `tools/list`，不沿用旧 Schema。
- `tools/list_changed` 先使能力 stale，再由 supervisor 自动刷新。
- `Disable` 保留连接；`Disconnect` 停止自动重连，直到显式连接。
- 支持 `streamable-http`、`sse` 和 `stdio`；SSE 使用持久生命周期上下文，stdio 子进程由 transport 管理并随断连/关闭回收。
- stdio 只允许启动时显式命令配置，不开放控制 API 或 Desktop 的任意命令入口。
- `--mcp-config-file` 可在启动时注册多个额外 Server；每个 Server 的 transport、enabled、凭据、连接和能力独立，工具以 `server_id + tool_name` 隔离。

CLI 通过 `--mcp-auth-token-file` 读取 Bearer Token：

- Token 只保存在 Runtime 内存。
- Token 不进入参数、URL、Snapshot、Event、日志或 Desktop。
- 只有 Core 与 MCP 为完全相同的 HTTP origin 时才复用 Token。
- 退避和健康间隔通过 `--mcp-reconnect-min`、`--mcp-reconnect-max`、`--mcp-health-interval` 显式配置。

## 3. 状态与安全

- 连接、能力、启用和 readiness 继续独立建模。
- Health 失败只记录受限 FailureReason。
- Ready=false 时 Host 不返回旧工具。
- 凭据不得出现在事件 payload 中。
- 不同 origin、不同端口或 scheme 变化均禁止自动复用凭据。

## 4. 验证

- supervisor 自动健康失败、重连、能力重载和重新启用测试。
- `tools/list_changed` 后自动能力刷新测试。
- Bearer Token 仅传给 client factory、不进入 Snapshot 的测试。
- 受保护的真实 Streamable HTTP MCP Server 握手测试。
- 真实 SSE MCP 握手、能力读取和启用测试。
- 真实 stdio 子进程握手、能力读取、启用和关闭回收测试。
- 真实非 Core stdio MCP 通过多来源配置文件注册并被 Agent Session 调用的端到端测试。
- 真实 Core：初始连接、能力读取、启用和连续 ping 后保持 `ready=true`。

## 5. 尚未完成

- OAuth 交互、动态凭据刷新和外部凭据 Vault。
- Header 名称可配置的非 Bearer 认证。
