# Termcp Harness

Termcp Harness 是 **MCP 原生的 Agent Runtime 与人机共享的终端工作台**。它管理 Agent 执行、MCP 工具、上下文、会话、策略、操作轨迹和恢复，并通过可选的 Terminal Workspace 让人类观察、审批和接管同一执行现场。

它不是第二个 Core，也不是 Termcp-Desktop 的复制品。Core 是真实执行平面，Harness Runtime 是 Agent/工具控制平面，Terminal Workspace 是第一方体验平面。

当前 `dev` 开发快照：`v0.1.3`。版本演进与验证记录见 [CHANGELOG.md](CHANGELOG.md)。

## 当前状态

- 已建立 Go 1.27 Headless Runtime 骨架，入口为 `cmd/termcp-harness`。
- Harness Runtime 已有实例生命周期、并发安全快照、优雅关闭、健康检查和本地控制 API。
- 已有独立建模的工具调用与审批状态机，覆盖结果未知、可信终态、revision CAS 和自动重试安全条件。
- 已建立 MCP Server 注册表模型与只读 Streamable HTTP 探测器，并通过本机 Core 0.2.8 的真实 `initialize`、`tools/list` 和只读 `term` 帮助调用测试。
- 已建立持久 MCP Host：真实连接、能力读取、启用/停用、断连和 `tools/list_changed` 失效已接入，并写入控制事件。
- 已建立有界事件日志、`/v1/events` 补拉 API 和 `/v1/events/stream` SSE；控制事件不淘汰，诊断事件截断水位可见。`--event-log-file` 可原子持久化当前保留窗口、sequence、诊断丢弃水位和 overflow；Event Journal 与 Session Store 在 Windows 上对 sharing/lock/access-denied 原子替换执行 5/10/25/50 ms 有界重试，耗尽后仍 fail-closed。Desktop 使用固定 Go 桥进行 cursor 重连、有界缓存、事件驱动快照刷新，并在同一 Runtime 实例内持久化客户端 cursor。
- 已建立 Core 实例识别与所有权绑定：读取 `/api/version` 和 `/api/daemon`，用端点、PID、启动时间和版本生成实例键；默认将现有 Core 标记为 external，受管绑定必须通过 PID + 启动时间校验；控制 API 和 Desktop 已展示身份快照。
- 已建立受控工具调用流水线：服务器、工具和精确参数 allowlist，参数/结果大小上限，状态机转换和控制事件轨迹。成功、工具级失败、传输超时/断连后的结果未知已由本地测试覆盖；真实 Core `term` 的幂等覆盖写入、副作用成功/超时未知、Kali POSIX 上主动断开 MCP Host 后的 `unknown` 与不重试，以及 review 挂起、批准、拒绝、并发决定和断开后审批均已通过 opt-in 集成测试。`review_pending` 现在映射为 `approval_pending`，不再误记为成功。
- 已建立审批关联与协调层：只有显式 `approval_id` 可形成 `trusted` 关联，缺失或冲突不会被猜测；协调器按 revision 串行提交决定，超时/断连收敛为 `unknown`，真实决定只走 Core 公开 HTTP 契约。审批快照可携带独立的结构化 `execution`，区分 shell `submitted`、非 shell `succeeded`、执行 `failed` 和结果 `unknown`，不会把批准等同于执行成功；等待中的执行结果通过有界只读查询收敛，不重放决定。`--approval-store-file` 可持久化可信关联、决定 revision 和最后已知 Execution；重启时 `pending` / `submitting` 收敛为 `unknown`，恢复过程不自动调用 Core 或继续 Agent Loop。`unknown` 审批可通过受保护的只读 reconcile 入口查询 Core，远端仍为 `pending` 时继续保持未知。`/v1/approvals` 以 `correlation_capability` 表示当前观察结果：未配置为 `unavailable`，尚未取得可信审批引用为 `unverified`，建立可信关联后为 `trusted`。决定入口默认关闭，需显式配置受限 Bearer token 文件，并拒绝浏览器 Origin。
- 已建立确定性 Headless Agent Session 内核：显式步骤只能经过受控工具流水线执行，遇到审批挂起、阻断、失败、取消、过期或 `unknown` 会停止并保留明确状态；Step 支持 `accepted` / `completed` 完成级别，审批结构化结果会触发一次性自动消费，只有满足完成级别后才继续剩余步骤。批准本身不等同于工具执行成功，结果 `unknown` 不会自动重试。Agent 任务可由受保护 `/cancel` 显式停止：派发前进入 `cancelled`，模型请求或工具派发中先进入 `cancelling` 并由真实执行边界收敛；已派发但无法证明结果时保持 `unknown`。等待审批/执行时停止只停止 Harness 继续推进，不宣称远端审批或工具已终止。
- 已建立首个受保护 Agent Session 纵向切片：JSON 工具策略按 Server、工具和精确参数 allowlist 编译；`serve --tool-policy-file` 装配真实 MCP Host 与工具流水线；Bearer 保护的 `POST /v1/agent/sessions`、`/run` 和 CLI `agent run` 可创建并运行显式步骤；Desktop 展示 Session、审批和结构化执行状态，并可在 Go 进程安全持有 token 时提交批准/拒绝。运行响应不回传工具正文。
- 首个真实纵向实验已通过：本地 Core 开发构建 + Harness 临时二进制，固定只读 `term` 步骤完成，Session 为 `completed`，并各记录一条工具成功和 Session 完成事件。实验使用临时数据目录与随机端口，结束后按精确 PID 清理。
- 真实审批闭环已通过：`term` 文件写入返回可信 `Approval-ID`，批准前文件不存在；批准后 Core 执行写入，Harness 自动消费 `execution=succeeded` 并完成 Session，审批、恢复、完成事件各一条。
- 真实 Core 的调用取消已观测为本地 `unknown`，不能据此认定远端未执行；20,000 行输出和 12 路并发只读调用均通过，每个调用保留独立控制事件顺序。
- 已建立 Wails + React/TypeScript Desktop Shell，展示真实 Harness Runtime、MCP Server 和事件时间线。
- 已建立 Harness Canvas 初版 PoC：轻量 Shell、Workspace 模板、停靠网格中的面板拖动/缩放/最大化/关闭、标签合并、边缘分栏、模块抽屉、模板恢复、本机布局持久化、有界撤销/重做、Workspace Recipe v1 导入导出和跨 Workspace 布局复制；相关面板支持跟随/锁定 Session、关闭视图提示、后台任务计数和服务端审批回执，Canvas 状态使用 schema v2 并兼容旧布局。每个 Workspace 保存最多 20 步布局与 Session 引用快照，支持按钮、Ctrl+Z 和 Ctrl+Shift+Z，并在 Desktop 重启后继续恢复；输入框中的文本撤销不被拦截。Recipe 导入会先校验和预览，只替换当前 Workspace，失败或取消时保留原布局；损坏的单面板、无效上下文和未知字段被隔离或清理，重叠布局和超大文件明确拒绝。跨 Workspace 复制只携带布局和 Session 引用，预览目标及覆盖范围，复制后切换目标视图，并可用目标自己的历史撤销恢复。真实 Wails 窗口已验证导出下载、原生文件选择、无警告与带修复警告的预览、取消保留布局、确认替换和重启持久化，鼠标/键盘撤销、重做和重启后撤销，以及从日常协作复制 2 个面板覆盖安全测试 5 个面板后再撤销恢复。Terminal 已通过固定 Wails Go 桥接入真实 Core Shell，支持 `@xterm/xterm`、512 KiB 历史恢复、保序 WebSocket 输出、人工输入、PTY resize、Agent/人工输入来源、有界缓冲和断线未知状态；Core 本地开发分支现已增加 `watch_ready` 游标、终端绝对字节区间和 `terminal_done.cursor`，Harness 会校验历史结束、实时连续性和结束游标，缺口收敛为 `unknown`。隔离 E2E 已验证 488,728 字节历史恢复、实时输出、Shell 正常结束和 20,000 行洪峰；正式 Core 发布同步仍待完成。真实 Wails 窗口此前已验证输入/输出、关闭视图后 Shell 保持、重开历史恢复和 Shell 结束状态。已记录 20,000 行、输入响应、WebView2 内存和双 Shell 隔离基线。Harness 事件已通过 SSE 推送，Desktop 使用固定 Go 桥进行 cursor 重连、有界缓存和事件触发的合并快照刷新。第三方模块贡献仍待接入。
- Terminal Bridge 已支持自动重连：异常断开进入 `reconnecting`，按 200 ms 到 5 s 退避重试，用新的 `watch_ready.cursor` 分页补取 `[lastCursor,newCursor)`；只有全部区间严格连续才恢复 `connected`。超过 4 MiB、游标倒退、Core 已丢区间或任一页不一致时进入 `unknown`，不猜洞、不重放。重连中拒绝输入与 resize，UI 持续显示重试次数和恢复状态。
- Desktop 默认简体中文，初始窗口 `1280×800`，采用深海军蓝与靛蓝信号色。
- 控制 API 当前只允许绑定回环地址；审批决定与执行状态核对均受 Bearer token 保护，详见 `docs/contracts/harness/v0.1/control-api.md`。
- 工具调用状态机已通过 `internal/toolpipeline` 接入真实 MCP Host，当前仅支持显式 allowlist 下的同步调用；真实 Core 写测试只证明覆盖语义和 `unknown` 不自动重试，不证明 Core 支持 idempotency key。审批状态、受保护 CLI 决定、Session 结果观察、Desktop 审批操作、审批时间线和 `waiting_execution` 只读核对/恢复入口已接入。OpenAI-compatible 模型 Adapter 与有界 Agent Loop 已接入受保护 API/CLI，并通过 Control API → Harness MCP Host → 本机 Core 只读 `term` → DashScope 及 DeepSeek `deepseek-v4-pro` 的真实闭环；DeepSeek thinking/tool 续轮所需的 `reasoning_content` 仅驻留内存并回填 provider，不进入 transcript、Session Store、事件或 API。Session、模型消息和最终回复可选用本地文件持久化；`runtime_restart` 后若 checkpoint 位于 `model_requesting` 或 `tools_ready`，可由用户经 Bearer 保护入口人工恢复，流式输出和自动恢复仍未实现。
- Core 审批契约问题与上游同步状态统一记录在 `docs/contracts/core/v0.2.8/known-issues.md`；当前开发、集成和验收均以 Termcp 当前 `0.2.8` 版本基线的实际公开契约为准，不维护第二套 Harness 运行路径。
- MCP Bearer 认证引用、健康检查、退避重连、能力刷新、`tools/list_changed` 自动恢复，以及 Streamable HTTP/SSE/stdio 和受管 stdio 子进程已实现。模型 Agent Loop 已有 headless 切片和 Desktop 任务启动台，并在真实 Wails 窗口验证最终回复、审批和 `waiting_execution` 恢复按钮；`--session-store-file` 已通过真实 Runtime 重启验证，可从本地文件恢复 Session、3 条模型消息和最终回复。Session Store v2 另持久化活动 Loop checkpoint 的阶段、预算、工具目录指纹和待执行 call 摘要，但不复制工具参数正文；重启仍 fail-closed 为 `unknown`，只有经当前工具目录指纹和 transcript 参数哈希校验的 `model_requesting` / `tools_ready` 可由用户显式恢复。`--event-log-file` 可独立恢复有界事件窗口和 sequence，`--approval-store-file` 可恢复审批历史但不会恢复可决定审批队列；Desktop cursor 可按 endpoint 与 Runtime 实例恢复，Runtime 实例变化时安全重置到最新窗口。自动恢复、审批队列和工具派发中继续仍未实现。受管 Core 已实现基础子进程启动与停止。控制 API 提供受保护的 Session 创建、确定性运行、模型 Loop、安全检查点恢复、转录读取和单项审批决定，不提供任意工具调用、模型配置或通用 HTTP 桥接；Desktop 可创建或人工恢复模型任务、观察 Session 并提交审批决定。
- 多来源 MCP 可通过 `--mcp-config-file` 注册，每个来源独立 transport、凭据、enabled、健康和能力状态；非 Core stdio 工具已通过真实 Agent Session 调用闭环。
- 当前没有 CI；Desktop 已接入 `@xterm/xterm` 和真实 Core Terminal PoC，但尚未完成认证、窗口级性能基线或安装包生成。
- 本地工作区已初始化 Git，当前开发分支为 `dev`；`main` 未由本项目直接修改。
- 产品定位已由 `docs/adr/ADR-011-mcp-native-agent-runtime.md` 确认。
- 第一方 Terminal Workspace 技术方向暂定 Wails v2 + Go + React/TypeScript/Vite + `@xterm/xterm`。
- Harness Runtime 的长期进程模型、远程控制面和 MCP 实现仍需 PoC。

## 开发命令

Runtime 使用 Go 标准库与 `github.com/mark3labs/mcp-go v0.50.0`；共享依赖校验和与本机 Core 0.2.8 的 `go.sum` 一致。Desktop 另使用 Wails v2.15.0、`@xterm/xterm` 5.5.0 和 `@xterm/addon-fit` 0.10.0。

```powershell
go test -race ./...
go vet ./...
go build ./...
go run ./cmd/termcp-harness version
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787
go run ./cmd/termcp-harness mcp probe --endpoint http://127.0.0.1:18765/stream
go run ./cmd/termcp-harness serve --mcp-endpoint http://127.0.0.1:18765/stream
go run ./cmd/termcp-harness serve --mcp-endpoint http://127.0.0.1:18765/stream --mcp-auth-token-file '<private-token-file>' --mcp-reconnect-min 1s --mcp-reconnect-max 30s --mcp-health-interval 30s
go run ./cmd/termcp-harness serve --mcp-transport sse --mcp-endpoint http://127.0.0.1:18765/sse
go run ./cmd/termcp-harness serve --mcp-transport stdio --mcp-stdio-command '<mcp-server-executable>' --mcp-stdio-arg '<arg>' --mcp-stdio-workdir '<working-directory>'
go run ./cmd/termcp-harness serve --mcp-config-file .\examples\mcp-servers.example.json --approval-token-file '<private-token-file>'
go run ./cmd/termcp-harness core identify --endpoint http://127.0.0.1:18765
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --core-endpoint http://127.0.0.1:18765
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --mcp-endpoint http://127.0.0.1:18765/stream --tool-policy-file .\examples\tool-policy.readonly.json --approval-token-file '<private-token-file>'
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --mcp-endpoint http://127.0.0.1:18765/stream --tool-policy-file .\examples\tool-policy.readonly.json --approval-token-file '<private-token-file>' --model-endpoint http://127.0.0.1:11434/v1/chat/completions --model-id '<model-id>' --model-token-file '<private-model-token-file>'
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --mcp-endpoint http://127.0.0.1:18765/stream --tool-policy-file .\examples\tool-policy.readonly.json --approval-token-file '<private-token-file>' --session-store-file '<private-session-store-file>' --event-log-file '<private-event-store-file>' --approval-store-file '<private-approval-store-file>'
go run ./cmd/termcp-harness agent run --runtime http://127.0.0.1:8787 --id '<agent-session-id>' --steps-file .\examples\agent-steps.readonly.json --token-file '<private-token-file>'
go run ./cmd/termcp-harness agent loop --runtime http://127.0.0.1:8787 --id '<agent-session-id>' --input-file '<message-file>' --max-iterations 8 --max-tool-calls 16 --token-file '<private-token-file>'
go run ./cmd/termcp-harness agent resume --runtime http://127.0.0.1:8787 --id '<restarted-agent-session-id>' --token-file '<private-token-file>'
go run ./cmd/termcp-harness agent cancel --runtime http://127.0.0.1:8787 --id '<agent-session-id>' --revision 5 --token-file '<private-token-file>'
$env:TERMCP_MODEL_INTEGRATION = '1'
$env:TERMCP_MODEL_ENDPOINT = '<openai-compatible-chat-completions-url>'
$env:TERMCP_MODEL_ID = '<model-id>'
$env:TERMCP_MODEL_TOKEN_FILE = '<private-model-token-file>'
go test ./internal/agentsession -run '^TestModelAgentLoopProviderIntegration$' -count=1 -v
$env:TERMCP_MODEL_CORE_E2E = '1'
$env:TERMCP_CORE_MCP_URL = 'http://127.0.0.1:18765/stream'
go test ./cmd/termcp-harness -run '^TestModelAgentLoopCoreControlAPIIntegration$' -count=1 -v
go run ./cmd/termcp-harness approvals decide --runtime http://127.0.0.1:8787 --id '<approval-id>' --revision 1 --decision approve --token-file '<private-token-file>'
go run ./cmd/termcp-harness approvals refresh --runtime http://127.0.0.1:8787 --id '<approval-id>' --token-file '<private-token-file>'
go run ./cmd/termcp-harness approvals reconcile --runtime http://127.0.0.1:8787 --id '<unknown-approval-id>' --token-file '<private-token-file>'
```

`TERMCP_MODEL_INTEGRATION=1` 和 `TERMCP_MODEL_CORE_E2E=1` 都会产生 provider 调用和可能的费用。前者使用内存假工具；后者使用真实本机 Core MCP 的只读 `term` 指南调用。

Agent Session 工具策略不接受通配符，必须同时匹配 `server_id`、`tool_name` 和规范化后的完整参数对象：

模型 Loop 默认只向模型回填工具状态和结果字节数。只有同一 Server/Tool 的全部精确规则都显式加入 `"model_result":"content"` 时，结果正文才允许进入模型上下文；该声明只控制上下文投影，不改变工具执行策略或审批要求。

```json
{
  "rules": [
    {
      "server_id": "termcp-core",
      "tool_name": "term",
      "effect": "read_only",
      "arguments": { "cmd": "" }
    }
  ]
}
```

可直接使用 `examples/tool-policy.readonly.json` 与 `examples/agent-steps.readonly.json` 做只读冒烟测试。会话创建、运行和审批决定共用受限 `--approval-token-file` Bearer token；工具策略文件存在时该 token 为必填。

Desktop 也通过 `--approval-token-file` 启用批准/拒绝按钮。token 仅保存在 Desktop 的 Go 进程内存，不进入前端、localStorage、事件、URL 或日志。

Desktop 首次安装与构建：

```powershell
Set-Location .\desktop\frontend
npm ci
npm run check
npm test
npm run build
Set-Location ..
wails build -m -nosyncgomod -nopackage -clean
```

分离启动 Harness Runtime 与 Desktop：

```powershell
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787
.\desktop\build\bin\termcp-harness-desktop.exe --runtime http://127.0.0.1:8787 --approval-token-file '<private-token-file>'
```

运行单个测试：

```powershell
go test ./internal/runtime -run '^TestRuntimeLifecycle$' -count=1
go test ./internal/eventlog -run '^TestDiagnosticEvictionIsVisible$' -count=1
go test ./internal/toolpipeline -run '^TestPipelineMapsTransportErrorToUnknownWithoutRetry$' -count=1
$env:TERMCP_CORE_MCP_URL = 'http://127.0.0.1:18765/stream'
go test ./internal/mcpcontrol -run '^TestProbeTermcpCoreIntegration$' -count=1
go test ./internal/mcpcontrol -run '^TestHostLifecycleAndCapabilityNotification$' -count=1
go test ./internal/mcpcontrol -run '^TestHostSupervisor' -count=1 -v
go test ./internal/mcpcontrol -run '^TestHostAuthenticatesStreamableHTTPWithFileReferencedToken$' -count=1 -v
go test ./internal/mcpcontrol -run '^TestHostConnectsSSETransport$' -count=1 -v
go test ./internal/mcpcontrol -run '^TestHostConnectsStdioTransport$' -count=1 -v
go test ./internal/mcpcontrol -run '^TestHostKeepsDuplicateToolNamesScopedByServerID$' -count=1 -v
go test ./cmd/termcp-harness -run '^TestLoadMCPConfig' -count=1 -v
go test ./internal/toolpipeline -run '^TestTermcpCoreReadOnlyPipelineIntegration$' -count=1
$env:TERMCP_CORE_WRITE_TEST = '1'
$env:TERMCP_CORE_TEST_SESSION_ID = '<dedicated-session-id>'
$env:TERMCP_CORE_TEST_HANDLE = '<dedicated-session-id>:1'
$env:TERMCP_CORE_TEST_TEMP_DIR = $env:TEMP
go test ./internal/toolpipeline -run '^TestTermcpCoreWriteEffectsIntegration$' -count=1 -v
$env:TERMCP_CORE_POSIX_WRITE_TEST = '1'
$env:TERMCP_CORE_TEST_SESSION_ID = '<dedicated-posix-session-id>'
$env:TERMCP_CORE_TEST_HANDLE = '<dedicated-posix-session-id>:1'
$env:TERMCP_CORE_TEST_TEMP_DIR = '/tmp'
go test ./internal/toolpipeline -run '^TestTermcpCorePOSIXWriteEffectsIntegration$' -count=1 -v
$env:TERMCP_CORE_POSIX_DISCONNECT_TEST = '1'
$env:TERMCP_CORE_TEST_EFFECT_SESSION_ID = '<dedicated-effect-session-id>'
$env:TERMCP_CORE_TEST_EFFECT_HANDLE = '<dedicated-effect-session-id>:1'
$env:TERMCP_CORE_TEST_OBSERVER_SESSION_ID = '<dedicated-observer-session-id>'
go test ./internal/toolpipeline -run '^TestTermcpCorePOSIXTransportDisconnectIntegration$' -count=1 -v
$env:TERMCP_CORE_APPROVAL_TEST = '1'
$env:TERMCP_CORE_TEST_EFFECT_SESSION_ID = '<dedicated-effect-session-id>'
$env:TERMCP_CORE_TEST_EFFECT_HANDLE = '<dedicated-effect-session-id>:1'
$env:TERMCP_CORE_TEST_OBSERVER_SESSION_ID = '<dedicated-observer-session-id>'
go test ./internal/toolpipeline -run '^TestTermcpCoreApprovalReviewIntegration$' -count=1 -v
$env:TERMCP_CORE_APPROVAL_EXPIRY_TEST = '1'
go test ./internal/toolpipeline -run '^TestTermcpCoreApprovalExpiryContract$' -count=1 -v
$env:TERMCP_CORE_APPROVAL_SESSION_DEATH_TEST = '1'
go test ./internal/toolpipeline -run '^TestTermcpCoreApprovalSessionDeathStateContract$' -count=1 -v
$env:TERMCP_CORE_CANCEL_TEST = '1'
$env:TERMCP_CORE_OUTPUT_FLOOD_TEST = '1'
$env:TERMCP_CORE_CONCURRENT_TEST = '1'
go test ./internal/toolpipeline -run '^TestTermcpCoreCancellationIntegration$' -count=1 -v
go test ./internal/toolpipeline -run '^TestTermcpCoreOutputFloodIntegration$' -count=1 -v
go test ./internal/toolpipeline -run '^TestTermcpCoreConcurrentControlTraceIntegration$' -count=1 -v
$env:TERMCP_HARNESS_CONTROL = 'http://127.0.0.1:8787'
$env:TERMCP_HARNESS_TEST_AGENT_ID = '<dedicated-agent-session-id>'
$env:TERMCP_CORE_TEST_SHELL_ID = '<dedicated-shell-id>'
go test ./desktop -run '^TestTerminalBridgeCoreIntegration$' -count=1 -v
$env:TERMCP_CORE_OUTPUT_FLOOD_TEST = '1'
go test ./desktop -run '^TestTerminalBridgeCoreOutputFlood$' -count=1 -v
go test ./internal/controlapi -run '^TestEventStream' -count=1 -v
go test ./internal/eventstream -count=1 -v
go test ./desktop -run '^TestAppBridgesRealtimeEventStream$' -count=1 -v
# 测试结束后关闭并删除专用 session；测试只清理自己写入的临时文件。
```

daemon 启动后可读取本地状态：

```powershell
Invoke-RestMethod http://127.0.0.1:8787/healthz
Invoke-RestMethod http://127.0.0.1:8787/v1/runtime
Invoke-RestMethod 'http://127.0.0.1:8787/v1/events?after=0&limit=100'
```

除非沙盒阻止写入默认 Go 缓存，上述命令不需要额外环境变量。此限制不是应用运行要求。

## 产品结构

### Termcp Harness Runtime

- Agent Loop 与 Session 生命周期。
- MCP Client/Host 与工具能力发现。
- 工具调用前后的验证、策略、审批和结果记录。
- 上下文、事件日志、操作轨迹和诊断。
- 超时、取消、重试、断连和结果未知恢复。
- Headless、daemon、容器或桌面部署边界。

### Terminal Workspace

- 终端、Session 和 Shell 状态。
- Agent 工具调用、审批和输出时间线。
- 人工接管、继续和恢复操作。
- MCP 工具与健康状态。
- 同一执行现场中的人机协作。

Terminal Workspace 是体验层，不是 Runtime 本体，也不是安全保证。

## 模块安装与组合

Harness 提供固定的展示 Shell，并把所有功能模块放进同一份模块目录：

- 每个功能模块都有复选框。
- 根据使用场景预勾选推荐模块，但所有推荐项都可以取消。
- L0-L3（四级安装）只表示安装必要程度和场景分组，不是产品版本、权限或质量等级。
- 支持全选、全不选、搜索、按类型筛选和恢复推荐。
- 安装后从同一目录继续补装、启用、停用、更新和卸载模块。
- 当前组合和 Workspace 可以保存、导入、导出和分享。
- 全部功能模块取消时，Shell 仍展示空工作台、缺失能力说明和恢复入口。
- 复选框只表示选择；安装、启用、运行和能力就绪分别显示，不能互相冒充。

安装器和安装后模块管理复用同一份清单与状态。真正的模块包、下载、签名、更新和卸载仍待 PoC；功能开关不会被描述成安装裁剪。

## 技术方向

- 终端 UI：Wails v2 + Go + React/TypeScript/Vite + `@xterm/xterm`。
- Core 集成：公开 HTTP / WebSocket / MCP 契约。
- Harness Runtime：已有 Go 1.27 headless bootstrap；长期依赖、进程模型和跨平台仍待 `ADR-012` 的 PoC。
- 工具：MCP 优先，第三方工具不依赖 Harness 私有 SDK。
- 安全：不 import Core 内部包，不复制执行引擎，不提供任意命令/文件/URL 通用桥接。
- 状态：成功、失败、取消、过期和结果未知必须可区分；有副作用操作不得盲目重试。

## 文档入口

- `docs/README.md`：完整文档索引与阅读顺序。
- `docs/product/PROJECT-DISCUSSION.md`：群聊介绍、一页简报和分阶段讨论脚本。
- `docs/product/VISION_v0.9.md`：产品定位、Runtime/MCP/Workspace 能力模型与路线图。
- `docs/architecture/DESIGN_v0.4.md`：运行时、MCP 工具链、策略、会话、恢复、终端工作台和安全约束。
- `docs/architecture/UPSTREAM.md`：Core、Desktop、Harness 的公开仓库与契约关系。
- `docs/product/WEB-DEFERRED.md`：Web 暂停策略。
- `docs/adr/ADR-011-mcp-native-agent-runtime.md`：产品定位与三平面架构。
- `docs/adr/ADR-012-runtime-bootstrap-technology.md`：Go Runtime bootstrap 与待验证边界。
- `docs/adr/ADR-013-harness-canvas-layout.md`：Harness Canvas 初版布局与 Workspace 模板。
- `docs/adr/ADR-014-core-terminal-transport.md`：固定 Desktop Terminal 桥与性能基线。
- `docs/adr/ADR-015-realtime-event-stream.md`：Harness SSE 与 Desktop 实时事件协调。
- `docs/adr/ADR-016-mcp-supervised-lifecycle.md`：MCP 健康检查、退避重连与凭据引用。
- `docs/adr/ADR-017-model-adapter-agent-loop.md`：模型 Adapter、有界 Agent Loop 与安全边界（Proposed / 首个切片已实现）。
- `docs/adr/ADR-009-modular-composition.md`：模块目录、推荐安装与自由组合。
- `docs/contracts/README.md`：Core 与 Harness 公开契约的追踪规则。
- `docs/contracts/harness/v0.1/control-api.md`：当前本地控制 API 草案与明确边界。
- `docs/contracts/harness/v0.1/core-instance.md`：Core 实例身份、所有权和重启检测。
- `docs/contracts/harness/v0.1/state-machines.md`：工具调用、审批和自动重试状态语义。
- `docs/contracts/harness/v0.1/agent-session.md`：确定性 Session 与模型 Loop 状态、事件和当前边界。
- `docs/contracts/harness/v0.1/model-adapter.md`：OpenAI-compatible Adapter、上下文投影和工具回填契约。
- `docs/contracts/harness/v0.1/mcp-server-states.md`：MCP Server 登记、连接、能力和启用状态。
- `docs/contracts/harness/v0.1/event-log.md`：本地事件顺序、有界保留、截断和补拉语义。
- `docs/contracts/harness/v0.1/desktop-shell.md`：Desktop 桥接、端点限制和当前界面边界。
- `docs/contracts/core/v0.2.8/metadata.md`：已安装 Core 0.2.8 的版本与来源证据。
- `docs/contracts/core/v0.2.8/mcp.md`：真实 MCP 握手、能力与待验证语义。
- `docs/contracts/core/v0.2.8/known-issues.md`：Core 未关闭上游问题、严格契约测试和关闭条件。
- `AGENTS.md`：AI 编码代理的执行规则。

## 上游

- Core: <https://github.com/open-mcp-ai/Termcp>
- Desktop: <https://github.com/open-mcp-ai/Termcp-Desktop>

Core 是执行权威与契约上游；Desktop 是成熟的人类终端工作台与参考实现；Harness 通过公开契约保持独立的 Runtime/Workspace 边界。

## 下一阶段

1. 审批时间线、Event Journal、审批历史持久化、`unknown` 审批只读收敛以及 Agent 停止任务/人工接管已实现本地闭环；继续补齐真实晚到回执和可见 Wails 窗口复验。
2. 模型 Adapter、有界 Agent Loop、Desktop 交互、基础 Session/transcript 持久化和安全检查点人工恢复已完成首个切片；继续补其他 provider、流式输出、自动恢复和摘要。
3. 完成安装包、日志导出、跨平台和性能基线。
