# ADR-020：审批历史本地持久化

- 状态：Proposed / 首个切片已实现
- 日期：2026-10-31
- 责任范围：Harness 审批关联、决定结果和最后已知执行元数据
- 关联：`ADR-005`、`ADR-007`、`ADR-018`、`ADR-019`、`docs/contracts/harness/v0.1/state-machines.md`

## 1. 背景

审批协调器原先只在内存保存已可信关联的审批请求。Runtime 重启后，已批准、已拒绝、已过期和已取消的审批历史全部丢失，无法解释此前的工具调用，也不能重新显示最后已知的结构化执行结果。

审批历史的持久化不等于恢复远端审批队列，更不允许重放决定、重新派发工具或自动继续 Agent Loop。Core 仍是审批和执行的权威；Harness 只保存自己曾经观察到的事实。

## 2. 决策

- 新增显式 `--approval-store-file <path>`；未配置时审批协调器保持纯内存。
- 文件保存可信关联 `Tracked` 的 Approval Snapshot 与 Reference，包括最后已知的 `decision`、`revision` 和结构化 `execution`。
- 每次新建可信关联、提交/确认决定和更新执行元数据后同步持久化。
- 文件格式使用 schema version 1、同目录临时文件、`Sync` 和原子替换；默认最多 4096 条审批、32 MiB，POSIX 目标权限 `0600`。
- `Tracked` 文件可能包含审批摘要和执行错误文本，属于敏感本地状态；不得放入普通日志、URL 或前端持久化。
- 持久化写入串行化，确保较旧的跨审批快照不会晚于新快照落盘。

## 3. 重启恢复

- `approved`、`rejected`、`expired`、`cancelled` 和 `unknown` 作为只读历史恢复。
- 持久化的 `pending` / `submitting` 在重启后 fail-closed 收敛为 `unknown`，revision 增加，保留可信 `approval_id`、`call_id`、原决定和 session/expiry 关联。
- 恢复请求设置 `recovered=true` 和 `recovery_reason=runtime_restart`；不再允许提交批准或拒绝。
- 恢复过程不调用 Core Lookup、不启动自动 Execution Reconciliation、不通知 Agent Session、不重放审批决定或工具调用。
- 已批准且执行仍未完成的恢复记录可由用户显式触发既有只读 Execution 刷新；刷新只读取 Core，不重新提交决定。
- `unknown` 审批可经受保护的 `POST /v1/approvals/{id}/reconcile` 执行一次只读 Core Lookup；远端仍为 `pending` 时继续保持 `unknown`，远端终态才更新本地历史。
- 只读收敛写入 `approval.reconciled` 事件，记录 `remote_status`、`changed`、`resolved` 和 `read_only_lookup` 原因；不得生成新的决定提交。
- 文件损坏、未知版本或结构不合法时启动失败，不静默清空或猜测修复。
- 同一文件只支持一个 Runtime 进程独占使用；跨进程文件锁尚未实现。

## 4. 失败语义

- 新建关联后持久化失败会撤销内存登记并返回错误。
- 提交决定前持久化失败时不调用远端，内存状态收敛为 `unknown`。
- 远端决定已经确认但本地历史持久化失败时，不撤销或伪造远端结果；API 返回已确认 Snapshot，并带本地历史未持久化警告。
- 审批 Store、Event Journal 和 Session Store 之间没有跨文件原子事务；部分写入可能留下可诊断的不一致，不能宣称三者原子提交。

## 5. 非目标

- 不恢复 Core 侧审批队列，不把本地历史重新登记为可决定请求。
- 不自动批准、拒绝、重试工具调用或继续 Agent Loop。
- 不替代事件日志；事件用于操作轨迹，审批 Store 用于当前历史快照。
- 不提供审批正文归档、分页历史、加密、密钥轮换或跨进程锁。

## 6. 验证

- 已确认审批和结构化执行结果跨 Coordinator 重建恢复。
- `pending` / `submitting` 恢复为 `unknown`，revision 前进，决定入口关闭，远端决定调用数为 0。
- 恢复 approved 历史时不自动 Lookup、Reconciliation 或通知 Session；显式只读刷新可更新执行元数据。
- 持久化失败时新建关联回滚、决定提交前的远端调用被阻止、确认后的远端结果保留并显式返回持久化错误。
- 并发关联写入后恢复全部审批。
- 文件大小、schema 和 POSIX 权限测试。
- 真实 Runtime 两次启动/停止：持久化 `pending` 首次恢复为 `unknown`、revision 2；已批准审批及 `succeeded` Execution 保持。第二次重启后 `unknown` revision 仍为 2，恢复事件原因保持 `runtime_restart`，Core 桩收到的审批决定/Lookup 调用数为 0。
- `unknown` 显式只读收敛：远端 approved/rejected/expired/cancelled 更新历史并通知观察者；远端 pending 保持 unknown；决定调用数为 0。
- 真实 Runtime + 本地 Core HTTP 桩：`unknown` 经 reconcile 得到 `rejected`、revision 3、`changed=true/resolved=true`；Core 仅收到 1 次 GET Lookup，批准/拒绝 POST 为 0，审批 Store 最终状态为 `rejected`。
- 真实 Wails 窗口：重启后显示 `approval-approved` 的已批准/执行成功历史和 `approval-pending` 的恢复未知状态；点击“只读核对”后显示“已拒绝 / revision 3 / 服务端已确认”，审批时间线出现 `approval.reconciled`。再次重启 Runtime 并重开 Desktop 后，审批仍显示已拒绝 revision 3，事件窗口保留 `approval.reconciled`。

## 7. 尚未完成

- 审批历史分页、归档、压缩、加密和迁移。
- Session Store、Event Journal Store 与 Approval Store 的跨文件一致性。
- 多 Runtime 共享同一审批历史文件的跨进程互斥。
