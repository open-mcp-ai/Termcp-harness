# Harness MCP Server 状态 v1（草案）

> 状态：持久 Streamable HTTP、SSE、stdio、受管 stdio 子进程、能力读取、启用/停用、健康检查、退避重连、能力自动刷新、Bearer 凭据引用、本机 Codex global MCP 发现和受保护显式注册已接入。
>
> 实现：`internal/mcpcontrol`、`internal/mcpdiscovery`

## 1. 五个独立维度

一个 MCP Server 不能用单一 `connected` 或 `enabled` 布尔值表示。当前模型分开记录：

| 维度 | 状态 |
|---|---|
| 登记 | `discovered`、`registered` |
| 连接 | `not_attempted`、`connecting`、`connected`、`disconnected`、`unavailable` |
| 能力 | `not_loaded`、`loading`、`loaded`、`stale` |
| 启用 | `disabled`、`enabled` |
| 所有权 | `external`、`managed` |
| 传输 | `streamable-http`、`sse`、`stdio` |

`ready` 是派生值，只有同时满足以下条件才为真：

- 已登记；
- 连接为 `connected`；
- 能力为 `loaded`；
- 启用为 `enabled`。

复选框、登记、连接和能力读取都不能单独冒充 `ready`。

## 2. 强制规则

- `discovered` 不能直接连接；必须先进入 `registered`。
- 未连接时不能读取能力。
- 能力未读取时不能启用。
- 断连或不可用会把 `loaded` 能力标为 `stale`，旧的工具 Schema 不能继续视为可用。
- 连接丢失保留用户的 `enabled` 偏好，但 `ready=false`；恢复后必须重新读取能力。
- `managed` 与 `external` 描述所有权，不表示网络健康、权限或启用状态。
- stdio 强制为 `managed`；子进程由 mcp-go transport 持有，断连/关闭时终止并回收。
- 失败原因只保存受限枚举，不保存任意服务端错误文本，避免凭据或终端内容进入状态。
- 状态更新使用 revision compare-and-swap，旧连接或旧能力结果不能覆盖新状态。
- 能力读取后的 `ToolDescriptor.Schema` 必须是 MCP `inputSchema` 本身；禁止把 `{annotations,description,inputSchema,name}` 完整 Tool 对象作为参数 Schema 投影给模型或调用方。

## 3. 当前网络边界

v1 注册表与探测器只接受 HTTP/HTTPS 回环 endpoint，并拒绝 URL userinfo、query 和 fragment。该限制避免在 URL、日志或快照中携带凭据。

## 4. 持久 Host

`internal/mcpcontrol.Host` 持有真实 MCP 客户端，并按 Server 串行化生命周期操作：

```text
Register
  -> Connect（协议初始化）
  -> LoadCapabilities（读取工具与 Schema）
  -> Enable（加入当前 Runtime）
  -> Ready
```

- `Connect` 成功不会自动产生 `Ready`。
- `LoadCapabilities` 失败会把连接标为 `unavailable`，不保留可用工具引用。
- `Disable` 保留连接和能力，但从工作区撤销能力。
- `Disconnect` 关闭客户端并保留用户的启用偏好，能力变为 `stale`。
- `notifications/tools/list_changed` 先把能力标为 `stale` 和 `Ready=false`；supervisor 随后自动重读能力，不继续使用旧 Schema。
- 并发 `Connect` 只建立一个客户端；旧异步结果不能覆盖更新的 revision。
- 生命周期控制事件写入 Event Log，事件不包含凭据或工具结果。

Host supervisor：

- 初始连接、能力读取和启用由统一 supervisor 执行。
- 已连接时默认每 30 秒发送 MCP `ping`；失败立即把连接标为 `unavailable`。
- 非预期断连按 1 秒到 30 秒指数退避重连；重连后必须重新读取能力，再按原启用偏好决定是否启用。
- `Disable` 保留连接和监督；`Disconnect` 停止自动重连，直到显式 `Connect` 或 `Enable`。
- `Ready=false` 期间旧工具引用不可用。

Runtime 启动时可用以下选项接入一个回环 Streamable HTTP Server：

```powershell
go run ./cmd/termcp-harness serve `
  --listen 127.0.0.1:8787 `
  --mcp-endpoint http://127.0.0.1:18765/stream
```

可选参数：`--mcp-id`、`--mcp-name`、`--mcp-enable=false`。连接和初始能力读取在后台执行；失败只显示 `unavailable`，不会阻止 Headless Runtime 启动。

认证与退避参数：

```powershell
go run ./cmd/termcp-harness serve `
  --mcp-endpoint http://127.0.0.1:18765/stream `
  --mcp-auth-token-file '<private-token-file>' `
  --mcp-reconnect-min 1s `
  --mcp-reconnect-max 30s `
  --mcp-health-interval 30s
```

- Token 只从受限文件读取，不进入命令行、URL、Snapshot、Event Log、Desktop 或普通日志。
- 仅当 Core 与 MCP endpoint 是完全相同的 HTTP origin 时，才把 MCP Token 复用于 Core 身份和审批客户端；不同 origin 不复用。
- Token 文件为空、超限或 POSIX 下 group/other 可读时拒绝启动。

SSE：

```powershell
go run ./cmd/termcp-harness serve `
  --mcp-transport sse `
  --mcp-endpoint http://127.0.0.1:18765/sse
```

stdio：

```powershell
go run ./cmd/termcp-harness serve `
  --mcp-transport stdio `
  --mcp-stdio-command '<mcp-server-executable>' `
  --mcp-stdio-arg '<arg>' `
  --mcp-stdio-workdir '<working-directory>'
```

stdio 命令、参数和工作目录来自启动配置或受保护的 MCP discovery 注册；控制 API 和 Desktop 不提供任意用户输入命令执行入口。stdio 不得配置 endpoint 或 Bearer Token，子进程继承 Runtime 环境并追加显式环境项。

## 5. 多来源配置

`--mcp-config-file` 可注册最多 31 个额外 MCP Server，并可与现有单 Core MCP 参数并存：

```json
{
  "servers": [
    {
      "id": "external-http",
      "name": "External HTTP",
      "transport": "streamable-http",
      "endpoint": "http://127.0.0.1:18888/stream",
      "auth_token_file": "external.token",
      "enabled": true
    },
    {
      "id": "external-stdio",
      "name": "External Stdio",
      "transport": "stdio",
      "command": "<mcp-server-executable>",
      "args": [],
      "enabled": false
    }
  ]
}
```

- 文件最大 1 MiB，拒绝未知字段、重复 ID 和无效 transport。
- `auth_token_file` 和相对 `workdir` 相对于配置文件目录解析。
- Token 只在启动时读入内存，不进入 Snapshot、Event、URL 或 Desktop。
- 每个 Server 独立维护连接、能力、enablement、revision 和 supervisor。
- 工具冲突使用显式 `server_id + tool_name` 命名空间；同名工具不合并，也不按注册顺序猜测来源。
- Agent Step 和 allowlist 规则必须写明 `server_id`。

## 6. 本机 MCP 发现

`GET /v1/mcp/discovery` 执行一次只读发现，当前支持 Codex global 配置：

- [OpenAI Codex MCP 文档](https://developers.openai.com/codex/mcp)确认官方配置位置为 `~/.codex/config.toml`，服务定义位于 `[mcp_servers.<name>]`；Harness 通过 Codex CLI 的 `mcp list --json` 读取有效 MCP 定义。
- 每个条目返回稳定发现 ID、名称、客户端、scope、tags、transport、enabled、可加载状态和受限原因。
- 当前 tags 为 `global`、`codex`；后续客户端和目录来源必须使用独立 tag 和真实 schema，不复用 Codex 格式。
- API、Desktop、Event Log 和日志不返回 command、args、env、header、token 或配置文件路径。
- env 字面值、需要额外 HTTP headers 或缺少环境变量的定义不会被标记为可加载。
- 非回环 HTTP MCP 会被识别，但以 `unsupported` 返回，不尝试连接。
- Windows npm `.cmd` shim 只在能解析为同一目录内受约束脚本和 Node.js 时可加载；不通过任意 shell 命令拼接执行。

`POST /v1/mcp/discovery/{id}/register` 使用与审批相同的受限 Bearer 控制：

- 必须已在最近一次 discovery snapshot 中发现且 `loadable=true`。
- 注册只创建 `registered` 状态；连接、能力读取和启用继续由受管 supervisor 推进，不能把注册成功写成 `ready`。
- 认证、凭据和 live process configuration 不进入 Discovery Snapshot 或事件。
- 当前不扫描指定目录、OpenCode 或其他客户端；这些来源和 tag 需在独立适配器与契约测试完成后加入。

## 7. 尚未支持

- 非回环 MCP endpoint；
- OAuth 交互和动态凭据刷新；
- 独立于 MCP transport 的 PID/进程树所有权诊断；
- OpenCode、Claude、VS Code 等客户端配置和用户指定目录的 MCP 发现；
- 已注册 MCP 的动态停用、卸载和配置变更检测。

新增、编辑、移除和持久化 Harness 自有 MCP 定义属于 `v0.2.0` 计划，不进入 `v0.1.9` 推送范围。实施边界和验收见 `docs/development/MCP-MANAGEMENT-v0.2.0.md`。
