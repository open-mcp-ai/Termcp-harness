# Harness control API v1（草案）

> 状态：代码与本地测试已覆盖；公开兼容性尚未承诺。
>
> 日期：2026-10-30
>
> 适用实现：`cmd/termcp-harness serve`

## 边界

本版提供 Harness Runtime 生命周期、事件窗口、MCP Server 列表、已绑定 Core 的只读身份快照、审批快照、受保护决定/Execution 刷新/`unknown` 只读收敛，以及受保护的 Agent Session 创建、确定性运行、显式停止、模型 Loop 和安全检查点人工恢复。它不提供任意工具调用、模型 endpoint 修改或远程控制入口。

事件补拉入口见 `event-log.md`；事件模型也已单独版本化。

控制 API v1 当前只允许绑定回环地址。审批决定、Agent Session 创建/运行/恢复的写控制要求 Bearer token；其余只读端点没有认证。远程访问、TLS、通用 CORS 和第三方客户端认证均未设计。在这些边界完成前不得暴露到非回环网络。

## `GET /healthz`

Runtime 处于 `running` 时返回 `200`，其他生命周期状态返回 `503`。

```json
{
  "status": "ok",
  "state": "running",
  "version": "dev"
}
```

## `GET /v1/runtime`

返回当前 Runtime 实例的一致性快照：

```json
{
  "instance_id": "32-hex-character-local-instance-id",
  "version": "dev",
  "state": "running",
  "started_at": "2026-10-28T12:00:00Z"
}
```

`instance_id` 标识本次 Harness Runtime 进程实例，不是 Core 实例、MCP Server 或 Session ID。生命周期状态包括：

- `new`
- `running`
- `stopping`
- `stopped`

`failed` 已为未来失败收敛保留，本版控制 API 不会主动产生该状态。

## `GET /v1/recovery`

返回 Event、Session 和 Approval Store 的最近恢复证据。详细字段、状态和边界见 `recovery-manifest.md`。

典型响应：

```json
{
  "manifest_configured": true,
  "manifest_present": true,
  "manifest_version": 1,
  "recorded_at": "2026-11-02T00:00:00Z",
  "state": "complete",
  "consistent": true,
  "stores": [
    {
      "name": "events",
      "configured": true,
      "state": "present",
      "exists": true,
      "size": 4096,
      "fingerprint": "<sha256>"
    }
  ]
}
```

`complete` 只证明 Store 集合与最后正常基线匹配，不表示审批队列、MCP 连接或活动工具执行已经恢复。

## `GET /v1/events`

按 Harness 本地 `sequence` 补拉有界事件窗口。详细语义、容量、截断标记和 payload 限制见 `event-log.md`。

配置 `--event-log-file` 时，启动会恢复持久化的有界窗口和 sequence；响应结构不变。恢复事件只是历史元数据，不表示审批队列、MCP 连接或 Agent Loop 已恢复。

## `GET /v1/events/stream`

使用 SSE 推送同一有界事件窗口。`after` 或 `Last-Event-ID` 指定 cursor；每批最多 200 条，空闲时每 15 秒发送 heartbeat。完整 frame、截断和 Desktop 重连语义见 `event-log.md`。

## `GET /v1/core`

返回当前 Harness 绑定的 Core 实例身份。未配置 Core 时：

```json
{"configured":false}
```

已配置且获得快照时返回 `configured=true` 和 `core`。字段、所有权、冲突和重启语义见 `core-instance.md`。

该端点只证明 Harness 基于公开契约观察到的实例身份；`ownership=external` 表示不拥有 Core 进程，不得据此停止进程。

## `GET /v1/core/sessions`

只读返回 Core 当前公开列出的 Session 元数据。未配置 Core Session inventory 时返回：

```json
{"configured":false,"total":0,"sessions":[]}
```

已配置时最多接受 1000 个 Session、1 MiB 响应。每项只包含 `id`、`name`、`status`、`rows`、`cols`、`ssh_endpoint`、`ssh_config`、`namespace` 和 `created_at`；不返回 SSH 凭据、Core token、私有数据目录或文件路径。

该端点不把 Core Session 登记为 Harness Agent Session，也不证明外部 Agent 的 transcript、工具策略或审批历史。Desktop 必须通过固定桥读取该清单；浏览器不能获得 Core 地址、token 或通用 HTTP 能力。

## `GET /v1/mcp/servers`

返回当前已注册 MCP Server 的连接、能力、启用、所有权和派生 `ready` 状态。注册、连接和能力读取不是同一状态，具体语义见 `mcp-server-states.md`。

## `GET /v1/mcp/discovery`

执行一次本机 MCP 配置发现。当前支持 Codex global 配置，返回来源和 Server 的无凭据元数据；不自动连接或修改客户端配置。

## `POST /v1/mcp/discovery/{id}/register`

使用 Bearer 保护，把最近一次发现结果中 `loadable=true` 的 MCP 显式注册到 Harness Host。成功返回 `201` 和初始 `registered` 快照；后续连接和能力读取异步收敛。

- `404`：发现 ID 不存在；
- `409`：定义不可加载或注册冲突；
- `503`：Runtime 未启用 mutation control。

Discovery 不返回 command、args、env、header、token 或配置文件路径。当前不提供任意目录扫描，也不接受任意用户命令。

## `GET /v1/approvals`

返回当前 Harness 已通过可信 `approval_id` 关联的审批请求。未配置审批协调器时：

```json
{
  "configured": false,
  "correlation_capability": "unavailable",
  "total": 0,
  "approvals": []
}
```

已配置时，每项包含本地审批状态机快照和远端关联元数据：

```json
{
  "configured": true,
  "correlation_capability": "trusted",
  "total": 1,
  "approvals": [
    {
      "request": {
        "id": "approval-id",
        "call_id": "harness-call-id",
        "status": "approved",
        "decision": "approve",
        "revision": 3,
        "recovered": true,
        "recovery_reason": "runtime_restart",
        "execution": {
          "state": "running",
          "started_at": "2026-10-30T03:00:00Z"
        }
      },
      "reference": {
        "correlation": "trusted",
        "approval_id": "approval-id",
        "session_id": "core-session-id",
        "expires_at": "2026-10-30T03:00:00Z",
        "summary": "Submitted for review: write 6 bytes to /tmp/x"
      }
    }
  ]
}
```

该端点只读。`unknown` 或 `conflict` 关联不会被登记为可信审批请求。
`summary` 是可展示的申请摘要，统一空白并限制长度；它仍是外部文本，不作为指令或安全证明。

配置 `--approval-store-file` 后，端点也会返回已持久化的审批历史。`recovered=true` 表示该快照来自本地历史；`pending` / `submitting` 在 Runtime 重启后会收敛为 `unknown`，不能再次提交决定。恢复不会自动查询 Core、重放决定、重新派发工具或通知 Agent Session。

`correlation_capability` 独立表示可信审批关联能力：

- `unavailable`：未配置审批协调器。
- `unverified`：尚未观察到可信审批引用。Core 版本号本身不能证明或否定该能力。
- `trusted`：已观察到至少一个通过精确 `approval_id` 校验的可信审批引用。

`approvals` 不能用时间、文本或相邻事件补造关联。只有显式可信 `approval_id` 才能把能力状态推进为 `trusted`。

## `POST /v1/approvals/{id}/decision`

提交一次明确的人工审批决定。默认关闭；只有 `serve --approval-token-file <path>` 指向至少 32 字节的受限文件时才启用。

请求必须满足：

- `Authorization: Bearer <token>`。
- `Content-Type: application/json`。
- 不带 `Origin`；浏览器跨源和同源请求在 CSRF 设计完成前均被拒绝。
- 使用当前观察到的 `revision`；旧 revision 返回冲突，不覆盖新状态。

```json
{
  "decision": "approve",
  "expected_revision": 1
}
```

结果：

- `200`：收到可信远端审批终态，返回 `outcome=confirmed` 和审批快照；快照中的 `execution` 表示执行结果，`execution.state=failed` 不撤销已确认的审批。若本地事件或审批历史记录失败，会附 `warning`。
- `202`：决定未提交或已提交但终态未知，返回 `status=unknown` 与 `outcome=unknown`；不得自动重试。审批历史写入失败会附 `warning`。
- `401` / `403`：认证失败或请求携带 `Origin`。
- `404`：审批不存在。
- `409`：revision 过期、请求已非 pending，或远端决定矛盾。
- `415`：Content-Type 不是 JSON。
- `503`：未配置审批决定入口。

CLI 调用：

```powershell
go run ./cmd/termcp-harness approvals decide `
  --runtime http://127.0.0.1:8787 `
  --id '<approval-id>' `
  --revision 1 `
  --decision approve `
  --token-file '<private-token-file>'
```

Token 和请求正文不写入普通日志、事件或 URL。

## `POST /v1/approvals/{id}/execution/refresh`

重新读取一次已批准审批的权威 Execution 快照。该入口默认关闭，与审批决定使用同一受限 Bearer token；它**不会**重新提交审批决定或已批准的工具调用。

请求必须满足：

- `Authorization: Bearer <token>`。
- `Content-Type: application/json`。
- 不带 `Origin`。
- 正文为 `{}`；不接受的字段会被拒绝。

```json
{}
```

成功响应：

```json
{
  "approval": {
    "request": {
      "id": "approval-id",
      "call_id": "harness-call-id",
      "status": "approved",
      "decision": "approve",
      "revision": 3,
      "execution": {
        "state": "succeeded",
        "started_at": "2026-10-30T03:00:00Z",
        "finished_at": "2026-10-30T03:00:01Z"
      }
    },
    "reference": {
      "correlation": "trusted",
      "approval_id": "approval-id"
    }
  },
  "changed": true,
  "complete": true
}
```

- `changed=true` 表示本次只读查询获得了不同于 Harness 快照的 Execution。
- `complete=true` 表示 Execution 已是 `succeeded` 或 `failed` 可信终态。
- 无论 `changed` 是否为 true，成功的刷新都会通知观察者；`waiting_execution` Session 可因此从已有可信证据恢复尚未执行的步骤。
- 刷新成功但本地事件记录失败时返回 `200` 并附 `warning`；不得据此重新提交工具调用。

错误：

- `401` / `403`：认证失败或携带 `Origin`。
- `404`：审批不存在。
- `409`：审批尚未进入 `approved`。
- `415`：Content-Type 不是 JSON。
- `502`：读取 Core 的权威执行状态失败；保留 Harness 当前快照。
- `503`：执行刷新入口未启用或 Core 查询能力不可用。

## `POST /v1/approvals/{id}/reconcile`

对本地状态为 `unknown` 的审批执行一次只读 Core Approval Lookup。该入口默认关闭，与审批决定使用同一受限 Bearer token；它**不会**提交批准/拒绝，也不会重放工具调用。

请求要求与 Execution 刷新相同：Bearer token、JSON `{}`、不带 `Origin`。

成功响应：

```json
{
  "approval": {
    "request": {
      "id": "approval-id",
      "call_id": "harness-call-id",
      "status": "approved",
      "decision": "approve",
      "revision": 3,
      "execution": {
        "state": "running",
        "started_at": "2026-10-30T03:00:00Z"
      }
    },
    "reference": {
      "correlation": "trusted",
      "approval_id": "approval-id"
    }
  },
  "changed": true,
  "resolved": true
}
```

- 远端 `approved` / `rejected` / `expired` / `cancelled`：更新本地状态，返回 `changed=true, resolved=true`，并通知观察者。
- 远端仍为 `pending`：本地保持 `unknown`，返回 `changed=false, resolved=false`；不得提交决定。
- `resolved=true` 只表示审批决定状态已收敛；`approved` 对应的 Execution 仍可能处于 `running`、`submitted` 或 `unknown`。
- 只有 `unknown` 审批可使用本入口；已批准审批的执行刷新使用 `/execution/refresh`。
- 成功读取后写入 `approval.reconciled` 事件，记录字段 `reason=read_only_lookup`、`remote_status`、`changed`、`resolved`。

错误：

- `401` / `403`：认证失败或携带 `Origin`。
- `404`：审批不存在。
- `409`：审批不是 `unknown`，或远端终态与本地已提交决定矛盾。
- `415`：Content-Type 不是 JSON。
- `502`：Core Lookup 失败；保留 Harness 当前快照。
- `503`：入口未启用或 Core 查询能力不可用。

CLI 调用：

```powershell
go run ./cmd/termcp-harness approvals reconcile `
  --runtime http://127.0.0.1:8787 `
  --id '<unknown-approval-id>' `
  --token-file '<private-token-file>'
```

## `GET /v1/agent/sessions`

返回当前 Runtime 中的只读 Agent Session 快照。未配置 Session 管理器时返回 `configured=false` 和空数组。

```json
{
  "configured": true,
  "total": 1,
  "sessions": [
    {
      "id": "agent-session-1",
      "core_session_id": "core-session-1",
      "state": "waiting_approval",
      "revision": 4,
      "current_call_id": "call-1",
      "current_approval_id": "approval-1",
      "current_approval_status": "approved",
      "current_tool_status": "unknown",
      "current_completion": "completed",
      "model_turns": 3,
      "model_tool_calls": 2,
      "transcript_messages": 7,
      "output_bytes": 84,
      "model_loop_recovery": {
        "stage": "waiting_approval",
        "safe_boundary": false,
        "blocked_reason": "approval_outcome_unknown",
        "iteration": 2,
        "tool_calls": 1,
        "pending_tool_calls": 1,
        "current_call_id": "call-1",
        "checkpoint_revision": 7,
        "updated_at": "2026-10-30T03:00:01Z"
      },
      "current_execution": {
        "state": "unknown",
        "started_at": "2026-10-30T03:00:00Z"
      }
    }
  ]
}
```

状态和关联规则见 `agent-session.md`。

`model_loop_recovery` 是诊断摘要，本身不触发恢复。`safe_boundary=true` 只表示 checkpoint 位于模型请求或尚未派发工具的安全边界；是否允许恢复还必须满足 `state=unknown`、`recovered=true`、`recovery_reason=runtime_restart`，并使用受保护恢复入口。`tool_dispatching`、`tool_observed`、`waiting_approval` 和 `waiting_execution` 必须保持阻塞。该字段不包含模型正文、工具参数或结果正文。

## `GET /v1/agent/sessions/{id}`

只读返回单个 Agent Session 快照，响应为 `{"session": {...}}`，字段与列表项一致。未配置 Session 管理器返回 `503`，ID 不存在返回 `404`。该端点不支持修改 Session 状态或触发工具执行。

## `GET /v1/agent/sessions/{id}/transcript`

返回单个 Agent Session 的持久化模型消息和最终回复。该端点默认关闭，必须和审批决定使用同一受限 Bearer token；正文可能包含用户输入、工具结果或敏感业务内容，不能作为无认证快照暴露。

```json
{
  "transcript": {
    "session": {
      "id": "agent-session-1",
      "state": "completed",
      "revision": 12,
      "transcript_messages": 4,
      "output_bytes": 84
    },
    "messages": [
      {"role": "system", "content": "..."},
      {"role": "user", "content": "..."},
      {"role": "assistant", "content": "..."},
      {"role": "tool", "content": "...", "tool_call_id": "call-1"}
    ],
    "output": "最终模型文本"
  }
}
```

- `401` / `403`：认证失败或携带 `Origin`。
- `404`：Session 不存在。
- `503`：未配置 Session transcript 或 Bearer 控制。
- 响应体上限为 16 MiB；客户端不得把 transcript 写入无认证 URL、普通日志或前端持久化。

未配置 `--session-store-file` 时，transcript 仍可在当前 Runtime 内存中读取；配置后可在同一文件恢复的终态 Session 上读取。

## `POST /v1/agent/sessions`

创建确定性 Agent Session。必须携带与审批决定相同的 Bearer token，且请求必须是 JSON。

```json
{
  "id": "agent-session-1",
  "core_session_id": "optional-trusted-core-session"
}
```

成功返回 `201` 和 `session` 快照；重复 ID 返回 `409`。

## `POST /v1/agent/sessions/{id}/run`

运行 1-64 个显式步骤。每个步骤都必须经过工具策略 allowlist，Server、工具和精确参数任一不匹配就不派发。

```json
{
  "steps": [
    {
      "call_id": "call-1",
      "server_id": "termcp-core",
      "tool_name": "term",
      "required_completion": "accepted",
      "arguments": {
        "cmd": "printf harness-agent"
      }
    }
  ]
}
```

响应只包含 Session 状态、call ID、工具状态、结果字节数和 `is_error`，不回传原始工具结果正文。完成返回 `200`；等待审批或执行结果返回 `202`。该端点在未配置工具策略或未启用 Bearer token 时返回 `503`。

`required_completion` 默认 `accepted`，表示工具调用本身得到可信结果即可继续。需要命令或操作真正执行完成时设为 `completed`；对 shell 输入，`execution=submitted` 只能满足 `accepted`。

## `POST /v1/agent/sessions/{id}/cancel`

请求停止一个 Agent Session。默认关闭；只有 Runtime 配置受限审批 token 时启用，且请求必须携带 Bearer token、`Content-Type: application/json`。

```json
{
  "expected_revision": 4
}
```

语义：

- `new`、`waiting_approval`、`waiting_execution` 可立即进入 `cancelled`。
- `running` 先返回 `cancelling`；模型请求中或工具派发前的取消最终进入 `cancelled`，已派发工具无法证明结果时进入 `unknown`。
- 停止任务不关闭 Core Session、Shell、终端桥或远端审批。
- 等审批时取消保留 `cancellation.approval_pending=true`；等执行时取消保留 `cancellation.remote_outcome_unknown=true`。
- 重复取消 `cancelling` / `cancelled` 幂等；revision 不一致或其他终态返回 `409`。

响应：

```json
{
  "session": {
    "id": "agent-session-1",
    "state": "cancelling",
    "revision": 5,
    "current_call_id": "call-1",
    "cancellation": {
      "requested_at": "2026-11-01T08:20:16Z",
      "phase": "tool_dispatch"
    }
  }
}
```

- `200`：已进入可信 `cancelled`，或幂等返回既有取消状态。
- `202`：进入 `cancelling`，等待当前执行边界收敛。
- `400`：缺少 `expected_revision`。
- `401` / `403`：认证失败或携带 `Origin`。
- `404`：Session 不存在。
- `409`：revision 冲突或 Session 已进入其他终态。
- `415`：Content-Type 不是 JSON。
- `503`：取消能力或认证控制未配置。

CLI：

```powershell
go run ./cmd/termcp-harness agent cancel `
  --runtime http://127.0.0.1:8787 `
  --id '<agent-session-id>' `
  --revision 5 `
  --token-file '<private-token-file>'
```

## `POST /v1/agent/sessions/{id}/loop`

运行一次有界模型 Agent Loop。默认关闭；只有 Runtime 同时配置受限审批 token、精确工具策略、MCP 工具来源和 OpenAI-compatible 模型 endpoint 时启用。

```json
{
  "input": "检查当前工作区并总结",
  "max_iterations": 8,
  "max_tool_calls": 16
}
```

- `input`：必填、去空白后非空、最多 256 KiB。
- `max_iterations`：可选，默认 8，范围 1-32。
- `max_tool_calls`：可选，默认 16，范围 1-64。
- 模型工具目录来自精确 allowlist 与当前已加载 MCP Schema 的交集；不等于允许模型任意调用 MCP。

响应：

```json
{
  "session": {
    "id": "agent-session-1",
    "state": "completed",
    "revision": 12,
    "model_turns": 3,
    "model_tool_calls": 2
  },
  "output": "最终模型文本",
  "calls": [
    {"call_id": "model-call-id", "status": "succeeded", "result_bytes": 128, "is_error": false}
  ]
}
```

- `200`：Session 已完成或进入明确终态；最终模型文本通过 `output` 返回。
- `202`：Session 进入 `waiting_approval` 或 `waiting_execution`。
- `400`：input、预算或模型 Loop 前置条件无效。
- `401` / `403`：认证失败或请求携带 `Origin`。
- `404`：Session 不存在。
- `415`：Content-Type 不是 JSON。
- `502`：模型或 Session 执行失败，响应仍可携带失败 Session 快照。
- `503`：模型 Loop、工具策略、MCP 工具或审批控制未配置。

CLI：

```powershell
go run ./cmd/termcp-harness agent loop `
  --runtime http://127.0.0.1:8787 `
  --id '<agent-session-id>' `
  --input-file '<message-file>' `
  --max-iterations 8 `
  --max-tool-calls 16 `
  --token-file '<private-token-file>'
```

模型正文、工具参数和工具结果正文不进入事件或普通日志。模型 endpoint 与模型凭据只能在 Runtime 启动时配置，控制 API 不能修改。

## `POST /v1/agent/sessions/{id}/loop/resume`

人工领取一个 Runtime 重启后处于 `unknown` 的模型 Loop 安全检查点。默认关闭；只有 Runtime 同时配置受限审批 token、工具策略、MCP 工具、OpenAI-compatible 模型 endpoint、Invoker 和 Session Store 时可用。

请求必须携带 Bearer token、`Content-Type: application/json`，正文为 `{}`。客户端只能提供 Session ID；模型 endpoint、凭据、工具目录和 Invoker 使用当前 Runtime 配置，不能由请求覆盖。

恢复前置条件：

- Session 为 `unknown`、`recovered=true`、`recovery_reason=runtime_restart`；
- checkpoint 为 `model_requesting` 或 `tools_ready`，且 `safe_boundary=true`；
- 当前工具目录的名称、Server/工具身份、完成级别、结果暴露策略、Schema hash 和描述 hash 与 checkpoint 完全一致；
- `tools_ready` 的持久化 assistant tool calls、参数 hash/字节数、已完成结果数量和 `next_tool` 与 checkpoint 一致；
- 同一 Session 尚未被另一个恢复请求领取。

成功响应与 `/loop` 相同。`model_requesting` 可能重新请求模型；`tools_ready` 会执行尚未派发的工具，并继续经过策略与审批。

错误：

- `400`：恢复能力参数无效。
- `401` / `403`：认证失败或携带 `Origin`。
- `404`：Session 不存在。
- `409`：Session 不是可领取的安全恢复点、已被领取，或当前工具目录/transcript 与 checkpoint 不一致。
- `415`：Content-Type 不是 JSON。
- `502`：模型或 Session 执行失败，响应仍可携带失败 Session 快照。
- `503`：模型恢复、工具、MCP 或审批控制未配置。

CLI：

```powershell
go run ./cmd/termcp-harness agent resume `
  --runtime http://127.0.0.1:8787 `
  --id '<restarted-agent-session-id>' `
  --token-file '<private-token-file>'
```

恢复入口不自动重试。`tool_dispatching`、`tool_observed`、`waiting_approval`、`waiting_execution` 和任何信息不完整的 checkpoint 都不得由该入口继续。

## 非目标

- 不从本地时间推断 Core 或 MCP 事件因果。
- 不把健康响应描述为 Core、MCP 工具或 Agent 能力就绪。
- 不把端口可达或 PID 观测写成永久进程所有权。
- 不提供任意 URL、文件、命令或 MCP 调用的通用桥接。
- 不通过无认证回环 API 提交批准、拒绝或副作用决定。
