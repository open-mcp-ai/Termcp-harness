# Core v0.2.8 已知上游问题

> 状态：本地修复已验证（2026-10-30）
>
> 适用：Core v0.2.8、Harness 通过公开 MCP/HTTP 契约的集成验证
>
> 证据：`internal/toolpipeline/approval_integration_test.go`

## 1. 用途

本文是 Core v0.2.8 上游缺陷和契约偏差的唯一登记入口，用于避免后续开发、测试和汇报遗漏。

未关闭问题必须遵守：

- Core 相关问题开始前先检查本文。
- 未关闭问题不得被写成“已通过”或“已兼容”。
- 默认测试可以跳过昂贵或会产生副作用的 opt-in 测试，但严格契约测试必须保留。
- Harness 侧只能 fail-closed、降级和显式标记未知，不得伪造 Core 已满足契约。
- 不修改 `D:\data\code\github\Termcp-web` 来让 Harness 测试变绿；修复由 Core 上游负责。
- 汇报未关闭问题时，在回复中先输出醒目的红色警告块；当前客户端不支持真实红字时使用 `> [!CAUTION]` 和 `🟥`。

## 2. 来源与边界

- Core 版本：`v0.2.8`
- 已安装 Core commit：`0595cb5`
- 验证日期：2026-10-29
- 验证方式：运行中的本机 Core、Kali POSIX session、MCP Streamable HTTP、公开 HTTP API
- 本地修复分支：`fix/approval-lifecycle`
- 本地修复提交：`f6cefec`
- 本地验证（2026-10-30）：`go test -race ./internal/approval ./internal/session ./internal/webui -count=1` 通过
- 上游 PR #226 仅作为发布同步记录，不作为本地验收阻塞条件
- Harness E2E（2026-10-30）：在独立端口启动本地修复构建，剩余 7 个 opt-in 测试全部通过
- Harness 未 import Core 内部包，未修改 Core 仓库，未直接读写 Core 私有数据库或配置。

## 3. 未关闭问题

### CORE-0.2.8-001：审批过期未 fail-closed

- 状态：**FIXED_LOCAL（Harness E2E 已验证）**
- 严重度：**高，阻塞完整审批状态闭环**
- 首次确认：2026-10-29
- 本地修复：`f6cefec`，队列在 List/Get/Approve/Reject/Cancel 等路径先结算过期状态
- 公开契约：审批超时后必须进入 `expired`，不能批准，更不能执行。
- 实际行为：设置 `timeout_seconds=1` 并等待 2 秒后，请求仍为 `pending`；批准返回 `200`，实际写入文件。
- 证据测试：`TestTermcpCoreApprovalExpiryContract`
- 触发条件：MCP review 请求没有 waiter 消费 `Queue.Wait`，列举和批准路径未结算过期。
- Harness 影响：不能用本地列表判断过期请求已经安全；不能把 `pending` 当成仍可批准的权威状态。
- Harness 应对：
  - 保留严格失败测试。
  - 不根据本地时间自动把远端 `pending` 改成 `expired`。
  - 审批功能若接入 Runtime，必须把该能力标记为 Core 降级。
- 责任方：Core 上游。
- 关闭条件：
  - 超时后 `GET /api/approvals` 返回 `expired` 或不再把请求列为可决定。
  - `/approve` 返回冲突且不执行写入。
  - `TestTermcpCoreApprovalExpiryContract` 通过。

### CORE-0.2.8-002：session 关闭后的 cancelled 状态不可查询

- 状态：**FIXED_LOCAL（Harness E2E 已验证）**
- 严重度：**中高，阻塞完整审批历史和状态展示**
- 首次确认：2026-10-29
- 本地修复：`f6cefec`，关闭 session 时原子关闭审批队列并保留只读历史
- 公开契约：session 死亡会取消 pending 审批，状态可在 `pending|approved|rejected|expired|cancelled` 中观察。
- 实际行为：session 关闭后批准返回 `404` 或 `409`，文件不会创建；但 pending 请求从 `/api/approvals` 消失，无法观察到 `cancelled`。
- 证据测试：`TestTermcpCoreApprovalSessionDeathStateContract`
- 原因边界：Core 在关闭 session 时关闭 approval queue 并将 queue 从 session 摘除，查询入口无法继续列出该请求。
- Harness 影响：安全结果是 fail-closed，但无法显示取消历史，无法证明 pending 请求最终状态。
- Harness 应对：
  - 安全上不得再批准已关闭 session 的请求。
  - 状态上必须显示“不可确认/历史缺失”，不能补造 `cancelled`。
  - 保留严格失败测试。
- 责任方：Core 上游。
- 关闭条件：
  - session 关闭后原请求仍可按 ID 查询为 `cancelled`，或提供等价的可信审计记录。
  - 对已取消请求批准返回冲突且不执行。
  - `TestTermcpCoreApprovalSessionDeathStateContract` 通过。

## 4. 新问题登记模板

新增 Core 上游问题时，至少记录：

```text
ID：
状态：
严重度：
首次确认：
公开契约：
实际行为：
最小复现/测试：
影响范围：
Harness 降级策略：
责任方：
关闭条件：
```

## 5. 严格契约测试

```powershell
$env:TERMCP_CORE_MCP_URL = 'http://127.0.0.1:18765/stream'
$env:TERMCP_CORE_APPROVAL_EXPIRY_TEST = '1'
$env:TERMCP_CORE_APPROVAL_SESSION_DEATH_TEST = '1'
$env:TERMCP_CORE_TEST_EFFECT_SESSION_ID = '<dedicated-effect-session-id>'
$env:TERMCP_CORE_TEST_OBSERVER_SESSION_ID = '<dedicated-observer-session-id>'
$env:TERMCP_CORE_TEST_TEMP_DIR = '/tmp'

go test ./internal/toolpipeline -run '^TestTermcpCoreApprovalExpiryContract$' -count=1 -v
go test ./internal/toolpipeline -run '^TestTermcpCoreApprovalSessionDeathStateContract$' -count=1 -v
```

当前预期：

- 对已安装 Core v0.2.8（`0595cb5`），以上两个测试仍失败；它们不是普通回归失败，而是已发布基线的可执行证据。
- 对本地修复提交 `f6cefec`，Core 的 approval/session/webui race 测试已通过。
- Harness 的两项严格契约测试已针对本地修复构建独立复跑并通过。
- 上游 PR 状态不阻塞本地闭环；本地修复构建已经满足上述两项 Harness 严格契约。

## 6. 本地 E2E 验收记录（2026-10-30）

- 本地 Core：`fix/approval-lifecycle` / `f6cefec`，构建版本 `dev`
- 运行方式：独立端口 `127.0.0.1:18766`、临时数据目录、一次性 Kali `kali-gc` effect/observer sessions
- Core 测试：`go test -race ./internal/approval ./internal/session ./internal/webui -count=1` 通过
- Harness 标准回归：`go test -race ./... -count=1`、`go vet ./...`、`go build ./...`、`npm run check` 通过
- Harness 真实 Core opt-in 测试全部通过：
  - `TestTermcpCoreApprovalReviewIntegration`
  - `TestTermcpCoreApprovalExpiryContract`
  - `TestTermcpCoreApprovalSessionDeathStateContract`
  - `TestTermcpCoreCancellationIntegration`
  - `TestTermcpCoreOutputFloodIntegration`
  - `TestTermcpCorePOSIXWriteEffectsIntegration`
  - `TestTermcpCorePOSIXTransportDisconnectIntegration`
- 清理结果：临时 Core 已停止，running session 为 0，临时数据目录已删除
- 边界：以上证明本地修复构建与 Harness 闭环；不把该结论自动扩展到尚未包含修复的上游发布版本

## 7. 非本清单事项

- Harness 自身状态机、策略、事件日志和 UI 缺陷不登记为 Core 上游问题。
- Core 尚未实现的能力应登记为能力缺口，不应写成“测试通过”。
- Core 进程识别、PID、启动时间和所有权属于 Harness 的本地实例识别能力，不等同于审批契约问题。
