# Termcp Core v0.2.8 契约元数据

> 状态：原 release 基线保留；2026-10-31 已用本地 `f6cefec-dirty` 源码构建覆盖当前运行实例进行审批关联验证。本文记录两者的来源和边界。
>
> 不等同于完整行为兼容性认证。

## 来源

- 本地启动器：`D:\software\fnm\aliases\default\termcp.ps1`
- npm package：`@open-mcp-ai/termcp`
- `termcp --version`：`termcp v0.2.8 (commit 0595cb5) built 2026-10-26T15:58:14Z`
- 运行端点：`http://127.0.0.1:18765`
- 官方仓库：<https://github.com/open-mcp-ai/Termcp>
- 官方 `main` 基线：`0595cb5c9ce2181de3f8049b06dc4c0ef0c2a6db`

原 v0.2.8 release 证据来自已安装 release 的版本输出、运行时端点和实例自报文档。当前本机源码目录 `D:\data\code\github\Termcp-web` 位于 `fix/approval-correlation`，HEAD 为 `f6cefec`，审批关联改动仍在工作区；本地重建实例是 `dev-f6cefec-dirty`，不能等同于正式 v0.2.8 发布包。

## 已核验实例信息

原 release 的 `GET /api/version`：

```json
{"version":"v0.2.8"}
```

`GET /api/daemon` 的已核验字段：

```json
{
  "daemon": true,
  "idle_timeout_ms": 30000,
  "pid": 12092,
  "started_at": "2026-10-28T00:11:23Z",
  "version": "v0.2.8"
}
```

PID 和启动时间是这次观测值，不是稳定契约字段。

## 当前本地重建实例（2026-10-31）

```json
{
  "daemon": true,
  "idle_timeout_ms": 30000,
  "pid": 23424,
  "started_at": "2026-10-30T17:58:37Z",
  "version": "dev-f6cefec-dirty"
}
```

- `termcp --version`：`termcp dev-f6cefec-dirty (commit f6cefec) built 2026-10-30T17:57:19Z`。
- 安装路径仍为 `C:\Users\miles\.termcp\versions\v0.2.8\termcp.exe`，这是本地临时覆盖，不代表该目录内容仍是正式 v0.2.8 release。
- 安装二进制 SHA-256：`09DC2C859687E120BBC0FDEEFC9911F51A43F728DA0090C42A655E9408204166`。
- 原 release 二进制备份：`C:\Users\miles\.termcp\versions\v0.2.8\backups\termcp.exe.20261031-pre-approval-correlation`。
- 审批过期、Session 关闭后 `cancelled`、可信 `approval_id` 和 Desktop 批准闭环的复验结果见 `known-issues.md` 第 9 节。

## 实例自报文档

- `GET /api.md`：HTTP 200，UTF-8 Markdown，是当前实例公开的传输/API 参考。
- `GET /openapi.json`：HTTP 404，本版本未公开该路径。
- `GET /skills.md`：HTTP 404；不能依据旧文档假设该入口存在。

## 认证与暴露

本次实例绑定回环地址且未启用 HTTP authentication。Harness v1 MCP 注册表同样只允许回环 HTTP/HTTPS endpoint，且拒绝 URL userinfo、query 和 fragment，避免把凭据放进 URL。

远程或带认证 Core 接入尚未实现。
