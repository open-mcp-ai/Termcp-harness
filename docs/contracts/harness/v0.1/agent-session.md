# Harness Agent Session v1（草案）

> 状态：确定性 Headless Session、受保护创建/运行、受保护停止任务、有界模型 Agent Loop、完成级别、审批结果自动消费、步骤恢复、`waiting_execution` 人工核对入口、可选本地 Session/transcript 持久化和活动 Loop 安全检查点人工恢复已覆盖；Event Journal 可独立持久化，审批队列及活动 Loop 自动恢复尚未接入。
>
> 日期：2026-10-30
>
> 实现：`internal/agentsession`、`GET /v1/agent/sessions`

## 1. 边界

Agent Session 属于 Harness Runtime，不是 Core Session、Shell 或终端通道。`core_session_id` 只在存在可信关联时记录；缺失时保持空值，不按名称、时间或当前活动 session 猜测。

当前版本支持两种入口：调用方提供的确定性步骤，以及从显式模型工具目录出发的有界模型循环。模型上下文按完整工具轮次有界投影，不做摘要；模型不能绕过工具策略或审批。

## 2. 状态

```text
new
  └─ running
       ├─ cancelling ─ cancelled / unknown
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
- `POST /v1/agent/sessions/{id}/cancel` 使用 revision CAS 请求停止。`new`、`waiting_approval`、`waiting_execution` 可立即收敛；`running` 先进入 `cancelling`，由当前执行边界决定最终状态。
- 模型请求中停止不会派发工具；工具派发前通过原子 dispatch gate 停止时进入 `cancelled`。
- 工具已派发后停止不宣称远端已终止。可信成功结果记录实际副作用后进入 `cancelled`；无法证明结果时进入 `unknown`，并设置 `remote_outcome_unknown=true`。
- 等待审批时停止不等于拒绝审批。Session 可进入 `cancelled`，但 `approval_pending=true`，原远端审批事实仍须单独处理。
- 等待执行时停止只停止 Harness 继续推进；`remote_outcome_unknown=true`，不得据此重试工具或假定 Core 未执行。
- `cancelled` 和 `cancelling` 的重复停止请求幂等返回；其他终态返回冲突。
- 模型 Loop 与确定性步骤共用同一个 Session 状态机和工具流水线，不能并发启动。
- 每个 Step 都经过 `internal/toolpipeline`，不能绕过校验、策略和事件记录。
- 第一个非成功工具状态会停止后续步骤。
- 工具 `succeeded` 只表示该工具调用返回可信结果，不表示整个 Session 已完成。
- `approval_pending` 映射为 `waiting_approval`；只有 trusted `approval_id` 会写入 `current_approval_id`。
- 审批 `approved` 只进入 `waiting_execution`，不得据此把工具或 Session 标记为成功；`rejected` 进入 `blocked`，`expired` / `cancelled` 进入对应状态。
- 审批获批时可携带 Core 的结构化 `execution`：`submitted` 只表示 shell 输入已写入，`succeeded` 只表示文件或端口转发执行完成，`failed` 进入 Session `failed`，`running` / `unknown` 保持 `waiting_execution`。
- `submitted` 或 `succeeded` 的执行结果仍需经过可信工具结果消费后才能继续剩余步骤；不能仅因审批接口返回 `200` 就推进 Session。
- 每个 Step 声明 `required_completion`：默认 `accepted`，允许在可信 `submitted` 或 `succeeded` 后继续；`completed` 只接受可信 `succeeded`，`submitted` 继续等待。
- 审批协调器收到可继续的结构化执行结果后，会启动一次异步自动恢复；同一 Session 恢复只领取一次，结果 `unknown` 时保持等待，不自动重试。
- `waiting_execution` 可由受限控制入口触发一次只读 Execution 刷新。刷新不会重新提交当前工具调用；若已有可信 `submitted` / `succeeded` 证据，可重新触发尚未完成的 Session 恢复。刷新失败时保留原状态，不把读取失败写成工具失败或未执行。
- 审批 `unknown` 保持 `waiting_approval`，并单独记录 `current_approval_status=unknown`。
- 已确认的审批终态不能回退；后续异步观测与其冲突时拒绝更新并保留原状态。
- 只有与当前 call ID 一致、且审批状态已为 `approved` 的可信工具结果可以回填 `waiting_execution`；结果不匹配或仍为中间态时拒绝更新。
- 工具 call ID 在 Runtime 内必须保持唯一，避免审批协调器把一个 call 关联到多个请求。审批登记失败时 Session 进入 `unknown`，保留可信审批 ID 供诊断，但不得显示为可管理的 `waiting_approval`。
- 可信 `succeeded` 才允许继续尚未执行的步骤；`failed` / `blocked` / `cancelled` / `expired` 停止 Session；`unknown` 保持 `waiting_execution` 并记录 `current_tool_status=unknown`，不自动重试。
- 已成功继续的 Session 不重新执行历史步骤；同一 call ID 的重复成功结果幂等，成功后再到矛盾结果不能覆盖终态。
- `unknown` 不自动重试，也不自动变成失败或成功。
- Step 执行前发现上下文已取消时进入 `cancelled`；已派发调用取消后的工具结果仍按工具状态机处理。
- `finished_at` 只出现在 `completed`、`blocked`、`failed`、`cancelled`、`expired` 和 `unknown`；`waiting_approval` / `waiting_execution` 表示现场仍可继续，不设置该字段。
- 模型 Loop 每次 completion 前保留 system/user 和最新可容纳的完整 assistant/tool 轮次；被裁剪的轮次数记录在 `agent.model.requested`，不使用摘要补造历史。
- 模型工具目录只能来自显式绑定。未知工具、非法 JSON 参数、重复工具名、工具调用预算或迭代预算超限会失败，不执行工具。
- 模型返回的工具调用仍经过 `internal/toolpipeline`；工具结果只有在可信终态后才作为 `tool` 消息回填模型上下文。
- 工具结果正文是否回填由精确 allowlist 规则的 `model_result` 决定；同一 Server/Tool 的全部规则均为 `content` 才回填受限正文，否则只回填状态和字节数。
- 模型无工具调用的文本响应完成 Session；模型返回文本与工具调用并存时，只有所有工具结果回填后才请求下一轮。
- 模型消息和最终回复保存在 Session 内存中；配置 `--session-store-file` 后同步写入有界本地文件，并可通过 Bearer 保护的 transcript 端点读取。
- 配置 `--session-store-file` 时，模型 Loop 在请求模型、工具目录就绪、工具派发前、工具结果观察、审批等待和执行等待边界持久化 checkpoint。checkpoint 保存迭代/预算、工具身份及描述/Schema hash、待执行 call 的 ID/名称/参数 hash 和字节数，不复制工具参数正文。
- `model_requesting` 与 `tools_ready` 表示尚未发生新的工具副作用，可用于后续人工恢复决策；`tool_dispatching`、`tool_observed`、`waiting_approval` 和 `waiting_execution` 一律阻塞自动继续。
- Runtime 重启仍把所有非终态 Session 收敛为 `unknown`。快照中的 `model_loop_recovery` 只暴露阶段、安全边界、阻塞原因、计数和 checkpoint revision，不暴露模型正文、工具参数或结果正文。
- 只有 `unknown + recovered + recovery_reason=runtime_restart + safe_boundary=true` 的模型 Session 可由用户显式恢复；同一 Session 只允许一个恢复请求领取，第二次请求返回冲突。
- 恢复必须使用当前 Runtime 的模型 Adapter、Invoker 和工具目录，不能从 Session 文件恢复执行权限。当前工具名称、Server/工具身份、完成级别、结果暴露策略、Schema hash 和描述 hash 必须与 checkpoint 完全一致。
- `tools_ready` 恢复还要从持久化 transcript 校验 assistant tool call、待执行参数 hash/字节数、已经完成的工具结果数量与 `next_tool`；任何不一致都保持 `unknown`，不调用模型或工具。
- `model_requesting` 可能重新发起一次模型请求；`tools_ready` 会执行 checkpoint 中尚未派发的工具调用。两者仍经过现有策略、审批、结果未知和事件流水线。
- `reasoning_content` 不进入持久化；需要该字段的 thinking provider 在重启后续轮可能拒绝恢复请求。此失败按新的模型执行失败处理，不能伪造成原请求仍在运行。

## 3. 控制事件

- `agent.session.started`
- `agent.tool.started`
- `agent.tool.finished`
- `agent.model.requested`
- `agent.model.responded`
- `agent.model.failed`
- `agent.session.waiting_approval`
- `agent.session.waiting_execution`
- `agent.session.cancel_requested`
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

Session 事件以 Agent Session ID 为 `subject_id`，当前工具调用 ID 为 `correlation_id`。普通事件只记录状态、revision、Core Session 关联 ID、工具状态、模型迭代/消息/工具计数和字节数，不记录模型正文、工具参数或结果正文。

## 4. 当前限制

- 未配置 `--session-store-file` 时，Session、模型上下文和回复只在内存中，Runtime 重启后丢失。
- 配置 `--session-store-file` 后，终态 Session、模型消息、最终回复和活动 Loop checkpoint 可恢复；Session Store v2 兼容读取 v1。Session 文件本身不含事件日志。另配 `--event-log-file` 可恢复有界事件窗口与 sequence，`--approval-store-file` 可恢复审批历史；审批队列、MCP 连接和活动工具执行仍不回放。
- Runtime 重启时仍处于 `running`、`cancelling`、`waiting_approval` 或 `waiting_execution` 的 Session 收敛为 `unknown`，设置 `recovered=true` 和 `recovery_reason=runtime_restart`；已有取消请求时同时设置 `cancellation.remote_outcome_unknown=true`，不推断远端正处于成功、失败或已取消状态。
- checkpoint 诊断本身不等于恢复执行。安全边界只表示 checkpoint 尚未产生新的工具副作用；恢复仍必须由用户显式触发并经过工具目录/transcript 一致性校验。
- 模型 Adapter 首个实现为 OpenAI-compatible 非流式 completion；已通过 DashScope 真实模型与 Core 只读工具纵向闭环，其他第三方服务、真实流式输出和 provider 级重试尚未验证。
- 已持久化原始模型消息，但没有摘要或对话继续执行；事件轨迹需要独立配置 Event Journal Store。
- 已验证 Runtime 重启后恢复 completed Session、3 条模型消息和最终回复；当次验证未配置 Event Journal Store，因此恢复后的 Session 没有旧 Runtime 事件日志。
- 已通过带 Session Store 的临时 Runtime 和真实 Wails 窗口验证：持久化的 `waiting_approval` checkpoint 重启后 Session 为 `unknown`，Agent 面板显示“等待审批 / 审批结果未确认”、iteration、tool count、pending count 和 call ID，且没有继续执行按钮。
- 安全 checkpoint 的人工恢复已由 Session 单元测试、Control API、Control Client、Desktop 固定桥和前端交互测试覆盖；真实 Wails 窗口验证记录见 Desktop Shell 契约。
- `POST /v1/agent/sessions` 与 `POST /v1/agent/sessions/{id}/run` 必须携带 Bearer token；运行还必须配置精确 allowlist 的工具策略文件。
- CLI 通过 `termcp-harness agent run` 创建并运行显式步骤，通过 `agent loop` 创建并运行模型循环；运行响应不直接回传工具正文，模型循环只返回最终模型输出、Session 快照和调用摘要。
- CLI 通过 `termcp-harness agent cancel --id ... --revision ... --token-file ...` 请求停止。停止请求不关闭 Core Session、Shell 或远端审批。
- Harness 已接入 Termcp 结构化 `execution` 的自动消费、人工只读核对与步骤恢复；字段缺失时保持 `waiting_execution`，不根据输出文本或时间补造结果。
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

2026-11-01 完成停止任务真实 HTTP 闭环：

- 临时 Core Session 上通过精确 allowlist 派发长时 `term run`，Session 进入 `running` 且记录当前 call。
- 受保护 `/cancel` 接受 revision 后返回 `202`、`state=cancelling`、`phase=tool_dispatch`。
- 工具调用随后收敛为 `unknown`；最终 Session 为 `unknown`、`current_tool_status=unknown`、`cancellation.remote_outcome_unknown=true`，未自动重试。
- 另一个尚未运行的 Agent Session 以 `before_dispatch` 停止，直接进入 `cancelled`，无远端执行风险。
- 测试结束后关闭专用 Core Session、停止临时 Harness，并删除一次性 token、策略、日志和临时目录。
