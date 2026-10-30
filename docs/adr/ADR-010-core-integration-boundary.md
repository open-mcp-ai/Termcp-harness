# ADR-010：Core 消费方式与分离边界

- 状态：Proposed / 目标方向明确，具体启动模式待确认
- 日期：2026-10-28
- 责任范围：Harness + Core 契约协作
- 关联：`docs/architecture/UPSTREAM.md`、`docs/architecture/DESIGN_v0.4.md`、`docs/contracts/README.md`、`docs/adr/ADR-011-mcp-native-agent-runtime.md`

## 1. 背景

需要回答一个关键问题：Harness Runtime 如何通过公开契约消费 Termcp Core，而不是把 Core 执行能力复制进 Harness？

当前事实：

- Termcp Core 是可执行的 Go 服务，提供 HTTP、WebSocket 和 MCP。
- Core 的 Web UI 资源与 Core 服务在同一仓库、同一可执行程序中。
- Termcp-Desktop 是独立仓库，但它的文档说明可以直接复用 Core 的 Go 内部组件，并在某些模式下把 Core 放在同一进程内。
- Harness Runtime 的硬边界要求进程外通信、公开契约、不 import Core 内部包。

因此，“从 Termcp 分离 Core”不应理解为把源码拆成第二个项目，而应理解为确认 Core 的独立运行和公开契约。

## 2. 候选方案

### 方案 A：Harness Runtime 管理独立 Core 进程（建议目标）

Harness Runtime 使用 Core 的独立可执行文件：

- 明确传入 `--host`、`--port` 和 `--data-dir`。
- 通过 `/api/version`、`/api/daemon` 等公开接口检查实例。
- 只关闭 Harness 明确启动并拥有的进程。
- 通过 HTTP/WebSocket/MCP 与 Core 通信，并把 Agent 工具调用纳入 Harness 的策略、事件和恢复链路。

优点：

- 符合 Harness Runtime 的硬边界。
- Core 和 Harness Runtime 可以独立升级。
- 不需要复制 Core 的 SSH、PTY、审批和权限实现。
- 可以分别测试 Core 版本和 Harness 适配层。

需要确认：

- Core 是否有稳定的前台启动方式，适合由父进程管理。
- 是否提供实例 ID、启动握手、动态端口或明确的子进程所有权信号。
- `/api/daemon` 的返回是否足以区分“Harness 拥有的实例”和“外部实例”。
- Core 的 Web UI 资源是否可以完全不使用，而不影响服务启动。

### 方案 B：使用 Core daemon

Core 已有 `daemon start/status/stop` 等概念，能复用已有实例并查询 PID、URL、版本和日志。

风险：

- CLI 文档说明 daemon 会复用端口上已有应答实例。
- 如果 Harness 直接调用 daemon，可能连接到不是自己启动的实例。
- “停止”和“拥有”必须由可验证的实例身份支撑，不能只凭端口。

可以采用，但必须先确认实例所有权语义。默认优先考虑 Harness 自己启动的受管子进程。

### 方案 C：提取 Core 源码为独立仓库

不推荐作为 Harness 当前任务。

这需要 Core 团队拆分模块、定义发布边界、迁移测试和兼容策略，属于上游重构。Harness 不应先 fork 或复制内部包来“实现分离”。

### 方案 D：import Core 内部包

禁止。

这会绕过公开契约、制造版本耦合，并破坏 Core 与 Harness 的独立更新。

## 3. 版本关系

Core、Desktop、Harness 可以有不同发布节奏：

- Core 发布 vX.Y.Z。
- Desktop 选择兼容的 Core 版本。
- Harness 独立选择兼容的 Core 版本。
- 每个 Harness 版本记录已测试的 Core 版本范围。
- Desktop 的 Core 版本不能自动成为 Harness 的基线。

兼容性通过公开 HTTP/WebSocket/MCP 契约验证，不通过共享源码保证。

## 4. 多客户端共存与冲突处理

### 4.1 三个不同场景

1. **多个客户端连接同一个 Core**
   - 例如 Core Web、Desktop 和 Harness 同时连接一个可验证的 Core。
   - 原则上可以共存，但输入、审批和状态并发语义必须由 Core 保证并测试。
2. **多个 Core 监听同一个地址/端口**
   - 不允许静默复用或抢占。
   - 端口被占用不等于可以连接，也不等于可以终止。
3. **多个 Core 使用同一个数据目录**
   - 默认禁止，除非 Core 明确支持并有验证证据。
   - 这是数据损坏风险，不是普通连接冲突。

### 4.2 启动时的探测顺序

Harness Runtime 启动或连接 Core 时按以下顺序：

1. 检查用户配置的 Core 端点。
2. 请求 `/api/version` 确认服务身份和版本。
3. 请求 `/api/daemon` 获取 daemon、pid、version、started_at、log 等信息。
4. 判断版本和 Harness 适配层是否兼容。
5. 兼容则附着为“外部 Core”，不取得所有权。
6. 不兼容或身份未知则不启动第二份冲突实例，进入诊断状态。
7. 没有可用 Core 时，使用独立端口和独立数据目录启动受管进程。

探测不得通过进程表猜测，不得按端口 kill 进程，也不得把一次成功请求当作永久所有权。

### 4.3 所有权记录

Harness Runtime 只能停止自己启动并持续跟踪的 Core。至少记录：

- 启动命令和参数。
- 端口和 URL。
- 数据目录。
- 子进程 PID。
- `/api/version` 和 `/api/daemon` 的启动时间。
- 版本和契约快照。
- 是否由 Harness 启动。
- 是否已被用户显式接管或断开。

PID 可能复用，端口可能被重新绑定，因此不能只用 PID 或端口作为长期身份。当前 `/api/daemon` 没有明确的数据目录或稳定实例 ID，这属于需要 Core 确认的契约缺口。

### 4.4 默认产品策略

首版安全策略：不自动扫描任意端口，不自动接管未知进程。只探测用户明确配置的端点，或由 Harness 启动独立 Core。

推荐提供两个清晰选项：

- **连接现有 Core**：优先兼容实例，标记为外部 Core，不自动停止。
- **启动独立 Core**：使用独立端口和独立数据目录，标记为受管 Core。

发生冲突时不要自动选择删除、覆盖或重启；显示冲突对象、版本、端点和无法确认的信息，让用户决定。

### 4.5 多客户端并发

多个客户端同时连接同一 Core 时：

- 本地锁和 UI 去重不能描述成跨客户端互斥。
- 输入、审批、会话关闭和重连的并发语义必须由 Core 契约和真实集成测试确认。
- 如果一个客户端无法确认另一客户端的操作，显示来源未知或操作状态未知，不伪造唯一控制权。
## 5. 需要 Core 团队提供的能力

如果现有公开契约不足，应向 Core 团队提出：

1. 清晰的 standalone/headless Core 启动约定。
2. 可验证的实例 ID 和所有权信息。
3. 可选端口、健康检查和优雅关闭语义。
4. 版本发现和兼容范围。
5. 数据目录冲突检测。
6. Web UI 是否可禁用，以及禁用后的服务行为。
7. 事件 sequence/correlation、审批终态和操作查询等能力。

“分离 Core”如果是源码仓库或发布物层面的工作，应由 Core 维护者决定；Harness 只提出使用场景和验收需求。

## 6. 暂定结论

**Harness Runtime 不拆分 Core 源码，而是消费 Core 独立进程的公开契约。**

**优先验证 Harness 自己启动受管 Core 子进程；daemon 模式作为备选，前提是实例所有权可验证。**

**如果 Core 无法提供足够的启动、身份、健康或数据目录契约，记录为 Core 协作项，不在 Harness 内复制内部实现。**
