# Harness 工具调用与审批状态机 v1（草案）

> 状态：代码与本地测试已覆盖；工具调用已通过 `internal/toolpipeline` 接入 MCP，远端 `review_pending` 已映射为 `approval_pending`，并支持 trusted/unknown/conflict 审批关联；安全审批决定、Core 结构化执行结果解析、Step 完成级别、自动消费、后续步骤恢复和可选审批历史持久化已接入 Runtime；未承诺公开兼容性。
>
> 日期：2026-10-30
>
> 实现：`internal/toolcall`、`internal/toolpipeline`、`internal/approval`
>
> Core v0.2.8 未关闭审批问题见 `../../core/v0.2.8/known-issues.md`。

## 1. 边界

工具调用、审批、MCP 连接和 Core 会话是四个独立状态域。本文件只定义前两者，不把本地状态当作 Core 或 MCP 服务端的执行保证。

两个状态机都使用 revision compare-and-swap。调用方只能基于已观察到的 revision 提交下一步；旧异步结果不能覆盖新状态。

## 2. 工具调用主路径

```text
proposed
  ├─ ready ─ dispatched ─ accepted ─ running ─ succeeded
  ├─ approval_pending ─ ready ─ ...
  ├─ blocked
  ├─ cancelled
  └─ expired

dispatched / accepted / running
  ├─ succeeded
  ├─ failed
  ├─ cancelled（可信远端确认）
  ├─ expired（可信远端确认）
  └─ unknown（超时、断连或传输结果不明确）

dispatched
  └─ approval_pending（远端 review 已接受，尚未执行）

unknown
  └─ succeeded / failed / cancelled / expired（迟到的可信证据）
```

强制规则：

- `proposed` 不能直接进入 `dispatched`；必须先经过验证/策略并进入 `ready`，或进入审批/阻断状态。
- `unknown` 不是终态，但也不能仅凭本地时间自动变成 `failed` 或 `succeeded`。
- 传输超时、断连和响应丢失只能形成 `unknown`。
- 本地截止时间在派发前可形成 `expired`；派发后必须形成 `unknown`，除非远端明确确认过期。
- 成功后不能再次转成失败、取消或未知。
- 旧 revision 的转换返回 stale 错误且不改变状态。

## 3. 工具副作用与自动重试

工具必须声明以下之一：

- `read_only`
- `idempotent_write`
- `side_effect`
- `unspecified`

自动重试默认关闭。`MaxAttempts` 为零时表示没有配置自动重试预算，不是无限重试。

只有在 `failed` 或 `unknown` 且预算未耗尽时才评估自动重试：

| 副作用类别 | 自动重试条件 |
|---|---|
| `read_only` | 预算允许即可 |
| `idempotent_write` | 工具明确声明幂等，且预算允许 |
| `side_effect` | 幂等键、去重语义，或已确认前一次未执行 |
| `unspecified` | 禁止 |

“可查询”不等于“可安全重试”。查询本身必须先确认前一次没有产生副作用，或者下一次调用能使用同一幂等键。

## 4. 审批状态

```text
pending
  ├─ submitting(approve/reject) ─ approved / rejected
  ├─ submitting ─ unknown ─ approved / rejected / expired / cancelled
  ├─ expired（尚未提交）
  └─ cancelled（尚未提交）
```

强制规则：

- 点击或提交“拒绝”后，状态只能是 `submitting`，决定为 `reject`。
- 只有收到可信服务端确认后，才能显示 `rejected`。
- 审批请求提交后超时或断连，状态是 `unknown`，不是失败，也不是已拒绝。
- 决定响应不明确时，Runtime 可在同一上下文内执行一次只读 Core 审批查询来恢复结果；不得重新提交批准或拒绝请求。
- `expires_at` 只作为远端提供的截止时间元数据。v1 不以本地时钟把远端 `pending` 自动改写为 `expired`；`expired` 终态必须来自可信远端观察。
- 最终确认必须与已提交的决定一致；矛盾结果进入错误路径，不能静默改写。
- 审批终态不能再转换。

## 4.2 执行结果

审批确认和执行业务结果必须分开建模。Core 在同意执行后返回独立 `execution`：

- `running`：执行已开始，尚无终态结果。
- `submitted`：shell 输入字节已写入；不表示命令完成，也不表示退出码成功。
- `succeeded`：文件操作或端口转发执行完成。
- `failed`：执行器报告失败；审批仍保持 `approved`，且不保证部分副作用不存在。
- `unknown`：丢失响应或无法信任执行观察，不得改写为成功或失败。

Harness 只把显式 `execution` 与该审批关联；不能从终端文本、时间或相邻事件推断。`submitted` / `succeeded` 仍需进入 Session 的可信结果消费路径后才能继续后续步骤。

自动消费由审批协调器触发，只领取一次。Step 的 `required_completion=accepted` 可由 `submitted` 或 `succeeded` 满足；`completed` 只由 `succeeded` 满足。`unknown` 保持等待，恢复失败写诊断事件且不记录错误正文。审批确认后，协调器可用有界的只读 `Lookup` 收敛迟到执行证据；不得重放批准/拒绝请求。`running`、`submitted`、`unknown` 不等于成功，执行状态必须单调推进。

## 4.1 审批关联

`approval_pending` 只说明远端已接受 review，不说明 Harness 已知道具体审批请求。关联状态必须单独表示：

- `trusted`：`review_pending` 结果显式提供合法 `approval_id`；可附带 `session_id` 和 `expires_at`。
- `unknown`：结果确认 pending，但没有可信 `approval_id`；不得根据 session、文本、时间或相邻事件推断。
- `conflict`：多个结果来源给出互相矛盾的 ID、session 或过期时间，或字段类型/格式非法；不得自行选择其中一个。

强制规则：

- `approval_id`、`session_id` 只接受非空、无空白和控制字符、长度不超过 128 的值。
- `expires_at` 接受 Unix milliseconds 或 RFC3339；非法值形成 `conflict`。
- 结构化 `structuredContent` 和 JSON 文本可提供关联字段；TERM 文本兼容路径若没有可信状态头，只能是 `unknown`。
- Core `term` 外层 `202` 文本可提供 `Approval-ID`、`Session-ID`、`Expires-At` 状态头；字段合法且不冲突时形成 `trusted` 关联。
- 关联状态不会改变 `approval_pending` 的安全含义：无论是否存在 ID，远端执行都尚未被 Harness 确认为成功。
- 未关联的 pending 请求不能被 Harness 批准或拒绝；必须先建立可信关联或等待人工在 Core 侧处理。


## 5. 当前 MCP 接入范围

- `internal/toolpipeline` 只允许显式 allowlist 规则：必须同时匹配 Server ID、工具名和精确 JSON 参数。
- allowlist 必须声明 `read_only`、`idempotent_write` 或 `side_effect`；没有规则时不会派发，不能根据工具名称或 Schema 猜测副作用。
- 参数必须是 JSON object，默认上限 1 MiB；结果默认上限 4 MiB。
- 当前同步路径记录 `proposed`、`ready`、`dispatching`、`succeeded`、`failed` 或 `unknown`。没有远端接受或进度证据时，不产生 `accepted` / `running`。
- MCP 调度错误、超时、断连、缺失结果或无法接受的结果统一收敛为 `unknown`；MCP 返回 `isError=true` 的可见工具结果收敛为 `failed`。
- 自动重试没有接入执行器；一次 `unknown` 不会触发第二次派发。
- 工具参数和结果正文不写入普通事件日志；事件只记录调用 ID、来源、工具名、副作用类别、状态、revision、策略原因和结果字节数等元数据。
- opt-in 真实 Core 测试已覆盖 `idempotent_write` 的重复覆盖最终状态，以及 `side_effect` 成功、上下文超时和调用侧 MCP Host 断开后的 `unknown` 与“无自动重试”。断开测试不等同于远程网络分区。该测试不证明 Core 提供 idempotency key、operation ID 或可信的未执行查询。
- Core 的 `TERM/1 202 Accepted` + `Submitted for review:` 或结构化 `review_pending=true, approved=false` 会收敛为 `approval_pending`，不会记为 `succeeded`。该映射只证明本地不伪造执行成功，不代表 Runtime 已能管理审批决定。
- 当前 Termcp 0.2.8 开发基线在 review 结果中提供 `approval_id`，并可附带 `session_id` 和 `expires_at`。Harness 只使用显式字段；字段缺失或冲突时保持关联未知，不使用版本号推断能力。
- `internal/approval` 的协调器只登记 `trusted` 关联，按 revision 串行提交决定，并在远端超时、断连或矛盾结果时收敛为 `unknown`；收到可信远端终态后才进入 `approved`、`rejected`、`expired` 或 `cancelled`。
- 配置 `--approval-store-file` 后，协调器原子保存可信关联、决定 revision 和最后已知 Execution。重启时 `pending` / `submitting` fail-closed 收敛为 `unknown`，不恢复为可决定请求。
- 审批历史恢复不调用 Core、不自动 Reconciliation、不通知 Session；已批准记录只能通过显式只读 Execution 刷新收敛，不能重放批准或工具调用。
- `unknown` 审批可通过受保护的 `POST /v1/approvals/{id}/reconcile` 执行一次只读 Lookup；Core 仍为 `pending` 时保持 `unknown`，只有可信远端终态才推进本地状态。
- 审批历史持久化失败不得触发远端决定；远端决定已经确认时保留该结果并显式报告本地历史未持久化。
- `internal/coreapproval` 只通过 Core 公开 HTTP 契约调用 `/api/approvals/{id}/approve|reject`，解析决定响应中的结构化 `execution`，不读取 Core 内部数据；错误响应不会把凭据或审批正文写入普通日志。
- Control API 暴露只读 `/v1/approvals` 快照；受保护的 `POST /v1/approvals/{id}/decision` 仅在显式配置 Bearer token 文件时启用，要求 JSON、revision CAS，并拒绝带 `Origin` 的请求。
- 受保护的 `POST /v1/approvals/{id}/execution/refresh` 只读取已批准请求的 Core Execution，不重提决定或工具调用；成功读取会通知 Session，使 `waiting_execution` 能从已有可信证据恢复。读取失败保留原审批与工具状态。
- 决定提交返回 `unknown` 时使用 HTTP `202`，不得把它显示为已批准、已拒绝或明确失败。
- opt-in 真实 Core 测试已覆盖 review 挂起不执行、提交侧 MCP Host 断开后请求仍可批准、拒绝不执行、批准后执行、并发批准/拒绝首个决定生效，以及 session 关闭后批准被拒绝且不产生文件。
- 已观测到 Core 0.2.8 的审批契约偏差：没有 waiter 的过期请求仍可被批准并执行；session 关闭后 pending 请求从 `/api/approvals` 消失，`cancelled` 状态不可查询。安全结果仍为拒绝执行，但状态可见性不符合公开文档。
- 调用上下文取消只形成本地 `unknown`；真实 Core 的远端命令可能继续执行并完成副作用。取消不得被解释为远端已停止或未执行。
- 20,000 行输出与 12 路并发只读调用已通过真实 Core 测试；并发事件的全局 sequence 可以交错，但同一 call ID 的内部顺序仍为 `proposed → ready → dispatching → terminal`。
- 模型 Agent Loop 只产生工具调用提议；每个提议仍进入同一验证、策略、派发、观察和收敛链。模型不能直接调用 MCP Host，工具结果是否回填模型上下文由精确规则的 `model_result` 单独控制。

## 6. 未验证项

- 多客户端高并发审批原子性和压力下的首个决定保证；当前只有一次并发批准/拒绝实测。
- MCP Server 是否声明副作用、幂等、去重或操作查询能力。
- Termcp 后续迭代是否继续稳定提供可信 operation/correlation ID 和 `review_pending` 关联字段；变更时必须同步契约并重跑真实集成测试。
- 审批请求在 Core 重启后的恢复行为，以及过期/取消状态修复后的可见性。
- 重试预算和退避与 Agent/Session 总预算的关系。

在上述事项由真实契约与集成测试确认前，本状态机只证明 Harness 本地不伪造结论，不证明远端执行行为。
