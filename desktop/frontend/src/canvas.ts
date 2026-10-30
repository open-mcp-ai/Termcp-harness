export const CANVAS_COLUMNS = 12;
export const CANVAS_ROWS = 12;

export type WorkspaceId = "daily" | "security" | "mcp" | "diagnostics" | "custom";
export type ModuleId =
  | "sessions"
  | "agent"
  | "approvals"
  | "terminal"
  | "mcp"
  | "activity"
  | "diagnostics";

export interface ModuleDefinition {
  id: ModuleId;
  title: string;
  description: string;
  minW: number;
  minH: number;
}

export interface PanelLayout {
  moduleId: ModuleId;
  x: number;
  y: number;
  w: number;
  h: number;
  maximized?: boolean;
  restore?: {
    x: number;
    y: number;
    w: number;
    h: number;
  };
}

export const moduleDefinitions: ModuleDefinition[] = [
  {
    id: "sessions",
    title: "Sessions",
    description: "查看运行中、等待审批和失败的 Agent Session。",
    minW: 3,
    minH: 4,
  },
  {
    id: "agent",
    title: "Agent Session",
    description: "查看当前 Agent 的步骤、工具调用和审批位置。",
    minW: 4,
    minH: 5,
  },
  {
    id: "approvals",
    title: "审批请求",
    description: "处理待审批请求并核对执行结果。",
    minW: 4,
    minH: 5,
  },
  {
    id: "terminal",
    title: "Terminal",
    description: "真实终端现场。当前为 Canvas 接入占位。",
    minW: 4,
    minH: 4,
  },
  {
    id: "mcp",
    title: "MCP 工具",
    description: "查看 MCP Server、工具目录和能力状态。",
    minW: 4,
    minH: 5,
  },
  {
    id: "activity",
    title: "Activity",
    description: "查看实时事件、控制轨迹和诊断记录。",
    minW: 4,
    minH: 4,
  },
  {
    id: "diagnostics",
    title: "系统诊断",
    description: "查看 Runtime、Core 和 MCP 的当前状态。",
    minW: 5,
    minH: 5,
  },
];

export const workspaceLabels: Record<WorkspaceId, string> = {
  daily: "日常协作",
  security: "安全测试",
  mcp: "MCP 开发",
  diagnostics: "诊断中心",
  custom: "自定义工作区",
};

const workspaceTemplates: Record<WorkspaceId, PanelLayout[]> = {
  daily: [
    { moduleId: "sessions", x: 0, y: 0, w: 3, h: 7 },
    { moduleId: "agent", x: 3, y: 0, w: 9, h: 7 },
    { moduleId: "activity", x: 0, y: 7, w: 12, h: 5 },
  ],
  security: [
    { moduleId: "sessions", x: 0, y: 0, w: 3, h: 12 },
    { moduleId: "agent", x: 3, y: 0, w: 5, h: 7 },
    { moduleId: "approvals", x: 8, y: 0, w: 4, h: 7 },
    { moduleId: "terminal", x: 3, y: 7, w: 5, h: 5 },
    { moduleId: "activity", x: 8, y: 7, w: 4, h: 5 },
  ],
  mcp: [
    { moduleId: "mcp", x: 0, y: 0, w: 6, h: 12 },
    { moduleId: "activity", x: 6, y: 0, w: 6, h: 8 },
    { moduleId: "diagnostics", x: 6, y: 8, w: 6, h: 4 },
  ],
  diagnostics: [
    { moduleId: "diagnostics", x: 0, y: 0, w: 6, h: 5 },
    { moduleId: "mcp", x: 6, y: 0, w: 6, h: 5 },
    { moduleId: "activity", x: 0, y: 5, w: 12, h: 7 },
  ],
  custom: [
    { moduleId: "sessions", x: 0, y: 0, w: 3, h: 6 },
    { moduleId: "agent", x: 3, y: 0, w: 9, h: 6 },
    { moduleId: "activity", x: 0, y: 6, w: 12, h: 6 },
  ],
};

export function cloneTemplate(workspace: WorkspaceId): PanelLayout[] {
  return workspaceTemplates[workspace].map((panel) => ({ ...panel }));
}

export function defaultWorkspaceLayout(workspace: WorkspaceId): PanelLayout[] {
  return cloneTemplate(workspace);
}

export function normalizeLayout(panels: PanelLayout[]): PanelLayout[] {
  const seen = new Set<ModuleId>();
  return panels
    .filter((panel) => moduleDefinitions.some((definition) => definition.id === panel.moduleId))
    .filter((panel) => {
      if (seen.has(panel.moduleId)) return false;
      seen.add(panel.moduleId);
      return true;
    })
    .map((panel) => {
      const restored = panel.maximized && panel.restore
        ? { ...panel, ...panel.restore, maximized: false, restore: undefined }
        : panel;
      const definition = moduleDefinitions.find((candidate) => candidate.id === restored.moduleId)!;
      const w = clamp(restored.w, definition.minW, CANVAS_COLUMNS);
      const h = clamp(restored.h, definition.minH, CANVAS_ROWS);
      return {
        ...restored,
        x: clamp(restored.x, 0, CANVAS_COLUMNS - w),
        y: clamp(restored.y, 0, CANVAS_ROWS - h),
        w,
        h,
      };
    });
}

export function overlaps(left: PanelLayout, right: PanelLayout): boolean {
  if (left.moduleId === right.moduleId) return false;
  return left.x < right.x + right.w &&
    left.x + left.w > right.x &&
    left.y < right.y + right.h &&
    left.y + left.h > right.y;
}

export function layoutHasOverlap(panels: PanelLayout[]): boolean {
  for (let i = 0; i < panels.length; i += 1) {
    for (let j = i + 1; j < panels.length; j += 1) {
      if (overlaps(panels[i], panels[j])) return true;
    }
  }
  return false;
}

export function findFreeSlot(panels: PanelLayout[], moduleId: ModuleId): PanelLayout | null {
  const definition = moduleDefinitions.find((candidate) => candidate.id === moduleId);
  if (!definition) return null;
  for (let y = 0; y <= CANVAS_ROWS - definition.minH; y += 1) {
    for (let x = 0; x <= CANVAS_COLUMNS - definition.minW; x += 1) {
      const candidate: PanelLayout = {
        moduleId,
        x,
        y,
        w: definition.minW,
        h: definition.minH,
      };
      if (!panels.some((panel) => overlaps(candidate, panel))) return candidate;
    }
  }
  return null;
}

export function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(Math.max(value, minimum), maximum);
}

export function readStoredLayouts(): Partial<Record<WorkspaceId, PanelLayout[]>> {
  try {
    const raw = localStorage.getItem("termcp.canvas.layouts");
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Partial<Record<WorkspaceId, PanelLayout[]>>;
    const result: Partial<Record<WorkspaceId, PanelLayout[]>> = {};
    for (const workspace of Object.keys(workspaceLabels) as WorkspaceId[]) {
      const panels = parsed[workspace];
      if (Array.isArray(panels)) result[workspace] = normalizeLayout(panels);
    }
    return result;
  } catch {
    return {};
  }
}

export function writeStoredLayout(workspace: WorkspaceId, panels: PanelLayout[]): void {
  try {
    const stored = readStoredLayouts();
    stored[workspace] = normalizeLayout(panels);
    localStorage.setItem("termcp.canvas.layouts", JSON.stringify(stored));
  } catch {
    // Layout persistence is best-effort; runtime state must not depend on it.
  }
}
