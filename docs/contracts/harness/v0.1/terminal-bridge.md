# Desktop Terminal Bridge v0.1（草案）

> 状态：固定 Wails Go 桥在回环 Desktop 范围 Accepted；真实 Wails/xterm 性能基线与双 Shell 隔离已验证。认证、重连和原子历史恢复尚未实现。
>
> 实现：`desktop/app.go`、`internal/coreterminal/`、`desktop/frontend/src/Panels.tsx`
>
> 决策：`docs/adr/ADR-014-core-terminal-transport.md`

## 1. 边界

Desktop 的 Terminal Bridge 只连接 Harness 已识别的回环 Core。React 前端不能获得 Core URL、通用 WebSocket、通用 HTTP 或凭据。

Harness Runtime 仍拥有 Agent Session 与 Core Session 的关联。Bridge 在打开终端前必须：

1. 从 `/v1/core` 读取已识别 Core 实例。
2. 从 `/v1/agent/sessions/{id}` 读取 Agent Session。
3. 要求非空 `core_session_id`。
4. 从 Core `GET /api/sessions/{id}/shells` 读取 Shell，并验证目标 Shell 属于该 Session 且状态为 `running`。

缺失或冲突的关联显示“未知”，不得按当前选择、时间、标题或文本推断。

## 2. 固定绑定

| 绑定 | 作用 |
|---|---|
| `TerminalShells(agentSessionId)` | 返回可信 Core Session 与 Shell 列表 |
| `TerminalOpen(agentSessionId, shellId, rows, cols)` | 恢复尾部历史并建立单 Shell WebSocket watch |
| `TerminalPoll(id)` | 取出有序输出、连接状态、输入来源和丢弃字节数 |
| `TerminalInput(id, data)` | 向当前 Shell 写入原始输入 |
| `TerminalResize(id, rows, cols)` | 更新 Core PTY 尺寸 |
| `TerminalClose(id)` | 关闭本地桥会话；不关闭 Core Shell |

没有 `TerminalURL`、通用 `WebSocket` 或任意 Core 请求绑定。

## 3. 状态

桥只暴露以下连接状态：

- `connecting`：本地正在建立桥。
- `connected`：WebSocket 已建立，已发送 watch/resize；Core 当前协议没有 `watch_add` 确认。
- `ended`：收到当前 Shell 的可信 `terminal_done`。
- `unknown`：WebSocket 断开或本地读取失败；远端 Shell 状态未知。

`unknown` 不是失败、停止或取消。UI 不根据本地时间把 `unknown` 改成 `ended`。

`shell_activity.src`：

- `api`：人工输入中。
- `ai`：Agent 输入中。
- 空值：没有可确认的当前输入来源。

## 4. 数据与顺序

- 历史通过 `GET /api/shells/{id}/output-range?tail=1&max=524288` 恢复。
- 历史不覆盖时返回最近 512 KiB；更早内容显示截断提示。
- 实时输出只接受 `terminal.id == selected_shell_id` 的帧。
- 同一桥会话内按 WebSocket 接收顺序写入单一输出缓冲。
- 输出缓冲上限 1 MiB；溢出后不再接受新字节，并累计 `dropped_output_bytes`，UI 显示可见缺口。
- `terminal_done` 前已接收的输出必须先于 `ended` 状态被前端读取。
- 不同 Shell 必须使用不同桥会话，不交叉复用输出缓冲。

### 4.1 Core 当前限制

`watch_add` 没有服务端确认，而且只观察注册后的新输出。当前实现先读取历史、再连接并注册 watch；两步之间没有原子游标，因此不能承诺历史与实时边界无缺口或无重复。

该限制在 Core 提供历史游标、订阅确认或等价原子能力前保持可见，不以字符串匹配、时间窗口或文本相似性补洞。

## 5. 生命周期

- 关闭 Terminal 面板或切换 Shell 只关闭本地桥会话；Core Session 与 Shell 继续运行。
- 打开面板会重新读取 Shell 列表和保留历史。
- WebSocket 断开后桥进入 `unknown`；当前版本要求用户显式重连。
- Bridge 和 Desktop 关闭时清理所有本地 WebSocket 与缓冲，不向 Core 发送 Shell 删除。

## 6. 安全和输入

- 仅接受 `http`/`https` 回环 Core origin，不允许 userinfo、path、query 或 fragment。
- `input` 最大 64 KiB/次，`rows` 限 1–1000，`cols` 限 1–2000。
- 终端数据、输入内容和本地桥 ID 不写普通日志或布局持久化。
- Core 认证接入后，凭据仍只在 Go 进程内存；不得返回前端或写入事件。

## 7. 验证

已通过：

- `go test -race ./internal/coreterminal`
- 真实 Core 的 `TestTerminalBridgeCoreIntegration`
- 真实 Core 的 20,000 行 `TestTerminalBridgeCoreOutputFlood`
- `npm run check`
- `npm run build`
- Wails production build
- 真实 Wails 窗口中的 Session 选择、Terminal 添加、人工输入/输出、关闭视图、历史恢复和 Shell 结束状态

性能基线（2026-10-31）：

- 桥层 20,000 行：240,599 字节、首字节 10 ms、总计 2.598 秒，无缓冲丢弃。
- Wails/xterm 回车到输出可见：212 / 222 / 238 ms，均为包含自动化与截图开销的上界。
- 20,000 行 UI 洪峰：含窗口操作总等待 4.2 秒内完成。
- Wails/WebView2 进程树内存：445.8 MiB 基线、451.6 MiB 峰值、449.4 MiB 最终值。
- 两个 Shell 切换后验证标记只在目标 Shell，未跨 Shell 混流。

未完成：

- Core 认证、自动重连和原子历史恢复。
