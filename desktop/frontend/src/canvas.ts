export const CANVAS_COLUMNS = 12;
export const CANVAS_ROWS = 12;
export const CANVAS_GAP = 8;

export function canvasRowHeight(boardHeight: number): number {
  if (!Number.isFinite(boardHeight) || boardHeight <= 0) return 1;
  return Math.max(
    1,
    (boardHeight - CANVAS_GAP * (CANVAS_ROWS - 1)) / CANVAS_ROWS,
  );
}

export type WorkspaceId = "daily" | "security" | "mcp" | "diagnostics" | "custom";
export type ModuleId =
  | "sessions"
  | "agent"
  | "approvals"
  | "terminal"
  | "mcp"
  | "activity"
  | "diagnostics";

export type ContextualModuleId = "agent" | "approvals" | "terminal" | "activity";
export type PanelContextMode = "follow" | "pinned";

export interface ModuleDefinition {
  id: ModuleId;
  title: string;
  description: string;
  minW: number;
  minH: number;
  contextScope: "session" | "global";
}

export interface PanelContextBinding {
  mode: PanelContextMode;
  sessionId?: string;
}

export interface PanelLayout {
  id: string;
  moduleId: ModuleId;
  tabs?: ModuleId[];
  x: number;
  y: number;
  w: number;
  h: number;
  contextByModule?: Partial<Record<ContextualModuleId, PanelContextBinding>>;
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
    contextScope: "global",
  },
  {
    id: "agent",
    title: "Agent Session",
    description: "查看当前 Agent 的步骤、工具调用和审批位置。",
    minW: 4,
    minH: 5,
    contextScope: "session",
  },
  {
    id: "approvals",
    title: "审批请求",
    description: "处理待审批请求并核对执行结果。",
    minW: 4,
    minH: 5,
    contextScope: "session",
  },
  {
    id: "terminal",
    title: "Terminal",
    description: "通过固定 Desktop 桥接入真实 Core PTY，显示 Agent 与人工共享的 Shell。",
    minW: 4,
    minH: 5,
    contextScope: "session",
  },
  {
    id: "mcp",
    title: "MCP 工具",
    description: "查看 MCP Server、工具目录和能力状态。",
    minW: 4,
    minH: 5,
    contextScope: "global",
  },
  {
    id: "activity",
    title: "Activity",
    description: "查看实时事件、控制轨迹和诊断记录。",
    minW: 4,
    minH: 4,
    contextScope: "session",
  },
  {
    id: "diagnostics",
    title: "系统诊断",
    description: "查看 Runtime、Core 和 MCP 的当前状态。",
    minW: 5,
    minH: 5,
    contextScope: "global",
  },
];

export const CANVAS_STATE_VERSION = 2;
const CANVAS_STATE_KEY = "termcp.canvas.state";
const LEGACY_LAYOUTS_KEY = "termcp.canvas.layouts";

export interface StoredCanvasState {
  version: typeof CANVAS_STATE_VERSION;
  layouts: Record<WorkspaceId, PanelLayout[]>;
  selectedSessionByWorkspace: Partial<Record<WorkspaceId, string>>;
}

export const workspaceLabels: Record<WorkspaceId, string> = {
  daily: "日常协作",
  security: "安全测试",
  mcp: "MCP 开发",
  diagnostics: "诊断中心",
  custom: "自定义工作区",
};

const workspaceTemplates: Record<WorkspaceId, PanelLayout[]> = {
  daily: [
    { id: "sessions", moduleId: "sessions", x: 0, y: 0, w: 3, h: 7 },
    { id: "agent", moduleId: "agent", x: 3, y: 0, w: 9, h: 7 },
    { id: "activity", moduleId: "activity", x: 0, y: 7, w: 12, h: 5 },
  ],
  security: [
    { id: "sessions", moduleId: "sessions", x: 0, y: 0, w: 3, h: 12 },
    { id: "agent", moduleId: "agent", x: 3, y: 0, w: 5, h: 7 },
    { id: "approvals", moduleId: "approvals", x: 8, y: 0, w: 4, h: 7 },
    { id: "terminal", moduleId: "terminal", x: 3, y: 7, w: 5, h: 5 },
    { id: "activity", moduleId: "activity", x: 8, y: 7, w: 4, h: 5 },
  ],
  mcp: [
    { id: "mcp", moduleId: "mcp", x: 0, y: 0, w: 6, h: 12 },
    { id: "activity", moduleId: "activity", x: 6, y: 0, w: 6, h: 8 },
    { id: "diagnostics", moduleId: "diagnostics", x: 6, y: 8, w: 6, h: 4 },
  ],
  diagnostics: [
    { id: "diagnostics", moduleId: "diagnostics", x: 0, y: 0, w: 6, h: 5 },
    { id: "mcp", moduleId: "mcp", x: 6, y: 0, w: 6, h: 5 },
    { id: "activity", moduleId: "activity", x: 0, y: 5, w: 12, h: 7 },
  ],
  custom: [
    { id: "sessions", moduleId: "sessions", x: 0, y: 0, w: 3, h: 6 },
    { id: "agent", moduleId: "agent", x: 3, y: 0, w: 9, h: 6 },
    { id: "activity", moduleId: "activity", x: 0, y: 6, w: 12, h: 6 },
  ],
};

export function cloneTemplate(workspace: WorkspaceId): PanelLayout[] {
  return workspaceTemplates[workspace].map((panel) => ({
    ...panel,
    tabs: panel.tabs ? [...panel.tabs] : undefined,
    contextByModule: panel.contextByModule ? { ...panel.contextByModule } : undefined,
    restore: panel.restore ? { ...panel.restore } : undefined,
  }));
}

export function defaultWorkspaceLayout(workspace: WorkspaceId): PanelLayout[] {
  return cloneTemplate(workspace);
}

interface NormalizeLayoutOptions {
  collapseMaximized?: boolean;
}

export function normalizeLayout(
  panels: PanelLayout[],
  options: NormalizeLayoutOptions = {},
): PanelLayout[] {
  const seen = new Set<ModuleId>();
  const seenPanelIds = new Set<string>();
  return panels
    .filter(isRecord)
    .map((raw, index): PanelLayout | null => {
      const rawTabs = Array.isArray(raw.tabs)
        ? raw.tabs.filter(isModuleId).filter((moduleId, tabIndex, tabs) => tabs.indexOf(moduleId) === tabIndex)
        : [];
      const rawModuleId = isModuleId(raw.moduleId) ? raw.moduleId : rawTabs[0];
      if (!rawModuleId) return null;
      const tabs = rawTabs.length ? rawTabs : [rawModuleId];
      const moduleId = tabs.includes(rawModuleId) ? rawModuleId : tabs[0];
      const requestedId = typeof raw.id === "string" ? raw.id.trim() : "";
      const id = requestedId && !seenPanelIds.has(requestedId)
        ? requestedId
        : `panel-${moduleId}-${index + 1}`;
      seenPanelIds.add(id);
      const restore = normalizeRestore(raw.restore);
      return {
        id,
        moduleId,
        tabs,
        x: finiteNumber(raw.x, 0),
        y: finiteNumber(raw.y, 0),
        w: finiteNumber(raw.w, 0),
        h: finiteNumber(raw.h, 0),
        contextByModule: normalizePanelContexts(raw.contextByModule, tabs),
        maximized: raw.maximized === true && restore !== undefined,
        restore,
      };
    })
    .filter((panel): panel is PanelLayout => panel !== null)
    .filter((panel) => panel.tabs?.length)
    .filter((panel) => {
      panel.tabs = panel.tabs?.filter((moduleId) => {
        if (seen.has(moduleId)) return false;
        seen.add(moduleId);
        return true;
      });
      return Boolean(panel.tabs?.length);
    })
    .map((panel) => {
      const restored = options.collapseMaximized && panel.maximized && panel.restore
        ? { ...panel, ...panel.restore, maximized: false, restore: undefined }
        : panel.maximized && !panel.restore
          ? { ...panel, maximized: false, restore: undefined }
          : panel;
      const tabs = restored.tabs ?? [restored.moduleId];
      const moduleId = tabs.includes(restored.moduleId) ? restored.moduleId : tabs[0];
      const min = panelMinimumSize({ ...restored, moduleId, tabs });
      const w = clamp(restored.w, min.w, CANVAS_COLUMNS);
      const h = clamp(restored.h, min.h, CANVAS_ROWS);
      return {
        ...restored,
        moduleId,
        tabs,
        contextByModule: normalizePanelContexts(restored, tabs),
        x: clamp(restored.x, 0, CANVAS_COLUMNS - w),
        y: clamp(restored.y, 0, CANVAS_ROWS - h),
        w,
        h,
      };
    });
}

function normalizePanelContexts(
  rawContexts: unknown,
  tabs: ModuleId[],
): Partial<Record<ContextualModuleId, PanelContextBinding>> | undefined {
  if (!isRecord(rawContexts)) return undefined;
  const contexts: Partial<Record<ContextualModuleId, PanelContextBinding>> = {};
  for (const moduleId of tabs) {
    if (!isContextualModule(moduleId)) continue;
    const context = rawContexts[moduleId];
    if (!isRecord(context)) {
      contexts[moduleId] = { mode: "follow" };
      continue;
    }
    const sessionId = typeof context.sessionId === "string" ? context.sessionId.trim() : "";
    contexts[moduleId] = context.mode === "pinned"
      ? { mode: "pinned", ...(sessionId ? { sessionId } : {}) }
      : { mode: "follow" };
  }
  return Object.keys(contexts).length ? contexts : undefined;
}

function normalizeRestore(value: unknown): PanelLayout["restore"] | undefined {
  if (!isRecord(value)) return undefined;
  if (
    !isFiniteNumber(value.x) ||
    !isFiniteNumber(value.y) ||
    !isFiniteNumber(value.w) ||
    !isFiniteNumber(value.h)
  ) {
    return undefined;
  }
  return { x: value.x, y: value.y, w: value.w, h: value.h };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function finiteNumber(value: unknown, fallback: number): number {
  return isFiniteNumber(value) ? value : fallback;
}

function isModuleId(value: unknown): value is ModuleId {
  return typeof value === "string" &&
    moduleDefinitions.some((definition) => definition.id === value);
}

export function isContextualModule(moduleId: ModuleId): moduleId is ContextualModuleId {
  return moduleDefinitions.find((definition) => definition.id === moduleId)?.contextScope === "session";
}

export function panelContext(panel: PanelLayout, moduleId: ModuleId): PanelContextBinding | undefined {
  if (!isContextualModule(moduleId)) return undefined;
  const context = panel.contextByModule?.[moduleId];
  if (context?.mode === "pinned") {
    const sessionId = context.sessionId?.trim();
    return { mode: "pinned", ...(sessionId ? { sessionId } : {}) };
  }
  return { mode: "follow" };
}

export function setModuleContext(
  panel: PanelLayout,
  moduleId: ModuleId,
  binding: PanelContextBinding,
): PanelLayout {
  if (!isContextualModule(moduleId)) return panel;
  return {
    ...panel,
    contextByModule: {
      ...panel.contextByModule,
      [moduleId]: binding,
    },
  };
}

export function removeModuleContext(panel: PanelLayout, moduleId: ModuleId): PanelLayout {
  if (!isContextualModule(moduleId) || !panel.contextByModule) return panel;
  const next = { ...panel.contextByModule };
  delete next[moduleId];
  return {
    ...panel,
    contextByModule: Object.keys(next).length ? next : undefined,
  };
}

export function createPanelId(moduleId: ModuleId): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `panel-${moduleId}-${crypto.randomUUID()}`;
  }
  return `panel-${moduleId}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function overlaps(left: PanelLayout, right: PanelLayout): boolean {
  if (left.id === right.id) return false;
  return left.x < right.x + right.w &&
    left.x + left.w > right.x &&
    left.y < right.y + right.h &&
    left.y + left.h > right.y;
}

export function panelTabs(panel: PanelLayout): ModuleId[] {
  return panel.tabs?.length ? panel.tabs : [panel.moduleId];
}

export function panelContains(panel: PanelLayout, moduleId: ModuleId): boolean {
  return panelTabs(panel).includes(moduleId);
}

export function panelMinimumSize(panel: PanelLayout): { w: number; h: number } {
  return panelTabs(panel).reduce(
    (minimum, moduleId) => {
      const definition = moduleDefinitions.find((candidate) => candidate.id === moduleId);
      return definition
        ? { w: Math.max(minimum.w, definition.minW), h: Math.max(minimum.h, definition.minH) }
        : minimum;
    },
    { w: 1, h: 1 },
  );
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
        id: createPanelId(moduleId),
        moduleId,
        tabs: [moduleId],
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
  if (!Number.isFinite(value)) return minimum;
  return Math.min(Math.max(value, minimum), maximum);
}

export function defaultCanvasState(): StoredCanvasState {
  const layouts = (Object.keys(workspaceLabels) as WorkspaceId[]).reduce(
    (result, workspace) => {
      result[workspace] = normalizeLayout(defaultWorkspaceLayout(workspace), {
        collapseMaximized: true,
      });
      return result;
    },
    {} as Record<WorkspaceId, PanelLayout[]>,
  );
  return {
    version: CANVAS_STATE_VERSION,
    layouts,
    selectedSessionByWorkspace: {},
  };
}

export function readStoredCanvasState(): StoredCanvasState {
  const fallback = defaultCanvasState();
  try {
    const current = localStorage.getItem(CANVAS_STATE_KEY);
    if (current) {
      const parsed = JSON.parse(current) as Partial<StoredCanvasState>;
      if (parsed.version === CANVAS_STATE_VERSION && parsed.layouts && typeof parsed.layouts === "object") {
        for (const workspace of Object.keys(workspaceLabels) as WorkspaceId[]) {
          const panels = parsed.layouts[workspace];
          if (Array.isArray(panels)) {
            fallback.layouts[workspace] = normalizeLayout(panels, {
              collapseMaximized: true,
            });
          }
        }
        fallback.selectedSessionByWorkspace = normalizeSelectedSessions(
          parsed.selectedSessionByWorkspace,
        );
        return fallback;
      }
    }

    const legacy = localStorage.getItem(LEGACY_LAYOUTS_KEY);
    if (!legacy) return fallback;
    const parsedLegacy = JSON.parse(legacy) as Partial<Record<WorkspaceId, PanelLayout[]>>;
    for (const workspace of Object.keys(workspaceLabels) as WorkspaceId[]) {
      const panels = parsedLegacy[workspace];
      if (Array.isArray(panels)) {
        fallback.layouts[workspace] = normalizeLayout(panels, {
          collapseMaximized: true,
        });
      }
    }
    return fallback;
  } catch {
    return fallback;
  }
}

export function writeStoredCanvasState(state: StoredCanvasState): void {
  try {
    const normalized: StoredCanvasState = {
      version: CANVAS_STATE_VERSION,
      layouts: (Object.keys(workspaceLabels) as WorkspaceId[]).reduce(
        (result, workspace) => {
          result[workspace] = normalizeLayout(state.layouts[workspace] ?? [], {
            collapseMaximized: true,
          });
          return result;
        },
        {} as Record<WorkspaceId, PanelLayout[]>,
      ),
      selectedSessionByWorkspace: normalizeSelectedSessions(state.selectedSessionByWorkspace),
    };
    localStorage.setItem(CANVAS_STATE_KEY, JSON.stringify(normalized));
  } catch {
    // Layout persistence is best-effort; runtime state must not depend on it.
  }
}

function normalizeSelectedSessions(
  selected: Partial<Record<WorkspaceId, string>> | undefined,
): Partial<Record<WorkspaceId, string>> {
  if (!selected || typeof selected !== "object") return {};
  const result: Partial<Record<WorkspaceId, string>> = {};
  for (const workspace of Object.keys(workspaceLabels) as WorkspaceId[]) {
    const sessionId = selected[workspace];
    if (typeof sessionId === "string" && sessionId.trim()) result[workspace] = sessionId.trim();
  }
  return result;
}
