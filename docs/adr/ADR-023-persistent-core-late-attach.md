# ADR-023：持久本地 Core 与 Harness 迟附着

- 状态：**Accepted / 首个切片已实现**
- 日期：2026-11-05
- 关联：`ADR-010`、`ADR-014`、`ADR-015`、`docs/contracts/harness/v0.1/core-instance.md`、`docs/contracts/harness/v0.1/terminal-bridge.md`、`docs/contracts/harness/v0.1/control-api.md`

## 1. 背景

外部 Agent 可以先通过 Termcp Core 创建和执行 Session，而 Harness 尚未启动。旧实现让 Desktop 把可执行文件旁的 Core 作为子进程启动，并在 Desktop 退出时停止它；这使 Core 的生命周期依赖 Harness，也无法在 Harness 迟到时复用由其他客户端创建的执行现场。

## 2. 决策

Harness 支持一个显式的、本地持久的 Core service profile：

- Core 使用自己的公开 `daemon start/status/stop` 命令运行，不由 Harness 子进程监督。
- profile 固定 Core 二进制绝对路径、版本、SHA-256、loopback endpoint 和持久数据目录。
- 凭据只通过受限文件引用；profile 不保存 token 明文。
- Harness 只把该实例识别为 `external`，退出时只停止自己的 Runtime。
- Desktop 与 headless `serve` 在没有其他 Core 配置时可自动加载默认 profile。

默认 profile 位于用户配置目录下的 `termcp-harness/core-service.json`。默认 Core 端点为 `http://127.0.0.1:18765`，默认数据目录为 Core 自身的 `~/.termcp`，使已有 Termcp 客户端可以继续使用同一实例。

## 3. 迟附着

Harness 启动时重新通过 `/api/version` 和 `/api/daemon` 识别当前 Core 实例。端点可达不等于所有权，也不等于历史 Session 自动成为 Harness Agent Session。

核心事实：

- Core 进程持续运行：Harness 可接入仍存活的 Session、Shell 和后续输出。
- Core 曾重启：持久 Session 和输出历史可恢复为 DEAD 数据，但原 SSH/PTY 连接不能复活。
- 外部 Agent 的模型 transcript 不在 Core 中：Harness 不能重建该 Agent 的对话、策略或审批历史。
- Harness 可以读取 Core Session 清单，并让用户明确使用已有 `core_session_id` 创建新的 Harness Agent Session。

## 4. 只读清单与终端观察

Runtime 提供固定只读 `GET /v1/core/sessions`。Desktop 只能通过该桥读取有界 Session 清单，不能在浏览器中获得 Core token 或通用 HTTP 能力。

Desktop 的 Core Sessions 面板可以：

- 显示 Core 中的 running 和 DEAD Session。
- 在验证 Shell 属于所选 Session 且仍为 running 后，直接接入终端。
- 复制 Session ID，供用户在新 Agent 的 Core Session 绑定输入中使用。

直接观察不会创建伪造的 Harness Agent Session。Core terminal bridge 使用 `ObservationOnly` 语义允许缺少 Agent Session ID，但不改变 Harness Session Store。

## 5. 安全边界

- 只接受 loopback endpoint。
- Core 二进制必须匹配 profile 的 SHA-256，版本必须与 profile 一致。
- `/api/daemon` 返回的日志路径必须位于 profile 数据目录内，避免连接到同版本但不同数据目录的实例。
- profile 和 token 文件不进入命令参数、事件、普通日志、模型上下文或前端持久化。
- 用户明确执行 stop 时才请求停止 Core；Harness 自身关闭不取得该权力。
- 不扫描任意端口，不按端口杀死进程。
- 不在 Harness 中读取 Core 私有数据库；Session 清单和 Shell 仍通过公开 HTTP/WebSocket 契约读取。

## 6. 当前限制

- 尚无 Windows Service、登录自启动、托盘控制或 Core 崩溃自动重启。
- 尚无 Core 二进制签名验证、补丁更新和回滚。
- 自动迟附着要求目标 Core 的版本与 profile 一致。
- 外部 Agent transcript、Harness 工具策略和审批历史不能从 Core 终端日志反推。
- Core 重启后的 DEAD Session 目前可出现在清单中，但完整只读历史终端视图尚未实现。

## 7. 安装器选项与升级

安装器的“持久 Core”复选框只决定首次安装是否创建 Core profile：

- 未勾选：保持原有 managed Core 生命周期，Harness 退出时停止 Core。
- 勾选：调用安装后的 CLI 创建 profile，并立即启动 external Core。
- 静默安装可使用 `/PERSISTENT_CORE=1`。

升级路径遵守以下规则：

1. 如果已有 profile，安装器读取安装目录记录的 profile 路径并默认保持持久模式。
2. 在覆盖程序文件前调用旧 CLI 停止旧 Core。
3. 替换 Desktop、CLI 和 Core 后，用新二进制重新写入版本与 SHA-256；endpoint、data dir 和 token 文件引用保持不变。
4. 重新启动新 Core。live SSH/PTY 会断开，持久 Session 与输出保留。
5. 卸载时停止持久 Core，但保留用户 profile 和 Core 数据目录，便于重装后继续使用。

用户可在升级时显式取消持久模式。安装器会停止 Core，将 active profile 重命名为
`core-service.json.disabled.<tick>`，然后按 managed 模式继续；Core 数据目录不删除，
可用该备份恢复配置。

安装器模板和构建入口分别为 `desktop/installer/project.nsi` 与 `desktop/scripts/build-installer.ps1`。登录自启动和崩溃恢复仍未实现。
