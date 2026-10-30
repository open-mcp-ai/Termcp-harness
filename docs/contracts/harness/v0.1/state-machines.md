# Harness 工具调用与审批状态机 v1（草案）

> 状态：代码与本地测试已覆盖；工具调用已通过 `internal/toolpipeline` 接入 MCP，远端 `review_pending` 已映射为 `approval_pending`，审批决定和恢复尚未接入 Runtime；未承诺公开兼容性。
>
> 日期：2026-10-28
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
- 待处理审批的本地过期与已提交审批的远端过期必须区分。
- 最终确认必须与已提交的决定一致；矛盾结果进入错误路径，不能静默改写。
- 审批终态不能再转换。

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
- opt-in 真实 Core 测试已覆盖 review 挂起不执行、提交侧 MCP Host 断开后请求仍可批准、拒绝不执行、批准后执行、并发批准/拒绝首个决定生效，以及 session 关闭后批准被拒绝且不产生文件。
- 已观测到 Core 0.2.8 的审批契约偏差：没有 waiter 的过期请求仍可被批准并执行；session 关闭后 pending 请求从 `/api/approvals` 消失，`cancelled` 状态不可查询。安全结果仍为拒绝执行，但状态可见性不符合公开文档。
- 调用上下文取消只形成本地 `unknown`；真实 Core 的远端命令可能继续执行并完成副作用。取消不得被解释为远端已停止或未执行。
- 20,000 行输出与 12 路并发只读调用已通过真实 Core 测试；并发事件的全局 sequence 可以交错，但同一 call ID 的内部顺序仍为 `proposed → ready → dispatching → terminal`。

## 6. 未验证项

- 多客户端高并发审批原子性和压力下的首个决定保证；当前只有一次并发批准/拒绝实测。
- MCP Server 是否声明副作用、幂等、去重或操作查询能力。
- Core/MCP 事件是否提供可信 operation/correlation ID。
- 审批请求在 Core 重启后的恢复行为，以及过期/取消状态修复后的可见性。
- 重试预算和退避与 Agent/Session 总预算的关系。

在上述事项由真实契约与集成测试确认前，本状态机只证明 Harness 本地不伪造结论，不证明远端执行行为。
