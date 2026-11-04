# ADR-019：Harness Event Journal 本地持久化

- 状态：Proposed / 首个切片已实现
- 日期：2026-10-31
- 责任范围：Harness 本地事件顺序、保留窗口和跨 Runtime 重启恢复
- 关联：`ADR-002`、`ADR-015`、`ADR-018`、`docs/contracts/harness/v0.1/event-log.md`

## 1. 背景

Event Journal 原先只保存在内存。Runtime 退出后，已接受的 Runtime、MCP、工具、审批和 Agent Session 控制事件全部丢失；`--session-store-file` 只能恢复 Session 快照、模型消息和最终回复，不能恢复事件 sequence、截断水位或操作轨迹。

事件历史恢复、审批队列恢复、MCP Host 状态恢复和活动 Agent Loop 继续执行是不同问题。持久化事件只能证明 Harness 曾接受并保留某条本地事件，不能证明远端操作仍在运行、已经成功或应被自动重试。

## 2. 决策

- 新增显式 `--event-log-file <path>`；未配置时保持纯内存 Event Journal。
- 文件保存当前有界保留窗口，而不是无限归档：控制事件、诊断事件、`next_sequence`、诊断丢弃计数/水位和 `control_overflow`。
- 进程重启后从文件恢复 sequence 和保留窗口；新事件继续使用单调递增 sequence，不复用旧序号。
- 文件格式使用 schema version 1、JSON object、同目录临时文件、`Sync` 和原子替换；默认上限 64 MiB，POSIX 目标权限 `0600`。
- 每次接受事件后同步持久化。持久化失败时回滚本次内存追加并返回错误，避免把未落盘事件当成已提交事实。
- Windows 上仅对 `ACCESS_DENIED`、`SHARING_VIOLATION` 和 `LOCK_VIOLATION` 使用 5/10/25/50 ms 有界重试，最多 5 次 rename；重试耗尽仍返回错误并回滚，不把扫描器/文件锁导致的瞬时失败写成成功。
- 持久化写入位于 Journal 串行临界区，确保同一进程内磁盘状态不会因旧快照晚到而回退。
- 控制事件容量耗尽时继续 fail-closed，并持久化 `control_overflow`；诊断事件淘汰继续暴露 `dropped_diagnostics` 与 `diagnostic_dropped_through`。
- 文件损坏、版本不支持或结构不合法时启动失败，不静默清空或猜测修复。
- 同一个 Event Journal 文件只支持一个 Runtime 进程独占使用；本轮尚未实现跨进程文件锁。

## 3. 非目标

- 不恢复未决审批、MCP 连接、终端字节流或活动 Agent Loop。
- 不把历史事件重放成新的工具执行或审批决定。
- 不承诺超出内存保留窗口的无限历史；本轮没有压缩归档、分段存储或导出。
- 不持久化 Desktop 的管理 token、模型凭据、终端正文或模型正文。
- Event Journal 文件本身不保存客户端 cursor；Desktop 使用独立 versioned localStorage 记录 endpoint、Runtime instance ID 和最后已消费 sequence，避免把客户端消费位置混入 Runtime 审计文件。

## 4. 验证

- 写入控制/诊断事件后重新创建 Journal，事件、类型和 sequence 保持。
- 恢复后的第一次追加继续使用后续 sequence。
- 诊断淘汰、`truncated`、丢弃水位和 `control_overflow` 跨重启保持。
- 持久化失败时控制/诊断追加均回滚内存状态，sequence 和淘汰计数不前进。
- 并发持久化追加保持唯一、连续 sequence。
- 文件大小边界、JSON schema 和 POSIX 权限测试。
- 真实 Runtime 进程重启：首次启动产生 `sequence=1`，停止并以同一 `--event-log-file` 重启后恢复到 `latest_sequence=2`；两条 `runtime.started` 分属不同 Runtime instance ID，文件 schema v1 保留 2 条控制事件。

## 5. 尚未完成

- 真实 Wails 进程重启后的 cursor 闭环验证；当前只有前端存储解析、实例隔离和重置语义的 Node 回归。
- 事件归档、压缩、分段存储、迁移和导出。
- 审批队列、MCP 状态和活动 Agent Loop 恢复。
- 更高吞吐的批量/追加日志格式；当前同步整窗快照适合首个可靠切片，不是大数据量最终形态。
- 多 Runtime 共享同一 Event Journal 文件的跨进程互斥。
