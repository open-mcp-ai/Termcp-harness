# ADR-021：Agent 任务停止与人工接管

- 状态：Accepted / 本地闭环已实现
- 日期：2026-11-01
- 责任范围：Agent Session 停止状态、工具派发边界、Control API、Desktop 操作和恢复语义
- 关联：`ADR-017`、`ADR-018`、`docs/contracts/harness/v0.1/agent-session.md`、`docs/contracts/harness/v0.1/control-api.md`

## 1. 背景

Desktop 已能观察 Agent Session 和 Terminal，但关闭面板只关闭视图，不能停止 Agent；正在运行的模型请求和工具派发也没有显式停止入口。简单把 Session 标记为 `cancelled` 会混淆三种事实：

- Harness 已停止继续推进。
- 工具可能已经派发并产生副作用。
- Core 审批或执行事实仍可能独立存在。

## 2. 决策

新增受保护的 `POST /v1/agent/sessions/{id}/cancel`，使用 `expected_revision` 做 CAS，并采用以下状态：

- `new`、`waiting_approval`、`waiting_execution`：直接进入 `cancelled`。
- `running`：进入非终态 `cancelling`，等待当前模型请求或工具派发边界收敛。
- 模型请求中或工具派发前：最终进入 `cancelled`，不产生新工具调用。
- 工具已派发：只接受实际工具结果。可信成功记录副作用后进入 `cancelled`；结果未知进入 `unknown`。
- `waiting_approval` 取消设置 `approval_pending=true`；这不等于拒绝或取消远端审批。
- `waiting_execution` 取消设置 `remote_outcome_unknown=true`；这不等于终止 Core 执行。
- `cancelling` / `cancelled` 的重复请求幂等；其他终态和 revision 冲突返回 `409`。

工具派发必须通过原子 gate：只有 Session 仍为 `running` 才能进入 Invoker。取消与派发之间不存在“已经收到停止但仍开始工具调用”的窗口。

## 3. Runtime

- Session 保存当前运行 context 的 cancel function。
- `Run`、`RunModel`、`ResumeModel` 和审批后的自动/人工继续都注册活动 context。
- 取消请求先记录 `agent.session.cancel_requested`，再取消当前 context。
- Runtime 重启后 `cancelling` 按非终态恢复规则收敛为 `recovered unknown`，并保留取消阶段和远端风险标记。
- 停止 Agent Session 不关闭 Core Session、Shell 或 Terminal Bridge。

## 4. Desktop

- 模型任务启动和人工恢复改为异步：创建/领取成功后立即返回 Session，Desktop 可选择该 Session 并显示停止入口。
- Agent 面板提供“停止任务”，要求确认，并在服务端回执后显示安全取消、审批未解决或远端结果未知。
- 关闭面板仍只关闭视图；背景任务计数包含 `running`、`cancelling` 和等待状态。
- 人工接管通过绑定同一 Agent/Core Session 的 Terminal 面板继续；停止任务不会自动关闭该终端。

## 5. 验证

- Session：派发前、模型请求中、工具派发中、等待审批、等待执行、stale revision 和重复取消。
- Control API：Bearer 认证、JSON、revision、`200` / `202` / `409` 和幂等。
- Control Client、CLI 和 Desktop 固定桥。
- Desktop：异步启动不阻塞停止入口；取消只对可控非终态显示；前端 `tsc` 与 Node 回归。
- 真实 HTTP E2E：长时 `term run` 派发中停止返回 `cancelling/tool_dispatch`，最终收敛为 `unknown` 且 `remote_outcome_unknown=true`；`new` 状态停止直接 `cancelled/before_dispatch`。

## 6. 非目标

- 不提供 Core 远端进程终止保证。
- 不把停止任务解释为拒绝审批、关闭 Session 或取消已派发工具。
- 不在远端结果未知时自动重试。
