# Harness control API v1（草案）

> 状态：代码与本地测试已覆盖；公开兼容性尚未承诺。
>
> 日期：2026-10-29
>
> 适用实现：`cmd/termcp-harness serve`

## 边界

本版提供 Harness Runtime 生命周期、事件窗口、MCP Server 列表和已绑定 Core 的只读身份快照。它不包含 Session、工具调用、审批决定或 Core 进程控制能力。

事件补拉入口见 `event-log.md`；事件模型也已单独版本化。

控制 API v1 当前只允许绑定回环地址。认证、远程访问、TLS、CORS 和第三方客户端兼容性均未设计；在这些边界完成前不得暴露到非回环网络。

## `GET /healthz`

Runtime 处于 `running` 时返回 `200`，其他生命周期状态返回 `503`。

```json
{
  "status": "ok",
  "state": "running",
  "version": "dev"
}
```

## `GET /v1/runtime`

返回当前 Runtime 实例的一致性快照：

```json
{
  "instance_id": "32-hex-character-local-instance-id",
  "version": "dev",
  "state": "running",
  "started_at": "2026-10-28T12:00:00Z"
}
```

`instance_id` 标识本次 Harness Runtime 进程实例，不是 Core 实例、MCP Server 或 Session ID。生命周期状态包括：

- `new`
- `running`
- `stopping`
- `stopped`

`failed` 已为未来失败收敛保留，本版控制 API 不会主动产生该状态。

## `GET /v1/events`

按 Harness 本地 `sequence` 补拉有界事件窗口。详细语义、容量、截断标记和 payload 限制见 `event-log.md`。

## `GET /v1/core`

返回当前 Harness 绑定的 Core 实例身份。未配置 Core 时：

```json
{"configured":false}
```

已配置且获得快照时返回 `configured=true` 和 `core`。字段、所有权、冲突和重启语义见 `core-instance.md`。

该端点只证明 Harness 基于公开契约观察到的实例身份；`ownership=external` 表示不拥有 Core 进程，不得据此停止进程。

## 非目标

- 不从本地时间推断 Core 或 MCP 事件因果。
- 不把健康响应描述为 Core、MCP 工具或 Agent 能力就绪。
- 不把端口可达或 PID 观测写成永久进程所有权。
- 不提供任意 URL、文件、命令或 MCP 调用的通用桥接。
