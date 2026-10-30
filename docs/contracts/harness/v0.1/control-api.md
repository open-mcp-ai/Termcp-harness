# Harness control API v1（草案）

> 状态：代码与本地测试已覆盖；公开兼容性尚未承诺。
>
> 日期：2026-10-28
>
> 适用实现：`cmd/termcp-harness serve`

## 边界

本版只提供 Harness Runtime 的本地生命周期与健康快照。它不包含 Session、MCP Server、工具调用、审批或 Core 状态，也不代表这些能力已经实现。

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

## 非目标

- 不从本地时间推断 Core 或 MCP 事件因果。
- 不把健康响应描述为 Core、MCP 工具或 Agent 能力就绪。
- 不提供任意 URL、文件、命令或 MCP 调用的通用桥接。
