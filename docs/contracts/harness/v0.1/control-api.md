# Harness control API v1（草案）

> 状态：代码与本地测试已覆盖；公开兼容性尚未承诺。
>
> 日期：2026-10-30
>
> 适用实现：`cmd/termcp-harness serve`

## 边界

本版提供 Harness Runtime 生命周期、事件窗口、MCP Server 列表、已绑定 Core 的只读身份快照、审批快照与受保护决定入口，以及受保护的 Agent Session 创建/运行。它不提供任意工具调用或远程控制入口。

事件补拉入口见 `event-log.md`；事件模型也已单独版本化。

控制 API v1 当前只允许绑定回环地址。审批决定、Agent Session 创建和运行的写控制要求 Bearer token；其余只读端点没有认证。远程访问、TLS、通用 CORS 和第三方客户端认证均未设计。在这些边界完成前不得暴露到非回环网络。

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

## `GET /v1/events`

按 Harness 本地 `sequence` 补拉有界事件窗口。详细语义、容量、截断标记和 payload 限制见 `event-log.md`。

## `GET /v1/core`

返回当前 Harness 绑定的 Core 实例身份。未配置 Core 时：

```json
{"configured":false}
```

已配置且获得快照时返回 `configured=true` 和 `core`。字段、所有权、冲突和重启语义见 `core-instance.md`。

该端点只证明 Harness 基于公开契约观察到的实例身份；`ownership=external` 表示不拥有 Core 进程，不得据此停止进程。

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

- `200`：收到可信远端审批终态，返回 `outcome=confirmed` 和审批快照；快照中的 `execution` 表示执行结果，`execution.state=failed` 不撤销已确认的审批。若本地事件记录失败，会附 `warning`。
- `202`：决定已提交但终态未知，返回 `status=unknown` 与 `outcome=unknown`；不得自动重试。
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
      "current_execution": {
        "state": "unknown",
        "started_at": "2026-10-30T03:00:00Z"
      }
    }
  ]
}
```

状态和关联规则见 `agent-session.md`。

## `GET /v1/agent/sessions/{id}`

只读返回单个 Agent Session 快照，响应为 `{"session": {...}}`，字段与列表项一致。未配置 Session 管理器返回 `503`，ID 不存在返回 `404`。该端点不支持修改 Session 状态或触发工具执行。

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

## 非目标

- 不从本地时间推断 Core 或 MCP 事件因果。
- 不把健康响应描述为 Core、MCP 工具或 Agent 能力就绪。
- 不把端口可达或 PID 观测写成永久进程所有权。
- 不提供任意 URL、文件、命令或 MCP 调用的通用桥接。
- 不通过无认证回环 API 提交批准、拒绝或副作用决定。
