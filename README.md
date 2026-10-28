# Termcp Harness

Termcp Harness 是 **MCP 原生的 Agent Runtime 与人机共享的终端工作台**。它管理 Agent 执行、MCP 工具、上下文、会话、策略、操作轨迹和恢复，并通过可选的 Terminal Workspace 让人类观察、审批和接管同一执行现场。

它不是第二个 Core，也不是 Termcp-Desktop 的复制品。Core 是真实执行平面，Harness Runtime 是 Agent/工具控制平面，Terminal Workspace 是第一方体验平面。

## 当前状态

- 已建立 Go 1.27 Headless Runtime 骨架，入口为 `cmd/termcp-harness`。
- Harness Runtime 已有实例生命周期、并发安全快照、优雅关闭、健康检查和本地控制 API。
- 已有独立建模的工具调用与审批状态机，覆盖结果未知、可信终态、revision CAS 和自动重试安全条件。
- 已建立 MCP Server 注册表模型与只读 Streamable HTTP 探测器，并通过本机 Core 0.2.8 的真实 `initialize`、`tools/list` 和只读 `term` 帮助调用测试。
- 已建立持久 MCP Host：真实连接、能力读取、启用/停用、断连和 `tools/list_changed` 失效已接入，并写入控制事件。
- 已建立有界事件日志和 `/v1/events` 补拉 API；控制事件不淘汰，诊断事件截断水位可见。
- 已建立 Wails + React/TypeScript Desktop Shell，展示真实 Harness Runtime、MCP Server 和事件时间线。
- Desktop 默认简体中文，初始窗口 `1280×800`，采用深海军蓝与靛蓝信号色。
- 控制 API 当前只允许绑定回环地址，详见 `docs/contracts/harness/v0.1/control-api.md`。
- 状态机语义见 `docs/contracts/harness/v0.1/state-machines.md`；尚未接入真实 MCP/Core。
- MCP 认证、自动重连、SSE/stdio 和受管子进程尚未实现；Agent Loop、Session、真实终端工具执行和审批交互也尚未接入。
- 当前没有 CI；Desktop 尚未接入 `@xterm/xterm`、认证或安装包生成。
- 本地工作区已初始化 Git，当前开发分支为 `dev`；`main` 未由本项目直接修改。
- 产品定位已由 `docs/adr/ADR-011-mcp-native-agent-runtime.md` 确认。
- 第一方 Terminal Workspace 技术方向暂定 Wails v2 + Go + React/TypeScript/Vite + `@xterm/xterm`。
- Harness Runtime 的长期进程模型、远程控制面和 MCP 实现仍需 PoC。

## 开发命令

Runtime 使用 Go 标准库与 `github.com/mark3labs/mcp-go v0.50.0`；共享依赖校验和与本机 Core 0.2.8 的 `go.sum` 一致。Desktop 另使用 Wails v2.15.0。

```powershell
go test -race ./...
go vet ./...
go build ./...
go run ./cmd/termcp-harness version
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787
go run ./cmd/termcp-harness mcp probe --endpoint http://127.0.0.1:18765/stream
go run ./cmd/termcp-harness serve --mcp-endpoint http://127.0.0.1:18765/stream
```

Desktop 首次安装与构建：

```powershell
Set-Location .\desktop\frontend
npm ci
npm run check
Set-Location ..
wails build -m -nosyncgomod -nopackage -clean
```

分离启动 Harness Runtime 与 Desktop：

```powershell
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787
.\desktop\build\bin\termcp-harness-desktop.exe --runtime http://127.0.0.1:8787
```

运行单个测试：

```powershell
go test ./internal/runtime -run '^TestRuntimeLifecycle$' -count=1
go test ./internal/eventlog -run '^TestDiagnosticEvictionIsVisible$' -count=1
$env:TERMCP_CORE_MCP_URL = 'http://127.0.0.1:18765/stream'
go test ./internal/mcpcontrol -run '^TestProbeTermcpCoreIntegration$' -count=1
go test ./internal/mcpcontrol -run '^TestHostLifecycleAndCapabilityNotification$' -count=1
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
- `docs/adr/ADR-009-modular-composition.md`：模块目录、推荐安装与自由组合。
- `docs/contracts/README.md`：Core 与 Harness 公开契约的追踪规则。
- `docs/contracts/harness/v0.1/control-api.md`：当前本地控制 API 草案与明确边界。
- `docs/contracts/harness/v0.1/state-machines.md`：工具调用、审批和自动重试状态语义。
- `docs/contracts/harness/v0.1/mcp-server-states.md`：MCP Server 登记、连接、能力和启用状态。
- `docs/contracts/harness/v0.1/event-log.md`：本地事件顺序、有界保留、截断和补拉语义。
- `docs/contracts/harness/v0.1/desktop-shell.md`：Desktop 桥接、端点限制和当前界面边界。
- `docs/contracts/core/v0.2.8/metadata.md`：已安装 Core 0.2.8 的版本与来源证据。
- `docs/contracts/core/v0.2.8/mcp.md`：真实 MCP 握手、能力与待验证语义。
- `AGENTS.md`：AI 编码代理的执行规则。

## 上游

- Core: <https://github.com/open-mcp-ai/Termcp>
- Desktop: <https://github.com/open-mcp-ai/Termcp-Desktop>

Core 是执行权威与契约上游；Desktop 是成熟的人类终端工作台与参考实现；Harness 通过公开契约保持独立的 Runtime/Workspace 边界。

## 下一阶段

1. 增加 MCP 自动重连、退避、健康检查和能力刷新。
2. 支持 MCP HTTP 认证的凭据引用，并保持日志、状态和诊断脱敏。
3. 完成 SSE/stdio 传输与受管 Core 子进程实例所有权边界。
4. 接入 Core 终端 Session、`@xterm/xterm` 和保序 WebSocket 字节流。
5. 把审批与工具状态机接入真实派发链路并接入 Desktop。
6. 完成安装包、日志导出、跨平台和性能基线。
