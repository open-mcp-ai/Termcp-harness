# Harness MCP Server 状态 v1（草案）

> 状态：代码与本地测试已覆盖；尚未接入持久连接、能力变更通知或 Event Log。
>
> 实现：`internal/mcpcontrol`

## 1. 五个独立维度

一个 MCP Server 不能用单一 `connected` 或 `enabled` 布尔值表示。当前模型分开记录：

| 维度 | 状态 |
|---|---|
| 登记 | `discovered`、`registered` |
| 连接 | `not_attempted`、`connecting`、`connected`、`disconnected`、`unavailable` |
| 能力 | `not_loaded`、`loading`、`loaded`、`stale` |
| 启用 | `disabled`、`enabled` |
| 所有权 | `external`、`managed` |

`ready` 是派生值，只有同时满足以下条件才为真：

- 已登记；
- 连接为 `connected`；
- 能力为 `loaded`；
- 启用为 `enabled`。

复选框、登记、连接和能力读取都不能单独冒充 `ready`。

## 2. 强制规则

- `discovered` 不能直接连接；必须先进入 `registered`。
- 未连接时不能读取能力。
- 能力未读取时不能启用。
- 断连或不可用会把 `loaded` 能力标为 `stale`，旧的工具 Schema 不能继续视为可用。
- 连接丢失保留用户的 `enabled` 偏好，但 `ready=false`；恢复后必须重新读取能力。
- `managed` 与 `external` 描述所有权，不表示网络健康、权限或启用状态。
- 失败原因只保存受限枚举，不保存任意服务端错误文本，避免凭据或终端内容进入状态。
- 状态更新使用 revision compare-and-swap，旧连接或旧能力结果不能覆盖新状态。

## 3. 当前网络边界

v1 注册表与探测器只接受 HTTP/HTTPS 回环 endpoint，并拒绝 URL userinfo、query 和 fragment。该限制避免在 URL、日志或快照中携带凭据。

尚未支持：

- 非回环 MCP endpoint；
- HTTP 认证头和凭据引用；
- SSE 与 stdio 的持久连接；
- `tools/list_changed` 等能力变更通知；
- 自动重连、退避、健康检查和能力刷新；
- 受管 MCP 子进程生命周期。
