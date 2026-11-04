# MCP 管理写入能力实施计划（v0.2.0）

> 状态：Planned；不进入当前 `v0.1.10` 推送范围，代码尚未实现。
>
> 目标版本：`v0.2.0`
>
> 关联：`docs/contracts/harness/v0.1/mcp-server-states.md`、`docs/contracts/harness/v0.1/control-api.md`、`docs/contracts/harness/v0.1/desktop-shell.md`

## 1. 版本边界

### v0.1.9 / v0.1.10

- 只做当前能力收敛、缺陷修复、构建与推送稳定性。
- 保留已实现的 Codex global 发现和受保护显式注册。
- 不新增 MCP 新增/编辑/删除表单，不持久化动态发现注册，不写回 Codex/OpenCode 配置。

当前仓库 tag 最新为 `v0.1.9`，工作树版本为 `v0.1.10`。`v0.1.10` 仍是发布收敛版：

- 必须在 changelog 中明确它是未单独发布的 `v0.1.7`、`v0.1.8` 之后的首个实际推送版本；
- 不得把 `v0.1.7` 或 `v0.1.8` 写成已发布 tag；
- 在 release 前统一 CLI、Desktop、Wails、package 和 tag 版本。

### v0.2.0

- 增加 Harness 自有 MCP 注册表的完整写入生命周期。
- 支持添加、编辑、移除、持久化和重启恢复。
- 支持从本机发现结果导入为 Harness 管理项。
- 不自动修改外部 Codex/OpenCode 配置；外部同步另立显式导出/注入能力。

## 2. 用户能力

### 添加 MCP

表单至少包含：

- 稳定且不可重复的 Server ID；
- 显示名称；
- transport：`stdio`、`streamable-http` 或 `sse`；
- stdio：command、args、workdir、允许转发的环境变量名；
- 回环 HTTP/SSE：endpoint、可选 Bearer token 文件引用；
- enabled 初始意图；
- 用户 tags。

约束：

- ID 创建后不可原地修改；如需新 ID，创建新项后再移除旧项。
- 非回环 HTTP/SSE 继续拒绝。
- 不在 UI、事件、日志或普通配置中保存 token 值。
- 不允许任意 shell 字符串；stdio 必须是可验证的 executable 或受约束的 npm shim。

### 编辑 MCP

- 显示名称、用户 tags、enabled 意图可直接编辑。
- command、args、workdir、endpoint、transport、凭据引用发生变化时，必须替换 live client 并重新读取能力。
- 修改使用 revision CAS；旧 revision 返回冲突，不覆盖新状态。
- 该 Server 存在无法证明结果的工具调用或正在派发时，拒绝会改变运行时定义的编辑，避免旧 call 被重新映射到新配置。
- 编辑后旧工具描述和 Schema 立即失效；新能力未读取前不能进入 `ready`。

### 移除 MCP

- Desktop 必须显示移除影响并要求确认。
- Harness 拥有的 stdio 子进程由 Host 停止并回收。
- 外部 HTTP/SSE 只断开 Harness 连接，不终止远端服务。
- 停止失败、断开失败或存在结果未知调用时，不伪造“已移除”；保留诊断并提供重试。
- 移除 persistent 条目先进入明确的 pending/unknown 收敛，再删除配置；不得因请求超时直接丢失所有权证据。

### 从发现导入

- Codex global 等发现项可复制成 Harness 自有定义。
- 保留只读来源 tags，例如 `codex`、`global`、`discovered`。
- 用户 tags 与来源 tags 分开保存。
- 带未知 env/header/凭据的定义不能一键导入，必须进入编辑表单补齐非敏感配置或凭据引用。
- 导入不写回来源客户端。

## 3. Runtime 与存储

### 持久化注册表

新增独立的 MCP Registry Store，建议通过显式 `--mcp-registry-file` 启用：

- versioned JSON schema；
- 有界文件大小；
- 原子替换与受限权限；
- 不保存 token/Authorization 值；
- 不保存模型上下文、工具参数或工具结果；
- 不保存普通日志或终端正文。

`--mcp-config-file` 继续表示启动时只读的兼容配置来源，不在 UI 编辑时原地重写，避免破坏已有部署配置。Registry 与只读配置的合并规则必须显式：

- ID 冲突时启动失败并显示冲突；
- 来源和用户 tags 分开；
- 删除 Registry 条目不能删除只读配置文件中的定义。

### Host 生命周期

`internal/mcpcontrol` 需要增加：

- 安全替换同一 ID 的运行时定义；
- 注销并回收 managed stdio；
- 编辑/移除前的 active call 门；
- revision 单调推进；
- 旧连接、旧能力读取和旧 supervisor 结果不能覆盖新定义。

任何当前执行结果无法确认时，保留 `unknown`，不得为完成编辑而强制删除状态。

## 4. 控制 API

所有写入口默认关闭，使用现有受限 Bearer 控制，并冻结为固定路由，不增加通用 HTTP 桥：

### `POST /v1/mcp/servers`

创建并持久化 Harness 自有 MCP 定义。响应区分：

- `201`：配置已保存且完成初始注册；
- `202`：配置已保存、连接仍在后台收敛；
- `400/422`：参数或 transport 不合法；
- `409`：ID 冲突；
- `503`：mutation control 未配置。

### `PATCH /v1/mcp/servers/{id}`

请求必须带 `expected_revision`。显示属性、启用意图和运行时定义采用不同语义；需要替换连接的修改返回新的 revision 和中间状态。

### `DELETE /v1/mcp/servers/{id}`

请求必须带 `expected_revision`。成功只表示 Harness 已移除自己的注册和受管进程；外部 Server 的远端进程仍按独立生命周期运行。

### 生命周期端点

复用受保护控制，补齐：

- Connect
- Disconnect
- Enable
- Disable

每个操作返回服务端 revision 和实际状态，不在前端乐观更新为终态。

## 5. Desktop

- MCP 面板增加“添加 MCP”和每项“编辑/移除/连接/断开/启用/停用”。
- 顶层统计必须明确区分“已发现 MCP”和“已登记 MCP `ready/total`”；发现项不进入 ready 分母，也不把发现或登记显示成已连接/已启用。详细交互见 `UX-BL-011`。
- 添加/编辑使用同一表单，运行时字段变更显示“将重新连接并刷新能力”提示。
- 凭据使用受限文件选择或已有凭据引用，不提供把 Token 存入 localStorage 的字段。
- 保存失败、连接失败、能力读取失败和配置写入失败分别展示。
- 删除外部服务时显示“只断开 Harness”；删除 managed stdio 时显示“将停止 Harness 启动的进程”。
- 发现导入先进入预览，不能静默覆盖已存在 ID。

## 6. 验收

- 新增 stdio MCP 后持久化，Runtime 重启恢复并达到 ready。
- 新增回环 Streamable HTTP MCP 后连接、读取能力和启用。
- 编辑显示名/tags/enabled 不重启无关 Server。
- 编辑 transport、command 或 endpoint 会替换连接，旧工具引用不可继续使用。
- revision 冲突、非法 ID、重复 ID、非回环 endpoint 和包含明文凭据的请求被拒绝。
- 移除 managed stdio 后子进程退出；移除 external HTTP 不影响远端服务。
- Registry 原子写入失败时旧配置仍可读取，错误可见。
- 删除或编辑期间出现不可确认调用时保持 `unknown`，不自动重放。
- 发现导入保留来源 tags，且不修改 Codex/OpenCode 原配置。
- API 与 Desktop 回归、敏感值泄漏扫描、Windows/POSIX 文件和路径测试通过。

## 7. 后续独立能力

以下不纳入 `v0.2.0` 必需范围：

- 写回 Codex/OpenCode/Claude/VS Code 配置；
- 自动 OAuth 登录和非回环 MCP；
- MCP 市场、签名目录和在线安装；
- 跨设备配置同步；
- 基于 UI 表单直接执行任意脚本。

外部配置注入必须是显式预览、备份、可逆且按客户端 schema 实现的独立能力。
