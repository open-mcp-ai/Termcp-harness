# Harness Event Log v1（草案）

> 状态：内存实现与本地测试已覆盖；尚未持久化、压缩归档或提供实时订阅。
>
> 实现：`internal/eventlog`

## 1. 顺序语义

每个事件有 Harness 本地单调递增的 `sequence`。它只表示 Harness 接受事件的本地顺序：

- 不是 Core 或 MCP 的远端执行顺序；
- 不能用于推断没有可信 ID 的跨系统因果关系；
- 不能把时间接近解释成先后或因果。

事件保留 `correlation_id` 和 `subject_id` 字段。缺失可信 ID 时保持为空，不使用接收时间或相似文本补造关联。

## 2. 事件信封

```json
{
  "event_id": "32-hex-character-id",
  "sequence": 1,
  "occurred_at": "2026-10-28T12:00:00Z",
  "type": "runtime.started",
  "class": "control",
  "subject_id": "runtime-instance-id",
  "correlation_id": "",
  "data": {}
}
```

- `event_id`：单条事件身份，不替代远端 operation ID。
- `class`：`control` 或 `diagnostic`。
- `data`：可选 JSON，单条上限 64 KiB；不放置 Token、Authorization、私钥或敏感终端内容。

当前 MCP 生命周期会写入以下控制事件：

- `mcp.server.registered`
- `mcp.server.connecting`
- `mcp.server.connected`
- `mcp.server.capabilities_loading`
- `mcp.server.capabilities_loaded`
- `mcp.server.capabilities_stale`
- `mcp.server.enabled`
- `mcp.server.disabled`
- `mcp.server.disconnected`
- `mcp.server.unavailable`

Core 实例识别会写入以下控制事件：

- `core.instance.bound`
- `core.instance.changed`
- `core.instance.unavailable`
- `core.ownership.conflict`

当前受控工具调用会写入以下控制事件：

- `tool.call.proposed`
- `tool.call.ready`
- `tool.call.blocked`
- `tool.call.dispatching`
- `tool.call.approval_pending`
- `tool.call.succeeded`
- `tool.call.failed`
- `tool.call.outcome_unknown`

审批协调器会写入以下控制事件：

- `approval.request.tracked`
- `approval.decision.submitting`
- `approval.approved`
- `approval.rejected`
- `approval.expired`
- `approval.cancelled`
- `approval.outcome_unknown`
- `approval.execution.observed`

审批收敛失败会写入诊断事件 `approval.reconciliation.failed`。

工具调用事件以 Harness 生成的 call ID 作为 `subject_id`。当前没有可信的远端 operation ID，因此 `correlation_id` 保持为空。事件不记录工具参数正文或结果正文，只记录来源、工具名、副作用类别、状态、revision、策略原因和结果字节数等元数据。`tool.call.dispatching` 在 MCP 请求前写入，表示即将派发，不代表远端已接受或开始执行。`tool.call.approval_pending` 表示远端 review 已接受请求但尚未执行，只记录结果和挂起说明的字节数，不记录命令正文。

审批事件以 `approval_id` 作为 `subject_id`，以 Harness call ID 作为 `correlation_id`。事件只记录审批状态、决定、revision、Core session ID、过期时间和关联可信度，不记录审批正文、命令、Token 或其他凭据。`unknown` 关联只保留在工具调用事件中，不会被审批协调器登记。

`approval.execution.observed` 只表示只读查询获得了新的可信 Execution 快照，不表示审批决定被重新提交。`approval.reconciliation.failed` 只记录原因分类和错误字节数，不记录错误正文。

附带执行结果时，事件额外记录 `execution_state`、开始/结束时间和 `execution_error_bytes`，不写入执行错误正文。

确定性 Agent Session 会写入以下控制事件：

- `agent.session.started`
- `agent.tool.started`
- `agent.tool.finished`
- `agent.session.waiting_approval`
- `agent.session.waiting_execution`
- `agent.session.approval_observed`
- `agent.tool.result_observed`
- `agent.session.resumed`
- `agent.session.resume_failed`
- `agent.session.completed`
- `agent.session.blocked`
- `agent.session.failed`
- `agent.session.cancelled`
- `agent.session.expired`
- `agent.session.outcome_unknown`

Session 事件不记录工具参数或结果正文。它们记录 Session 状态、revision、Core Session 关联和工具状态，用于重建确定性执行轨迹。

审批协调器通知 Session 失败时写入诊断事件 `approval.observer.failed`；该失败不改变已经确认的审批结果，但会使受影响的 Session 状态保持可诊断。

## 3. 有界保留

- 控制事件默认保留 4096 条，不自动淘汰。
- 诊断事件默认保留 8192 条，超限时淘汰同通道最旧事件。
- 控制事件容量耗尽时拒绝新事件，返回 `control capacity exceeded`，而不是静默丢弃。
- 诊断事件淘汰累积到 `dropped_diagnostics`，并以 `diagnostic_dropped_through` 暴露已丢弃水位。
- `truncated=true` 表示请求 cursor 早于已知诊断缺口；客户端必须显示历史不完整，不得冒充完整恢复。

当前事件日志只在内存中，Runtime 退出即丢失。持久化和跨重启恢复尚未实现。

## 4. `GET /v1/events`

查询参数：

- `after`：返回 sequence 严格大于该值的本地事件；默认 `0`。
- `limit`：单次最多返回条数，范围 `1..1000`；默认 `100`。

响应：

```json
{
  "events": [],
  "latest_sequence": 0,
  "dropped_diagnostics": 0,
  "diagnostic_dropped_through": 0,
  "truncated": false,
  "has_more": false,
  "control_overflow": false
}
```

`has_more=true` 表示当前保留窗口中还有比本页更新的事件。客户端使用最后一条事件的 `sequence` 作为下一次 `after`。

本版没有实时 push、SSE/WebSocket 订阅和游标签名。Desktop 首版使用轮询补拉。
