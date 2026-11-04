import {
  CANVAS_COLUMNS,
  CANVAS_ROWS,
  isWorkspaceId,
  layoutHasOverlap,
  moduleDefinitions,
  normalizeLayout,
  type ModuleId,
  type PanelContextBinding,
  type PanelLayout,
  type WorkspaceId,
} from "./canvas.ts";

export const WORKSPACE_RECIPE_FORMAT = "termcp.harness.workspace";
export const WORKSPACE_RECIPE_VERSION = 1;
export const MAX_WORKSPACE_RECIPE_BYTES = 1 << 20;
export const MAX_WORKSPACE_RECIPE_PANELS = 64;

export interface WorkspaceRecipe {
  format: typeof WORKSPACE_RECIPE_FORMAT;
  version: typeof WORKSPACE_RECIPE_VERSION;
  workspace: WorkspaceId;
  layout: PanelLayout[];
  selected_session_id?: string;
}

export interface WorkspaceRecipeParseResult {
  recipe: WorkspaceRecipe;
  warnings: string[];
}

export type WorkspaceRecipeErrorCode =
  | "too_large"
  | "invalid_json"
  | "unsupported_format"
  | "unsupported_version"
  | "invalid_workspace"
  | "invalid_layout"
  | "empty_layout"
  | "too_many_panels"
  | "overlapping_panels";

export class WorkspaceRecipeError extends Error {
  code: WorkspaceRecipeErrorCode;

  constructor(code: WorkspaceRecipeErrorCode, message: string) {
    super(message);
    this.name = "WorkspaceRecipeError";
    this.code = code;
  }
}

export function createWorkspaceRecipe(
  workspace: WorkspaceId,
  layout: PanelLayout[],
  selectedSessionID?: string,
): WorkspaceRecipe {
  const normalized = normalizeLayout(layout, { collapseMaximized: true });
  if (!normalized.length) {
    throw new WorkspaceRecipeError("empty_layout", "当前 Workspace 没有可导出的面板。");
  }
  if (layoutHasOverlap(normalized)) {
    throw new WorkspaceRecipeError(
      "overlapping_panels",
      "当前 Workspace 包含重叠面板，无法导出为有效 Recipe。",
    );
  }
  const sessionID = normalizeSessionID(selectedSessionID);
  return {
    format: WORKSPACE_RECIPE_FORMAT,
    version: WORKSPACE_RECIPE_VERSION,
    workspace,
    layout: normalized,
    ...(sessionID ? { selected_session_id: sessionID } : {}),
  };
}

export function serializeWorkspaceRecipe(recipe: WorkspaceRecipe): string {
  return `${JSON.stringify(recipe, null, 2)}\n`;
}

export function parseWorkspaceRecipe(text: string): WorkspaceRecipeParseResult {
  if (new TextEncoder().encode(text).byteLength > MAX_WORKSPACE_RECIPE_BYTES) {
    throw new WorkspaceRecipeError(
      "too_large",
      `Workspace Recipe 不能超过 ${MAX_WORKSPACE_RECIPE_BYTES / 1024} KiB。`,
    );
  }

  let value: unknown;
  try {
    value = JSON.parse(text.replace(/^\uFEFF/, ""));
  } catch {
    throw new WorkspaceRecipeError("invalid_json", "Workspace Recipe 不是有效 JSON。");
  }
  if (!isRecord(value)) {
    throw new WorkspaceRecipeError("invalid_layout", "Workspace Recipe 必须是 JSON 对象。");
  }
  if (value.format !== WORKSPACE_RECIPE_FORMAT) {
    throw new WorkspaceRecipeError(
      "unsupported_format",
      "文件不是 Termcp Harness Workspace Recipe。",
    );
  }
  if (value.version !== WORKSPACE_RECIPE_VERSION) {
    throw new WorkspaceRecipeError(
      "unsupported_version",
      `不支持 Workspace Recipe version ${String(value.version)}。`,
    );
  }
  if (!isWorkspaceId(value.workspace)) {
    throw new WorkspaceRecipeError("invalid_workspace", "Workspace Recipe 的工作区类型无效。");
  }
  if (!Array.isArray(value.layout)) {
    throw new WorkspaceRecipeError("invalid_layout", "Workspace Recipe 缺少面板数组。");
  }
  if (value.layout.length > MAX_WORKSPACE_RECIPE_PANELS) {
    throw new WorkspaceRecipeError(
      "too_many_panels",
      `Workspace Recipe 最多包含 ${MAX_WORKSPACE_RECIPE_PANELS} 个面板。`,
    );
  }

  const warnings: string[] = [];
  const panels: PanelLayout[] = [];
  value.layout.forEach((raw, index) => {
    const panel = sanitizePanel(raw, index, warnings);
    if (panel) panels.push(panel);
  });
  const normalized = normalizeLayout(panels, { collapseMaximized: true });
  if (!normalized.length) {
    throw new WorkspaceRecipeError("empty_layout", "Workspace Recipe 没有可用面板。");
  }
  if (normalized.length < panels.length) {
    warnings.push("重复占用的面板类型已从布局中移除。");
  }
  if (layoutHasOverlap(normalized)) {
    throw new WorkspaceRecipeError(
      "overlapping_panels",
      "Workspace Recipe 包含重叠面板，请修复后重新导入。",
    );
  }

  let selectedSessionID: string | undefined;
  if (value.selected_session_id !== undefined) {
    selectedSessionID = normalizeSessionID(value.selected_session_id);
    if (!selectedSessionID) {
      warnings.push("无效的 Session 引用已忽略。");
    }
  }
  return {
    recipe: {
      format: WORKSPACE_RECIPE_FORMAT,
      version: WORKSPACE_RECIPE_VERSION,
      workspace: value.workspace,
      layout: normalized,
      ...(selectedSessionID ? { selected_session_id: selectedSessionID } : {}),
    },
    warnings,
  };
}

export function downloadWorkspaceRecipe(recipe: WorkspaceRecipe, now = new Date()): void {
  const blob = new Blob([serializeWorkspaceRecipe(recipe)], {
    type: "application/json;charset=utf-8",
  });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = workspaceRecipeFilename(recipe.workspace, now);
  anchor.style.display = "none";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function workspaceRecipeFilename(workspace: WorkspaceId, now = new Date()): string {
  const timestamp = [
    now.getFullYear(),
    String(now.getMonth() + 1).padStart(2, "0"),
    String(now.getDate()).padStart(2, "0"),
    "-",
    String(now.getHours()).padStart(2, "0"),
    String(now.getMinutes()).padStart(2, "0"),
  ].join("");
  return `termcp-workspace-${workspace}-${timestamp}.json`;
}

function sanitizePanel(
  value: unknown,
  index: number,
  warnings: string[],
): PanelLayout | null {
  if (!isRecord(value)) {
    warnings.push(`第 ${index + 1} 个面板不是对象，已跳过。`);
    return null;
  }
  const tabs = Array.isArray(value.tabs)
    ? value.tabs.filter(isModuleId).filter((moduleId, tabIndex, items) => items.indexOf(moduleId) === tabIndex)
    : [];
  const moduleId = isModuleId(value.moduleId) ? value.moduleId : tabs[0];
  if (!moduleId) {
    warnings.push(`第 ${index + 1} 个面板的类型无效，已跳过。`);
    return null;
  }
  if (Array.isArray(value.tabs) && value.tabs.some((tab) => !isModuleId(tab))) {
    warnings.push(`第 ${index + 1} 个面板包含未知类型，已忽略对应标签。`);
  }

  const definition = moduleDefinition(moduleId);
  const x = coordinate(value.x, 0, 0, CANVAS_COLUMNS - definition.minW, index, "x", warnings);
  const y = coordinate(value.y, 0, 0, CANVAS_ROWS - definition.minH, index, "y", warnings);
  const w = coordinate(value.w, definition.minW, definition.minW, CANVAS_COLUMNS, index, "w", warnings);
  const h = coordinate(value.h, definition.minH, definition.minH, CANVAS_ROWS, index, "h", warnings);
  const contextByModule = sanitizeContexts(value.contextByModule, tabs.length ? tabs : [moduleId], index, warnings);
  return {
    id: typeof value.id === "string" ? value.id : "",
    moduleId,
    tabs: tabs.length ? tabs : [moduleId],
    x,
    y,
    w,
    h,
    ...(contextByModule ? { contextByModule } : {}),
  };
}

function sanitizeContexts(
  value: unknown,
  tabs: ModuleId[],
  panelIndex: number,
  warnings: string[],
): Partial<Record<"agent" | "approvals" | "terminal" | "activity", PanelContextBinding>> | undefined {
  if (!isRecord(value)) return undefined;
  const result: Partial<Record<"agent" | "approvals" | "terminal" | "activity", PanelContextBinding>> = {};
  for (const moduleId of tabs) {
    if (!isContextual(moduleId)) continue;
    const raw = value[moduleId];
    if (!isRecord(raw)) {
      result[moduleId] = { mode: "follow" };
      continue;
    }
    const sessionID = normalizeSessionID(raw.sessionId);
    if (raw.mode === "pinned" && !sessionID) {
      warnings.push(`第 ${panelIndex + 1} 个面板的锁定 Session 无效，已改为跟随当前 Session。`);
      result[moduleId] = { mode: "follow" };
    } else {
      result[moduleId] = raw.mode === "pinned"
        ? { mode: "pinned", sessionId: sessionID }
        : { mode: "follow" };
    }
  }
  return Object.keys(result).length ? result : undefined;
}

function coordinate(
  value: unknown,
  fallback: number,
  minimum: number,
  maximum: number,
  panelIndex: number,
  field: string,
  warnings: string[],
): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    warnings.push(`第 ${panelIndex + 1} 个面板的 ${field} 无效，已使用安全值。`);
    return fallback;
  }
  if (value < minimum || value > maximum) {
    warnings.push(`第 ${panelIndex + 1} 个面板的 ${field} 超出 Canvas 范围，已裁剪。`);
  }
  return Math.min(Math.max(value, minimum), maximum);
}

function moduleDefinition(moduleId: ModuleId) {
  return moduleDefinitions.find((definition) => definition.id === moduleId) ??
    moduleDefinitions[0];
}

function isContextual(
  moduleId: ModuleId,
): moduleId is "agent" | "approvals" | "terminal" | "activity" {
  return moduleId === "agent" ||
    moduleId === "approvals" ||
    moduleId === "terminal" ||
    moduleId === "activity";
}

function isModuleId(value: unknown): value is ModuleId {
  return typeof value === "string" &&
    moduleDefinitions.some((definition) => definition.id === value);
}

function normalizeSessionID(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const sessionID = value.trim();
  return sessionID && sessionID.length <= 256 ? sessionID : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
