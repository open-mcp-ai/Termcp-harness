# Termcp Harness Web 暂停策略

> 状态：Deferred（暂停，不删除、不否定）
>
> 日期：2026-10-28
>
> 关联：`docs/product/VISION_v0.9.md`、`docs/architecture/DESIGN_v0.4.md`
>
> 本文只说明 Web 的当前处理方式，不定义 Runtime 或 Terminal Workspace 的实现计划，也不产生当前开发任务。

## 1. 当前结论

**Harness Runtime 与 Terminal Workspace 是当前开发主线。Harness Web 暂停新增功能，保留为既有成果与设计参考。**

“暂停”表示：

- 不进入当前路线图。
- 不新增 Web 专属功能。
- 不为了未来 Web 预建跨端通用框架。
- 不因 Web 需求拆分 Runtime、Terminal Workspace 的模块、状态机或 UI。
- 不删除历史成果，也不宣布永久放弃 Web。

## 2. 需要区分的三个概念

1. **Harness Web**：Harness 自己的 Web 客户端。当前暂停。
2. **Core Web UI**：Core 自带的人类操作界面。由 Core 负责，不是 Harness Web。
3. **Core WebSocket/HTTP 契约**：Terminal Workspace 和未来 Web 都可能使用的公开通信接口。当前仍然重要。

WebSocket 不等于 Web 客户端。Terminal Workspace 仍需要核对和使用 Core 的公开 WebSocket/HTTP 契约。

## 3. 暂停原因

不是因为这些方向没有价值，而是因为当前资源必须优先保证：

- Runtime 的 Agent Loop、MCP 工具、策略、事件与恢复可靠。
- 连接、Core 实例和子进程生命周期明确。
- 审批、断连、超时和结果未知状态不误导用户。
- 来源关联、输出定位和历史回看有真实证据支撑。
- 契约测试、平台回归和性能基线建立起来。
- Terminal Workspace 技术栈已经确定，当前应集中完成 Headless Runtime、MCP 工具链和 Wails/Go/React PoC，不适合同时维护两套客户端。

## 4. 对当前开发的影响

### 4.1 允许

- 保留现有 Web 代码或文档作为参考。
- 提取经过验证的产品规则、协议定义和测试样例。
- 为未来复用保留清晰的业务边界，但不为了复用强行抽象。
- 在 Terminal Workspace 中遵守 Runtime 控制契约和 Core 公开 HTTP/WebSocket 契约。

### 4.2 不允许

- 因“以后可能有 Web”而推迟 Runtime 或 Terminal Workspace 的必要设计。
- 为 Web 预建跨端组件库、状态框架或平台适配层。
- 在 Vision 或路线图中把 Web 写成当前交付目标。
- 让 Web 文档影响 Runtime 或 Terminal Workspace 的优先级。
- 把 Core Web UI 的职责和 Harness Web 混为一谈。

## 5. 何时重新评估

只有在 Runtime 与 Terminal Workspace 核心工作流稳定后，才重新评估 Web。至少检查：

- 是否存在 Terminal Workspace 不能满足的真实使用场景。
- Core 自带 Web UI 是否已经满足需求。
- Harness Web 相对 Core Web UI 的独立价值是什么。
- 现有 Web v1 哪些部分可复用，哪些应重写。
- 远程访问、认证、部署、升级和运维成本是否可接受。
- 恢复 Web 是否会拖慢 Runtime 和 Terminal Workspace 的可靠性与安全建设。
- 当前技术栈是否适合 Web，还是需要独立架构。

## 6. 如果恢复 Web

恢复不是自动继续旧实现，而是一次新的产品决策。可以：

- 重新设计界面和交互。
- 重新选择适合 Web 的技术栈。
- 复用经过验证的业务规则、契约和测试。
- 不承诺复用 Terminal Workspace 的布局、组件或运行时架构。
- 明确 Web 与 Core Web UI 的差异化价值。

恢复前必须记录新的范围、技术方案、认证模型、数据边界和验收条件。

## 7. 文档规则

- `docs/product/VISION_v0.9.md` 只说明“Web 暂停，见本文”，不展开 Web 路线图。
- 本文不维护 Web 具体功能清单。
- 若将来恢复 Web，应将本文改为新的决策文档或升版，而不是悄悄把 Web 功能塞回 Vision。
- 当前任何 Agent 不得以本文为由削弱 Runtime/Terminal Workspace 对公开 WebSocket/HTTP 契约的支持。
