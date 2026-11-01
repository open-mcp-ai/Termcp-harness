# Harness Event Log v1（草案）

> 状态：内存实现、SSE 实时订阅、游标重连、Desktop 有界桥接、同 Runtime 实例客户端 cursor 恢复和可选本地保留窗口持久化已覆盖；尚无压缩归档，审批/MCP/活动 Loop 状态不会从事件恢复。
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
- `approval.execution.refreshed`
- `approval.reconciled`

审批收敛失败会写入诊断事件 `approval.reconciliation.failed`。

工具调用事件以 Harness 生成的 call ID 作为 `subject_id`。当前没有可信的远端 operation ID，因此 `correlation_id` 保持为空。事件不记录工具参数正文或结果正文，只记录来源、工具名、副作用类别、状态、revision、策略原因和结果字节数等元数据。`tool.call.dispatching` 在 MCP 请求前写入，表示即将派发，不代表远端已接受或开始执行。`tool.call.approval_pending` 表示远端 review 已接受请求但尚未执行，只记录结果和挂起说明的字节数，不记录命令正文。

审批事件以 `approval_id` 作为 `subject_id`，以 Harness call ID 作为 `correlation_id`。事件只记录审批状态、决定、revision、Core session ID、过期时间和关联可信度，不记录审批正文、命令、Token 或其他凭据。`unknown` 关联只保留在工具调用事件中，不会被审批协调器登记。

`approval.execution.observed` 只表示只读查询获得了新的可信 Execution 快照，不表示审批决定被重新提交。`approval.execution.refreshed` 表示一次人工触发的只读执行状态刷新已完成，并记录 `changed` 与 `execution_complete`；即使快照未变化也会记录，以支持 `waiting_execution` 恢复操作审计。`approval.reconciliation.failed` 只记录原因分类和错误字节数，不记录错误正文。

`approval.reconciled` 只表示对本地 `unknown` 审批执行了一次显式只读 Core Lookup，记录 `reason=read_only_lookup`、`remote_status`、`changed` 和 `resolved`。它不表示提交了新决定；远端仍为 `pending` 时本地保持 `unknown`。

配置 `--approval-store-file` 后，Runtime 重启时若发现持久化的 `pending` / `submitting` 审批，会写入 `approval.outcome_unknown`，`Data.reason=runtime_restart`。该事件只表示本地历史恢复为未知，不证明远端仍未决定、已执行或已取消。

配置 `--session-store-file` 后，Runtime 重启恢复未完成模型 Loop 时，`agent.session.outcome_unknown` 事件只附带 `checkpoint_stage` 的恢复摘要，不包含模型正文、工具参数、参数 hash 以外的执行细节或工具结果正文。

附带执行结果时，事件额外记录 `execution_state`、开始/结束时间和 `execution_error_bytes`，不写入执行错误正文。

确定性 Agent Session 会写入以下控制事件：

- `agent.session.started`
- `agent.tool.started`
- `agent.tool.finished`
- `agent.model.requested`
- `agent.model.responded`
- `agent.model.failed`
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

模型事件同样不记录模型正文、工具参数或工具结果正文。`agent.model.requested` 记录迭代序号、投影后消息数、被省略轮次数、上下文字节数和工具数；`agent.model.responded` 记录输出字节数和工具调用数；`agent.model.failed` 是诊断事件，只记录原因分类和错误字节数。

审批协调器通知 Session 失败时写入诊断事件 `approval.observer.failed`；该失败不改变已经确认的审批结果，但会使受影响的 Session 状态保持可诊断。

## 3. 有界保留与本地持久化

- 控制事件默认保留 4096 条，不自动淘汰。
- 诊断事件默认保留 8192 条，超限时淘汰同通道最旧事件。
- 控制事件容量耗尽时拒绝新事件，返回 `control capacity exceeded`，而不是静默丢弃。
- 诊断事件淘汰累积到 `dropped_diagnostics`，并以 `diagnostic_dropped_through` 暴露已丢弃水位。
- `truncated=true` 表示请求 cursor 早于已知诊断缺口；客户端必须显示历史不完整，不得冒充完整恢复。

默认仍为纯内存，Runtime 退出即丢失。显式配置 `--event-log-file <path>` 后：

- 文件保存当前有界保留窗口、`next_sequence`、`dropped_diagnostics`、`diagnostic_dropped_through` 和 `control_overflow`。
- 新 Runtime 从文件恢复事件与 sequence，继续使用严格递增的本地序号；旧序号不复用。
- 每次事件接受后同步持久化。写入或原子替换失败时，本次内存追加回滚并返回错误，不能把未落盘事件当成已提交事实。
- Windows 原子替换遇到 `ACCESS_DENIED`、`SHARING_VIOLATION` 或 `LOCK_VIOLATION` 时，最多执行 5 次 rename（初始尝试 + 4 次重试），等待序列为 5/10/25/50 ms；耗尽后返回错误并执行相同回滚。其他错误不重试。
- 文件使用 schema version 1、同目录临时文件、`Sync` 和原子替换；默认上限 64 MiB，POSIX 目标权限 `0600`。
- 文件损坏、未知版本或结构不合法时启动失败，不静默清空、不猜测修复。
- 持久化只保留内存窗口，不归档已淘汰诊断事件，也不保证无限历史。
- 同一个文件只允许一个 Runtime 进程独占使用；跨进程文件锁尚未实现。

Agent Session 的 `--session-store-file` 与 Event Journal 的 `--event-log-file` 相互独立。前者保存 Session Snapshot、模型消息和最终回复，后者保存事件元数据；两者都不保存 Desktop cursor、审批队列、MCP 连接、终端字节流或活动 Agent Loop 的继续执行状态。Desktop 的 cursor 单独保存在本机前端 localStorage 的 `termcp.harness.event-cursor.v1`，内容仅包含 schema version、Runtime endpoint、Runtime instance ID 和最后已消费 sequence，不包含凭据或事件正文。

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

## 5. `GET /v1/events/stream`

SSE 实时订阅入口，查询参数和 `Last-Event-ID` 使用与 `/v1/events` 相同的 sequence 游标：

- 请求参数 `after` 优先；缺失时读取 `Last-Event-ID`；都缺失时为 `0`。
- 每批最多 200 条事件，SSE frame 为：

```text
id: 42
event: events
data: {"events":[...],"latest_sequence":42,...}

```

- 连接建立后即使没有事件，也发送一次完整 `Window` 元数据。
- 无事件时每 15 秒发送 SSE comment heartbeat。
- 客户端断线后必须用最后已确认 cursor 重连；不得根据时间或文本补洞。
- `truncated`、`diagnostic_dropped_through`、`control_overflow` 继续使用查询窗口语义。

Desktop 不把 SSE URL 暴露给前端。Wails Go 桥维护订阅并最多缓存 2048 条尚未消费事件；缓存耗尽进入 `lagged`，前端必须重新拉取权威快照并从最新 cursor 重开，而不是静默丢弃。

实时事件用于立即更新 Activity 和触发 120 ms 合并后的 Runtime 快照刷新。Session、审批、MCP、Core 和 Runtime 状态仍以控制 API 快照为权威；Desktop 保留 15 秒兜底快照轮询。

配置 `--event-log-file` 后，Runtime 重启会从持久化窗口恢复事件和 sequence，SSE 可继续从保留窗口补拉。Desktop 在同一 Runtime instance 内重启时，会从保存的最后已消费 sequence 继续订阅并补拉；endpoint 或 Runtime instance ID 不匹配、存储损坏或 cursor 超前时，会重置到当前 `latest_sequence` 并覆盖保存值，避免把旧日志序号套到新实例。

cursor 恢复仍不等于完整历史恢复。请求位置早于已知丢弃水位时继续显示 `truncated`；Desktop 本地 2048 条队列溢出时先进入 `lagged` 并从权威快照重开。Event Journal 的 runtime-instance 重置不会自动沿用旧 cursor，因此不能仅凭本地保存值宣称跨 Runtime 的连续轨迹。
