export type Locale = "zh-CN";

const zhCN = {
  locale: "zh-CN",
  brand: {
    name: "Termcp Harness",
    tagline: "面向终端的人机协作运行时",
  },
  state: {
    new: "启动中",
    running: "运行中",
    stopping: "停止中",
    stopped: "已停止",
    failed: "失败",
    unknown: "状态未知",
  },
  common: {
    unknown: "未知",
    notRecorded: "未记录",
    none: "无",
  },
  window: {
    controls: "窗口控制",
    minimise: "最小化",
    maximise: "最大化或还原",
    close: "关闭窗口",
  },
  nav: {
    label: "工作区导航",
    ledger: "事件流",
    runtime: "Harness Runtime",
    mcp: "MCP 服务",
    railNote: "控制面\n独立运行",
  },
  app: {
    context: "运行概览",
    headline: "Agent 在终端里工作，\n人始终看得到现场。",
    checked: "更新于",
    waiting: "等待 Harness Runtime",
    connecting: "连接中",
    refresh: "刷新",
    unreachable: "Harness Runtime 未连接",
    unreachableBody: "无法读取本地 Harness Runtime。请确认进程已启动并监听当前地址。",
    technicalDetails: "技术详情",
    tryAgain: "重试",
    unavailableTitle: "无法读取运行记录",
    unavailableBody: "启动 Harness Runtime 后点击“重试”。",
    openingLedger: "正在读取运行记录",
    waitingBridge: "正在等待桌面桥接",
    reading: "读取实例、事件与 MCP 服务状态。",
    runThroughDesktop: "请从 Desktop 打开工作区。",
    inspectorPlaceholder: "连接 Harness Runtime 后显示实例状态。",
    readOnly: "只读视图",
    controlAPI: "控制 API v1",
    authoritative: "Harness Runtime 状态以服务端为准",
    noSnapshot: "暂无 Harness Runtime 快照",
    observerNote: "Desktop 只负责观察，Harness Runtime 独立运行",
    runtimeDetails: "Harness Runtime 详情",
  },
  event: {
    control: "控制事件",
    diagnostic: "诊断事件",
    subject: "主体",
    correlation: "关联",
    eventID: "事件 ID",
    occurred: "发生时间",
    localSequence: "本地序号",
    noPayload: "此事件没有载荷。",
  },
  ledger: {
    context: "事件流",
    contextStale: "事件流 · 最后快照",
    title: "按本地处理顺序记录",
    retained: "条事件",
    latestSequence: "最新序号",
    filterLabel: "筛选事件",
    summaryLabel: "事件统计",
    filterAll: "全部",
    filterControl: "控制",
    filterDiagnostic: "诊断",
    historyIncomplete: "部分历史已丢失。",
    diagnosticEvicted: (count: number, sequence: number) =>
      `已清理 ${count} 条诊断事件（截至序号 ${sequence}）。`,
    controlOverflow: "控制事件缓冲区已满，后续事件未记录。",
    emptyAll: "暂无事件",
    emptyControl: "暂无控制事件",
    emptyDiagnostic: "暂无诊断事件",
    emptyBody: "生命周期、MCP、审批与工具状态会记录在这里。",
    sequenceNote: "序号仅反映 Harness 的处理顺序，不代表远端执行顺序。",
    olderOutside: "更早事件已在当前窗口之外。",
    complete: "当前窗口已完整加载。",
    lastKnownNote: "Harness Runtime 当前不可达。以下事件来自最后一次成功读取。",
  },
  runtime: {
    context: "控制平面",
    contextStale: "最后已知状态",
    title: "Harness Runtime",
    instance: "实例",
    version: "版本",
    started: "启动时间",
    health: "健康状态",
    observer: "关闭窗口不会停止 Harness Runtime。",
    healthOk: "正常",
    healthUnavailable: "不可用",
    lastKnownNote: "Harness Runtime 当前不可达。以下字段是最后一次成功读取的快照，不代表当前状态。",
  },
  mcp: {
    context: "工具来源",
    contextStale: "工具来源 · 最后快照",
    title: "MCP 服务",
    ready: "就绪",
    controlUnavailable: "MCP 控制面不可用",
    controlUnavailableBody: "当前 Harness Runtime 未提供 MCP 注册表。",
    empty: "暂未接入 MCP 服务",
    emptyBody: "连接、能力与健康状态会显示在这里。",
    toolCount: (count: number) => `${count} 个工具`,
    readyState: "就绪",
    lastKnownReady: "最后已知就绪",
    lastKnownNote: "Harness Runtime 当前不可达。以下 MCP 状态可能已经过期。",
    lastKnownCount: (ready: number, total: number) => `最后 ${ready}/${total}`,
  },
  mcpState: {
    discovered: "已发现",
    registered: "已登记",
    not_attempted: "未尝试连接",
    connecting: "连接中",
    connected: "已连接",
    disconnected: "已断开",
    unavailable: "不可用",
    not_loaded: "能力未读取",
    loading: "读取能力中",
    loaded: "能力已读取",
    stale: "能力已过期",
    disabled: "已停用",
    enabled: "已启用",
    external: "外部服务",
    managed: "受管服务",
  },
  mcpFailure: {
    configuration: "配置错误",
    authentication: "认证失败",
    network: "网络不可达",
    protocol: "协议不兼容",
    capability: "能力读取失败",
    process: "进程不可用",
    permission: "权限不足",
    unknown: "未知错误",
  },
  loading: {
    readFailed: "读取失败",
  },
} as const;

const catalogs = {
  "zh-CN": zhCN,
} satisfies Record<Locale, typeof zhCN>;

export const defaultLocale: Locale = "zh-CN";
export const messages = catalogs[defaultLocale];

export function mcpStateLabel(value: string): string {
  return messages.mcpState[value as keyof typeof messages.mcpState] ?? value;
}

export function mcpFailureLabel(value?: string): string {
  if (!value) return "";
  return messages.mcpFailure[value as keyof typeof messages.mcpFailure] ?? value;
}
