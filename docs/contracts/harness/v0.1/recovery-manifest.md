# Harness Recovery Manifest v1（草案）

> 状态：可选跨 Store 恢复证据与显式基线重建已实现；不提供跨文件原子事务，不恢复审批队列、MCP 连接或活动工具执行。
>
> 实现：`internal/recovery`

## 1. Store

清单可以覆盖：

- `events`：`--event-log-file`
- `sessions`：`--session-store-file`
- `approvals`：`--approval-store-file`

未配置的 Store 标记为 `not_configured`。

## 2. 文件

```json
{
  "version": 1,
  "recorded_at": "2026-11-02T00:00:00Z",
  "stores": {
    "events": {
      "exists": true,
      "size": 4096,
      "fingerprint": "<sha256>"
    },
    "sessions": {
      "exists": true,
      "size": 8192,
      "fingerprint": "<sha256>"
    }
  }
}
```

清单不包含 Store 路径或业务正文。文件上限 1 MiB，Store 指纹检查上限 256 MiB；各 Store 自身保留更严格的业务上限。

## 3. API

`GET /v1/recovery`：

```json
{
  "manifest_configured": true,
  "manifest_present": true,
  "manifest_version": 1,
  "recorded_at": "2026-11-02T00:00:00Z",
  "state": "complete",
  "consistent": true,
  "stores": [
    {
      "name": "events",
      "configured": true,
      "state": "present",
      "exists": true,
      "size": 4096,
      "fingerprint": "<sha256>"
    }
  ]
}
```

Store 状态：

- `not_configured`
- `missing`
- `present`
- `unreadable`

整体状态：

- `not_configured`
- `unverified`
- `complete`
- `partial`

## 4. 边界

- `complete` 只证明所列文件在最近一次正常基线时一致，不代表审批队列、MCP 连接或工具执行已恢复。
- `unverified` 和 `partial` 必须在 CLI 日志、Control API 和 Desktop 中可见。
- `partial` 恢复不得在退出时自动覆盖为完整基线。
- 未知版本、未知字段、多 JSON 值、超大清单和超大 Store 均 fail-closed。
- 自动恢复仍必须同时经过业务状态机和安全 checkpoint 校验。

## 5. 离线检查与显式重建

`recovery status` 使用与 `serve` 相同的 Store 参数计算恢复快照，但不启动 Runtime，也不写入清单：

```powershell
termcp-harness recovery status `
  --recovery-manifest-file '<manifest>' `
  --event-log-file '<events>' `
  --session-store-file '<sessions>' `
  --approval-store-file '<approvals>'
```

`recovery rebaseline --confirm` 将当前可读 Store 的存在性、大小和 SHA-256 写入新的恢复基线，并在 JSON 输出中返回 `previous` 和 `current` 两个快照。该操作用于用户已经核对并接受当前文件集合、准备结束旧的 `partial` 或 `unverified` 状态时。

规则：

- 必须显式提供 `--confirm`。
- 必须提供恢复清单和至少一个 Store。
- 任一已配置 Store 不可读时拒绝重建，并保留旧清单。
- 命令必须只在 Harness Runtime 完全停止后执行；检查与写入之间没有跨进程 Store 锁。
- 不读取或修改 Store 业务正文，不合并不同代次，不修复损坏文件，不回放审批队列、MCP 连接或活动工具执行。
- 新建基线后的 `complete` 只表示当前文件集合与清单一致，不表示内容可继续执行。
- CLI 输出不包含 Store 路径、事件正文、Session 内容、工具参数、审批详情或凭据。

### Deferred：Runtime 活动锁

`HARNESS-RECOVERY-LOCK-001` 已登记但未实现。当前仍要求用户确保 Harness Runtime 完全停止；CLI 不能自动证明该前提。后续计划由 Runtime 与基线命令共享 manifest 旁的活动锁，并让 `status` 显式报告运行中状态。验收与风险说明见 `docs/adr/ADR-022-cross-store-recovery-manifest.md` 第 8 节。
