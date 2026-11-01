# ADR-017：可替换模型 Adapter 与有界 Agent Loop

- 状态：Proposed / 首个纵向切片已实现
- 日期：2026-10-31
- 责任范围：Harness 模型边界、上下文投影、模型工具调用与恢复接入
- 关联：`ADR-011`、`ADR-012`、`docs/contracts/harness/v0.1/agent-session.md`、`docs/contracts/harness/v0.1/model-adapter.md`

## 1. 背景

Runtime 已有确定性 Agent Session、受控 MCP 工具流水线、审批执行结果消费和后续步骤恢复，但还没有模型驱动的规划循环。模型选择不能成为 Runtime 内核的硬依赖，模型输出也不能绕过现有验证、策略、审批和事件边界。

## 2. 决策

- 在 `internal/model` 定义最小 `Adapter` 契约，请求一次 completion；Adapter 不执行工具，也不解释审批或执行状态。
- 首个实现为 OpenAI-compatible Chat Completions HTTP Adapter，endpoint、模型 ID、可选 Bearer token、超时和输出预算均显式配置。
- `internal/agentsession` 只从调用方提供的显式 `ModelTool` 目录中选择工具；生产装配从精确 allowlist 和当前已加载 MCP Schema 投影工具，不把连接成功当成工具已授权。
- 模型返回的工具名和 JSON 参数按不可信输入重新校验；未知工具、非法参数、重复名称、预算超限和超大输出直接失败，不执行旁路。
- 工具调用仍进入 `internal/toolpipeline`。审批挂起、执行结果、`waiting_execution` 恢复和 `unknown` 不自动重试沿用既有状态机。
- 模型上下文只包含系统规则、用户输入、已发生的 assistant/tool 消息和显式工具定义；按完整 assistant/tool 轮次做有界投影，不按文本相似度推断缺失结果。
- 模型请求、响应和失败分别记录控制/诊断事件，只记录迭代次数、消息数、工具数、输出字节数和结果字节数，不记录模型正文、工具参数或结果正文。
- 部分 thinking provider 返回的 `reasoning_content` 仅在 Agent Loop 内存中保留并在下一轮回填；持久化、事件、控制 API 和普通 JSON 序列化必须排除。MCP 工具参数 Schema 只使用 `inputSchema`，不发送完整 Tool 描述对象。

## 3. 安全边界

- 模型不能创建工具、扩大 allowlist、跳过策略或直接调用 MCP Host。
- Token 只通过受限文件读入 Runtime 内存，不进入请求 URL、事件或模型上下文。
- 远程模型 endpoint 必须使用 HTTPS；HTTP 只允许 loopback。
- 模型工具结果进入下一步上下文前有字节上限，截断可见；持久化格式和恢复边界由 ADR-018 单独定义。
- `model_requesting` / `tools_ready` checkpoint 支持受保护、一次性领取的人工恢复；恢复前用当前工具目录和 transcript/参数指纹复验。自动恢复、活动工具派发中的继续、模型级重试和多 Agent 编排未实现。

## 4. 验证

- Adapter 请求结构、工具调用响应解析、非法参数、响应上限和端点约束测试。
- Session Loop 的工具成功后继续、审批挂起后恢复、未知工具不执行和上下文块投影测试。
- Control API 的受保护 `/loop`、CLI `agent loop` 和 control client 测试。
- 安全 checkpoint 人工恢复的 Session、Control API、Control Client、CLI 和 Desktop 固定桥测试；工具目录或 transcript 不一致时不得产生模型/工具调用。
- DashScope OpenAI-compatible 模式下使用 `deepseek-v4.1-flash-1017` 完成真实两轮工具调用闭环，并通过 Control API、Harness MCP Host 与本机 Core 只读 `term` 工具完成纵向验证。
- DeepSeek `deepseek-v4-pro` 完成真实工具续轮与 Control API + Core 只读 `term` E2E；修复 token/消息模式要求的 transient reasoning 回填，并增加“reasoning 不进入持久化/API”的回归测试。
- 真实 Wails 窗口已完成模型任务提交与最终回复显示；真实 Core 审批链和专用 fixture 的 `waiting_execution` 恢复按钮分别验证。
- Wails 生产构建不依赖模型 Adapter；Desktop 通过固定桥接提供模型任务启动台，但模型 endpoint/凭据仍只在 Runtime 启动时配置。

## 5. 尚未完成

- 其他第三方模型服务的质量、兼容性和流式响应验证。
- 模型流式输出、取消传播、重试策略和成本核算。
- 活动模型上下文自动恢复、不能证明未执行边界的恢复和摘要链；基础 transcript 持久化与安全边界人工恢复已由 ADR-018 接入。
- Desktop 侧模型 endpoint 配置和流式响应。
