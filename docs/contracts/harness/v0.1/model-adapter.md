# Harness Model Adapter v1（草案）

> 状态：首个 OpenAI-compatible Adapter 与有界 Agent Loop 已由单元测试、受保护控制入口、DashScope 与 DeepSeek `deepseek-v4-pro` 真实模型及本机 Core MCP 完整闭环覆盖；其他 provider 与流式行为未验证。
>
> 日期：2026-10-31
>
> 实现：`internal/model`、`internal/agentsession/model_loop.go`

## 1. 边界

Model Adapter 只负责一次 completion。它不执行工具、不读取 MCP Host、不提交审批、不把模型文本提升为系统指令，也不决定工具调用是否成功。

Agent Loop 只接收显式 `ModelTool` 目录。生产装配必须从精确工具 allowlist 和当前已加载的工具 Schema 生成该目录；MCP 已连接、已启用或 Schema 已读取都不等于模型可调用。

## 2. OpenAI-compatible Adapter

配置：

- `endpoint`：完整 Chat Completions URL，必须使用 HTTPS；HTTP 仅允许 loopback。
- `model`：模型 ID，必填，最多 256 字符。
- `token`：可选 Bearer token，只通过受限文件读取并驻留内存。
- Token 不写入 endpoint、请求正文、事件、普通日志、模型上下文或控制 API 响应；POSIX 下 token 文件不得允许 group/other 访问。
- `timeout`：显式 completion 超时。
- `max_output_tokens`：可选输出 token 上限。

请求使用 `messages`、`tools`、`tool_choice=auto`。模型返回：

```json
{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": null,
        "tool_calls": [
          {
            "id": "provider-call-id",
            "type": "function",
            "function": {
              "name": "termcp-core__term",
              "arguments": "{\"cmd\":\"pwd\"}"
            }
          }
        ]
      }
    }
  ]
}
```

Adapter 会校验工具名、参数必须为 JSON object、工具调用数量和响应大小。Provider error 正文不进入 Harness 错误文本、事件或普通日志。

部分 OpenAI-compatible thinking 模型（已验证 DeepSeek `deepseek-v4-pro`）返回 `reasoning_content`，并要求后续 tool 轮次原样回填。Adapter 在内存 `Message` 中保留该字段并在下一轮 assistant 消息回填，但使用 `json:"-"` 禁止写入 transcript、Session Store、事件、控制 API 或普通序列化。它不是用户或系统指令，不能用于推断工具结果。

工具 `parameters` 只能是 MCP 的 `inputSchema`。完整 `mcp.Tool` 描述对象不能作为 JSON Schema 发送给 provider。

## 3. Agent Loop

- 每次请求前按完整 assistant/tool 轮次裁剪上下文，保留 system/user 和最新可容纳轮次；被省略轮次数写入事件。
- 模型每轮可返回文本、零到多个工具调用，或两者兼有。
- 一轮有工具调用时按顺序逐个进入现有工具流水线；每个工具结果以对应 call ID 回填为 `tool` 消息。
- 工具结果正文默认不进入模型上下文；同一 Server/Tool 的**全部**精确 `tool-policy` 规则都显式声明 `"model_result":"content"` 时才允许回填结果正文。只要任一规则未声明或为 `"none"`，模型只收到状态、错误标记和结果字节数。
- 审批挂起后 Session 进入 `waiting_approval`；可信批准只进入 `waiting_execution`。只有可信工具结果可继续 Loop。
- `unknown` 保持等待，不自重试；终态失败、阻断、取消、过期和预算耗尽分别收敛。
- 模型无工具调用且返回文本时，Session 完成并可返回该文本。
- 模型工具名在目录内稳定命名，通常为 `<server>__<tool>`；原始 Server/Tool 身份保留在 Runtime 绑定中。
- 默认上限为 128 个模型工具、8 次迭代、16 次工具调用、512 KiB 投影上下文、64 KiB 单次工具结果和 256 KiB 单次模型输出；请求参数可在契约范围内调整迭代/工具调用预算。

## 4. 事件

- `agent.model.requested`：迭代、消息数、省略轮数、上下文字节数和工具数。
- `agent.model.responded`：输出字节数和工具调用数。
- `agent.model.failed`：原因分类和错误字节数，诊断事件。

事件不包含模型正文、工具参数或工具结果正文。

## 5. 当前限制

- 已验证 DashScope 与 DeepSeek `deepseek-v4-pro` OpenAI-compatible 模式，包括 thinking 模型的 tool-call 续轮；其他 provider、流式输出和 provider 原生 tool-result 引用尚未验证。
- 不提供模型自动重试、成本核算、活动 Loop 自动恢复和摘要。
- `model_requesting` / `tools_ready` 的 Runtime 重启安全边界支持用户显式人工恢复；当前工具目录指纹或 transcript/参数 hash 不一致时拒绝恢复。自动恢复仍不提供。
- Desktop 可通过固定桥接创建、启动和人工恢复模型 Session，显示本轮最终回复、等待状态和调用摘要；配置 `--session-store-file` 后可读取终态 Session 的持久化模型消息和回复。模型 endpoint/凭据仍只能由 Runtime 启动参数配置。
- `reasoning_content` 不持久化，因此需要该字段续轮的 thinking provider 可能在恢复请求处失败；不得从文本、时间或摘要伪造该字段。
- `model_result=content` 是对精确 allowlist 规则的额外授权，不表示结果已脱敏；应只用于参数受限且输出内容适合模型读取的调用。

## 6. 验证记录

2026-10-31 使用 DashScope `compatible-mode/v1`、模型 `deepseek-v4.1-flash-1017` 完成：

- 非流式 Chat Completions 文本响应。
- 标准 OpenAI `tool_calls` 响应，工具名和 JSON 参数可被 Adapter 解析。
- Harness Agent Loop 两轮闭环：模型提议只读工具调用，Harness 通过内存工具返回固定结果，模型消费该结果并完成 Session。
- Control API → Harness MCP Host → 本机 Core `term` 只读指南 → DashScope 模型 → Session 完成的真实纵向闭环，事件中观察到模型请求/响应、工具成功和 Session 完成。
- 真实 Wails 窗口提交模型任务并显示最终回复；模型结果与所属 Session 绑定，切换 Session 后不会残留。

该记录只证明此模型和 endpoint 的实际兼容性，不推广为其他 OpenAI-compatible 服务的保证。

2026-10-31 另使用 DeepSeek `deepseek-v4-pro`（`https://api.deepseek.com/v1/chat/completions`）完成：

- 首轮标准 `tool_calls` 与第二轮带 `reasoning_content` 的工具结果回填。
- 内存工具 Provider Integration。
- Control API → Harness MCP Host → 本机 Core 只读 `term` 指南 → DeepSeek → Session completed 的完整 E2E。
- 修复 MCP 工具 Schema 误把完整 Tool 对象当 `parameters` 发送的问题；Regression 断言只暴露 `inputSchema`。
