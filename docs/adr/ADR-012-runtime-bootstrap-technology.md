# ADR-012：Headless Runtime Bootstrap 技术选择

- 状态：Proposed / 已实现 bootstrap，待 PoC 与跨平台验证
- 日期：2026-10-28
- 责任范围：Harness Runtime 核心实现语言与进程内基础组件
- 关联：`docs/product/VISION_v0.9.md`、`docs/architecture/DESIGN_v0.4.md`、`docs/adr/ADR-008-desktop-technology-stack.md`、`docs/adr/ADR-011-mcp-native-agent-runtime.md`

## 1. 背景

Runtime 必须能够 headless 运行，同时承担 Agent Loop、MCP Client/Host、工具流水线、策略、Session、事件和恢复。架构文档此前将实现语言留待 PoC，但继续停留在文档阶段无法验证状态机、超时和进程边界。

本 ADR 记录 bootstrap 选择，不等同于长期部署形态已经确认。

## 2. 暂定决策

Harness Runtime 的核心使用 Go 1.27 实现：

- Runtime 核心与本地控制面优先使用 Go 标准库。
- `cmd/termcp-harness` 提供 CLI 与 headless 入口。
- Runtime、MCP 控制面和 Terminal Workspace 保持包/进程边界，不因同语言而混成单一模型。
- 在 MCP SDK、事件与持久化 PoC 完成前，不引入框架或依赖注入容器。

选择 Go 的理由：

- 与同机 Core、Desktop 和旧 Harness 的工具链一致，便于使用公开契约，但不复用其内部包作为基线。
- 能产出无 Node 运行时依赖的 headless 可执行文件。
- 标准库已覆盖当前本地 HTTP 控制面、并发同步和进程生命周期需求。
- 后续仍需通过 MCP 客户端、stdio 子进程和跨平台测试验证，而不是仅凭编译通过认定可行。

## 3. 当前 bootstrap 边界

已实现：

- Runtime 实例生命周期与并发安全快照。
- 只绑定回环地址的本地健康/状态 API。
- HTTP 优雅关闭与 Runtime 状态收敛。
- 单元测试、race、vet、build 和进程级冒烟路径。

未实现或未验证：

- Agent Loop、MCP Client/Host、工具流水线和 Session。
- 配置、持久化、事件日志和恢复。
- Linux/macOS 构建与运行。
- 远程 API、认证、TLS 和多客户端语义。
- 容器、daemon supervisor 或 Wails 集成。

## 4. 被否决或暂缓的方案

- 暂不选 Node.js/TypeScript 作为 Runtime 核心：需要单独评估 MCP SDK 生态与发行约束，不能仅因 UI 使用 TypeScript 就统一运行时。
- 暂不选 Rust：安全与性能潜力需要和开发成本、现有 Core 集成成本一起验证，当前没有证据支持额外复杂度。
- 不直接把旧 Harness 或 Desktop 的进程内架构作为基线：它们是行为参考，不是本工作区架构基线。

## 5. 接受条件

本 ADR 从 Proposed 转为 Accepted 前至少需要：

1. 使用标准 MCP stdio Server 完成连接、能力读取、停用和断连 PoC。
2. 验证子进程拥有关系、取消、退出与结果未知语义。
3. 在 Windows 与至少一个非 Windows 平台跑通同一构建和测试基线。
4. 评估 MCP SDK 依赖、协议版本和未来升级路径。
5. 明确本地控制 API 的版本化、认证和远程暴露边界。

## 6. 影响

当前代码是实验性 bootstrap，可以在保持公开边界可替换的前提下继续演进。任何后续把 Runtime 暴露为远程服务、持久化执行事实或引入第三方插件执行器的改动，都必须单独经过架构与安全评审。
