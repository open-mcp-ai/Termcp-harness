# Terminal Workspace Shell v0.1（草案）

> 状态：Wails 桌面壳、真实 Runtime 读取、Session/审批观察、审批决定和 Harness Canvas 初版 PoC 已实现；面板跨 Session 上下文跟随/锁定、关闭视图提示和服务端审批回执已接入前端；真实终端交互、实时订阅和 Workspace 导入导出尚未实现。
>
> 实现：`desktop/`

## 1. 边界

Desktop 是 Terminal Workspace 的第一个 Shell，不是 Harness Runtime 本体。它通过固定路径的控制客户端读取 Headless Harness Runtime，并可在显式配置受限 token 文件后提交审批决定；不直接启动 Agent Loop、不持有 MCP 会话，也不把 UI 状态当作安全保证。

关闭 Desktop 不会关闭 Harness Runtime。Harness Runtime 当前需由用户单独启动。

## 2. 技术基线

- Wails v2.15.0
- Go 1.27
- React 19.3.1
- TypeScript 5.9.3
- Vite 7.3.7

React/TypeScript/Vite 使用 `package-lock.json` 固定实际依赖。`@xterm/xterm` 尚未接入。

- 默认界面语言为简体中文（`zh-CN`），文案集中在 `desktop/frontend/src/i18n.ts`。
- 初始窗口为 `1280×800`，最小窗口为 `900×600`，适配 1920×1024 工作区。

## 3. Wails 桥接面

前端只能访问以下绑定方法：

| 方法 | 作用 |
|---|---|
| `Bootstrap()` | 读取 Health、Harness Runtime 快照、最新事件窗口、Core 身份和 MCP Server 列表 |
| `Events(after, limit)` | 按 Harness 本地序号补拉事件 |
| `RuntimeEndpoint()` | 返回当前回环 Harness Runtime 地址 |
| `ApprovalControlAvailable()` | 报告 Go 进程是否已安全载入审批 token |
| `DecideApproval(id, revision, decision)` | 提交一次批准或拒绝，不向前端返回 token |
| `WindowMinimise()` | 最小化窗口 |
| `WindowToggleMaximise()` | 最大化或还原窗口 |
| `WindowClose()` | 关闭窗口 |

没有通用 HTTP、URL、文件或命令桥接方法。

## 4. Harness Runtime 端点

Desktop 默认连接：

```text
http://127.0.0.1:8787
```

可用 `--runtime <loopback-url>` 或 `TERMCP_HARNESS_CONTROL` 覆盖。

审批决定仅在显式提供 `--approval-token-file <path>` 或 `TERMCP_HARNESS_APPROVAL_TOKEN_FILE` 时启用。Desktop 启动时读取 token 并只保存在 Go 进程内存；不写入前端、localStorage、事件、URL 或普通日志，关闭时清空引用。

控制客户端只接受：

- `http`；
- `localhost` 或 loopback IP；
- 无路径、query、fragment 和 URL userinfo。

每项请求有超时，响应体上限为 4 MiB。Desktop 每 2.5 秒刷新一次；当前没有 WebSocket/SSE 实时订阅。

## 5. 当前界面

- 轻量 Shell + Harness Canvas：Workspace 模板、模块抽屉、停靠面板、标签合并和布局恢复。
- Workspace 顶部选择当前 Agent Session，并持续显示运行中、等待审批和等待执行的后台任务数量。
- Agent、审批、Terminal 和 Activity 面板显示“跟随当前”“已锁定”或“关联未知”；锁定目标随布局持久化，切换 Session 时不会静默改绑。
- 面板关闭明确提示关联任务、Session 和审批不会停止；关闭审批详情不代表拒绝或取消。
- Canvas 状态使用 schema v2，面板 ID 与模块上下文分别保存，并兼容旧版布局数据。
- Activity 显示本地 sequence、时间、类型、subject/correlation、class 筛选；缺少关联 ID 时显示“关联未知”。
- Agent Session、关联审批及摘要、Harness Runtime 生命周期、Core 身份和 MCP 状态由 Canvas 模块呈现。
- 待处理审批在可信摘要存在且 token 已加载时提供“批准/拒绝”；提交期间禁用按钮，结果未知明确要求不要重复操作。
- 审批提交中、服务端确认和结果未知分别表达；只有可信服务端回执才更新审批终态。当前 UI 仍通过轮询快照收敛。
- 不可达时顶部状态为“状态未知”，Harness Runtime、Core、MCP 和事件流统一标记为“最后快照”。
- 保留快照中的“运行中”“就绪”只作为最后已知值，并附带过期说明，不作为当前状态。
- 首次启动即不可达时显示明确空态，不再保留“正在读取”加载提示。
- 网络错误使用简短中文说明，原始技术错误和启动命令收在可展开的“技术详情”中。

视觉基线：

- 深海军蓝固定轨道，搭配靛蓝信号色，不使用墨绿作为产品主色。
- 中文标题使用 Noto Serif SC，正文使用 Noto Sans SC，技术数据使用 Cascadia Mono。
- 中文文案优先表达产品意图，不逐字照搬英文；协议名和事件类型保留原始技术写法。

视觉验证使用真实临时 Harness Runtime 和 Wails 窗口完成。事件展开与筛选空态通过键盘可访问的控件验证。

## 6. 尚未实现

- 终端 Session、`@xterm/xterm` 与 WebSocket 字节流。
- Harness Runtime 自动启动、停止和实例所有权管理。
- Core 子进程自动启动、停止和 managed 所有权创建。
- Desktop 认证、远程 Harness Runtime、TLS 和凭据引用。
- MCP 连接配置、能力变更通知和持久会话。
- 工具时间线和结果未知恢复交互。
- 安装包、签名、升级、日志导出和跨平台回归。
