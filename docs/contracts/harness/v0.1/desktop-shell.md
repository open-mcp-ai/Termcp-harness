# Terminal Workspace Shell v0.1（草案）

> 状态：Wails 桌面壳、真实 Harness Runtime 读取和本机窗口验证已完成；终端交互与实时订阅尚未实现。
>
> 实现：`desktop/`

## 1. 边界

Desktop 是 Terminal Workspace 的第一个 Shell，不是 Harness Runtime 本体。它通过固定路径的只读控制客户端读取 Headless Harness Runtime，不直接启动 Agent Loop、不持有 MCP 会话，也不把 UI 状态当作安全保证。

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

控制客户端只接受：

- `http`；
- `localhost` 或 loopback IP；
- 无路径、query、fragment 和 URL userinfo。

每项请求有超时，响应体上限为 4 MiB。Desktop 每 2.5 秒刷新一次；当前没有 WebSocket/SSE 实时订阅。

## 5. 当前界面

- 左侧固定 Rail：事件时间线、Harness Runtime、Termcp Core、MCP Server。
- 中间事件带：本地 sequence、时间、类型、subject/correlation、class 筛选和可展开载荷。
- 右侧事实检查器：Harness Runtime 生命周期、Core 端点/所有权/PID/启动时间/实例键，以及 MCP 登记/连接/能力/启用状态。
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
- 审批决定、工具时间线和结果未知恢复交互。
- 安装包、签名、升级、日志导出和跨平台回归。
