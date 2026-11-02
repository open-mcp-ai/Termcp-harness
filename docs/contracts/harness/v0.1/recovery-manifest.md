# Harness Recovery Manifest v1（草案）

> 状态：可选跨 Store 恢复证据已实现；不提供跨文件原子事务，不恢复审批队列、MCP 连接或活动工具执行。
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
