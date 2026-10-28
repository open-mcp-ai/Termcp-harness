# Termcp Harness 文档索引

> 状态：文档结构 v2（2026-10-28）
>
> 当前产品范围以 `docs/product/VISION_v0.9.md` 为准；运行时、MCP 工具链和终端工作台约束以 `docs/architecture/DESIGN_v0.4.md` 为准；代理执行规则以根目录 `AGENTS.md` 为准。

## 目录结构

```text
docs/
├── README.md
├── product/
│   ├── VISION_v0.9.md
│   ├── PROJECT-DISCUSSION.md
│   └── WEB-DEFERRED.md
├── architecture/
│   ├── DESIGN_v0.4.md
│   └── UPSTREAM.md
├── governance/
│   └── AGENTS_v0.3.md
├── ux/
│   └── UX-RECOVERY.md
├── development/
│   └── BRANCHING.md
├── contracts/
│   ├── README.md
│   ├── core/v0.2.8/
│   │   ├── metadata.md
│   │   └── mcp.md
│   └── harness/v0.1/
│       ├── control-api.md
│       ├── mcp-server-states.md
│       └── state-machines.md
└── adr/
    ├── ADR-001-input-control-ownership.md
    ├── ADR-002-event-ordering-correlation.md
    ├── ADR-003-terminal-backpressure.md
    ├── ADR-004-core-instance-data-directory.md
    ├── ADR-005-approval-security-boundary.md
    ├── ADR-006-disconnect-timeout-cleanup.md
    ├── ADR-007-contract-baseline-and-testing.md
    ├── ADR-008-desktop-technology-stack.md
    ├── ADR-009-modular-composition.md
    ├── ADR-010-core-integration-boundary.md
    ├── ADR-011-mcp-native-agent-runtime.md
    └── ADR-012-runtime-bootstrap-technology.md
```

## 阅读顺序

1. `docs/product/PROJECT-DISCUSSION.md`（首次讨论）
2. `docs/product/VISION_v0.9.md`
3. `docs/adr/ADR-011-mcp-native-agent-runtime.md`（产品定位）
4. `docs/architecture/UPSTREAM.md`
5. `docs/architecture/DESIGN_v0.4.md`
6. `docs/adr/ADR-009-modular-composition.md`（模块安装与自由组合）
7. `docs/ux/UX-RECOVERY.md`
8. 相关 ADR
9. `docs/development/BRANCHING.md`
10. `docs/contracts/README.md`
11. `docs/contracts/harness/v0.1/control-api.md`（当前本地控制 API 草案）
12. `docs/contracts/harness/v0.1/state-machines.md`（工具调用与审批状态语义）
13. `docs/contracts/core/v0.2.8/metadata.md` 与 `mcp.md`（已验证 Core 基线）
14. `docs/contracts/harness/v0.1/mcp-server-states.md`（MCP 生命周期与能力状态）

## 文档职责

| 目录 | 负责内容 |
|---|---|
| `docs/product/` | 产品定位、Runtime/MCP/Workspace 范围、长期方向、Web 策略 |
| `docs/architecture/` | 三平面架构、工具链、状态语义、上游关系与开放边界 |
| `docs/adr/` | 已提出或已接受的产品、架构和契约决策 |
| `docs/governance/` | 代理规则归档；当前规则以根目录 `AGENTS.md` 为准 |
| `docs/ux/` | 高风险场景的用户提示与恢复行动 |
| `docs/development/` | 分支、提交、发布和开发流程 |
| `docs/contracts/` | Core 与 Harness 公开契约、来源、版本和验证记录 |

## 状态规则

- 代码或测试确认：可以作为当前事实陈述。
- Core/MCP 公开契约：注明来源、版本和确认状态。
- 团队确认：注明责任方与适用范围。
- 推断：明确标记，不写成结论。
- 未知：保留未知，不用本地逻辑补造确定性。
- ADR：除明确标注外均为 Proposed；Accepted ADR 也不等于代码已实现。
- `VISION_v0.9.md` 和 `DESIGN_v0.4.md` 是当前版本；升版时必须同步交叉引用，不能保留并行版本。

## 当前产品摘要

Termcp Harness 是 MCP 原生 Agent Runtime 与人机共享的终端工作台：

- Core 负责 SSH、PTY、Shell、会话和真实执行。
- Harness Runtime 负责 Agent Loop、MCP Host、工具策略、Session、事件和恢复。
- Terminal Workspace 负责终端、审批、接管、操作时间线和 MCP 状态。
- Runtime 必须可 headless 运行；Terminal Workspace 是第一方体验层，不是唯一入口。
- 稳定内核、开放边缘；不采用“一切皆插件”。
- 所有功能模块使用同一份复选框目录；推荐项可取消，L0-L3 只是安装分组。
- Harness Shell 固定存在；安装器与模块管理复用同一组合模型。
- 选择、安装、启用、运行和能力就绪分开表示；统一目录不要求统一生命周期。
