# ADR-015：Harness 实时事件流

- 状态：Accepted / Event Journal 持久化切片见 ADR-019
- 日期：2026-10-31
- 责任范围：Harness Runtime 事件推送与 Desktop 协调
- 关联：`ADR-002`、`ADR-007`、`ADR-011`、`ADR-014`、`docs/contracts/harness/v0.1/event-log.md`

## 1. 背景

Desktop 原先每 2.5 秒读取 Bootstrap 和事件窗口。该方式可以工作，但 Activity、审批和 Session 状态更新存在固定延迟；单纯降低轮询频率会增加请求量和无变化重绘。

## 2. 决策

Harness Runtime 提供 `GET /v1/events/stream` SSE：

- 使用本地单调 sequence 作为 cursor。
- 每批发送完整有界 `Window`，保留截断和 overflow 元数据。
- 支持 `after` 和 `Last-Event-ID`。
- 无事件时发送 heartbeat。
- 可选 `--event-log-file` 持久化有界 Event Journal 与 sequence；恢复仍以控制 API 快照为权威。

Desktop 在 Wails Go 进程维护 SSE 连接和重连：

- React 只使用固定 `OpenEventStream`、`PollEventStream`、`CloseEventStream`，不获得 URL 或通用连接能力。
- 未消费事件最多缓存 2048 条；超过上限进入可见 `lagged`，通过重新获取 Bootstrap 和最新 cursor 恢复。
- 事件立即合并到 Activity；控制事件在 120 ms 防抖后触发一次完整快照刷新。
- Session、审批、MCP、Core 和 Runtime 状态仍以控制 API 快照为权威。
- 每 15 秒保留一次兜底 Bootstrap，不依赖事件流证明 UI 仍完整。

## 3. 顺序与恢复

- SSE 的 `event_id` 不替代 operation/correlation ID。
- 同连接和同一 Runtime 实例内按 `sequence` 去重和排序。
- Runtime 实例变化时关闭旧订阅并从新实例最新 cursor 重开。
- 诊断事件淘汰或本地队列 lag 必须可见；不得静默丢控制事件。
- 配置 Event Journal 持久化后，Runtime 重启可恢复保留窗口和 sequence。Desktop 将最后消费的 cursor 按 Runtime endpoint 和 instance ID 保存到本机 localStorage；同一实例重启 Desktop 可继续补拉，实例变化、记录损坏或 cursor 超前时重置到最新 sequence。该机制仍不承诺恢复 Event Journal 已淘汰的区段。

## 4. 验证

已执行：

- Event Log Wait/广播测试。
- SSE 初始窗口、实时事件和 `Last-Event-ID` 测试。
- Desktop SSE 桥接测试。
- Subscription 断线重连、cursor 去重和 retention metadata 测试。
- 前端事件合并、去重、排序、截断和 1000 条可见窗口测试。
- 真实 Wails：`Events 实时` 状态下创建并运行 Agent Session，界面在约 108 ms 内更新，远早于 15 秒兜底轮询。
- Event Journal 文件恢复、sequence 连续、淘汰/overflow 水位和持久化失败回滚测试。

## 5. 尚未完成

- Event Journal 已淘汰区段的归档恢复和客户端跨 Runtime 轨迹连续性。
- 事件归档、压缩和分段存储。
- 多 Runtime 实例路由和认证订阅。
