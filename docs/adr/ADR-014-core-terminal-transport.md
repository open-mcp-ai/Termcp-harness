# ADR-014：Core Terminal 数据路径

- 状态：Accepted / 回环 Desktop 范围；Core 本地开发分支的订阅游标和自动重连/缺口恢复已接入，正式发布复验与生产认证仍待实现
- 日期：2026-10-31
- 责任范围：Terminal Workspace 到 Termcp Core 的终端数据路径
- 关联：`ADR-008`、`ADR-010`、`ADR-013`、`docs/architecture/DESIGN_v0.4.md`、`docs/contracts/harness/v0.1/terminal-bridge.md`

## 1. 背景

Harness Canvas 已有 Terminal 面板、Session 上下文绑定和布局恢复，但没有真实终端数据通道。Core 通过公开 `GET /api/ui/ws` 提供交互终端：

- `watch_add` / `watch_remove`
- `terminal`
- `terminal_done`
- `shell_activity`
- `input`
- `resize`

历史字节通过 `GET /api/shells/{id}/output-range` 读取。Core 本地开发分支的 WebSocket `watch_add` 已返回 `watch_ready.cursor`，终端帧和结束帧携带绝对字节区间，用于恢复历史与实时输出的原子交接。

## 2. 当前 PoC 决策

第一个实现采用 **Terminal Workspace 经固定的 Wails Go 桥直连回环 Core WebSocket**：

```text
React / xterm
    │ fixed Terminal* bindings
    ▼
Wails Go Terminal Bridge
    │ Core REST: shells + output-range
    │ Core WebSocket: watch/input/resize/output
    ▼
Termcp Core (loopback)
```

Harness Runtime 继续负责 Agent Session、审批、策略和工具控制面；它不代理高频终端字节。前端不会获得 Core 地址、WebSocket 构造能力、通用 HTTP 桥或凭据。

选择当前路径的原因：

- Core 已公开终端 WebSocket 和历史字节契约。
- 终端输入属于操作员表面，Core 明确不会经过 AI review gate；再加一层 Harness 代理会把传输独立性问题混入策略控制。
- xterm 需要低延迟交互字节，固定 Go 桥能直接约束连接、队列和状态。
- 前端直连会暴露端点与认证边界；通用代理则会扩大凭据和网络暴露面。

该决策的适用范围是当前第一方 Wails Desktop 与回环 Core。真实 Wails/xterm 基线、双 Shell 隔离和最大化尺寸变化均已验证；远程部署、Core 认证和自动重连不在本次 Accepted 范围内。

## 3. 约束

- 只接受已识别的回环 Core；不接受任意 URL。
- Agent Session 必须具有可信 `core_session_id`，Shell 必须属于该 Core Session 且状态为 `running`；退出 Shell 当前不伪装成实时终端。
- 一次桥会话只 watch 一个 Shell；不同 Shell 使用不同桥会话，不混流。
- Terminal 输出保持输入顺序；内部缓冲上限为 1 MiB。
- 超过缓冲上限时不静默丢弃：桥返回累计 `dropped_output_bytes`，UI 显示可见缺口。
- `terminal_done` 映射为可信 `ended`；WebSocket 断开映射为 `unknown`，不推断远端 Shell 已停止。
- 关闭面板只关闭本地桥连接，不关闭 Core Session 或 Shell。
- 输入、resize 和输出均不写普通日志、URL 或持久化布局。
- 首次恢复 `watch_add` 确认游标之前最多 512 KiB 的保留输出；更早历史明确标记未载入。
- 实时帧必须与 `watch_ready.cursor` 及其后续区间连续；任何缺失、跳号或结束游标不一致都进入 `unknown`。
- 同一 Shell 重新订阅时，旧 watch generation 不得在新 `watch_ready` 后发送帧。

## 4. 备选方案

### 4.1 Terminal Workspace 经 Harness Runtime 转发全部字节

保留单控制面入口，但增加 Runtime 代理、背压和认证职责；当前没有证据证明该复杂度能改善首个闭环的安全或延迟。

### 4.2 React 直接建立 Core WebSocket

实现最少，但需要向 WebView 暴露端点、认证和通用连接能力，破坏现有固定桥边界。

### 4.3 混合方式由 Runtime 转发控制、终端数据直连 Core

本质上接近当前方案，但当前控制仍由 Harness 查询，终端桥位于 Desktop；没有额外引入 Runtime 终端代理。

## 5. 验证与后续

已执行：

- `go test -race ./internal/coreterminal`
- `TestManagerReconnectsAndRecoversExactOutputGap`
- `TestManagerReconnectGapFailsClosed`
- `TestManagerRecoversGapInBoundedPages`
- 真实 Core `TestManagerCoreReconnectIntegration`：TCP 代理强制断连，自动重连补齐断线输出并恢复到游标 `4026`。
- Core 本地开发分支 `go test -race ./internal/webui`
- 真实本地 Core 下的 `TestTerminalBridgeCoreIntegration`
- 真实本地 Core 下 20,000 行输出的 `TestTerminalBridgeCoreOutputFlood`
- `npm run check`
- `npm run build`
- `wails build -s -m -nosyncgomod -nopackage -clean`

真实集成测试验证了 Shell 归属查询、历史读取、WebSocket watch、输入、输出、resize、20,000 行无桥缓冲丢弃和桥关闭。2026-11-01 使用 Core 本地游标构建复验：历史恢复 488,728 字节，Shell 从游标 965 正常结束于 2406，20,000 行洪峰 482,765 字节、首字节 11 ms、总计 2.266 秒且桥无丢弃。真实 Wails 窗口此前进一步验证了 Session 选择、Terminal 添加、人工输入与输出、关闭视图不停止 Shell、重新打开历史恢复，以及 Core Session 删除后的 `Shell 已结束` 状态。

性能基线（2026-10-31）：

- 固定桥 20,000 行：240,599 字节，首字节 10 ms，总计 2.598 秒，无桥缓冲丢弃。
- 真实 Wails/xterm：包含命令输入的回车到验证标记可见采样为 212 / 222 / 238 ms；数值是包含 Computer Use 操作与截图开销的上界，不是精细 p50/p95。
- 20,000 行 UI 洪峰在含窗口操作的总等待 4.2 秒内完成，最终标记可见。
- Wails 及 WebView2 进程树：基线 445.8 MiB，峰值 451.6 MiB，20 秒后 449.4 MiB；峰值增量 5.9 MiB，保留增量 3.6 MiB。
- 同一 Core Session 的两个 Shell 切换后，验证标记只出现在目标 Shell；前一个 Shell 不含该标记。

生产化仍需：

- Core 认证后的凭据引用。
- 长时间断网、Core 重启和大规模缺口的重连压力测试。
- 将包含 `watch_ready` / 字节区间扩展的 Core 本地构建同步到正式发布包。
