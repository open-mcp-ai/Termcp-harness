# ADR-018：Agent Session 与模型 Transcript 本地持久化

- 状态：Proposed / 首个切片与安全检查点人工恢复已实现
- 日期：2026-10-31
- 责任范围：Harness Agent Session、模型上下文和最终回复的本地持久化边界
- 关联：`ADR-011`、`ADR-017`、`docs/contracts/harness/v0.1/agent-session.md`、`docs/contracts/harness/v0.1/control-api.md`

## 1. 背景

Agent Session 和模型上下文此前只存在于内存。Runtime 重启后，已完成任务、模型最终回复和完整对话轨迹都会丢失，Desktop 也只在当前视图临时保存最后一次模型回复。

事件日志持久化、审批协调器持久化和活动 Agent Loop 跨重启继续执行是不同问题，不能用一个本地 Session 文件假装全部恢复。

## 2. 决策

- 新增显式 `--session-store-file <path>`；未配置时保持纯内存行为。
- 文件保存有界 Agent Session Snapshot、模型消息和最终回复。模型消息可能包含用户输入、工具结果摘要或显式允许回填的工具正文，因此文件属于敏感本地状态。
- POSIX 下文件以 `0600` 写入；写入使用同目录临时文件、`Sync` 和原子替换。Windows 原子替换仅对 access-denied/sharing/lock 错误使用 5/10/25/50 ms 有界重试，耗尽后返回错误。文件大小默认上限 64 MiB，Session 数默认上限 1024。
- 每次 Session 控制事件成功记录后同步持久化当前 Session 集合；Manager 将“抓取跨 Session 快照 + 保存”作为同一临界区串行执行，避免旧快照晚到覆盖新状态；持久化失败作为显式错误返回，不静默忽略。
- 重启时恢复已完成和其他终态 Session、未运行的 `new` Session，以及 transcript/final output。
- 重启时发现的 `running`、`waiting_approval`、`waiting_execution` Session fail-closed 收敛为 `unknown`，保留 call/approval 元数据，记录 `runtime_restart` 原因，不推断远端已执行、失败或已取消。
- Session Store v2 保存活动 Loop checkpoint 的阶段、预算、工具身份/描述/Schema hash、待执行 call ID/名称/参数 hash/字节数。工具参数正文不复制进 checkpoint，仍只存在于既有 transcript 边界。
- checkpoint 在模型请求、工具就绪、工具派发前、工具结果观察、审批等待和执行等待边界同步更新。`model_requesting` / `tools_ready` 标记为无新工具副作用的边界；派发中、结果未持久化、审批或执行未确认均明确阻塞继续。
- 重启恢复只暴露无敏感正文的 recovery summary，并继续 fail-closed。`model_requesting` / `tools_ready` 可由 Bearer 保护的显式入口领取；领取前必须用当前 Runtime 工具目录和持久化 transcript/参数 hash 复验，同一 Session 只允许一次领取。自动恢复不在本切片内。
- 新增 Bearer 保护的 `GET /v1/agent/sessions/{id}/transcript`。Desktop 通过固定 Go 桥读取，不向浏览器暴露 token 或通用 HTTP。
- Session 文件不包含事件日志或审批历史；Event Journal 与审批历史可分别通过 `--event-log-file`、`--approval-store-file` 持久化（见 ADR-019、ADR-020）。MCP Host 状态和活动 Agent Loop 仍未持久化，不能宣称完整跨重启恢复。

## 3. 验证

- 完成模型 Session 写入文件后，新 Manager 恢复 Session、messages 和 final output。
- 持久化的 `waiting_approval` Session 重启后变为 `unknown`，保留审批关联并记录恢复原因。
- 活动 Loop checkpoint 在 waiting approval 和工具派发前可落盘；工具派发前 checkpoint 必须在调用远端工具前可见。
- 重启后 checkpoint 阶段、安全边界、阻塞原因和计数保留；工具参数正文不进入 checkpoint 或 recovery summary/event。
- Session Store v2 兼容读取 v1 终态 Session。
- 文件大小、JSON 结构、重复 Session ID 和 POSIX 权限测试。
- 并发创建 Session 时持久化保存不得重叠，且最后一次保存必须包含全部已创建 Session；防止旧快照回滚新状态。
- 受保护 transcript API、Control Client、Desktop Go 桥和前端类型测试。
- 真实 Runtime 进程重启验证：本地模型桩生成最终回复，`--session-store-file` 落盘；停止并重启 Runtime 后 Session 仍为 `completed`，受保护 transcript 返回原消息和 `persisted reply from local stub`。
- 真实 Wails 窗口验证：Runtime 重启后选择恢复的 Session，Desktop 显示“持久化模型回复”和 3 条模型消息；当次未配置 Event Journal Store，因此事件时间线为空。
- 真实 Wails checkpoint 验证：用待审批 checkpoint fixture 启动临时 Runtime，恢复 Session 仍为 `unknown`；Agent 面板显示等待审批阶段和“审批结果未确认”阻塞原因，并显示 iteration/tool/pending/call 元数据，没有自动或手动继续入口。
- 安全检查点恢复测试：从 `tools_ready` 恢复只执行待派发工具并在新模型轮完成；从 `model_requesting` 恢复不重复递增已完成轮次；工具描述哈希变化、unsafe checkpoint、重复领取均被拒绝且无模型/工具副作用。

## 4. 尚未完成

- Session Store 与 Event Journal Store 之间尚无跨文件原子事务；一侧写入失败时可能短暂出现历史与 Snapshot 不一致。
- 审批队列、MCP 连接和活动工具执行跨重启恢复。
- 活动 Loop 的自动恢复，以及无法证明未产生工具副作用的 checkpoint 人工继续。
- 模型对话继续执行或摘要压缩。
- 文件加密、密钥管理、轮换和存储迁移。
