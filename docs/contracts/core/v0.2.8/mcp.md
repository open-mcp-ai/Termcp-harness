# Termcp Core v0.2.8 MCP 观测契约

> 状态：真实 `initialize`、`tools/list`、只读 `term`、本机与 Kali POSIX 写入、副作用超时、MCP Host 断开和 review/approval 主流程已核验；已发现两个审批契约偏差，真实网络断连与 Core 重启恢复仍待验证。
>
> 未关闭上游问题、严格契约测试和关闭条件统一见 `known-issues.md`。

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

## 真实 Session 写入与超时观测

2026-10-28 通过 Harness 工具流水线和真实 MCP Host 验证：

- `term write` 对同一随机临时文件连续写入相同 sentinel 两次，最终文件只有一份 sentinel；这只证明最终状态幂等，不证明存在 idempotency key。
- `term run` 执行一次追加命令后，文件恰好包含一份 sentinel。
- `term run` 执行“追加后持续输出”的命令，并使用 750ms 父上下文截止时间，流水线进入 `unknown`；命令结束后文件仍只有一份 sentinel，证明 Harness 没有自动重试。
- 在 `term run` 进行中关闭对应 session，工具仍返回成功结果，而不是 MCP 传输错误；Harness 因此标记 `succeeded`。session close 不能可靠模拟传输断连，也不能用工具成功证明远端命令已结束。
- `term` 没有公开 operation ID、idempotency key 或结果查询接口；因此无法证明同 key 重试去重，也无法证明 `unknown` 后的人工重试安全。
- 2026-10-29 通过 Core 的 `kali-gc` 专用 session，在远程 Kali 的 `/tmp` 下重复同一组 POSIX 写入、副作用和 750ms 超时测试，结果与本机一致。远端只读 `ip -4 addr show` 当时显示 `192.168.1.6/24`，未显示 `192.168.174.129`。
- 2026-10-29 在 Kali 副作用执行期间主动关闭调用侧 MCP Host，工具流水线收敛为 `unknown`；随后使用第二个 MCP Host 和 Kali session 读回文件，sentinel 仍只有一份，且只记录一条 `tool.call.outcome_unknown`。这验证了客户端传输断开后的不重试路径，不等同于远程网络分区或 Core 进程故障。

## 真实 review / approval 观测

2026-10-29 通过 Kali POSIX 专用 session、MCP 工具流水线和公开 HTTP API 验证：

- `PATCH /api/sessions/{id}/approval` 能启用 review；`term write` 返回非错误文本 `TERM/1 202 Accepted`，正文包含 `Submitted for review:`，没有可轮询的审批 ID。
- 挂起写入没有创建文件。Harness 将该结果收敛为 `approval_pending`，不会误记为 `succeeded`。
- 关闭提交侧 MCP Host 后，请求仍保留在 `GET /api/approvals`；随后批准会成功执行并创建文件。第二次决定返回 `409`。
- 拒绝请求后文件不存在；随后批准同一请求返回 `409`。
- 单个请求并发提交批准和拒绝时，实测一个返回 `200`、另一个返回 `409`，最终状态和文件存在性一致。
- session 关闭后批准 pending 请求返回 `404` 或 `409`，文件不会创建，安全结果正确；但请求会从 `/api/approvals` 消失，文档承诺的 `cancelled` 状态不可查询。
- 设置 `timeout_seconds=1` 后等待 2 秒，请求在列表中仍为 `pending`；此时批准返回 `200` 并实际执行写入。公开文档声称过期 fail-closed，但当前实现的列举和决定路径没有在无 waiter 时结算过期，这是已确认的契约偏差。
- 审批请求没有 operation/correlation ID，因此 Harness 事件只能关联本地 call ID，不能声称与远端审批记录或后续执行形成可信因果链。
- 对执行中的 `term run` 取消本地调用上下文后，流水线收敛为 `unknown`；远端命令继续运行并完成了一次 sentinel 写入。取消不是远端终止保证，也不能据此认定命令未执行。
- 生成 20,000 行输出并提高返回窗口后，调用正常收敛为 `succeeded`。12 路并发只读 `term` 调用均成功，每个 call ID 都保留 `proposed → ready → dispatching → succeeded` 的控制事件顺序。

## 尚未验证

- 真实网络断连（区别于上下文超时和 session close）时，远端是否已经执行。
- 更强的远端取消/终止能力及服务端确认语义；当前观测只证明本地取消后远端可能继续。
- review 请求在 Core 重启后的最终状态与恢复方式。
- `202 review_pending` 是否总能与后续 shell 输出形成可信关联。
- 多客户端高并发决定审批时的压力与首个决定保证；当前只完成一次并发批准/拒绝和 12 路只读控制面实测。
- 审批转换写入 shell 字节日志的 `q` / `A` 标记。
- 更大规模输出洪峰、背压阈值和并发混合写操作。
- `term write` 在进程崩溃、断网或磁盘故障下的原子替换保证。
- auth、非回环绑定、Core 自动启动和停止的所有权行为。
