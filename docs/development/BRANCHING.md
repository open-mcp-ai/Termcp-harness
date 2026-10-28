# 上游分支观察与 Harness Runtime/Terminal Workspace 分支策略

> 状态：Working policy（2026-10-28）
>
> 上游分支信息来自 `git ls-remote --heads` 和公开 commits Atom 源；会随上游变化，不构成永久事实。
>
> 2026-10-28 已初始化 Git（`main`），当前产品开发集中在 `dev`。本文中“空仓库、初期不需要 dev”的描述仅保留为历史判断；当前执行规则见第 3、4 节的状态更新。

## 1. 上游 Core 分支观察

核验时 Core 的分支包括：

| 分支/模式 | 观察结果 | 判断 |
|---|---|---|
| `main` | `0595cb5c9ce2181de3f8049b06dc4c0ef0c2a6db` | 发布基线，最新提交对应 v0.2.8 |
| `dev` | `6c2646a566e5a12be966b620f7359bbbc0f0d057` | 持续集成分支，包含 v0.2.8 之后的修复 |
| `dev_newui` | `95a651e80664d96bd5e1ed88b9850098a6eae988` | 长期 Web UI 重构分支，最新提交为布局重构 |
| `daily/YYYY-MM-DD/*` | 与 `dev` 某日快照共享 SHA | 每日快照/构建分支 |
| `release/vX.Y.Z*` | 多个 rc 分支 | 发布候选与版本稳定化 |
| `dev_bad`、`encryption-test`、`dev_termcp` | 独立实验或早期方向 | 不作为 Harness 基线 |
| `pages` | 静态页面部署分支 | 不是业务代码基线 |

重要结论：

- Core `main` 是契约基线候选。
- Core `dev` 可以用于观察下一版本变化，但不能默认视为稳定契约。
- **`dev_newui` 是 Core 仓库中的 Web UI 重构分支，不是 Harness/Desktop 的新 UI。** 最新提交标题是 `feat(webui): 布局重构 — 归档列、扩展面板、底栏批量栏`。
- `daily/*`、`release/*` 和实验分支反映上游自己的发布节奏，不需要在 Harness 中复制。

## 2. 上游 Desktop 分支观察

核验时 Desktop 的分支包括：

| 分支/模式 | 观察结果 | 判断 |
|---|---|---|
| `main` | `d4386ecfa8e62110ab0351827a212e81bfab6c79` | 当前稳定基线 |
| `dev` | `da11d899b10853f405eed3316ac8adf48f73e250` | 下一版本集成分支 |
| `revert-13-*` | 一次性回滚分支 | 临时维护分支 |

Desktop `dev` 的最新提交是：

> `feat(desktop): complete Core 0.2.8 workflows`

说明它承载尚未进入 `main` 的下一批桌面功能。

## 3. Harness 是否也需要 `dev` / `dev_newui`

### 不需要复制 `dev_newui`

`dev_newui` 是 Core Web UI 的长期重构分支，与 Harness Runtime/Terminal Workspace 的开发模型不同。Harness 不应创建长期 UI 分叉。

如果未来需要大规模 UI 改版，应采用：

- 短生命周期的 `feat/new-ui-*` 分支。
- Feature flag 或路由级灰度。
- 小步合并到 `main`。
- 明确回滚点。

不要让一个长期分支独立演进数周后再合并。

### 初期不需要长期 `dev`

> 状态更新（2026-10-28）：该建议已被当前项目决定覆盖。`main` 保持发布基线，产品开发统一进入 `dev`；Desktop 第一版完成后，从短期功能分支向 `dev` 提交 PR。未经负责人明确指令，不把 `dev` 合入 `main`。

Harness 目前是单人主导、空仓库、尚未发布。推荐先采用 trunk-based development：

- `main`：始终可构建、可测试、可发布。
- `feat/<scope>`：新功能，短期存在。
- `fix/<scope>`：缺陷修复。
- `docs/<scope>`：文档。
- `chore/<scope>`：构建、依赖、工具。
- `spike/<scope>`：有时限的技术验证，验证后删除或转为正式实现。

PR 合并后删除短分支。

### 何时再引入 `dev`

只有出现以下需求时再考虑：

- 需要持续生成 nightly/preview 版本。
- 多位开发者同时合并多个较大功能。
- 发布稳定分支需要与日常开发隔离。
- CI 需要固定的 nightly 构建来源。

届时可以增加：

- `dev`：下一版本集成分支。
- `release/vX.Y.Z`：发布稳定化。
- `hotfix/vX.Y.Z`：生产修复。

不要因为上游有 `dev` 就机械复制。

## 4. Harness 推荐分支模型

### 现在采用

```text
main
└── dev
    ├── feat/runtime-kernel
    ├── feat/mcp-host
    ├── feat/tool-pipeline
    ├── feat/desktop-shell
    ├── fix/tool-state
    └── docs/runtime-boundary
```

- `main`：发布基线，当前不承载日常开发。
- `dev`：产品集成分支，Desktop 第一版开发和验证在这里继续。
- 短期功能分支：PR 目标为 `dev`。
- 只有负责人明确批准后，才创建 `dev -> main` 的发布 PR。

### 发布后可选

```text
main
├── dev
├── release/v0.1
├── hotfix/v0.1.1
├── feat/...
└── fix/...
```

### 标签

- `v0.1.0`：正式版本。
- `v0.1.0-rc.1`：发布候选。
- `v0.1.0-beta.1`：外部测试版本。

## 5. 分支规则

- `main` 必须始终可构建、可测试。
- 功能分支从 `main` 创建，PR 合入 `main`。
- 每个 PR 只做一个可审查目标。
- 不在 `main` 直接开发，除非是紧急且已记录的单文件修复。
- 契约、架构和安全变更必须关联 ADR 或文档。
- 分支合并前必须记录实际测试、未验证项和 Core 版本。
- 不长期保留已合并分支。
- 不使用 `dev_newui` 这类宽泛长期分支承载完整产品重写。
- 未经用户明确同意，不 commit、不 push、不合并。

## 6. 与上游分支的关系

- Harness 只以 Core `main` 或明确 release tag 作为契约基线。
- `dev` 仅用于提前观察下一版本，不自动作为兼容目标。
- 需要跟进上游变化时，建立短期 `chore/core-vX.Y.Z` 分支。
- 不直接在上游仓库提交 Harness 专属改动；Core 变更通过独立 issue/PR 协作。
- 不把上游 `daily/*`、实验分支或 WIP 当作既定设计。

## 7. 当前结论

**当前采用 `main` + `dev` + 短生命周期功能分支；功能 PR 合入 `dev`，`main` 等待负责人明确发布指令。**

**`dev_newui` 是 Core 的 Web UI 分支，不作为 Harness 的开发模型；Harness Desktop 使用独立 Wails 工程。**
