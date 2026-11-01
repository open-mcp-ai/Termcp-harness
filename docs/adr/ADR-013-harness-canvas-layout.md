# ADR-013：Harness Canvas 初版布局

- 状态：Accepted / Canvas 初版 PoC 已实现，继续迭代
- 日期：2026-10-31
- 责任范围：Terminal Workspace、模块展示层与 Workspace 组合模型
- 关联：`docs/adr/ADR-008-desktop-technology-stack.md`、`docs/adr/ADR-009-modular-composition.md`、`docs/adr/ADR-011-mcp-native-agent-runtime.md`、`docs/architecture/DESIGN_v0.4.md`

## 1. 决策

Harness Terminal Workspace 采用 **Harness Canvas** 作为最终界面方向，初版结构固定为：

```text
Harness Shell
├── Workspace 切换器
├── 添加模块
├── 搜索 / 命令面板
├── 全局状态与注意力徽标
└── 布局恢复入口
          │
          ▼
Harness Canvas
├── 可拖动、缩放、分栏和标签合并的面板
├── 模块实例
├── Workspace 模板
└── 布局保存、恢复与撤销
          │
          ▼
Modules
├── Agent Session
├── Terminal
├── Approval
├── MCP Console
├── Activity
└── 后续 Workflow、Code Audit 与第三方模块
```

固定 Shell 只承担全局操作和恢复能力。工作台、审批中心、任务时间线是 Workspace 模板，不是互相割裂的固定页面。

## 2. 已确认原则

### 2.1 Shell 保持轻量

Shell 不要求长期保留固定左侧导航。高频入口可以来自：

- Workspace 模板
- 添加模块
- 命令面板
- 全局注意力徽标
- 用户自行固定的快捷入口面板

如果真实使用数据证明审批、连接等入口高频，再增加快捷入口，不预先恢复固定图标导航。

### 2.2 Canvas 使用受约束的自由布局

初版采用停靠式网格布局，而不是任意重叠的悬浮窗口：

- 面板可拖动、缩放、分栏、最大化和合并为标签组。
- 面板停靠到网格，避免窗口尺寸变化后布局失效。
- 支持布局保存、恢复、撤销、模板重置和损坏恢复。
- 任意浮窗、跨显示器自由画布属于后续能力，不阻塞初版。

### 2.3 模块目录不是常驻面板

模块通过临时的“添加模块”抽屉或命令面板加入 Canvas。模块目录本身属于 Shell，不长期占用 Workspace 面板。

### 2.4 Workspace 先提供模板，再允许自由组合

初版至少提供：

- 日常协作：Sessions、Agent Session，可选 Terminal。
- 安全测试：Sessions、Agent Session、Terminal、Approval。
- MCP 开发：MCP Console、Tool Inspector、Activity。
- 任务追踪：Agent Timeline、Activity、关联执行详情。
- 自定义工作区：用户自行组合。

模板只提供初始布局，用户可以继续调整；系统记忆用户使用的 Workspace，而不是强制所有用户使用同一默认布局。

### 2.5 Activity 统一入口，保留不同视图

Activity 面板统一承载：

- 实时事件
- 历史活动
- 诊断

三者可以共用数据和查询入口，但保留不同的保留策略、权限和呈现方式，不合并成一个不可区分的日志列表。

### 2.6 Agent 与 Terminal 独立建模

- Agent Session 展示目标、步骤、工具调用、审批和完成情况。
- Terminal 展示真实 PTY、输入输出和人工接管现场。
- 两者通过 Session/call ID 建立可信关联，支持跳转、分屏和独立面板。
- 不把 Agent 文本输出冒充为真实终端。

### 2.7 MCP 状态必须区分对象

全局状态和面板必须分别表达：

- MCP Host
- MCP Server
- Tool
- 连接状态
- 能力就绪状态

不得用一个 `MCP 1/1` 同时代表不同层级。

### 2.8 初版采用轻量白色主题

- 初版以白色工作区、浅灰分层和蓝色主操作为主，降低首版视觉复杂度。
- 终端内容区可以保持深色，但整体 Shell 和普通面板使用浅色主题。
- 多主题、主题包和展示型主题插件不进入初版实现，后续通过模块和主题接口扩展。

## 3. 生命周期约束

面板、模块和任务的生命周期必须分开：

- 关闭面板不等于关闭 Core Session。
- 删除面板不等于卸载模块。
- 关闭 Approval Detail 不等于拒绝或取消审批。
- 关闭 Agent Session 面板不等于停止后台任务。
- 模块暂时不可用时保留面板位置和可恢复信息。
- 面板加载失败时提供重试、替换、移除和恢复布局入口。

## 4. 客户体验

- 首次启动可以从模板开始，不要求客户面对空白 Canvas。
- 普通客户使用较低密度模板；专业客户可以保存高密度 Workspace。
- 技术字段默认折叠，审批、风险和结果未知状态保持可见。
- 字体、焦点、键盘导航、缩放和响应式行为必须经过真实窗口验证。

## 5. 非目标

初版不承诺：

- 任意悬浮窗口和无限桌面画布。
- 所有模块组合都获得同等支持。
- 用 UI 布局代替模块安装、启用和能力状态。
- 用固定左侧导航承载所有未来模块。
- 在模块打包、签名和隔离完成前开放任意第三方前端代码进入主界面。

## 6. 实施顺序

1. 布局引擎 PoC：网格、拖动、缩放、分栏、标签合并和保存恢复。
2. 面板宿主：统一标题栏、加载、空态、错误态和生命周期提示。
3. 接入现有 Session、审批、Core、Runtime、MCP 和 Activity 内容。
4. 实现四个内置 Workspace 模板和自定义模板。
5. 实现模块抽屉、Workspace Recipe 导入导出和恢复。
6. 最后接入 Terminal 与受控模块贡献协议。

## 7. 初版 PoC 状态（2026-10-31）

已实现：

- 轻量 Shell：Workspace 切换、模块抽屉、命令入口、全局状态和窗口控制。
- 四个 Workspace 模板及自定义模板。
- 12 列停靠网格中的面板拖动、缩放、最大化、关闭和键盘移动。
- 12 行网格按窗口剩余高度自适应填充；Workspace 不出现整页纵向滚动条，内容溢出只在面板内部滚动。
- 面板拖动使用 `requestAnimationFrame` 帧节流和 Canvas 内部临时布局；拖动期间不持续更新全局 Canvas 状态或写入 `localStorage`，松手后才提交。面板内容按模块上下文的引用比较跳过无意义重绘。
- 拖到面板标题合并为标签组、拖到边缘分栏及落点预览。
- 标签切换和关闭单个标签；关闭最后一个标签才移除面板容器。
- 面板关闭后的重建与模板恢复。
- 本机布局与选中 Session 持久化；Canvas 状态使用 schema v2，并兼容旧布局读取。
- 当前 Workspace 可导出为 Recipe v1 JSON，并通过校验预览后导入；导入字段使用白名单，损坏单面板隔离，未知模块、无效上下文、重复模块、越界几何和重叠布局分别处理，失败或取消不会覆盖现有布局。
- 真实 Wails 窗口已验证下载、原生文件选择、无警告预览、取消保留布局、带修复警告预览、确认替换和 Desktop 重启后的导入布局持久化。
- 每个 Workspace 提供最多 20 步布局与 Session 引用撤销/重做；按钮和快捷键共用同一历史，状态使用独立 versioned localStorage 持久化。真实 Wails 窗口已验证鼠标撤销、键盘重做和重启后继续撤销。
- 跨 Workspace 采用复制而非移动：预览目标覆盖范围，确认后切换目标视图，只复制布局与 Session 引用；目标原布局写入其独立撤销历史。真实 Wails 窗口已验证 2 面板覆盖 5 面板后可撤销恢复。
- Agent、审批、Terminal 和 Activity 面板支持跟随当前 Session、锁定 Session 与关联未知状态；标签合并保留各模块上下文。
- 关闭面板时明确提示任务和审批未停止，Workspace 持续显示后台活动任务数量。
- 审批提交、服务端确认和结果未知分开表达；Activity 显示 subject/correlation，不以缺失关联推断当前任务。
- Sessions、Agent Session、Approval、Terminal 占位、MCP、Activity 和 Diagnostics 面板。
- Terminal 仅在绑定可信 running Shell 后显示真实 xterm；无 Shell 时显示明确空态，不再渲染伪造 `$` 提示符。
- 面板、模块和任务生命周期分离的界面文案与恢复边界。

尚未实现：

- Recipe 跨版本迁移和跨 Workspace 移动语义。
- 真实 Terminal WebSocket 字节流。
- 第三方模块贡献协议与动态面板注册。
