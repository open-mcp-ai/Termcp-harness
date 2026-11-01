# Desktop Terminal Bridge v0.1（草案）

> 状态：固定 Wails Go 桥在回环 Desktop 范围 Accepted；真实 Wails/xterm 性能基线与双 Shell 隔离已验证。Core 本地开发分支的订阅游标和终端字节区间已通过 Harness Desktop 桥 E2E、Shell 结束和 20,000 行洪峰验证；自动重连与精确缺口恢复已通过可断线 WebSocket 测试，正式 Core 发布复验和认证尚未完成。
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
| `TerminalOpen(agentSessionId, shellId, rows, cols)` | 等待 `watch_ready`，恢复到该游标为止的历史，并建立单 Shell WebSocket watch |
| `TerminalPoll(id)` | 取出有序输出、连接状态、输入来源和丢弃字节数 |
| `TerminalInput(id, data)` | 向当前 Shell 写入原始输入 |
| `TerminalResize(id, rows, cols)` | 更新 Core PTY 尺寸 |
| `TerminalClose(id)` | 关闭本地桥会话；不关闭 Core Shell |

没有 `TerminalURL`、通用 `WebSocket` 或任意 Core 请求绑定。

## 3. 状态

桥只暴露以下连接状态：

- `connecting`：本地正在建立桥。
- `reconnecting`：WebSocket 断开后正在自动重连，远端状态暂时未知；输入和 resize 被拒绝。
- `connected`：WebSocket 已建立，收到当前 Shell 的 `watch_ready`，且已发送 resize。
- `ended`：收到当前 Shell 的可信 `terminal_done`。
- `unknown`：WebSocket 断开或本地读取失败；远端 Shell 状态未知。

`unknown` 不是失败、停止或取消。UI 不根据本地时间把 `unknown` 改成 `ended`。

`shell_activity.src`：

- `api`：人工输入中。
- `ai`：Agent 输入中。
- 空值：没有可确认的当前输入来源。

## 4. 数据与顺序

- Core 在 `watch_ready` 中返回订阅的第一个可信绝对字节游标。
- 历史按该游标向请求 `GET /api/shells/{id}/output-range?start=<cursor-524288>&max=524288`，响应 `end` 必须等于 `watch_ready.cursor`。
- 历史不覆盖时返回最近 512 KiB；更早内容显示截断提示。
- 实时输出只接受 `terminal.id == selected_shell_id` 的帧。
- `terminal.start` / `terminal.end` 必须形成覆盖负载的半开绝对字节区间，并与当前游标连续；空帧也只能位于当前游标。
- 同一桥会话内按 WebSocket 接收顺序写入单一输出缓冲。
- 输出缓冲上限 1 MiB；溢出后不再接受新字节，并累计 `dropped_output_bytes`，UI 显示可见缺口。
- `terminal_done.cursor` 必须等于已消费游标；不一致时进入 `unknown`，不显示为正常结束。
- 缺失游标、空帧跳位、区间缺口、历史 `end` 不匹配或结束游标不匹配均 fail-closed 为 `unknown`。
- 不同 Shell 必须使用不同桥会话，不交叉复用输出缓冲。
- WebSocket 异常断开后进入 `reconnecting`，按 200 ms 到 5 s 有界退避重试；每次重试次数在快照中可见。
- 重连成功必须重新收到 `watch_ready.cursor`。Bridge 分页读取 `[lastCursor,newCursor)`，所有页的 `start/end` 与字节长度严格匹配后才安装新连接并开始实时读泵。
- 断线期间新增字节超过 4 MiB、Core 已丢失该区间、游标倒退或任一页不连续时停止自动恢复并进入 `unknown`；不根据文本、时间或近似重复补洞。
- 重连期间完成的历史补洞和后续实时帧保持单一游标顺序；本地 1 MiB 输出缓存溢出仍累计 `dropped_output_bytes`。

### 4.1 原子交接契约

Core 本地开发分支为 `watch_add` 增加 `watch_ready.cursor`，为 `terminal` 增加 `start` / `end`，为 `terminal_done` 增加最终 `cursor`。服务端先排队 `watch_ready`，之后才启动该 reader 的输出泵。

同一 Shell 被重复订阅时，旧 watch generation 在替换后不能再排队 terminal 或 `terminal_done`；Harness 使用新 `watch_ready.cursor` 读取历史并缓冲其后的实时帧。该实现消除了历史读取与实时订阅之间的盲区，不依赖时间、文本或相邻帧推断。

该能力目前属于 Core `v0.2.8` 本地开发基线，尚未进入正式发布。旧 Core 缺少 `watch_ready` 时，Harness 会在限时后报“无法确认终端订阅”，不会静默退回无游标的非原子路径。

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
- Core 本地开发分支 `go test -race ./internal/webui`
- 真实 Core 的 `TestTerminalBridgeCoreIntegration`
- 真实 Core 的 `TestTerminalBridgeCoreShellExit`
- 真实 Core 的 20,000 行 `TestTerminalBridgeCoreOutputFlood`
- 真实 Core 的 `TestManagerCoreReconnectIntegration`：通过本地 TCP 代理承载 Core 流量，在连续输出中强制断开连接，自动重连后恢复并通过最终标记，恢复游标到 `4026`。
- `npm run check`
- `npm run build`
- Wails production build
- 真实 Wails 窗口中的 Session 选择、Terminal 添加、人工输入/输出、关闭视图、历史恢复和 Shell 结束状态
- 2026-11-01 新游标协议隔离 E2E：历史 488,728 字节；Shell `opened=965 → ended=2406`；20,000 行 482,765 字节、首字节 11 ms、总计 2.266 秒、218 次轮询、无桥缓冲丢弃。

性能基线（2026-10-31）：

- 桥层 20,000 行：240,599 字节、首字节 10 ms、总计 2.598 秒，无缓冲丢弃。
- Wails/xterm 回车到输出可见：212 / 222 / 238 ms，均为包含自动化与截图开销的上界。
- 20,000 行 UI 洪峰：含窗口操作总等待 4.2 秒内完成。
- Wails/WebView2 进程树内存：445.8 MiB 基线、451.6 MiB 峰值、449.4 MiB 最终值。
- 两个 Shell 切换后验证标记只在目标 Shell，未跨 Shell 混流。

未完成：

- Core 认证、长时间重连压力测试和正式发布包同步。
