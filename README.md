# Termcp Harness

Termcp Harness 是 MCP 原生的 Agent Runtime 与人机共享终端工作台。

- **Termcp Core** 负责 SSH、PTY、Session、Shell、文件、端口转发、审批和真实执行。
- **Harness Runtime** 负责 Agent Loop、MCP Client/Host、工具策略、审批协调、事件、Session 和恢复。
- **Terminal Workspace** 负责展示同一执行现场，让人观察、审批、接管和恢复任务。

Harness 不 import Core 内部包，不复制 SSH/PTY 执行引擎，也不提供任意命令、文件或 URL 的通用桥接。

当前 `dev` 开发快照为 `v0.1.10`。版本记录见 [CHANGELOG.md](CHANGELOG.md)。

## 当前能力

- Go Headless Runtime、回环控制 API、健康检查和优雅关闭。
- 外置 MCP Host：Streamable HTTP、SSE、stdio、受管 stdio、健康检查、退避重连和能力刷新。
- 工具调用状态机：精确 allowlist、参数/结果上限、审批暂停、取消、失败和结果未知。
- Core 身份与所有权：`external`、`managed`、`unverified`，基于端点、PID、启动时间和版本识别。
- 持久本地 Core：固定 loopback endpoint、版本、SHA-256、数据目录和可选 token 文件引用。
- Core Session 发现：只读清单和固定 Desktop 终端桥，不伪造外部 Agent transcript。
- Agent Session：确定性步骤、模型 Loop、停止/接管、持久化 transcript 和人工安全检查点恢复。
- 事件系统：有界 Event Journal、SSE、Desktop cursor 重连和可选文件持久化。
- Desktop：Wails + React/TypeScript、真实 Terminal、审批时间线、MCP 面板、多 Workspace 和布局导入导出。
- 发布交付：完整 ZIP、安装器、完整 Core sidecar 和 Core overlay 补丁。

## 安装包与 Core 模式

发布包包含两个可执行入口：

```text
Termcp Harness.exe   Desktop
termcp-harness.exe   Headless Runtime / Core 管理 CLI
core/                与发布版本匹配的 Core
```

安装器提供“启用持久 Core”选项。

不启用时使用 **managed Core**：

- Desktop 启动 Harness Runtime 和 Core。
- Harness 退出时停止它启动的 Core。
- 适合只从 Harness 内部使用 Termcp 的用户。

启用后使用 **persistent Core**：

- Core 通过公开 daemon 命令独立运行。
- 默认端点为 `http://127.0.0.1:18765`。
- Harness 将其标记为 `external`，退出时不会停止 Core。
- 适合外部 Agent 先创建 Session，之后 Harness 再附带查看和接管的情况。

安装包的完整使用说明见 [desktop/installer/README.md](desktop/installer/README.md)。

## 持久 Core 快速操作

在安装目录或便携包目录执行：

```powershell
.\termcp-harness.exe core service install
.\termcp-harness.exe core service start
.\termcp-harness.exe core service status
```

停止：

```powershell
.\termcp-harness.exe core service stop
```

默认 profile：

```text
%APPDATA%\termcp-harness\core-service.json
```

默认 Core 数据目录：

```text
%USERPROFILE%\.termcp
```

安装器升级已有持久 profile 时，会先停止旧 Core、替换文件、重新校验版本与 SHA-256，再启动新 Core。Session 和输出历史保留，但 live SSH/PTY 会断开。

## 架构边界

```text
Agent / External Client
        |
        v
Harness Runtime ---- MCP Host ---- External MCP Servers
        |                  |
        | public HTTP/MCP  |
        v                  v
Termcp Core <--------------+
        |
        v
SSH / PTY / Remote Systems

Desktop Terminal Workspace
        |
        +-- fixed Go bridges --> Harness Runtime / Core
```

稳定内核包括 Agent Loop、工具流水线、策略、审批、事件、恢复和控制协议。模型、MCP Server、工具来源、UI、存储和桌面扩展属于可替换边缘。

## 开发环境

- Go 1.27
- `github.com/mark3labs/mcp-go v0.50.0`
- Wails v2.15.0
- Node.js 24
- React/TypeScript/Vite
- `@xterm/xterm` 5.5.0
- `@xterm/addon-fit` 0.10.0

若沙盒拒绝默认 Go 缓存，将 `GOCACHE` 和 `GOTMPDIR` 指向任务临时目录。

## 验证

Runtime：

```powershell
go test -race ./...
go vet ./...
go build ./...
go mod tidy -diff
```

Desktop 前端：

```powershell
Set-Location .\desktop\frontend
npm ci
npm run check
npm test
npm run build
```

Wails：

```powershell
Set-Location .\desktop
wails build -m -nosyncgomod -clean -s
```

不要使用 `-nopackage`：它会跳过 Windows 资源编译，导致 EXE 缺少项目图标等资源。

常用单测：

```powershell
go test ./internal/runtime -run '^TestRuntimeLifecycle$' -count=1
go test ./internal/coreservice -count=1
go test ./internal/coreinventory -count=1
go test ./internal/agentsession -run '^TestSessionModelLoop' -count=1
go test ./internal/eventlog -run '^TestDiagnosticEvictionIsVisible$' -count=1
go test ./cmd/termcp-harness -run '^TestRecovery' -count=1 -v
```

真实 Core 集成测试需要单独设置环境变量并准备专用 Session，命令示例见 [AGENTS.md](AGENTS.md) 和 [docs/contracts/core/v0.2.8/known-issues.md](docs/contracts/core/v0.2.8/known-issues.md)。

## 启动 Headless Runtime

忽略所有 Core 配置时，`serve` 使用原有受管 bundle 发现逻辑：

```powershell
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787
```

连接持久 Core profile：

```powershell
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --core-service-profile '<private-core-service-profile>'
```

显式连接外部 Core：

```powershell
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --core-endpoint http://127.0.0.1:18765
```

受保护 Agent 控制默认关闭，仅在显式提供受限 Bearer token 文件时启用：

```powershell
go run ./cmd/termcp-harness serve `
  --listen 127.0.0.1:8787 `
  --mcp-endpoint http://127.0.0.1:18765/stream `
  --tool-policy-file .\examples\tool-policy.readonly.json `
  --approval-token-file '<private-token-file>'
```

控制 API 只能绑定回环地址。Token 不得进入参数、普通日志、事件、URL、前端持久化或模型上下文。

## Desktop

Desktop 默认内嵌 Runtime，并使用相邻 `core/`：

```text
desktop\build\bin\
  termcp-harness-desktop.exe
  core\
    manifest.json
    termcp-core.exe
```

启动：

```powershell
.\desktop\build\bin\termcp-harness-desktop.exe
```

使用持久 Core profile：

```powershell
.\desktop\build\bin\termcp-harness-desktop.exe --core-service-profile '<private-core-service-profile>'
```

连接外部 Headless Runtime：

```powershell
.\desktop\build\bin\termcp-harness-desktop.exe --runtime http://127.0.0.1:8787
```

关闭窗口时：

- “最小化到任务栏”保留 Runtime 与任务。
- “退出 Harness”停止本次 Desktop 拥有的 Runtime；persistent Core 不会被停止。

## 发布构建

生成完整便携目录和 ZIP：

```powershell
.\desktop\scripts\package-release.ps1 -CoreSourceDir '<core-source-directory>'
```

开发候选必须显式允许 dirty 源：

```powershell
.\desktop\scripts\package-release.ps1 `
  -CoreSourceDir '<core-source-directory>' `
  -AllowDirty -SkipTests -SmokeTest
```

生成带持久 Core 选项的 NSIS 安装器：

```powershell
.\desktop\scripts\build-installer.ps1 `
  -PackageDir '<release-package-dir>' `
  -OutputDirectory '<output-dir>'
```

`build-installer.ps1` 需要 `makensis.exe` 在 PATH 中。安装器模板位于 `desktop/installer/`。

## 目录

```text
cmd/termcp-harness/   CLI 与 Headless Runtime 入口
internal/             Runtime、Core、MCP、工具、审批、事件和恢复实现
desktop/              Wails Desktop、固定桥、前端和发布脚本
docs/                 产品、架构、ADR 与公开契约
examples/             只读工具策略和 Agent 步骤示例
```

## 文档入口

- [docs/README.md](docs/README.md)：完整文档索引。
- [docs/product/VISION_v0.9.md](docs/product/VISION_v0.9.md)：产品定位和路线图。
- [docs/architecture/DESIGN_v0.4.md](docs/architecture/DESIGN_v0.4.md)：系统设计与安全约束。
- [docs/contracts/README.md](docs/contracts/README.md)：公开契约追踪规则。
- [docs/contracts/harness/v0.1/control-api.md](docs/contracts/harness/v0.1/control-api.md)：本地控制 API。
- [docs/contracts/harness/v0.1/core-instance.md](docs/contracts/harness/v0.1/core-instance.md)：Core 身份、所有权和持久 profile。
- [docs/contracts/harness/v0.1/agent-session.md](docs/contracts/harness/v0.1/agent-session.md)：Agent Session 与模型 Loop。
- [docs/contracts/harness/v0.1/terminal-bridge.md](docs/contracts/harness/v0.1/terminal-bridge.md)：终端游标与重连契约。
- [docs/adr/ADR-023-persistent-core-late-attach.md](docs/adr/ADR-023-persistent-core-late-attach.md)：持久 Core、迟附着和安装升级策略。

## 当前限制

- 模型 Adapter 仍以 OpenAI-compatible 非流式切片为主；其他 provider、流式输出、自动恢复和摘要未完成。
- 审批队列和 Event Journal 已淘汰区段不能跨重启完整恢复。
- Core 重启后的 DEAD Session 可发现，但完整只读历史终端视图尚未实现。
- Windows 登录自启动、托盘 Core 控制、崩溃自动重启和代码签名尚未实现。
- MCP 新增、编辑、移除、Registry 持久化和重启恢复属于 `v0.2.0`。

> [!CAUTION]
> **CORE 上游问题**：当前发布仍固定到本地 Core 集成基线。正式 Core 发布尚未包含全部审批关联和终端游标修复；升级或发布前必须核对 [known-issues.md](docs/contracts/core/v0.2.8/known-issues.md)，不得把本地验证写成正式 Core 已发布。

## 上游

- Core: <https://github.com/open-mcp-ai/Termcp>
- Desktop: <https://github.com/open-mcp-ai/Termcp-Desktop>
