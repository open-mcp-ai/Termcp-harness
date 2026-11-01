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

export type BuiltInWorkspaceId = "daily" | "security" | "mcp" | "diagnostics";
export type WorkspaceTemplateId = BuiltInWorkspaceId | "custom" | "blank";
export type WorkspaceId = BuiltInWorkspaceId | "custom" | `space-${string}`;

export interface WorkspaceRecord {
  id: WorkspaceId;
  name: string;
  template: WorkspaceTemplateId;
  kind: "preset" | "personal";
  createdAt?: number;
}

export interface WorkspaceCatalog {
  version: 1;
  spaces: WorkspaceRecord[];
}

export interface WorkspaceTemplateOption {
  id: WorkspaceTemplateId;
  label: string;
  description: string;
}

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}
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
    title: "MCP 总览",
    description: "统一查看 MCP Server 注册、连接、能力、健康与启用状态。",
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

export const WORKSPACE_CATALOG_VERSION = 1;
export const WORKSPACE_CATALOG_STORAGE_KEY = "termcp.canvas.spaces";
export const MAX_PERSONAL_WORKSPACES = 20;
export const MAX_WORKSPACE_NAME_LENGTH = 32;

export const workspaceLabels: Record<BuiltInWorkspaceId, string> = {
  daily: "日常协作",
  security: "安全测试",
  mcp: "MCP 管理",
  diagnostics: "诊断中心",
};

export const workspaceTemplateOptions: WorkspaceTemplateOption[] = [
  {
    id: "blank",
    label: "空白 Space",
    description: "从空画布开始，稍后按需添加模块。",
  },
  {
    id: "security",
    label: "安全测试",
    description: "Session、Agent、审批、终端和活动记录。",
  },
  {
    id: "daily",
    label: "日常协作",
    description: "以 Session、Agent 和活动记录为主。",
  },
  {
    id: "mcp",
    label: "MCP 管理",
    description: "集中查看 MCP、活动记录和诊断。",
  },
  {
    id: "diagnostics",
    label: "诊断中心",
    description: "以诊断、MCP 和活动记录为主。",
  },
];

const workspaceTemplates: Record<WorkspaceTemplateId, PanelLayout[]> = {
  blank: [],
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

export function cloneTemplate(template: WorkspaceTemplateId): PanelLayout[] {
  return workspaceTemplates[template].map((panel) => ({
    ...panel,
    tabs: panel.tabs ? [...panel.tabs] : undefined,
    contextByModule: panel.contextByModule ? { ...panel.contextByModule } : undefined,
    restore: panel.restore ? { ...panel.restore } : undefined,
  }));
}

export function defaultWorkspaceLayout(template: WorkspaceTemplateId): PanelLayout[] {
  return cloneTemplate(template);
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

export function defaultWorkspaceCatalog(): WorkspaceCatalog {
  return {
    version: WORKSPACE_CATALOG_VERSION,
    spaces: [
      ...(Object.keys(workspaceLabels) as BuiltInWorkspaceId[]).map(
        (id): WorkspaceRecord => ({
          id,
          name: workspaceLabels[id],
          template: id,
          kind: "preset",
        }),
      ),
      {
        id: "custom",
        name: "自定义工作区",
        template: "custom",
        kind: "personal",
      },
    ],
  };
}

export function isWorkspaceId(value: unknown): value is WorkspaceId {
  return value === "custom" ||
    value === "daily" ||
    value === "security" ||
    value === "mcp" ||
    value === "diagnostics" ||
    (typeof value === "string" && /^space-[a-z0-9][a-z0-9-]{5,63}$/.test(value));
}

export function isWorkspaceTemplateId(value: unknown): value is WorkspaceTemplateId {
  return value === "blank" ||
    value === "custom" ||
    value === "daily" ||
    value === "security" ||
    value === "mcp" ||
    value === "diagnostics";
}

export function workspaceNameFor(
  spaces: WorkspaceRecord[],
  workspace: WorkspaceId,
): string {
  const stored = spaces.find((space) => space.id === workspace)?.name.trim();
  if (stored) return stored;
  return workspace in workspaceLabels
    ? workspaceLabels[workspace as BuiltInWorkspaceId]
    : "未知 Space";
}

export function workspaceRecordFor(
  spaces: WorkspaceRecord[],
  workspace: WorkspaceId,
): WorkspaceRecord | undefined {
  return spaces.find((space) => space.id === workspace);
}

export function createPersonalWorkspaceRecord(
  name: string,
  template: WorkspaceTemplateId,
  now = Date.now(),
): WorkspaceRecord {
  const randomID = typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
    ? crypto.randomUUID().toLowerCase()
    : `${now.toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
  return {
    id: `space-${randomID.replace(/[^a-z0-9-]/g, "").slice(0, 64)}`,
    name: normalizeWorkspaceName(name),
    template,
    kind: "personal",
    createdAt: now,
  };
}

export function workspaceNameError(
  name: string,
  spaces: WorkspaceRecord[],
  excludeWorkspace?: WorkspaceId,
): string | null {
  const normalized = normalizeWorkspaceName(name);
  if (!normalized) return "请输入 Space 名称。";
  if (normalized.length > MAX_WORKSPACE_NAME_LENGTH) {
    return `Space 名称不能超过 ${MAX_WORKSPACE_NAME_LENGTH} 个字符。`;
  }
  const duplicate = spaces.some((space) =>
    space.id !== excludeWorkspace &&
    space.name.toLocaleLowerCase() === normalized.toLocaleLowerCase()
  );
  return duplicate ? "已经有同名 Space，请换一个名称。" : null;
}

export function personalWorkspaceCount(spaces: WorkspaceRecord[]): number {
  return spaces.filter((space) => space.kind === "personal").length;
}

export function readStoredWorkspaceCatalog(storage: StorageLike): WorkspaceCatalog {
  const fallback = defaultWorkspaceCatalog();
  let raw: string | null;
  try {
    raw = storage.getItem(WORKSPACE_CATALOG_STORAGE_KEY);
  } catch {
    return fallback;
  }
  if (!raw) return fallback;

  try {
    const parsed = JSON.parse(raw) as unknown;
    if (
      !isRecord(parsed) ||
      parsed.version !== WORKSPACE_CATALOG_VERSION ||
      !Array.isArray(parsed.spaces)
    ) {
      return fallback;
    }
    const personal: WorkspaceRecord[] = [];
    const seen = new Set<WorkspaceId>();
    const seenNames = new Set<string>();
    for (const rawSpace of parsed.spaces) {
      if (!isRecord(rawSpace) || !isWorkspaceId(rawSpace.id) || !isPersonalWorkspaceId(rawSpace.id)) {
        continue;
      }
      if (seen.has(rawSpace.id)) continue;
      const name = normalizeWorkspaceName(rawSpace.name);
      const normalizedName = name.toLocaleLowerCase();
      if (
        !name ||
        name.length > MAX_WORKSPACE_NAME_LENGTH ||
        seenNames.has(normalizedName) ||
        isBuiltInWorkspaceId(rawSpace.id)
      ) {
        continue;
      }
      const template = isWorkspaceTemplateId(rawSpace.template)
        ? rawSpace.template
        : rawSpace.id === "custom"
          ? "custom"
          : "blank";
      const createdAt = typeof rawSpace.createdAt === "number" && Number.isFinite(rawSpace.createdAt)
        ? rawSpace.createdAt
        : undefined;
      seen.add(rawSpace.id);
      seenNames.add(normalizedName);
      personal.push({
        id: rawSpace.id,
        name,
        template,
        kind: "personal",
        ...(createdAt ? { createdAt } : {}),
      });
      if (personal.length >= MAX_PERSONAL_WORKSPACES) break;
    }
    return {
      version: WORKSPACE_CATALOG_VERSION,
      spaces: [
        ...defaultWorkspaceCatalog().spaces.filter((space) => space.kind === "preset"),
        ...personal.slice(0, MAX_PERSONAL_WORKSPACES),
      ],
    };
  } catch {
    return fallback;
  }
}

export function writeStoredWorkspaceCatalog(
  storage: StorageLike,
  catalog: WorkspaceCatalog,
): boolean {
  try {
    storage.setItem(WORKSPACE_CATALOG_STORAGE_KEY, JSON.stringify(catalog));
    return true;
  } catch {
    return false;
  }
}

export function defaultCanvasState(
  spaces = defaultWorkspaceCatalog().spaces,
): StoredCanvasState {
  const layouts = spaces.reduce(
    (result, space) => {
      result[space.id] = normalizeLayout(defaultWorkspaceLayout(space.template), {
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

export function readStoredCanvasState(
  spaces = defaultWorkspaceCatalog().spaces,
): StoredCanvasState {
  const fallback = defaultCanvasState(spaces);
  try {
    const current = localStorage.getItem(CANVAS_STATE_KEY);
    if (current) {
      const parsed = JSON.parse(current) as Partial<StoredCanvasState>;
      if (parsed.version === CANVAS_STATE_VERSION && parsed.layouts && typeof parsed.layouts === "object") {
        for (const space of spaces) {
          const panels = parsed.layouts[space.id];
          if (Array.isArray(panels)) {
            fallback.layouts[space.id] = normalizeLayout(panels, {
              collapseMaximized: true,
            });
          }
        }
        fallback.selectedSessionByWorkspace = normalizeSelectedSessions(
          parsed.selectedSessionByWorkspace,
          spaces,
        );
        return fallback;
      }
    }

    const legacy = localStorage.getItem(LEGACY_LAYOUTS_KEY);
    if (!legacy) return fallback;
    const parsedLegacy = JSON.parse(legacy) as Partial<Record<WorkspaceId, PanelLayout[]>>;
    for (const space of spaces) {
      const panels = parsedLegacy[space.id];
      if (Array.isArray(panels)) {
        fallback.layouts[space.id] = normalizeLayout(panels, {
          collapseMaximized: true,
        });
      }
    }
    return fallback;
  } catch {
    return fallback;
  }
}

export function writeStoredCanvasState(
  state: StoredCanvasState,
  spaces = defaultWorkspaceCatalog().spaces,
): void {
  try {
    const normalized: StoredCanvasState = {
      version: CANVAS_STATE_VERSION,
      layouts: spaces.reduce(
        (result, space) => {
          result[space.id] = normalizeLayout(state.layouts[space.id] ?? [], {
            collapseMaximized: true,
          });
          return result;
        },
        {} as Record<WorkspaceId, PanelLayout[]>,
      ),
      selectedSessionByWorkspace: normalizeSelectedSessions(
        state.selectedSessionByWorkspace,
        spaces,
      ),
    };
    localStorage.setItem(CANVAS_STATE_KEY, JSON.stringify(normalized));
  } catch {
    // Layout persistence is best-effort; runtime state must not depend on it.
  }
}

function normalizeSelectedSessions(
  selected: Partial<Record<WorkspaceId, string>> | undefined,
  spaces: WorkspaceRecord[],
): Partial<Record<WorkspaceId, string>> {
  if (!selected || typeof selected !== "object") return {};
  const result: Partial<Record<WorkspaceId, string>> = {};
  for (const space of spaces) {
    const sessionId = selected[space.id];
    if (typeof sessionId === "string" && sessionId.trim()) result[space.id] = sessionId.trim();
  }
  return result;
}

function normalizeWorkspaceName(value: unknown): string {
  return typeof value === "string"
    ? value.replace(/\s+/g, " ").trim().slice(0, MAX_WORKSPACE_NAME_LENGTH + 1)
    : "";
}

function isBuiltInWorkspaceId(value: unknown): value is BuiltInWorkspaceId {
  return value === "daily" ||
    value === "security" ||
    value === "mcp" ||
    value === "diagnostics";
}

function isPersonalWorkspaceId(value: unknown): value is WorkspaceId {
  return value === "custom" ||
    (typeof value === "string" && /^space-[a-z0-9][a-z0-9-]{5,63}$/.test(value));
}
