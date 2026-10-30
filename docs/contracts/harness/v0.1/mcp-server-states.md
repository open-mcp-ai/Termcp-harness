# Harness MCP Server 状态 v1（草案）

> 状态：持久 Streamable HTTP 连接、能力读取、启用/停用、断连、`tools/list_changed` 失效和 Event Log 已接入；认证与自动重连尚未实现。
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

## 4. 持久 Host

`internal/mcpcontrol.Host` 持有真实 MCP 客户端，并按 Server 串行化生命周期操作：

```text
Register
  -> Connect（协议初始化）
  -> LoadCapabilities（读取工具与 Schema）
  -> Enable（加入当前 Runtime）
  -> Ready
```

- `Connect` 成功不会自动产生 `Ready`。
- `LoadCapabilities` 失败会把连接标为 `unavailable`，不保留可用工具引用。
- `Disable` 保留连接和能力，但从工作区撤销能力。
- `Disconnect` 关闭客户端并保留用户的启用偏好，能力变为 `stale`。
- `notifications/tools/list_changed` 只把能力标为 `stale` 和 `Ready=false`，不自动重读，也不继续使用旧 Schema。
- 并发 `Connect` 只建立一个客户端；旧异步结果不能覆盖更新的 revision。
- 生命周期控制事件写入 Event Log，事件不包含凭据或工具结果。

Runtime 启动时可用以下选项接入一个回环 Streamable HTTP Server：

```powershell
go run ./cmd/termcp-harness serve `
  --listen 127.0.0.1:8787 `
  --mcp-endpoint http://127.0.0.1:18765/stream
```

可选参数：`--mcp-id`、`--mcp-name`、`--mcp-enable=false`。连接和初始能力读取在后台执行；失败只显示 `unavailable`，不会阻止 Headless Runtime 启动。

## 5. 尚未支持

- 非回环 MCP endpoint；
- HTTP 认证头和凭据引用；
- SSE 与 stdio 的持久连接；
- 自动重连、退避、健康检查和后台能力刷新；
- 受管 MCP 子进程生命周期。
