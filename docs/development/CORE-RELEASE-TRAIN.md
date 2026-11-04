# Harness 与 Core 发布列车

> 状态：Draft（2026-11-03）
>
> 目标：避免把“Harness 适配 Core”建立在用户自行选择、安装和同步两个独立发布物的运气上。

## 1. 当前结论

Harness 不再把“官方 Core 发布包尚未同步”默认视为所有开发的阻塞项。正确的发布单位应是一次经过共同验证的 **Harness + Core 组合**：

- 每个 Harness 发布版本固定一个 Core 版本、commit 和二进制 SHA-256。
- 该 Core 可以来自 Termcp 正式发布，也可以来自 Termcp 当前开发基线或 Harness 集成分支，但必须记录来源，不能冒充正式发布。
- Harness 只通过公开 HTTP、WebSocket 和 MCP 契约使用 Core，不 import Core 内部包。
- 发布前必须用该组合跑真实契约和桌面 E2E，而不是只分别编译两个程序。

推荐产品形态是“**单安装包、双进程、同版本锁定**”：安装器携带匹配的 Harness 与 Core sidecar；运行时由 Harness 启动受管 Core，仍通过公开契约通信。这样解决单装和版本漂移问题，同时保留 headless、外部 Core 和故障隔离能力。

用户可见的最终形态可以进一步收敛为“**一个产品、一个入口、自带 Termcp**”：

```text
Termcp Harness 安装包 / 快捷方式
  -> 启动 Harness Runtime（可由 Desktop 启动器管理）
       -> 自动启动固定版本的 bundled Termcp Core
  -> 打开 Harness Desktop
```

这仍然保持两个核心进程，但用户不需要单独安装或手动启动 Core。当前基础已经包括：

- `core bundle` 生成带版本和 SHA-256 的 Core bundle。
- `corebundle.Discover` 自动查找可执行文件旁的 `core/`。
- `--core-bundle` 启动受管 Core。
- `coreprocess` 负责随机回环端口、临时认证、数据目录锁和进程回收。

Desktop 启动编排已实现：首个实例进入 Wails `OnStartup` 后，在同一 Desktop 进程内启动 Harness Runtime；Runtime 从可执行文件旁的 `core/` 发现并启动固定 bundle 的 Core。关闭按钮提供“最小化到任务栏”和“退出 Harness”，只有确认退出才按所有权停止 Runtime 与 Core。`--runtime` 仍保留为外部 Headless Runtime 调试入口。该目标不 import Core 内部包，也不等于把 Core 源码并进 Harness。

`desktop/scripts/package-release.ps1` 已能组装免安装目录和 ZIP：复制 Desktop EXE、生成 `core/` bundle、写入 `release.json`、逐文件 SHA-256 和 ZIP 校验文件。脚本默认拒绝 dirty Harness/Core；`-AllowDirty` 产物明确标记为 `candidate`，`-SmokeTest` 从包内入口启动并核验受管 Core。

仍未完成的是安装器、代码签名，以及后续 Core 补丁包的分发、签名、回滚和兼容性验证。

## 2. 为什么单纯“合并成一个进程”不是完整答案

合并进程或合并仓库可以消除“用户装错 Core”的问题，但不能消除：

- 行为契约变化和回归测试成本。
- Core 崩溃、内存泄漏或阻塞对 Harness/Desktop 的连带影响。
- Harness 仍需支持外部 Core、headless daemon 或远程 Core 时的双路径。
- 调试、权限、数据目录和进程所有权边界。

因此，“一个安装体验”应优先实现为打包一个匹配的 Core sidecar，而不是复制 Core 源码或绕过公开契约。

## 3. Harness 依赖的 Core 变化

以下基于 2026-11-03 本地仓库观察。远端实时状态因本机 Git/OpenSSL credential 错误未能重新核验。

| 能力 | Core 本地提交/位置 | 当前吸收状态 | Harness 用途 |
|---|---|---|---|
| 审批过期 fail-closed | `a2c9b4e` | 已在本地 `upstream/dev` | 无 waiter 的 review 请求也会过期，迟到的批准不能执行 |
| Session 关闭后保留审批终态 | `9d5306e` | 已在本地 `upstream/dev` | 关闭 Session 后仍能观察 `cancelled`，不伪造消失或成功 |
| 可信审批关联 ID 与 Execution | `938c53f` | 本地 `fix/approval-correlation` / `codex/core-local-integration`，尚未进入本地 `upstream/dev` | `review_pending` 返回 `Approval-ID`、`Session-ID`、`Expires-At`；Harness 据此建立 trusted correlation 并读取 Execution |
| 终端订阅 `watch_ready` | `d8261c7` | 本地 `codex/core-local-integration`，未 push | 历史读取和实时输出按绝对字节游标原子衔接 |
| 终端帧 `[start,end)` | `d8261c7` | 同上 | 检测重叠、重复和缺口，禁止按时间或文本猜测连续性 |
| `terminal_done.cursor` | `d8261c7` | 同上 | Shell 结束时证明最后输出边界 |
| `term` 的 shell review 文本 | 当前 Core 行为 | 已由 Harness 兼容 | 文件/转发使用 `Submitted for review:`，shell 输入使用 `Submitted for review.`；Harness 两种都识别 |

当前候选集成基线为：

```text
d8261c7 (approval correlation + terminal watch cursor)
```

`d8261c7` 是 `938c53f` 之上的本地集成 commit，工作树干净；尚未 push 或进入 `upstream/dev`。

## 4. 发布与兼容策略

### 方案 A：单安装包 + Core sidecar（推荐）

- 安装器中同时携带 Harness 和匹配的 Core。
- Harness 通过现有 `core bundle` / `--core-bundle` 启动 Core。
- bundle manifest 记录 Core 版本、commit、平台、SHA-256 和能力指纹。
- 用户只安装一个产品，但运行时仍是两个进程。
- Desktop 内嵌 Runtime 装配，Core 仍是受管子进程；本次已实现该启动边界，安装器组装仍待完成。

优点：单装、版本固定、故障隔离、仍可切换到外部 Core。

### 方案 B：Harness 自动下载固定 Core

- 安装包只含 Harness。
- 首次启动时从签名源下载锁定的 Core sidecar。
- 校验签名、SHA-256、版本和平台后启动。

优点：安装包更小；缺点：依赖下载网络和供应链，离线部署更复杂。

### 方案 C：版本适配层 + 兼容矩阵

- Harness 根据 `/api/version`、能力探测和适配层支持有限 Core 版本范围。
- 不满足必需能力时 fail-closed，并明确显示缺少的契约。

优点：允许用户连接已有或远程 Core；缺点：测试矩阵和兼容维护成本最高。

### 方案 D：真正的单进程/单二进制

- 将 Core 作为嵌入组件与 Harness 合并。

这可以作为未来可选 All-in-One 构建，但不建议替代公开契约边界。它会让 headless、外部 Core、独立升级和故障隔离能力更难保留。

### 方案 E：用户自行安装外部 Core

- 仅作为高级模式。
- 必须显示兼容状态、版本和缺失能力，不能默认假定兼容。

## 5. 建议的版本标识

示例：

```text
Harness v0.1.10+core.termcp-v0.2.8.d8261c7
```

至少记录：

- Harness 版本与 commit。
- Core 版本与 commit。
- Core 构建来源（官方 release、开发基线或本地集成分支）。
- Core 二进制 SHA-256。
- 是否包含终端 watch cursor 扩展。
- 执行过的契约/E2E 测试集合。

## 6. 发布流水线

1. 从固定 Core commit 构建目标平台 Core。
2. 生成 Core bundle manifest，校验 SHA-256。
3. 构建 Harness 与 Desktop。
4. 用该 bundle 启动受管 Core。
5. 跑审批、Session、Terminal、工具调用、重连和恢复的契约测试。
6. 运行真实 Wails 冒烟测试并记录版本、实例和事件证据。
7. 只在全部通过后生成同一版本的安装包和发布说明。

## 7. 尚未解决

- 本地 `upstream/dev` 尚未包含可信审批关联提交和终端 watch cursor 补丁。
- 远端 upstream 是否已吸收这些提交，当前未能实时核验。
- `d8261c7` 仍是本地 commit，尚未 push 或进入上游发布流程。
- 尚未定义 capability manifest、兼容矩阵和安装包签名格式。
- 尚未实现 Core 补丁包的下载、签名验证、原子替换、回滚和运行中升级提示。
- 仍未决定是否提供可选的单进程 All-in-One 构建。
