# Harness Agent Session v1（草案）

> 状态：确定性 Headless Session 内核、受保护创建/运行、完成级别、审批结果自动消费和步骤恢复已覆盖；模型、持久化和进程重启恢复尚未接入。
>
> 日期：2026-10-30
>
> 实现：`internal/agentsession`、`GET /v1/agent/sessions`

## 1. 边界

Agent Session 属于 Harness Runtime，不是 Core Session、Shell 或终端通道。`core_session_id` 只在存在可信关联时记录；缺失时保持空值，不按名称、时间或当前活动 session 猜测。

当前版本只执行调用方提供的确定性步骤，不包含模型规划、上下文压缩或自然语言循环。

## 2. 状态

```text
new
  └─ running
       ├─ completed
       ├─ waiting_approval
       │    └─ waiting_execution（审批已确认，但工具结果仍未知）
       │         ├─ trusted succeeded ─ running（继续剩余步骤）
       │         ├─ trusted failed/blocked/cancelled/expired ─ 对应终态
       │         └─ unknown ─ waiting_execution（等待迟到的可信证据）
       ├─ blocked
       ├─ failed
       ├─ cancelled
       ├─ expired
       └─ unknown
```

规则：

- 一个 Session 只能从 `new` 运行一次。
- 每个 Step 都经过 `internal/toolpipeline`，不能绕过校验、策略和事件记录。
- 第一个非成功工具状态会停止后续步骤。
- 工具 `succeeded` 只表示该工具调用返回可信结果，不表示整个 Session 已完成。
- `approval_pending` 映射为 `waiting_approval`；只有 trusted `approval_id` 会写入 `current_approval_id`。
- 审批 `approved` 只进入 `waiting_execution`，不得据此把工具或 Session 标记为成功；`rejected` 进入 `blocked`，`expired` / `cancelled` 进入对应状态。
- 审批获批时可携带 Core 的结构化 `execution`：`submitted` 只表示 shell 输入已写入，`succeeded` 只表示文件或端口转发执行完成，`failed` 进入 Session `failed`，`running` / `unknown` 保持 `waiting_execution`。
- `submitted` 或 `succeeded` 的执行结果仍需经过可信工具结果消费后才能继续剩余步骤；不能仅因审批接口返回 `200` 就推进 Session。
- 每个 Step 声明 `required_completion`：默认 `accepted`，允许在可信 `submitted` 或 `succeeded` 后继续；`completed` 只接受可信 `succeeded`，`submitted` 继续等待。
- 审批协调器收到可继续的结构化执行结果后，会启动一次异步自动恢复；同一 Session 恢复只领取一次，结果 `unknown` 时保持等待，不自动重试。
- 审批 `unknown` 保持 `waiting_approval`，并单独记录 `current_approval_status=unknown`。
- 已确认的审批终态不能回退；后续异步观测与其冲突时拒绝更新并保留原状态。
- 只有与当前 call ID 一致、且审批状态已为 `approved` 的可信工具结果可以回填 `waiting_execution`；结果不匹配或仍为中间态时拒绝更新。
- 工具 call ID 在 Runtime 内必须保持唯一，避免审批协调器把一个 call 关联到多个请求。审批登记失败时 Session 进入 `unknown`，保留可信审批 ID 供诊断，但不得显示为可管理的 `waiting_approval`。
- 可信 `succeeded` 才允许继续尚未执行的步骤；`failed` / `blocked` / `cancelled` / `expired` 停止 Session；`unknown` 保持 `waiting_execution` 并记录 `current_tool_status=unknown`，不自动重试。
- 已成功继续的 Session 不重新执行历史步骤；同一 call ID 的重复成功结果幂等，成功后再到矛盾结果不能覆盖终态。
- `unknown` 不自动重试，也不自动变成失败或成功。
- Step 执行前发现上下文已取消时进入 `cancelled`；已派发调用取消后的工具结果仍按工具状态机处理。
- `finished_at` 只出现在 `completed`、`blocked`、`failed`、`cancelled`、`expired` 和 `unknown`；`waiting_approval` / `waiting_execution` 表示现场仍可继续，不设置该字段。

## 3. 控制事件

- `agent.session.started`
- `agent.tool.started`
- `agent.tool.finished`
- `agent.session.waiting_approval`
- `agent.session.waiting_execution`
- `agent.session.approval_observed`
- `agent.tool.result_observed`
- `agent.session.resumed`
- `agent.session.resume_failed`（诊断事件，不记录错误正文）
- `agent.session.completed`
- `agent.session.blocked`
- `agent.session.failed`
- `agent.session.cancelled`
- `agent.session.expired`
- `agent.session.outcome_unknown`

Session 事件以 Agent Session ID 为 `subject_id`，当前工具调用 ID 为 `correlation_id`。普通事件只记录状态、revision、Core Session 关联 ID 和工具状态，不记录工具参数或结果正文。

## 4. 当前限制

- Session 与事件只在内存中，Runtime 重启后丢失。
- 没有模型 Adapter、用户消息、上下文投影或摘要。
- `POST /v1/agent/sessions` 与 `POST /v1/agent/sessions/{id}/run` 必须携带 Bearer token；运行还必须配置精确 allowlist 的工具策略文件。
- CLI 通过 `termcp-harness agent run` 创建并运行显式步骤；运行响应只返回调用状态和结果字节数，不直接回传工具正文。
- Harness 已接入 Termcp 结构化 `execution` 的自动消费与步骤恢复；字段缺失时保持 `waiting_execution`，不根据输出文本或时间补造结果。
- Termcp 0.2.8 开发基线在 review 结果中提供可信 `approval_id`；Harness 只在字段存在且通过校验时建立关联，并继续以结构化 `execution` 消费执行结果。

## 5. 首个纵向验证

2026-10-30 使用本地 Core 开发构建、Harness 临时二进制、独立临时数据目录和随机回环端口验证：

- `examples/tool-policy.readonly.json` 只允许 `termcp-core / term / {"cmd":""}`。
- Bearer 保护的 CLI `agent run` 创建并运行一个步骤。
- Session 最终为 `completed`，产生一条 `tool.call.succeeded` 和一条 `agent.session.completed`。
- 实验结束后按精确 PID 停止两个进程，并删除 TEMP 下的专用目录。

同日完成审批闭环实验：

- `term` 文件写入进入 `waiting_approval`，外层 `202` 返回可信 `Approval-ID`。
- 批准前目标文件不存在；批准后 Core 返回 `execution=succeeded` 并实际写入。
- Harness 自动消费执行结果，Session 进入 `completed`。
- 事件中审批成功、Session 恢复和 Session 完成各记录一次。
