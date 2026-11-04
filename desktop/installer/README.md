# Termcp Harness 安装说明

本目录是 Termcp Harness 的 Windows 发布包，包含 Desktop、Headless CLI、Termcp Core 和发布校验文件。

## 1. 安装

运行 `Harness_Install.exe` 或解压 `Harness.zip`：

```text
Termcp Harness.exe   Desktop 工作台
termcp-harness.exe   Headless Runtime / Core 管理 CLI
core/                与本次发布匹配的 Termcp Core
release.json         版本、commit、SHA-256 和工具链信息
checksums.txt        逐文件 SHA-256
```

安装器默认安装到：

```text
%LOCALAPPDATA%\Programs\Termcp Harness
```

## 2. Core 运行模式

### Managed Core（默认）

不勾选“启用持久 Core”时：

- Desktop 启动自己的 Harness Runtime 和 Core 子进程。
- Harness 退出时停止由它启动的 Core。
- 不需要后台进程或额外配置。

适用于只在 Harness 内使用 Termcp、对后台进程没有要求的场景。

### Persistent Core（持久 Core）

勾选“启用持久 Core”时，安装器会创建用户级 Core profile，并通过 Core 公开 daemon 命令启动它：

```text
http://127.0.0.1:18765
```

Harness 将 Core 识别为 `external`，退出 Harness 时不会停止该 Core。

以下情况建议启用：

- 其他 Agent 或 Termcp 客户端需要在 Harness 未启动时直接使用 Core。
- 外部 Agent 先创建 Session，之后 Harness 需要重新连接并查看该 Session。
- 需要在 Desktop/Runtime 重启期间保留 Core Session、Shell 和输出历史。
- 多个客户端需要连接同一个本机 Core 端点。

以下情况不必启用：

- 只从 Harness 内部启动和管理任务。
- 不需要 Harness 退出后继续保留 Core。
- 不希望维护后台进程或固定回环端口。

## 3. 启用与维护持久 Core

安装时勾选“启用持久 Core”即可。安装完成后也可以在安装目录手工执行：

```powershell
.\termcp-harness.exe core service install
.\termcp-harness.exe core service start
.\termcp-harness.exe core service status
```

profile 默认位于：

```text
%APPDATA%\termcp-harness\core-service.json
```

Core 数据默认使用 Core 自身的：

```text
%USERPROFILE%\.termcp
```

停止持久 Core：

```powershell
.\termcp-harness.exe core service stop
```

目前不会在 Windows 登录时自动启动 Core，也不会在 Core 崩溃后自动重启。

## 4. 升级

再次运行新版本 `Harness_Install.exe` 即可原地升级。

- 检测到已有持久 Core profile 时，默认继续保持持久模式。
- 安装器先停止旧 Core，再替换 Desktop、CLI 和 Core。
- 新 Core 会重新校验版本与 SHA-256，然后重新启动。
- endpoint、数据目录和 token 文件引用保持不变。
- Core Session、输出历史和数据目录保留。
- 升级期间 live SSH/PTY 会断开，不能恢复为同一条 live 连接。

如明确不再需要持久模式，可以在安装器页面取消“启用持久 Core”。安装器会停止 Core，并把 profile 重命名为：

```text
core-service.json.disabled.<唯一后缀>
```

数据目录不会删除，可之后恢复 profile 或重新启用。

静默安装参数：

```powershell
Harness_Install.exe /S /PERSISTENT_CORE=1
Harness_Install.exe /S /PERSISTENT_CORE=0
```

测试或定制部署还可使用：

```text
/CORE_PROFILE=<profile-path>
/CORE_DATA_DIR=<core-data-dir>
/CORE_ENDPOINT_PORT=<host:port>
```

## 5. 卸载

卸载器会停止本次安装对应的持久 Core，但不会删除：

- `%APPDATA%\termcp-harness\core-service.json`
- `.disabled.*` profile 备份
- `%USERPROFILE%\.termcp`

因此重装后仍可使用原有 Core 数据。

## 6. 冲突与故障排查

- 固定端点已被不同版本或不同数据目录的 Core 占用时，安装器会失败，不会停止未知进程。
- 持久 Core 启停结果写入安装目录下的 `installer.log`。
- 检查状态：`termcp-harness.exe core service status`
- 检查版本：`termcp-harness.exe version`
- 检查 Core：访问 `http://127.0.0.1:18765/api/version` 和 `/api/daemon`

安装器、Desktop、CLI 和 Core 当前均未进行代码签名。
