# ADR-008：Terminal Workspace 技术栈决策与验证方案

- 状态：Accepted / 已确认（2026-10-28）
- 日期：2026-10-28
- 责任范围：第一方 Terminal Workspace；不覆盖 Harness Runtime 的部署或语言决策
- 关联：`docs/product/VISION_v0.9.md`、`docs/architecture/DESIGN_v0.4.md`、`docs/architecture/UPSTREAM.md`、`docs/adr/ADR-011-mcp-native-agent-runtime.md`

## 1. 背景

本 ADR 决策时 Harness 还是空工作区，没有 manifest、代码或可执行测试命令，因此不能把同机 Desktop、旧 Harness 或任何历史实现当作既定技术基线。2026-10-28 后续已出现 Headless Runtime bootstrap，见 `ADR-012`；本 ADR 仍只约束 Terminal Workspace。

已知产品约束：

- Harness 由 Headless Runtime 与第一方 Terminal Workspace 分层组成。
- 本 ADR 只决定 Terminal Workspace 的桌面技术栈，不决定 Runtime 的进程模型、协议或部署方式。
- 终端是主工作区，需要稳定、有界、可测量的高频流处理。
- Runtime 需要连接 Core 的公开 HTTP/WebSocket/MCP 契约；Terminal Workspace 通过 Runtime 控制契约附着。
- Runtime 需要管理 Harness 明确拥有的 Core 子进程。
- Runtime 与 Terminal Workspace 需要各自明确的配置、诊断、脱敏和安全桥接边界。
- Runtime 需要 MCP Client/Host、工具策略、Session、事件和恢复能力；Terminal Workspace 通过公开控制契约附着。
- 长期可能加入插件运行时、商城和桌面宠物。
- 不为暂停中的 Web 预建跨端通用框架。

## 2. 决策目标

技术栈需要同时满足：

1. 终端 UI 与高频输出性能。
2. Go/Core 生态集成效率。
3. 本地进程与文件操作能力。
4. 安全边界和未来插件隔离空间。
5. Windows 桌面打包、更新和诊断能力。
6. 测试、可重复构建和团队维护成本。
7. 未来 MCP/插件生态的可扩展性。

不能只用“能启动窗口”或“UI 好看”作为选型证据。

## 3. 候选方案

### 方案 A：Wails + Go + React/TypeScript + `@xterm/xterm`

**定位：已选技术栈，仍需完成 PoC 验证。**

优点：

- 与 Termcp Core 的 Go 生态最接近。
- 同机 Termcp-Desktop 和旧 Harness 已有 Wails + Go 经验，可参考但不必照搬。
- Go 适合进程管理、WebSocket、配置、日志和本地能力。
- React/TypeScript 生态适合终端工作台、面板和未来商城界面。
- `@xterm/xterm` 是成熟的前端终端渲染方案。
- 单二进制打包和开发链路相对直接。

风险：

- Wails 不自动提供插件安全沙箱。
- Go 与前端桥接的调试、错误传播和生命周期需要严格设计。
- 不同平台 WebView 差异需要实际回归。
- 未来插件运行时仍要单独设计隔离方案。

适用判断：如果团队希望最大化复用 Go 经验、快速完成可信 Terminal Workspace MVP，这是优先验证方案。

### 方案 B：Tauri + Rust + React/TypeScript + `@xterm/xterm`

**定位：安全边界与安装体积优先的候选。**

优点：

- Rust 后端适合对进程、文件、IPC 和资源边界做细粒度限制。
- Tauri 的权限/capability 模型更适合长期插件化产品。
- 安装体积和运行资源通常优于 Chromium 系方案。
- React/TypeScript 与前端终端生态可复用。

风险：

- 与 Go/Core 技术栈存在双语言成本。
- MCP Host、终端辅助和进程管理需要更多 Rust 实现或 sidecar。
- 团队若缺少 Rust 经验，迭代和故障定位成本更高。
- 安全收益必须通过实际配置和测试证明，不能只看框架宣传。

适用判断：如果安全隔离、插件权限和分发体积比初期开发速度更重要，应进入 PoC 对比。

### 方案 C：Electron + TypeScript/Node.js + React + `@xterm/xterm`

**定位：UI 与生态开发速度优先的候选。**

优点：

- 前端生态成熟，终端、面板、插件和商城 UI 开发速度快。
- Node.js 侧实现 MCP Client、进程管理和本地配置较直接。
- 调试工具和打包方案成熟。

风险：

- 内存、启动和安装体积通常高于原生壳方案。
- Chromium、Node 和插件共同扩大攻击面。
- 上下文隔离、IPC 白名单、CSP、签名和更新必须严格设计。
- 对“高可靠终端客户端”的产品定位可能偏重。

适用判断：如果首要目标是快速验证复杂 UI 和工具生态，而不是资源占用与最小攻击面，可做对照 PoC。

### 方案 D：Avalonia + C#/.NET

**定位：Windows 原生体验优先的候选。**

优点：

- 原生桌面体验、窗口和系统集成能力较好。
- .NET 异步、进程管理和打包工具成熟。
- 适合 Windows-first 的产品和桌面宠物等体验。

风险：

- 与 Go/Core 生态的语言距离较大。
- 终端渲染、Web 技术复用和 MCP 工具生态可能需要额外集成。
- 跨平台和 Linux/macOS 支持成本需要单独验证。

适用判断：如果明确以 Windows 原生体验为首要目标，且愿意承担与 Core 技术栈分离的成本，可评估。

## 4. 决策

**已选择方案 A，作为第一方 Terminal Workspace 的技术栈：Wails v2 + Go + React/TypeScript/Vite + `@xterm/xterm`。**

该决策不意味着：

- Harness Runtime 必须运行在 Wails 或桌面进程内。
- Runtime 必须使用同一进程模型或同一部署产物。
- Terminal Workspace 可以绕过公开控制契约直接控制 Agent Loop 或工具流水线。
- 插件、代码执行和外部 MCP 已具备安全隔离。

决策日期：2026-10-28。

已确认的初始基线：

- 桌面壳：Wails v2，初始基线 v2.15.0。
- 后端：Go 1.27.x。
- 前端：React + TypeScript + Vite。
- 终端：`@xterm/xterm` 及必要 addons。
- 运行时：系统 WebView，不打包 Chromium。
- 当前开发机工具链：Go 1.27.2、Wails v2.15.0、Node 24.21.0、npm 11.19.0。

选择依据：

- 与 Core、Desktop 的 Go 技术生态一致。
- 适合 Core 子进程管理和公开契约通信。
- React/TypeScript 能支撑终端工作台、MCP/插件管理和未来商城界面。
- 可以先用最小 PoC 验证终端流、桥接和打包，而不提前建设插件运行时。

这是基于技术方向与团队约束的确认，不是性能基准结论。Tauri 仍作为安全边界对照组保留；只有 PoC 证明 Wails 方案无法满足明确指标时，才重新打开选型。

## 5. 必须完成的 PoC

在开始正式功能开发前，至少验证：

1. Terminal Workspace 通过 Runtime 控制契约连接 Headless Runtime，并能显示连接状态。
2. 接收高频终端输出，保持字节顺序、有界缓冲和可测量延迟。
3. Runtime 作为独立进程启动、监控、优雅关闭，并在 Terminal Workspace 关闭后继续运行。
4. 配置扫描与敏感字段脱敏。
5. 前端与后端桥接的错误、超时和进程退出传播。
6. 打包后的安装、升级、日志和诊断导出。
7. 模拟 MCP 能力、工具调用、审批和恢复的隔离边界，不提前实现完整插件商城。
8. 单元测试、契约测试和单条测试命令的可执行性。

## 6. 验收指标

选型结果必须记录：

- 启动时间和安装体积。
- 持续输出下的内存峰值与输入延迟。
- 会话切换和窗口响应。
- 进程崩溃、断连和重启恢复表现。
- 桥接权限和敏感数据暴露面。
- 测试、构建和打包的可重复性。
- 未来插件/MCP 扩展需要重构的范围。

具体阈值应由实测基线确定，不在本 ADR 中预先编造。

## 7. 决策结果与后续动作

当前状态：**Accepted，已选定方案 A。**

在开始正式功能开发前，仍需完成：

1. 确认 Harness 仓库、Runtime 与 Terminal Workspace 的模块路径和发布边界。
2. 分别初始化 Headless Runtime 与 Wails v2 Terminal Workspace 的最小工程。
3. 写入真实依赖版本、锁文件和可执行命令。
4. 完成 ADR 第 5 节的 PoC，并记录实际结果。
5. 在 README 和 `AGENTS.md` 中登记安装、构建、测试和单条测试命令。
6. 将首批 Core 契约固定到明确版本或 commit SHA。

未完成上述步骤前，不得宣称项目脚手架或 Core 兼容性已经验证。
