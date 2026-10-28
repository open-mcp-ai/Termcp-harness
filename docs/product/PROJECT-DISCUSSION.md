# Termcp Harness 项目讨论稿

> 用途：首次向同事介绍项目、发起方向讨论，不替代 Vision、Design 或 ADR。
>
> 状态：讨论稿（2026-10-28，对应 `docs/product/VISION_v0.9.md`）
>
> 建议：群里先发一段介绍和一页简报；完整文档只在对方愿意深入时再提供。

## 1. 推荐发送方式

不要直接发送整套文档。建议分三层：

1. **群消息：** 一句话定位 + 当前阶段 + 3 个待讨论问题。
2. **一页简报：** 问题、产品层次、与 Desktop/DeepSeek Harness 的差异、MVP。
3. **深入资料：** Vision、Design、ADR、契约文档和仓库。

首次讨论的目标不是让大家读完文档，而是确认：

- Harness 是否应该作为独立 Agent Runtime 存在。
- 人机共享终端是否是正确切入点。
- 首个 MVP 是否应优先验证 Headless Runtime 和 MCP 工具链。

## 2. 可直接发群里的消息

> 大家好，我想同步一下 Termcp Harness 的新定位，想听听大家对边界的意见。
>
> 一句话：Harness 不再定位成第二个 Desktop，而是 **MCP 原生的 Agent Runtime 与人机共享的终端工作台**。它管理 Agent 执行、MCP 工具、上下文、会话、策略、操作轨迹和恢复，并提供一个终端工作台让人和 Agent 在同一个执行现场协作。
>
> 三个平面：
> 1. Core 负责 SSH、PTY、Shell 和真实执行。
> 2. Harness Runtime 负责 Agent Loop、MCP Client/Host、工具策略、Session、事件和恢复。
> 3. Terminal Workspace 负责终端、审批、接管、操作时间线和 MCP 状态。
>
> 和 Termcp-Desktop 的区别是：Desktop 重点解决人如何操作终端；Harness 重点解决 Agent 如何通过 MCP 执行任务，以及人如何与 Agent 共用同一执行现场。和 DeepSeek Harness 一类产品的区别是：我们不追求“一切皆插件”，而是稳定内核、开放边缘，并且必须可 headless 部署、可被其他 UI/Agent 通过公开契约接入。
>
> 当前还在文档与契约阶段，代码尚未开始。第一方终端 UI 暂定 Wails v2 + Go + React/TypeScript/Vite + `@xterm/xterm`，但 Runtime 的部署和协议仍需 PoC。
>
> 想先讨论三个问题：
> 1. Harness 是否应作为独立 Headless Runtime，而不是只做桌面 UI？
> 2. 首个 MVP 是否只验证一个 Core、一个外部 MCP、一个 Agent Session 和一个终端？
> 3. Terminal Workspace 直连 Core 终端流，还是经 Harness 转发？这会直接影响性能和策略覆盖。
>
> 安装器暂定采用“全部模块可见、推荐默认勾选、所有复选框可取消”的方式；L0-L3 只用于必要程度和场景分组，不形成固定产品档位。
>
> 我先发一页简报，完整文档按需展开。

## 3. 一页项目简报

### 项目是什么

Termcp Harness 是一个 MCP 原生 Agent Runtime 与终端工作台：

- 统一管理 Agent 执行、MCP 工具、上下文和会话。
- 在工具调用前执行策略和审批，在调用后验证并记录结果。
- 区分成功、失败、取消、过期和结果未知。
- 让终端、工具调用、审批和输出进入同一操作时间线。
- 让 Headless Runtime 与 Terminal Workspace 解耦。
- 用一份模块目录承载所有能力，用户通过复选框自由组合。
- 根据使用场景预勾选推荐模块，但所有推荐都可以取消。
- 通过公开协议接入 Agent、MCP Server、工具和第三方 UI。

### 不是什么

- 不是第二个 Core。
- 不是另一个完整 SSH 管理器。
- 不是只展示 Agent 日志的桌面 UI。
- 不是绑定单一模型厂商或固定规划范式的自主 Agent。
- 不是“一切皆插件”的通用 Agent 框架。
- 不复制 SSH、PTY、会话、审批或权限执行链路。

### 三个平面

| 平面 | 角色 |
|---|---|
| Core | SSH、PTY、Shell、会话与真实执行 |
| Harness Runtime | Agent Loop、MCP Host、工具策略、Session、事件、恢复 |
| Terminal Workspace | 终端、审批、接管、操作时间线和 MCP 状态 |

### 与现有产品的差异

- **相对 Termcp-Desktop：** Desktop 是人的终端工作台；Harness 是 Agent 运行时和人机共享执行现场。
- **相对 Core Web UI：** Harness 管理 Agent、MCP 和工具执行链，不只是操作单个 Core 界面。
- **相对 DeepSeek Harness：** 不追求一切皆插件；强调 MCP 原生、稳定内核、开放部署、终端现场和可恢复执行。

### 安装与组合

- Harness Shell 固定存在，提供展示、模块目录、布局和恢复入口。
- 所有功能模块都显示复选框，包括 L0 模块。
- L0-L3 只表示必要程度和适用场景，不是产品版本或权限等级。
- 推荐根据使用场景预勾选，但可以逐项取消、全选、全不选和恢复推荐。
- 安装后从同一目录继续补装模块。
- 当前模块组合与 Workspace 可以保存、导入、导出和分享。
- 复选框只代表选择；是否已安装、已启用、正在运行和能力就绪需要分别显示。

### 当前核心工作流

> 启动 Headless Harness → 连接 Termcp Core 与工具来源 → 创建 Agent Session → 打开关联终端 → Agent 发起工具调用 → 策略/人工审批 → 观察输出 → 记录成功/失败/未知 → 必要时接管或恢复。

### MVP 范围

计划包含：

- 一个 Headless Harness Runtime。
- 一个 Agent Session。
- Termcp Core MCP 连接。
- 至少一个外部 MCP Server 或等价工具来源。
- 工具调用前的策略、人工审批和调用后验证。
- 一个终端工作台，与 Agent Session 关联。
- 工具调用、审批和输出时间线。
- 结果未知、断连和恢复状态。
- 基础 MCP 状态面板。

暂不承诺：

- 完整 SSH 管理器。
- 多窗格、SFTP 和端口转发全套体验。
- MCP/插件商城。
- 任意代码插件运行时。
- 桌宠、主题和完整模块市场。
- 多 Agent、多 Core 或团队控制面。

### 技术方向

- Terminal Workspace：Wails v2 + Go + React/TypeScript/Vite + `@xterm/xterm`。
- Runtime：headless 优先；已有 Go bootstrap，但核心依赖、进程、协议、跨平台和打包方式仍待 PoC。
- 公开契约：MCP、HTTP、WebSocket、CLI/API。
- 不 import Core 内部包。
- 状态、事件和插件边界必须可测试、可导出、可降级。

## 4. 分阶段讨论脚本

### 阶段 1：产品定位

**目标：** 确认 Harness 是否应作为独立 Runtime。

讨论问题：

- 用户为什么需要 Harness，而不是 Desktop + 外部 Agent？
- Harness 的核心价值是 Agent 执行、MCP 管理、人机协作，还是终端体验？
- 哪些功能明确不应该进入 Harness？

预期产出：

- 一句话定位。
- 三个核心用户任务。
- 三条明确非目标。

### 阶段 2：运行时边界

**目标：** 确认 Runtime 与 UI、Core、Agent 的关系。

讨论问题：

- Runtime 是否必须支持 headless？
- Agent Loop 由 Harness 内置、外部 Agent 提供，还是两者都支持？
- Terminal Workspace 关闭后 Runtime 是否继续？
- Runtime 如何向第三方 UI 暴露控制 API 和事件流？

预期产出：

- 三平面责任表。
- Runtime 生命周期。
- 首批公开接口。

### 阶段 3：MCP 与工具链

**目标：** 定义 MCP 控制面和工具调用安全边界。

讨论问题：

- Harness 是否作为 MCP Client/Host 连接 Core 和外部 MCP？
- 如何处理工具名冲突、能力变化和断连？
- 工具调用前策略、审批和后验证分别由谁负责？
- 哪些调用可以重试，哪些必须标记结果未知？

预期产出：

- MCP 状态机。
- 工具调用状态机。
- 策略和审批责任表。

### 阶段 4：终端工作台

**目标：** 验证人机共享终端的真实价值。

讨论问题：

- 终端工作台直连 Core，还是经 Harness 转发？
- 第一版是否只需要一个终端，而不是完整 SSH 管理器？
- 人工接管、继续和停止的实际保证是什么？
- 工具调用时间线如何与终端输出关联？

预期产出：

- 一条端到端用户流程。
- 终端传输方案。
- MVP 验收条件和性能基线。

### 阶段 5：开放部署与扩展

**目标：** 确认“开放”的产品边界。

讨论问题：

- 首发部署形态是什么：CLI、daemon、容器还是桌面？
- 第三方 UI/Agent 如何接入 Harness？
- 插件是否是 MCP，还是需要额外 SDK？
- 模块安装是否必须真正减少安装包体积，还是首版允许配置开关？
- 推荐规则是否采用透明规则匹配，并允许用户完全覆盖？
- 全部功能模块取消时，Shell 应提供哪些恢复入口？
- 状态、配置和日志如何导出？

预期产出：

- 公开契约清单。
- 部署矩阵。
- 插件与模块边界。

### 阶段 6：发布与协作

**目标：** 确认开发和发布节奏。

讨论问题：

- 首发平台和 Runtime 部署目标是什么？
- 是否需要 nightly/preview？
- 谁负责 Core 契约变更和兼容性测试？
- 发布签名、更新和回滚怎么做？

预期产出：

- 发布矩阵。
- 分支与 PR 规则。
- 负责人和评审责任。

## 5. 首次讨论建议只问三个问题

如果会议只有 10–20 分钟，只讨论：

1. Harness 是否应该作为独立 Headless Runtime，而不是仅做 Desktop UI？
2. 首个 MVP 是否只验证一个 Core、一个外部 MCP、一个 Agent Session 和一个终端？
3. Terminal Workspace 直连 Core 终端流，还是经 Harness 转发？

这三个问题会直接影响产品定位、架构和排期。

## 6. 不建议首次发送的内容

- 完整 Vision 和全部 ADR。
- 未确认的 Core 内部细节。
- 复杂插件/商城路线图。
- 大量分支列表。
- 私有凭据、内部地址或未脱敏日志。

完整文档适合在第一次讨论后按主题发送。
