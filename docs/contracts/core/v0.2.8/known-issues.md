# Core v0.2.8 已知上游问题

> 状态：当前 Termcp 0.2.8 开发基线已验证（2026-10-30）
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
- 历史确认 Core commit：`0595cb5`
- 验证日期：2026-10-29
- 验证方式：运行中的本机 Core、Kali POSIX session、MCP Streamable HTTP、公开 HTTP API
- 当前开发分支：`fix/approval-lifecycle`
- 当前开发提交：`f6cefec`
- 本地验证（2026-10-30）：`go test -race ./internal/approval ./internal/session ./internal/webui -count=1` 通过
- 上游 PR #226 已采纳；当前 Termcp 开发基线包含审批关联契约
- Harness E2E（2026-10-30）：在独立端口启动当前 Termcp 开发基线，剩余 7 个 opt-in 测试全部通过
- Harness 未 import Core 内部包，未修改 Core 仓库，未直接读写 Core 私有数据库或配置。

## 3. 未关闭问题

### CORE-0.2.8-001：审批过期未 fail-closed

- 状态：**FIXED_LOCAL（源码与本地重建实例均已验证；正式发布待同步）**
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

- 状态：**FIXED_LOCAL（源码与本地重建实例均已验证；正式发布待同步）**
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

### CORE-0.2.8-003：review_pending 缺少 approval_id

- 状态：**UPSTREAM_ACCEPTED（PR #226 已采纳；源码关联测试、重建 Core 与真实 Desktop 闭环均已验证，正式发布待同步）**
- 严重度：**高，阻塞工具调用与审批请求的可信关联**
- 首次确认：2026-10-30
- 上游 issue：<https://github.com/open-mcp-ai/Termcp/issues/236>
- 本地补丁分支：`fix/approval-correlation`（基于 `fix/approval-lifecycle`）
- 公开契约缺口：`review_pending` 只返回 `ok`、`approved`、`review_pending`、`message`，没有稳定 `approval_id`。
- 实际影响：Harness 能看到 `/api/approvals` 中存在 pending 请求，但无法证明它对应哪一次 MCP 工具调用；禁止用文本、时间或相邻事件推断。
- Harness 应对：只接受显式合法 `approval_id`；缺失或冲突时保持 `approval_correlation=unknown`，不登记到审批协调器，也不开放决定入口。控制 API 仅按实际观察返回 `unverified` 或 `trusted`，不按版本号分叉运行路径。
- 本地修复：
  - 已排队的 shell command、file 和 forward operation 返回 `approval_id`、`session_id` 和 `expires_at`。
  - 仅 staged、尚未被 ending key 提交的文本不返回 `approval_id`。
  - 保留禁止 poll/resubmit/reword/retry 的规则。
- 本地验证：
  - `go test -race ./internal/mcp -run '^(TestHandleSendInputHoldsUnderReview|TestHandlePressKeyCommitsStagedText|TestHeldOperationReturnsApprovalCorrelation|TestFileWriteIsHeldAndExecutedOnApproval|TestGatedToolsAllHoldUnderReview|TestInstructionsCarryReviewPendingRule|TestReviewPendingRepliesStateTheNextStep)$' -count=1`
  - `go test ./... -run '^$' -count=1`
  - `go vet ./internal/mcp`
  - `go build ./...`
- 未完成验证：当前 Windows 环境完整 `go test ./internal/mcp -short` 因缺少 `sh`/`cat`、符号链接权限及测试超时而失败；这些失败不涉及本次审批关联测试。
- 责任方：Core 上游。
- 关闭条件：
  - 上述关联字段进入公开 MCP 契约。
  - 真实 Core 的 `review_pending` 与 approval queue ID 一致。
  - Harness 真实集成测试可从工具结果直接取得 trusted `approval_id`。

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

- 当前 Termcp 0.2.8 开发基线：Core 的 approval/session/webui race 测试及 Harness 两项严格契约测试均通过。
- 历史提交 `0595cb5` 的失败结果保留为问题来源证据，不作为当前开发目标。
- 每次 Termcp 迭代后都应在同一 `0.2.8` 版本数字基线上重跑这些严格契约测试，版本升级由 Termcp 发布流程统一决定。

## 6. 本地 E2E 验收记录（2026-10-30）

- Termcp：`fix/approval-lifecycle` / `f6cefec`，版本标记仍为 `0.2.8` 开发基线
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
- 边界：以上证明当前 Termcp 0.2.8 开发基线与 Harness 闭环；后续 Termcp 更新必须重新跑同一组 E2E。

## 7. 非本清单事项

- Harness 自身状态机、策略、事件日志和 UI 缺陷不登记为 Core 上游问题。
- Core 尚未实现的能力应登记为能力缺口，不应写成“测试通过”。
- Core 进程识别、PID、启动时间和所有权属于 Harness 的本地实例识别能力，不等同于审批契约问题。

## 8. 运行中 Core 实例复验（2026-10-31）

- 实例：`http://127.0.0.1:18765`，version `v0.2.8`，daemon PID 32124，启动时间 `2026-10-30T13:27:15Z`。
- 真实 Core MCP：Streamable HTTP `http://127.0.0.1:18765/stream`，协议 `2025-11-25`，工具列表可用。
- 通过：
  - `TestTermcpCoreReadOnlyPipelineIntegration`
  - `TestTermcpCoreConcurrentControlTraceIntegration`
  - `TestTermcpCorePOSIXWriteEffectsIntegration`
  - `TestTermcpCoreOutputFloodIntegration`
  - `TestTermcpCoreCancellationIntegration`
  - `TestTermcpCorePOSIXTransportDisconnectIntegration`
  - `TestTermcpCoreApprovalReviewIntegration`
- 失败：
  - `TestTermcpCoreApprovalExpiryContract`：过期审批仍返回 `200` 并被实际执行，运行中实例尚未包含 `CORE-0.2.8-001` 的本地修复。
  - `TestTermcpCoreApprovalSessionDeathStateContract`：关闭 Session 后审批未进入可查询的 `cancelled`，运行中实例尚未包含 `CORE-0.2.8-002` 的本地修复。
- 审批关联：Harness 控制 API 返回 `correlation_capability=unverified`；Core 队列存在 pending 审批，但工具结果没有可信 `approval_id`，Desktop 因此不显示批准/拒绝入口。这符合 `CORE-0.2.8-003` 的 fail-closed 策略，不代表审批 UI 操作已验证通过。
- 真实 Desktop：Wails 成功连接 Harness 与 Core，读取真实 Agent Session、事件、MCP 1/1 就绪和等待审批状态；Session 跟随/锁定、关联未知、后台任务计数、关闭面板不停止 Core Session 均按契约显示。生产版可执行文件在清理后复测可正常启动并暴露 `Termcp Harness` 窗口。
- Terminal UI：Canvas 明确显示“数据通道尚未接入”；本次验证的是 Core/MCP 终端工具执行，不是 Canvas 保序 PTY 字节流。
- 清理：所有专用测试 Session 均为 `exited`，测试临时文件已确认不存在，临时 Harness 监听进程、一次性 token 和测试策略文件已清理；外部 Core PID 32124 未停止。

## 9. 本地源码重建复验（2026-10-31）

第 8 节记录的是旧安装二进制。当前工作区随后按原 daemon 方式重建并重启，验证结果如下。

- 构建：`dev-f6cefec-dirty (commit f6cefec)`。
- 安装位置：`C:\Users\miles\.termcp\versions\v0.2.8\termcp.exe`。
- 旧二进制备份：`C:\Users\miles\.termcp\versions\v0.2.8\backups\termcp.exe.20261031-pre-approval-correlation`。
- 运行方式：`termcp daemon start --host 127.0.0.1 --port 18765 --data-dir C:\Users\miles\.termcp --assets C:\Users\miles\.termcp\assets --log-level info --idle-timeout 30s`。
- 运行实例：PID 23424，`daemon=true`，`idle_timeout_ms=30000`。
- `TestTermcpCoreApprovalExpiryContract`：通过；过期审批不能再执行。
- `TestTermcpCoreApprovalSessionDeathStateContract`：通过；Session 关闭后审批可查询为 `cancelled`。
- 审批关联：Harness 返回 `correlation_capability=trusted`，pending 结果包含可信 `approval_id`、`session_id` 和 `expires_at`。
- 真实 Desktop 正向闭环：点击批准后，服务端确认 `approved`、执行结果 `succeeded`、Agent Session `completed`；远程临时文件回读为预期测试内容。
- 真实 Desktop 过期路径：审批超时后点击批准得到服务端 `expired`，未执行写入，UI 显示“已过期”和“服务端已确认”。
- 本轮还修复了 Harness 前端在 Runtime 实例重启后仅比较事件 `latest_sequence`、导致新实例低序号快照被丢弃的问题；现在只有 `runtime.instance_id` 相同时才拒绝倒退序号。
- 发布边界：`approval_id` 关联改动仍在 Core 工作区，尚未提交或进入正式发布；当前验证对象是 `f6cefec-dirty`，不能据此宣称正式 v0.2.8 发布包已包含全部修复。
