# Terminal Workspace Shell v0.1（草案）

> 状态：Wails 桌面壳、真实 Runtime 读取、Session/审批观察、异步模型任务启动、Agent 停止任务与人工接管、安全检查点人工恢复、持久化模型回复读取、审批决定、审批时间线、审批历史恢复、`waiting_execution` 恢复交互、Harness Canvas、多 Space 创建/切换/重命名/删除、Workspace Recipe v1 导入导出、跨 Workspace 布局复制、有界布局撤销/重做、真实 Core Terminal、SSE 实时事件订阅、同 Runtime 实例客户端 cursor 恢复和可选 Event Journal 恢复已实现；面板跨 Session 上下文跟随/锁定、关闭视图提示和服务端审批回执已接入；Recipe 跨版本迁移尚未实现。
>
> 实现：`desktop/`

## 1. 边界

Desktop 是 Terminal Workspace 的第一个 Shell，不是 Harness Runtime 本体。它通过固定路径的控制客户端读取 Headless Harness Runtime，并可在显式配置受限 token 文件后提交审批决定、请求 Runtime 创建模型 Session 并运行 Agent Loop；不自行运行 Agent Loop、不持有 MCP 会话，也不把 UI 状态当作安全保证。

关闭 Desktop 不会关闭 Harness Runtime。Harness Runtime 当前需由用户单独启动。

## 2. 技术基线

- Wails v2.15.0
- Go 1.27
- React 19.3.1
- TypeScript 5.9.3
- Vite 7.3.7

React/TypeScript/Vite 使用 `package-lock.json` 固定实际依赖。`@xterm/xterm` 5.5.0 与 `@xterm/addon-fit` 0.10.0 已接入。

- 默认界面语言为简体中文（`zh-CN`），文案集中在 `desktop/frontend/src/i18n.ts`。
- 初始窗口为 `1280×800`，最小窗口为 `900×600`，适配 1920×1024 工作区。

## 3. Wails 桥接面

前端只能访问以下绑定方法：

| 方法 | 作用 |
|---|---|
| `Bootstrap()` | 读取 Health、Harness Runtime 快照、最新事件窗口、Core 身份和 MCP Server 列表 |
| `Events(after, limit)` | 按 Harness 本地序号补拉事件 |
| `OpenEventStream(after)` | 在 Go 进程建立固定 SSE 订阅 |
| `PollEventStream()` | 取出有界实时事件与 cursor/重连状态 |
| `CloseEventStream()` | 关闭本地 SSE 订阅 |
| `RuntimeEndpoint()` | 返回当前回环 Harness Runtime 地址 |
| `ApprovalControlAvailable()` | 报告 Go 进程是否已安全载入审批 token |
| `DecideApproval(id, revision, decision)` | 提交一次批准或拒绝，不向前端返回 token |
| `RefreshApprovalExecution(id)` | 只读核对 Core Execution，并允许 Session 从可信证据恢复；不重提审批或工具调用 |
| `ReconcileApproval(id)` | 对 `unknown` 审批执行一次只读 Core Lookup；不提交决定或重放工具调用 |
| `StartModelLoop(coreSessionId, input, maxIterations, maxToolCalls)` | 创建新的 Agent Session 并请求 Runtime 运行模型 Loop；不暴露 token、模型 endpoint 或通用 HTTP |
| `ResumeModelLoop(agentSessionId)` | 请求 Runtime 领取一个安全检查点；模型配置、工具目录和 Invoker 不可由前端替换 |
| `AgentSessionTranscript(id)` | 使用 Go 内存中的 Bearer token 读取持久化模型消息和最终回复；不向前端返回 token |
| `TerminalShells(agentSessionId)` | 解析可信 Core Session 和 Shell 列表 |
| `TerminalOpen(agentSessionId, shellId, rows, cols)` | 恢复历史并建立单 Shell Core WebSocket |
| `TerminalPoll(id)` | 取出有序输出、连接状态、输入来源和丢弃字节数 |
| `TerminalInput(id, data)` | 写入当前 Core Shell |
| `TerminalResize(id, rows, cols)` | 更新 Core PTY 尺寸 |
| `TerminalClose(id)` | 关闭本地桥；不关闭 Core Shell |
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

审批决定、Execution 核对、模型 Loop 和安全检查点恢复仅在显式提供 `--approval-token-file <path>` 或 `TERMCP_HARNESS_APPROVAL_TOKEN_FILE` 时启用。Desktop 启动时读取 token 并只保存在 Go 进程内存；不写入前端、localStorage、事件、URL 或普通日志，关闭时清空引用。

控制客户端只接受：

- `http`；
- `localhost` 或 loopback IP；
- 无路径、query、fragment 和 URL userinfo。

每项普通请求有超时，响应体上限为 4 MiB。事件使用 SSE；Session/审批等权威快照由事件触发合并刷新，并保留 15 秒兜底轮询。

## 5. 当前界面

- 轻量 Shell + Harness Canvas：Workspace 模板、模块抽屉、停靠面板、标签合并和布局恢复。
- Canvas 的 12 行网格按窗口剩余高度自适应填充；Runtime 不可达或错误横幅出现时不产生 Workspace 整页纵向滚动条，底部也不保留固定行高造成的空白。内容溢出由各面板内部滚动承担。
- 面板拖动按动画帧节流，仅在 Canvas 内部更新预览；松手后才提交布局并持久化，避免每个 pointer move 触发整个 App 和所有重内容面板重绘。
- Workspace 顶部选择当前 Agent Session，并持续显示运行中、等待审批和等待执行的后台任务数量。
- Agent、审批、Terminal 和 Activity 面板显示“跟随当前”“已锁定”或“关联未知”；锁定目标随布局持久化，切换 Session 时不会静默改绑。
- 面板关闭明确提示关联任务、Session 和审批不会停止；关闭审批详情不代表拒绝或取消。
- Canvas 状态使用 schema v2，面板 ID 与模块上下文分别保存，并兼容旧版布局数据。
- Space 切换器优先展示“我的 Spaces”，固定场景模板收在次级分组；用户可从空白或场景模板创建多个个人 Space，并在同一菜单重命名或删除。Space 目录保存在 `termcp.canvas.spaces`，布局、Session 引用和撤销历史按 Space ID 隔离。
- 新建 Space 当前最多保留 20 个个人 Space；删除确认明确只移除本地布局、历史和 Session 引用，不停止或重绑 Agent、Terminal、工具调用与审批。旧版唯一 `custom` Space 作为首个个人 Space 读取，不迁移为固定模板。
- 当前 Workspace 可导出为 Recipe v1 JSON；导入先显示来源、面板数和修复警告，只有用户确认后才替换当前 Workspace。
- Recipe 最大 1 MiB、最多 64 个面板；未知模块、损坏单面板和未知字段被隔离，越界坐标裁剪，无效锁定上下文改为跟随，重复模块和重叠布局不会静默接受。
- 真实 Wails 窗口已验证：导出触发下载并生成可解析的 Recipe v1 文件；原生文件选择器可载入导出的文件；无警告预览可取消并保留原布局；带损坏面板和无效锁定上下文的 Recipe 显示两项修复警告，确认后布局从 3 个面板替换为 2 个，Desktop 重启后仍保持导入结果。
- 每个 Workspace 维护最多 20 步布局历史，快照只包含规范化的面板和选中 Session 引用，不包含 Token、终端正文或活动连接。移动、缩放、最大化、添加/关闭面板、上下文绑定、模板恢复和 Recipe 导入可撤销；纯 Session 选择不进入布局历史。
- 顶部“+ 添加模块”只打开模块目录；“搜索或运行命令”与 `Ctrl+K` 打开独立命令面板，命令面板同时索引 UI 命令和可添加模块，不把任意 Shell 命令暴露为 UI 命令。
- 撤销、重做和刷新保留在工具栏；导入、导出、复制 Workspace 和恢复模板收进“布局设置”。设置页显示当前 12 × 12 网格、8 px 间距和拖动/拉伸行为，作为 Layout Inspector 的第一版边界。
- 审批面板将“状态来源、审批关联、操作权限”显示为三条紧凑状态；默认只显示摘要，完整解释只在悬停或键盘聚焦 `?` 时展开。
- “撤销/重做”按钮、`Ctrl+Z` 与 `Ctrl+Shift+Z` 作用于当前 Workspace；输入框、选择框和模态预览保留原生键盘行为。历史保存到 `termcp.canvas.history.v1`，损坏条目会按快照隔离，最大化状态在持久化副本中折叠。
- 真实 Wails 窗口已验证模板恢复后的鼠标撤销、键盘重做，以及 Desktop 重启后继续使用 `Ctrl+Z` 恢复上一布局。
- “复制到…”可将当前布局和 Session 引用复制到其他 Workspace。预览显示目标现有面板数，确认后替换目标并切换视图；目标原布局进入目标自己的历史，可独立撤销。该操作不移动或重启 Agent、Terminal、工具调用与审批。
- 真实 Wails 窗口已验证：日常协作的 2 面板布局复制到已有 5 面板的安全测试，预览正确提示覆盖范围；确认后切换为 2 面板，目标“撤销”恢复为原 5 面板。
- Activity 显示本地 sequence、时间、类型、subject/correlation、class 筛选；缺少关联 ID 时显示“关联未知”。
- Agent Session、关联审批及摘要、Harness Runtime 生命周期、Core 身份和 MCP 状态由 Canvas 模块呈现。
- Agent 面板显示 Session 的模型轮次、模型工具调用数、当前工具/审批/执行状态，并提供“发起 Agent 任务”启动台：可选择是否绑定 Core Session、设置模型轮次/工具调用预算并提交任务。模型 endpoint 和凭据仍只在 Runtime 启动时配置，Desktop 不能修改。
- 模型任务启动和安全检查点恢复在创建/领取成功后立即返回 Session，Runtime 请求在后台继续；Desktop 可选择该 Session 并显示停止入口。
- Agent 面板提供“停止任务”，提交前明确不会关闭 Core Session、Shell 或审批。服务端回执区分安全取消、`cancelling`、远端结果未知和审批仍待处理。
- “打开 Terminal 接管”选择当前 Agent Session；Terminal 若已锁定到其他上下文，用户必须显式切换，不会被静默重绑。
- 恢复的活动模型 Session 显示 checkpoint 阶段、更新时间、迭代/工具计数和阻塞原因；只有 `unknown + runtime_restart + safe_boundary` 的 `model_requesting` / `tools_ready` 显示“从安全检查点恢复”，按钮只提交固定 Session ID。
- 安全检查点恢复期间按钮禁用；完成后显示最终文本，进入审批/执行等待时显示最新状态，失败或冲突保留 Runtime 最新快照。`safe_boundary` 不表示自动继续。
- 真实 Wails 窗口已验证待审批 checkpoint 的恢复展示：Session 保持 `unknown`，显示“等待审批：审批结果未确认”，保留 iteration、工具/待执行计数和 call ID，且不提供继续 Loop 按钮。
- 模型任务完成后在当前视图显示本轮最终文本、Session 状态、模型轮次和工具调用数；该输出不持久化，离开面板后需要重新运行或等待后续上下文持久化能力。
- 模型结果只在所属 Session 视图中显示；切换到其他 Session 后不会把上一任务的回复误认为当前任务结果。
- 选择已完成 Session 时，Desktop 通过固定桥读取受保护的持久化 transcript；若当前没有本地本轮回执，则显示“持久化模型回复”。
- 审批面板对 `recovered=true` 的历史记录显示恢复警告；Runtime 重启前未确认的审批显示为 `unknown`，不会重新显示批准/拒绝按钮。
- 真实 Wails 窗口已验证 Runtime 重启后读取持久化回复和模型消息数量；当次未配置 Event Journal Store，因此恢复 Session 的旧事件轨迹为空。Desktop 现在将最后已消费 cursor 绑定 Runtime endpoint 与 instance ID 保存到本机 localStorage：同一实例重启 Desktop 可继续补拉，Runtime 实例变化、存储损坏或 cursor 超前时重置到最新 sequence；跨 Runtime 的轨迹连续性仍不冒充完整恢复。
- Runtime 重启后恢复的未完成 Session 显示 fail-closed 警告；不会把 `unknown` 显示为成功、失败或已取消。
- 真实 Wails 窗口已验证：模型任务提交、最终回复显示、Session 自动切换，以及真实 Core 的 `waiting_approval → approved → waiting_execution → resumed → completed` 事件链。
- 真实 Core 的同步写操作会自动快速恢复，无法稳定停留在恢复按钮；`waiting_execution → 核对并恢复 → execution 成功` 的按钮交互使用专用本地控制 API fixture 验证，不作为真实 Core 执行证据。
- 待处理审批在可信摘要存在且 token 已加载时提供“批准/拒绝”；提交期间禁用按钮，结果未知明确要求不要重复操作。
- `unknown` 审批在 token 已加载时提供“只读核对”；Core 仍为 `pending` 时保持未知，不显示批准/拒绝入口。
- 真实 Wails 窗口已验证审批历史恢复、恢复警告、只读核对按钮、`unknown → rejected` 服务端回执和 `approval.reconciled` 时间线；Runtime 再次重启后审批终态与事件仍可显示。
- 审批提交中、服务端确认和结果未知分别表达；只有可信服务端回执才更新审批终态。事件到达后触发合并快照刷新，并保留 15 秒兜底轮询。
- 每条审批展示基于精确 `approval_id`、`call_id` 或 `correlation_id` 的本地时间线，并明确“序号不代表远端执行顺序”；当前窗口之外的历史保持不可见。
- `waiting_execution` Session 在可信批准且 token 已加载时提供“核对并恢复”。该动作只读取权威 Execution，不重提审批或当前工具调用；读取到可信成功证据时可继续尚未执行的后续步骤。
- Terminal 从 Agent Session 的 `core_session_id` 和 Core Shell 列表建立显式绑定；多 Shell 可选择，不按当前 Session 或时间推断。
- 没有可信 running Shell 时 Terminal 显示 `NO TRUSTED SHELL` 等明确空态，不显示仿真命令提示符；只有绑定真实 Core Shell 后才出现 xterm 和真实 Prompt。
- xterm 接收保序输出，人工输入通过固定 Go 桥发送；resize 同步到 Core PTY，`shell_activity` 区分人工与 Agent 输入。
- 首次恢复 Core 保留输出的最后 512 KiB；历史截断、输出缓冲缺口、`terminal_done` 和 WebSocket 断线分别可见。
- WebSocket 异常断开后显示 `reconnecting` 和重试次数；Bridge 自动用新订阅游标补洞，全部连续时恢复输入，超过 4 MiB 或无法证明区间连续时进入 `unknown`。
- 关闭 Terminal 面板只关闭本地桥，不停止 Core Shell 或 Session。
- 真实 Wails 回归已验证 Session 选择、Terminal 添加、人工输入与输出、关闭视图后 Shell 保持、重开历史恢复和 Core Session 删除后的结束状态。
- 不可达时顶部状态为“状态未知”，Harness Runtime、Core、MCP 和事件流统一标记为“最后快照”。
- 保留快照中的“运行中”“就绪”只作为最后已知值，并附带过期说明，不作为当前状态。
- 首次启动即不可达时显示明确空态，不再保留“正在读取”加载提示。
- 网络错误使用简短中文说明，原始技术错误和启动命令收在可展开的“技术详情”中。

### Workspace Recipe v1

```json
{
  "format": "termcp.harness.workspace",
  "version": 1,
  "workspace": "security",
  "layout": [],
  "selected_session_id": "..."
}
```

- `layout` 只包含模块、稳定面板 ID、标签、网格几何和显式 Session 上下文；导出时折叠最大化状态并移除未知字段。
- `selected_session_id` 与锁定上下文中的 Session ID 只是引用，不是活动连接或凭据。导入后仍以 Runtime 快照验证；目标不存在时显示关联未知。
- 导入失败、格式/版本不支持或用户取消时不修改当前布局。成功只替换当前 Workspace，不删除其他 Workspace，也不停止任务、终端或审批。
- 当前只支持 Recipe v1 与复制式跨 Workspace 迁移；Recipe 跨版本迁移和跨 Workspace 移动尚未实现。

视觉基线：

- 深海军蓝固定轨道，搭配靛蓝信号色，不使用墨绿作为产品主色。
- 中文标题使用 Noto Serif SC，正文使用 Noto Sans SC，技术数据使用 Cascadia Mono。
- 中文文案优先表达产品意图，不逐字照搬英文；协议名和事件类型保留原始技术写法。

视觉验证使用真实临时 Harness Runtime 和 Wails 窗口完成。事件展开与筛选空态通过键盘可访问的控件验证。

## 6. 尚未实现

- 含终端游标扩展的 Core 正式发布同步、自动重连和认证凭据引用。
- Harness Runtime 自动启动、停止和实例所有权管理。
- Core 子进程自动启动、停止和 managed 所有权创建。
- Desktop 认证、远程 Harness Runtime、TLS 和凭据引用。
- MCP 连接配置、能力变更通知和持久会话。
- Desktop 侧模型 endpoint 配置、流式输出、自动恢复和完整人工接管控制。
- 安装包、签名、升级、日志导出和跨平台回归。
