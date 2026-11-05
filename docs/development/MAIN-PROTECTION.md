# `main` 分支保护策略

> 状态：策略已定义；服务端规则需要仓库管理员执行。

`main` 是发布基线。所有变更必须通过 Pull Request，并在合并前完成当前
`Verify` 工作流的两个检查：

- `Runtime and frontend`
- `Windows Desktop`

## 保护规则

机器可读配置位于 `.github/main-branch-protection.json`：

- 禁止直接推送和删除 `main`，必须通过 PR。
- 要求分支在最新 `main` 基础上运行状态检查。
- 要求上述两个检查成功。
- 要求所有 PR 对话已解决。
- 禁止 force push。
- 管理员同样受规则约束，避免日常操作绕过保护。
- 暂不强制审批数量：当前由单一维护者主导，强制一人审批会形成自锁；
  新增第二维护者后应改为至少 1 个批准。
- 不要求线性历史：保留发布 PR 的 merge commit 和已发布标签可达性。

## 应用

需要仓库管理员权限。执行：

```powershell
.\\.github\scripts\apply-main-branch-protection.ps1
```

脚本会通过 GitHub Branch Protection API 应用配置并回读验证。普通
`maintain` 权限不能修改分支保护；当前维护者需要由组织管理员授权，或在
GitHub 仓库设置中手工启用相同规则。

## 例外流程

默认不提供日常 bypass。紧急情况下，管理员必须：

1. 在 PR 或 issue 中记录绕过原因和影响范围。
2. 临时调整保护规则。
3. 完成修复后立即恢复配置并回读验证。
