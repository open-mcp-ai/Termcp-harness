# Harness Core 实例识别与所有权绑定 v1（草案）

> 状态：外部 Core 识别、身份快照、重启检测和受管所有权校验已有代码与测试；受管 Core bundle 的基础启动、就绪验证、停止回收和数据目录独占锁已实现。持久本地 Core profile、external daemon 启动和 Harness 迟附着已有代码、单元测试与真实进程验证。
>
> 日期：2026-11-03
>
> 实现：`internal/coreinstance`、`internal/coreprocess`、`internal/corebundle`、`internal/corelock`、`serve --core-bundle`、`cmd/termcp-harness core identify`、`GET /v1/core`

## 1. 边界

Harness 只通过 Core 公开 HTTP 契约识别实例：

1. `GET /api/version`
2. `GET /api/daemon`

不扫描端口，不通过端口推断进程，不读取 Core 私有数据库，不 import Core 内部包。

端点可达只说明能够通信，不等于 Harness 拥有或可以停止该进程。

## 2. 所有权

- `external`：Harness 连接并识别外部 Core，不拥有进程，不自动停止。
- `managed`：绑定记录包含 Harness 启动时获得的 PID 和进程启动时间；只有与 `/api/daemon` 匹配后才生效。
- `unverified`：声音称 managed 的绑定与观测身份不匹配，属于冲突状态；Harness 不得据此停止进程。

`serve` 显式配置 `--core-bundle` 时，通过 bundle manifest 的版本与 SHA-256 校验后启动受管 Core，并使用启动 PID、启动时间和 Core 自报身份完成 managed 绑定；未配置 bundle 时只绑定 `external`。不会把用户输入的 PID 直接提升为 managed。

## 3. 身份核验

`/api/version.version` 与 `/api/daemon.version` 必须存在且一致。

`/api/daemon` 至少需要：

- `pid > 0`
- 可解析的 `started_at`
- 非空 `version`

`instance_key` 由以下字段计算 SHA-256：

- Harness 规范化后的 Core HTTP origin
- PID
- Core 报告的启动时间
- Core 版本

不得只使用 PID、端口、版本或端点中的任意一项作为长期实例身份。

受管绑定校验：

- PID 必须一致。
- 进程启动时间与 Core `started_at` 的差值必须在容忍范围内；当前默认 10 秒，用于覆盖启动记录时差。
- 任一项不一致时返回 `conflict`，所有权变为 `unverified`。

## 4. 状态

- `identified`：通过版本、daemon 身份和所有权校验。
- `unavailable`：端点不可达、响应无效或协议错误；保留最后一次成功身份作为最后已知快照。
- `conflict`：绑定声明的受管身份与观测身份不一致。

`changed=true` 表示同一 Harness 本轮观察到的 `instance_key` 与上一快照不同。相同端点重新绑定到新进程时，必须视为 Core 已重启，旧 Session、审批和工具上下文不能自动继承。

## 5. CLI

```powershell
go run ./cmd/termcp-harness core identify --endpoint http://127.0.0.1:18765
go run ./cmd/termcp-harness core bundle --binary '<core-executable>' --version '<core-version>' --out '<core-bundle-dir>'
```

输出单个身份快照 JSON。

`serve` 参数：

```powershell
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --core-endpoint http://127.0.0.1:18765
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --core-bundle '<core-bundle-dir>' --core-data-dir '<private-core-data-dir>'
```

持久本地 Core：

```powershell
go run ./cmd/termcp-harness core service install --binary '<core-executable>' --endpoint http://127.0.0.1:18765 --data-dir '<private-core-data-dir>'
go run ./cmd/termcp-harness core service start
go run ./cmd/termcp-harness core service status
go run ./cmd/termcp-harness core service stop
go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787 --core-service-profile '<private-core-service-profile>'
```

profile 固定 Core 二进制绝对路径、版本、SHA-256、loopback endpoint 和持久数据目录；可选凭据只保存为 token 文件引用。Harness 不拥有通过 profile 启动的 daemon：Harness 关闭时只停止自己的 Runtime，Core 继续运行。

如果省略 `--core-endpoint` 但设置 `--mcp-endpoint`，Harness 使用 MCP 端点的 HTTP origin 进行 Core 身份识别。

`core bundle` 不修改源二进制，拒绝覆盖已有 bundle 文件，并写入版本、相对二进制路径和 SHA-256 manifest。`--core-bundle` 不能与 `--core-endpoint` 或 `--mcp-endpoint` 同时使用；受管 Core 默认使用流式 HTTP MCP transport。

`--core-service-profile` 不能与其他 Core/MCP endpoint、stdio MCP 或受管 bundle 组合。未显式传入时，Desktop 和 headless Runtime 会尝试读取默认用户 profile；文件不存在则继续原有受管 bundle 行为。

## 6. 控制 API

`GET /v1/core`：

```json
{
  "configured": true,
  "core": {
    "endpoint": "http://127.0.0.1:18765",
    "state": "identified",
    "ownership": "external",
    "ownership_verified": false,
    "version": "v0.2.8",
    "daemon": true,
    "pid": 14560,
    "started_at": "2026-10-28T13:28:08Z",
    "instance_key": "64-hex-character-key",
    "observed_at": "2026-10-29T01:00:00Z",
    "revision": 1,
    "changed": false
  }
}
```

未配置时返回 `{"configured":false}`。配置后尚未获得首份身份快照时，`configured=true` 且省略 `core`。

## 7. 控制事件

- `core.instance.bound`
- `core.instance.changed`
- `core.instance.unavailable`
- `core.ownership.conflict`

事件只记录端点、状态、所有权、版本、PID、实例键和错误摘要；不记录 Core 凭据或日志正文。

## 8. 已验证

- 对运行中的 Core v0.2.8 完成真实 `/api/version` + `/api/daemon` 识别。
- 真实实例识别结果：外部 Core、PID 14560、版本 `v0.2.8`。
- PID/启动时间匹配的 managed 绑定和身份不匹配的 conflict 分支有单元测试。
- 同一端点切换到新 PID/启动时间时产生 `changed=true` 和新 `instance_key`。
- Core 不可达时保留最后身份并标记 `unavailable`。
- README 和 Desktop 显示相同的只读快照。

## 9. 尚未实现

- 受管 Core 异常退出后的自动重启和崩后恢复策略。
- 非 Harness 启动的 Core 是否遵守同一数据目录协作锁，尚无上游契约保证。
- Core 可执行文件代码签名验证；路径、bundle 边界和 SHA-256 已校验。
- 远程 Core、认证、TLS 和凭据引用。
- 多 Core 实例注册与路由。
- 用真实 Core 重启验证跨重启 Session/审批失效联动。
- Windows Service、登录自启动、托盘控制和 Core 崩溃自动重启。
- Core 重启后 DEAD Session 的完整只读终端历史视图。
