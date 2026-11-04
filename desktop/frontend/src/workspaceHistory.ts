import {
  defaultWorkspaceCatalog,
  normalizeLayout,
  type PanelLayout,
  type WorkspaceId,
  type WorkspaceRecord,
} from "./canvas.ts";

export const WORKSPACE_HISTORY_STORAGE_KEY = "termcp.canvas.history.v1";
export const WORKSPACE_HISTORY_VERSION = 1;
export const WORKSPACE_HISTORY_LIMIT = 20;

export interface WorkspaceLayoutSnapshot {
  layout: PanelLayout[];
  selectedSessionId?: string;
}

export interface WorkspaceHistory {
  past: WorkspaceLayoutSnapshot[];
  future: WorkspaceLayoutSnapshot[];
}

export type WorkspaceHistoryMap = Record<string, WorkspaceHistory>;

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export function emptyWorkspaceHistoryMap(
  spaces = defaultWorkspaceCatalog().spaces,
): WorkspaceHistoryMap {
  return spaces.reduce(
    (result, space) => {
      result[space.id] = { past: [], future: [] };
      return result;
    },
    {} as Record<WorkspaceId, WorkspaceHistory>,
  );
}

export function recordWorkspaceHistory(
  histories: WorkspaceHistoryMap,
  workspace: WorkspaceId,
  current: WorkspaceLayoutSnapshot,
  limit = WORKSPACE_HISTORY_LIMIT,
): WorkspaceHistoryMap {
  const currentHistory = histories[workspace] ?? { past: [], future: [] };
  return {
    ...histories,
    [workspace]: {
      past: [...currentHistory.past, cloneSnapshot(current)].slice(-historyLimit(limit)),
      future: [],
    },
  };
}

export function undoWorkspaceHistory(
  histories: WorkspaceHistoryMap,
  workspace: WorkspaceId,
  current: WorkspaceLayoutSnapshot,
  limit = WORKSPACE_HISTORY_LIMIT,
): { histories: WorkspaceHistoryMap; snapshot: WorkspaceLayoutSnapshot } | null {
  const history = histories[workspace] ?? { past: [], future: [] };
  if (!history.past.length) return null;
  return {
    histories: {
      ...histories,
      [workspace]: {
        past: history.past.slice(0, -1),
        future: [cloneSnapshot(current), ...history.future].slice(0, historyLimit(limit)),
      },
    },
    snapshot: cloneSnapshot(history.past[history.past.length - 1]),
  };
}

export function redoWorkspaceHistory(
  histories: WorkspaceHistoryMap,
  workspace: WorkspaceId,
  current: WorkspaceLayoutSnapshot,
  limit = WORKSPACE_HISTORY_LIMIT,
): { histories: WorkspaceHistoryMap; snapshot: WorkspaceLayoutSnapshot } | null {
  const history = histories[workspace] ?? { past: [], future: [] };
  if (!history.future.length) return null;
  return {
    histories: {
      ...histories,
      [workspace]: {
        past: [...history.past, cloneSnapshot(current)].slice(-historyLimit(limit)),
        future: history.future.slice(1),
      },
    },
    snapshot: cloneSnapshot(history.future[0]),
  };
}

export function canUndoWorkspace(histories: WorkspaceHistoryMap, workspace: WorkspaceId): boolean {
  return Boolean(histories[workspace]?.past.length);
}

export function canRedoWorkspace(histories: WorkspaceHistoryMap, workspace: WorkspaceId): boolean {
  return Boolean(histories[workspace]?.future.length);
}

export function workspaceSnapshotsEqual(
  left: WorkspaceLayoutSnapshot,
  right: WorkspaceLayoutSnapshot,
): boolean {
  return JSON.stringify(cloneSnapshot(left)) === JSON.stringify(cloneSnapshot(right));
}

export function readStoredWorkspaceHistory(
  storage: StorageLike,
  spaces = defaultWorkspaceCatalog().spaces,
): WorkspaceHistoryMap {
  const fallback = emptyWorkspaceHistoryMap(spaces);
  let raw: string | null;
  try {
    raw = storage.getItem(WORKSPACE_HISTORY_STORAGE_KEY);
  } catch {
    return fallback;
  }
  if (!raw) return fallback;

  try {
    const value = JSON.parse(raw) as unknown;
    if (!isRecord(value) || value.version !== WORKSPACE_HISTORY_VERSION || !isRecord(value.histories)) {
      return fallback;
    }
    for (const space of spaces) {
      fallback[space.id] = normalizeHistory(value.histories[space.id], true);
    }
  } catch {
    return emptyWorkspaceHistoryMap(spaces);
  }
  return fallback;
}

export function writeStoredWorkspaceHistory(
  storage: StorageLike,
  histories: WorkspaceHistoryMap,
  spaces: WorkspaceRecord[] = defaultWorkspaceCatalog().spaces,
): boolean {
  const normalized = spaces.reduce(
    (result, space) => {
      result[space.id] = normalizeHistory(histories[space.id], true);
      return result;
    },
    {} as Record<WorkspaceId, WorkspaceHistory>,
  );
  try {
    storage.setItem(WORKSPACE_HISTORY_STORAGE_KEY, JSON.stringify({
      version: WORKSPACE_HISTORY_VERSION,
      histories: normalized,
    }));
    return true;
  } catch {
    return false;
  }
}

function normalizeHistory(value: unknown, collapseMaximized = false): WorkspaceHistory {
  if (!isRecord(value)) return { past: [], future: [] };
  return {
    past: normalizeSnapshotList(value.past, collapseMaximized).slice(-WORKSPACE_HISTORY_LIMIT),
    future: normalizeSnapshotList(value.future, collapseMaximized).slice(0, WORKSPACE_HISTORY_LIMIT),
  };
}

function normalizeSnapshotList(
  value: unknown,
  collapseMaximized: boolean,
): WorkspaceLayoutSnapshot[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((snapshot) => normalizeSnapshot(snapshot, collapseMaximized))
    .filter((snapshot): snapshot is WorkspaceLayoutSnapshot => snapshot !== null);
}

function normalizeSnapshot(
  value: unknown,
  collapseMaximized: boolean,
): WorkspaceLayoutSnapshot | null {
  if (!isRecord(value) || !Array.isArray(value.layout)) return null;
  const selectedSessionId = normalizeSessionID(value.selectedSessionId);
  return cloneSnapshot({
    layout: normalizeLayout(value.layout as PanelLayout[], { collapseMaximized }),
    ...(selectedSessionId ? { selectedSessionId } : {}),
  });
}

function cloneSnapshot(snapshot: WorkspaceLayoutSnapshot): WorkspaceLayoutSnapshot {
  const selectedSessionId = normalizeSessionID(snapshot.selectedSessionId);
  return {
    layout: normalizeLayout(snapshot.layout),
    ...(selectedSessionId ? { selectedSessionId } : {}),
  };
}

function normalizeSessionID(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const sessionID = value.trim();
  return sessionID && sessionID.length <= 256 ? sessionID : undefined;
}

function historyLimit(value: number): number {
  if (!Number.isFinite(value)) return WORKSPACE_HISTORY_LIMIT;
  return Math.max(1, Math.min(50, Math.floor(value)));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
