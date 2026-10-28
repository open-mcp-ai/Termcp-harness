# Termcp Core v0.2.8 MCP 观测契约

> 状态：真实 `initialize` 与 `tools/list` 已核验；工具执行与审批故障语义仍待集成测试。

## 传输

实例 `/api.md` 声明以下 MCP 入口：

- SSE：`GET /sse` + `POST /message`
- Streamable HTTP：`/stream`
- stdio：`termcp stdio` 或 `termcp daemon stdio` 作为到 HTTP 端点的桥

Harness 当前使用 Streamable HTTP `/stream`。

## Initialize

2026-10-28 手工请求协议版本 `2025-06-18` 时，实例接受并返回同版本；Harness 使用 `mcp-go v0.50.0` 的默认最新版本探测时，实际协商为 `2025-11-25`。因此客户端必须保存服务端返回的版本，不能假定请求版本就是最终版本。

请求 `2025-06-18` 时的关键结果：

```json
{
  "protocolVersion": "2025-06-18",
  "capabilities": {
    "prompts": {},
    "tools": {"listChanged": true}
  },
  "serverInfo": {
    "name": "termcp",
    "version": "v0.2.8"
  }
}
```

Harness CLI 使用 SDK 默认版本时观察到：

```json
{
  "server_name": "termcp",
  "server_version": "v0.2.8",
  "protocol_version": "2025-11-25",
  "tools_supported": true,
  "tool_count": 1,
  "tools": ["term"]
}
```

实际响应还包含较长的 `instructions`。该文本属于服务端提供的不可信模型输入，不应被 Harness 提升为系统指令。

## Tools

`tools/list` 在该实例上只返回一个工具：`term`。

`term` 的输入 Schema 公开以下属性：

- `cmd`
- `timeout`
- `bytes_limit`
- `lines_limit`
- `total_bytes_limit`
- `on_time_overflow`
- `on_bytes_overflow`
- `on_lines_overflow`
- `learn_mode`
- `structured_content`
- `args`
- `vars`
- `secrets`

所有属性都不是 Schema 必填项。工具描述说明空 `cmd` 返回引导信息，并公开当前实例 Web UI 地址。

Harness 已通过 MCP `CallTool` 调用 `term` 且传入空 `cmd`，收到非空、非错误内容。该调用只读取工具内置引导，不创建终端 Session，不执行宿主机命令；它证明 MCP 工具派发与结果解码可用，不证明终端执行语义。

实测 `annotations` 为空。Schema 没有声明只读、幂等、去重键或副作用等级，因此 Harness 不得自行推断 `term` 是只读工具；默认副作用类别应为 `unspecified`，自动重试应被拒绝。

## 已由实例文档公开、但尚未由 Harness 集成验证的语义

- review mode 会拦截 AI 经 MCP 发起的终端输入、文件传输和端口转发。
- 被 review 持有的写入返回 `202` 与 `review_pending`，不产生可轮询的审批 ID。
- 审批队列跨 session 提供 `GET /api/approvals`，决定由 `/approve` 或 `/reject` 提交。
- 审批首个决定生效；拒绝为终态；过期 fail-closed；session 死亡会取消待决请求。
- 审批状态包括 `pending|approved|rejected|expired|cancelled`。
- 审批转换写入 shell 字节日志的 `q` / `A` 标记。

这些是当前实例自报的公开行为说明，不等于 Harness 已证明其断连、并发、原子性或恢复实现。

## 尚未验证

- 派发后超时或断连时，远端是否已经执行。
- 取消请求是否终止已开始的工具调用。
- review 请求在 Core 重启、session 关闭或客户端断连后的最终状态与恢复方式。
- `202 review_pending` 是否总能与后续 shell 输出形成可信关联。
- 多客户端并发决定审批时的服务端原子性。
- 带真实 Session 的 `term` 执行、长任务、输出洪峰与控制事件顺序。
- auth、非回环绑定、Core 自动启动和停止的所有权行为。
