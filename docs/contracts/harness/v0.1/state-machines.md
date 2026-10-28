# Harness 工具调用与审批状态机 v1（草案）

> 状态：代码与本地测试已覆盖；尚未接入 MCP/Core，也未承诺公开兼容性。
>
> 日期：2026-10-28
>
> 实现：`internal/toolcall`、`internal/approval`

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

## 5. 未验证项

- Core 是否提供审批原子性、幂等和终态查询保证。
- MCP Server 是否声明副作用、幂等、去重或操作查询能力。
- Core/MCP 事件是否提供可信 operation/correlation ID。
- 服务端取消、过期和断连后的真实行为。
- 重试预算和退避与 Agent/Session 总预算的关系。

在上述事项由真实契约与集成测试确认前，本状态机只证明 Harness 本地不伪造结论，不证明远端执行行为。
