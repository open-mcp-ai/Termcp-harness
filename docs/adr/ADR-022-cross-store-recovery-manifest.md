# ADR-022：跨 Store 恢复清单与部分恢复诊断

- 状态：Accepted / 恢复清单与显式基线重建已实现
- 日期：2026-11-02
- 责任范围：Harness Event Journal、Agent Session Store、Approval Store 的跨文件恢复证据
- 关联：`ADR-018`、`ADR-019`、`ADR-020`、`docs/contracts/harness/v0.1/recovery-manifest.md`

## 1. 背景

Event Journal、Agent Session 和 Approval History 使用独立文件。每个 Store 都能独立验证 JSON、版本、容量和 fail-closed 语义，但独立可读不等于它们属于同一次一致恢复。

可能的故障包括：

- 一个 Store 写入成功而另一个失败。
- 进程崩溃导致恢复清单落后于部分 Store。
- 一个 Store 被旧文件替换。
- 单个 Store 损坏后，其他 Store 被误认为完整恢复。
- 客户端只看到部分数据，却以为所有 Store 都来自同一代次。

## 2. 决策

新增可选的 `--recovery-manifest-file`。清单只保存非敏感恢复证据：

- Manifest schema version。
- 最近一次完整基线时间。
- 每个已配置 Store 是否存在、文件大小和 SHA-256 指纹。

清单不保存文件路径、事件正文、Session 内容、工具参数、审批详情或凭据。

启动时返回以下状态：

- `not_configured`：未配置持久化 Store。
- `unverified`：Store 已配置或存在，但没有可用于证明同代次的清单。
- `complete`：所有已配置 Store 的是否存在、大小和指纹与清单一致。
- `partial`：清单损坏、版本不支持、Store 不可读，或任一 Store 与清单不一致。

## 3. 写入规则

- 第一次配置时，如果所有目标 Store 都不存在，可建立空基线。
- Runtime 正常停止、所有请求和后台写入收口后，更新恢复清单。
- 启动时已经判定为 `partial` 的恢复不得在退出时静默覆盖为新基线。
- 清单和 Store 都使用受限权限和原子文件替换。
- 清单写入失败必须记录可见错误；不得宣称恢复一致性已经建立。

## 4. 不承诺

- 不承诺跨文件原子事务。
- 不把 `complete` 解释为所有业务状态一定可继续；每个 Store 和业务状态机仍独立 fail-closed。
- 不从清单回放审批、工具执行、MCP 连接或 Agent Loop。
- 尚未实现的自动恢复和会话继续必须继续阻塞，不能仅凭清单完整就恢复副作用操作。

## 5. 验证

- 无 Store、无清单、匹配清单、Store 变更、Store 缺失、Store 不可读、清单损坏和未知版本测试。
- Control API 返回恢复状态但不返回 Store 路径。
- Desktop 诊断面板显示状态、清单版本、基线时间、Store 状态、大小和指纹摘要。
- CLI 启动日志记录恢复状态；`partial` 使用警告日志。

## 6. 后续

- 跨 Store 恢复代次的进一步绑定。
- 自动恢复只允许在完整一致性证据和现有安全检查点同时满足时进入。
- `HARNESS-RECOVERY-LOCK-001`（Deferred）：为 Runtime 与离线基线命令增加跨进程活动锁，消除“由用户保证 Runtime 已停止”的隐性前提。

## 7. 显式基线重建

`recovery status` 提供不启动 Runtime 的只读检查。`recovery rebaseline --confirm` 允许用户在接受当前文件集合后重建基线，并返回重建前后的快照。

该命令不把重建解释为业务一致性修复：它不合并 Store、不修复文件、不回放副作用，也不允许在 Runtime 运行期间安全执行。由于 Store 尚无跨进程锁，操作前提是 Harness Runtime 已完全停止。

## 8. Deferred：Runtime 活动锁

- 状态：**Deferred（2026-11-03）**，不阻塞当前 `v0.1.10` 开发快照。
- 风险：`recovery status` 与 `recovery rebaseline` 从读取 Store 到写入清单之间没有跨进程互斥；若 Runtime 同时运行，可能把不同时刻的文件状态记录成新的 `complete` 基线。它不会直接损坏 Store，也不会触发自动恢复，但会产生错误的恢复信心。
- 计划方案：Runtime 配置恢复清单时持有 manifest 旁的 OS 活动锁，从加载持久化 Store 起保持到停止时清单更新完成；`recovery rebaseline` 非阻塞获取同一锁，失败时拒绝读取后继续写入；`recovery status` 发现活动锁时明确返回运行中警告，不把结果当作稳定代次。
- 验收：两个独立进程竞争同一清单时只有一个获得活动权；Windows 与 POSIX 均覆盖；进程崩溃后锁可释放且不会依赖过期 PID 文件；`rebaseline` 在 Runtime 运行时拒绝写入且保留旧清单；`status` 不误报 `complete`。
- 提升时机：在把恢复清单用于自动恢复、提高 rebaseline 为常规运维入口，或关闭 `HARNESS-RECOVERY-LOCK-001` 前。
