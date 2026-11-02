import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
} from "react";
import {
  approvalControlAvailable,
  closeEventStream,
  desktopAvailable,
  loadBootstrap,
  openEventStream,
  pollEventStream,
  windowAction,
} from "./api";
import CanvasGrid from "./CanvasGrid";
import { mcpStatusSummary } from "./connectionStatus";
import {
  CANVAS_COLUMNS,
  CANVAS_GAP,
  CANVAS_ROWS,
  createPersonalWorkspaceRecord,
  defaultWorkspaceLayout,
  findFreeSlot,
  isContextualModule,
  moduleDefinitions,
  normalizeLayout,
  panelContains,
  panelContext,
  panelTabs,
  readStoredCanvasState,
  readStoredWorkspaceCatalog,
  removeModuleContext,
  setModuleContext,
  workspaceNameFor,
  workspaceRecordFor,
  writeStoredCanvasState,
  writeStoredWorkspaceCatalog,
  type ModuleId,
  type PanelContextBinding,
  type PanelLayout,
  type StoredCanvasState,
  type WorkspaceCatalog,
  type WorkspaceId,
  type WorkspaceRecord,
  type WorkspaceTemplateId,
} from "./canvas";
import { persistEventCursor, resolveEventCursor } from "./eventCursor";
import { mergeRealtimeEvents } from "./events";
import { agentStateLabel, coreStateLabel, messages, recoveryStateLabel } from "./i18n";
import {
  ActivityPanel,
  AgentPanel,
  ApprovalsPanel,
  DiagnosticsPanel,
  MCPPanel,
  SessionsPanel,
  TerminalPanel,
} from "./Panels";
import type {
  AgentSessionSnapshot,
  Bootstrap,
  EventStreamState,
  RuntimeState,
} from "./types";
import {
  canRedoWorkspace,
  canUndoWorkspace,
  readStoredWorkspaceHistory,
  recordWorkspaceHistory,
  redoWorkspaceHistory,
  undoWorkspaceHistory,
  workspaceSnapshotsEqual,
  writeStoredWorkspaceHistory,
  type WorkspaceLayoutSnapshot,
} from "./workspaceHistory";
import {
  MAX_WORKSPACE_RECIPE_BYTES,
  createWorkspaceRecipe,
  downloadWorkspaceRecipe,
  parseWorkspaceRecipe,
  type WorkspaceRecipeParseResult,
} from "./workspaceRecipe";
import {
  buildWorkspaceMigration,
  migrationDescription,
  type WorkspaceMigrationPreview,
} from "./workspaceMigration";
import SpaceSwitcher from "./SpaceSwitcher";

const stateCopy: Record<RuntimeState, string> = messages.state;
const eventStreamCopy: Record<EventStreamState, string> = {
  connecting: "连接中",
  connected: "实时",
  reconnecting: "重连中",
  lagged: "需补齐",
  stopped: "已停止",
};
const WORKSPACE_STORAGE_KEY = "termcp.canvas.workspace";
type PersistenceState = "checking" | "saved" | "failed";

function WindowControls() {
  if (!desktopAvailable()) return null;
  return (
    <div className="canvas-window-controls no-drag" aria-label={messages.window.controls}>
      <button type="button" aria-label={messages.window.minimise} onClick={() => void windowAction("minimise")}>−</button>
      <button type="button" aria-label={messages.window.maximise} onClick={() => void windowAction("maximise")}>□</button>
      <button type="button" aria-label={messages.window.close} onClick={() => void windowAction("close")}>×</button>
    </div>
  );
}

function initialWorkspace(spaces: WorkspaceRecord[]): WorkspaceId {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(WORKSPACE_STORAGE_KEY);
  } catch {
    // Fall back to the first personal Space when local storage is unavailable.
  }
  return spaces.some((space) => space.id === stored)
    ? stored as WorkspaceId
    : spaces.find((space) => space.kind === "personal")?.id ?? "security";
}

function preferredSession(sessions: AgentSessionSnapshot[]): AgentSessionSnapshot | undefined {
  return sessions.find((session) => session.state === "waiting_approval") ??
    sessions.find((session) => session.state === "waiting_execution") ??
    sessions.find((session) => session.state === "cancelling") ??
    sessions.find((session) => session.state === "running") ??
    sessions[0];
}

function shortID(value?: string): string {
  if (!value) return messages.common.unknown;
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

export default function App() {
  const [data, setData] = useState<Bootstrap | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [lastChecked, setLastChecked] = useState<Date | null>(null);
  const [approvalControl, setApprovalControl] = useState(false);
  const [eventStreamState, setEventStreamState] = useState<EventStreamState>("stopped");
  const [eventStreamError, setEventStreamError] = useState<string | null>(null);
  const [workspaceCatalog, setWorkspaceCatalog] = useState<WorkspaceCatalog>(
    () => readStoredWorkspaceCatalog(localStorage),
  );
  const [workspace, setWorkspace] = useState<WorkspaceId>(
    () => initialWorkspace(workspaceCatalog.spaces),
  );
  const [canvasState, setCanvasState] = useState<StoredCanvasState>(
    () => readStoredCanvasState(workspaceCatalog.spaces),
  );
  const [moduleDrawer, setModuleDrawer] = useState(false);
  const [moduleQuery, setModuleQuery] = useState("");
  const [commandPalette, setCommandPalette] = useState(false);
  const [commandQuery, setCommandQuery] = useState("");
  const [layoutSettings, setLayoutSettings] = useState(false);
  const [drawerMessage, setDrawerMessage] = useState<string | null>(null);
  const [lifecycleNotice, setLifecycleNotice] = useState<string | null>(null);
  const [recipeReview, setRecipeReview] = useState<WorkspaceRecipeParseResult | null>(null);
  const [recipeProblem, setRecipeProblem] = useState<string | null>(null);
  const [migrationTarget, setMigrationTarget] = useState<WorkspaceId | null>(null);
  const [workspaceHistory, setWorkspaceHistory] = useState(
    () => readStoredWorkspaceHistory(localStorage, workspaceCatalog.spaces),
  );
  const [persistenceState, setPersistenceState] = useState<PersistenceState>("checking");
  const inFlight = useRef(false);
  const runtimeInstance = useRef("");
  const eventCursor = useRef(0);
  const recipeFileInput = useRef<HTMLInputElement>(null);

  const openModuleDrawer = useCallback(() => {
    setCommandPalette(false);
    setLayoutSettings(false);
    setModuleDrawer(true);
  }, []);

  const openCommandPalette = useCallback(() => {
    setModuleDrawer(false);
    setLayoutSettings(false);
    setCommandQuery("");
    setCommandPalette(true);
  }, []);

  const openLayoutSettings = useCallback(() => {
    setModuleDrawer(false);
    setCommandPalette(false);
    setLayoutSettings(true);
  }, []);

  const refresh = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setLoading(true);
    try {
      const next = await loadBootstrap();
      if (runtimeInstance.current !== next.runtime.instance_id) {
        runtimeInstance.current = next.runtime.instance_id;
        eventCursor.current = resolveEventCursor(
          localStorage,
          next.endpoint,
          next.runtime.instance_id,
          next.events.latest_sequence,
        );
      }
      setData((current) => {
        if (
          current &&
          next.runtime.instance_id === current.runtime.instance_id &&
          next.events.latest_sequence < current.events.latest_sequence
        ) {
          return current;
        }
        return next;
      });
      setError(null);
      setLastChecked(new Date());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      inFlight.current = false;
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    const interval = window.setInterval(() => void refresh(), 15000);
    return () => window.clearInterval(interval);
  }, []);

  useEffect(() => {
    const runtimeInstanceID = data?.runtime.instance_id;
    const runtimeEndpoint = data?.endpoint;
    if (!desktopAvailable() || !runtimeInstanceID || !runtimeEndpoint) return;

    let cancelled = false;
    let pollTimer = 0;
    let refreshTimer = 0;
    const schedulePoll = (delay: number) => {
      if (cancelled) return;
      window.clearTimeout(pollTimer);
      pollTimer = window.setTimeout(() => void poll(), delay);
    };
    const scheduleRefresh = () => {
      if (cancelled) return;
      window.clearTimeout(refreshTimer);
      refreshTimer = window.setTimeout(() => void refresh(), 120);
    };
    const recoverLag = async (): Promise<boolean> => {
      await closeEventStream().catch(() => undefined);
      const next = await loadBootstrap();
      if (cancelled) return false;
      eventCursor.current = next.events.latest_sequence;
      persistEventCursor(
        localStorage,
        next.endpoint,
        next.runtime.instance_id,
        eventCursor.current,
      );
      setData(next);
      const opened = await openEventStream(eventCursor.current);
      if (cancelled) {
        await closeEventStream().catch(() => undefined);
        return false;
      }
      setEventStreamState(opened.state);
      setEventStreamError(null);
      schedulePoll(50);
      return true;
    };
    const poll = async () => {
      if (cancelled) return;
      try {
        const result = await pollEventStream();
        if (cancelled) return;
        setEventStreamState(result.lagged ? "lagged" : result.state);
        setEventStreamError(result.last_error ?? null);
        if (result.lagged) {
          await recoverLag();
          return;
        }
        if (result.events.length) {
          const nextCursor = Math.max(eventCursor.current, result.cursor);
          if (nextCursor > eventCursor.current) {
            eventCursor.current = nextCursor;
            persistEventCursor(
              localStorage,
              runtimeEndpoint,
              runtimeInstanceID,
              nextCursor,
            );
          }
          scheduleRefresh();
        }
        setData((current) => {
          if (!current) return current;
          const events = mergeRealtimeEvents(current.events, result);
          if (
            events.events.length === current.events.events.length &&
            events.latest_sequence === current.events.latest_sequence &&
            events.dropped_diagnostics === current.events.dropped_diagnostics &&
            events.diagnostic_dropped_through === current.events.diagnostic_dropped_through &&
            events.truncated === current.events.truncated &&
            events.control_overflow === current.events.control_overflow
          ) {
            return current;
          }
          return { ...current, events };
        });
        schedulePoll(result.events.length ? 0 : 100);
      } catch (reason) {
        if (cancelled) return;
        setEventStreamState("reconnecting");
        setEventStreamError(reason instanceof Error ? reason.message : String(reason));
        schedulePoll(1000);
      }
    };

    void openEventStream(eventCursor.current)
      .then((snapshot) => {
        if (cancelled) {
          void closeEventStream().catch(() => undefined);
          return;
        }
        setEventStreamState(snapshot.state);
        setEventStreamError(snapshot.last_error ?? null);
        schedulePoll(0);
      })
      .catch((reason) => {
        if (cancelled) return;
        setEventStreamState("reconnecting");
        setEventStreamError(reason instanceof Error ? reason.message : String(reason));
        schedulePoll(1000);
      });

    return () => {
      cancelled = true;
      window.clearTimeout(pollTimer);
      window.clearTimeout(refreshTimer);
      void closeEventStream().catch(() => undefined);
    };
  }, [data?.endpoint, data?.runtime.instance_id]);

  useEffect(() => {
    if (!desktopAvailable()) return;
    void approvalControlAvailable().then(setApprovalControl).catch(() => setApprovalControl(false));
  }, []);

  useEffect(() => {
    let selectedSpaceSaved = true;
    try {
      localStorage.setItem(WORKSPACE_STORAGE_KEY, workspace);
    } catch {
      selectedSpaceSaved = false;
    }
    const catalogSaved = writeStoredWorkspaceCatalog(localStorage, workspaceCatalog);
    const canvasSaved = writeStoredCanvasState(canvasState, workspaceCatalog.spaces);
    const historySaved = writeStoredWorkspaceHistory(
      localStorage,
      workspaceHistory,
      workspaceCatalog.spaces,
    );
    setPersistenceState(
      selectedSpaceSaved && catalogSaved && canvasSaved && historySaved ? "saved" : "failed",
    );
  }, [canvasState, workspace, workspaceCatalog, workspaceHistory]);

  useEffect(() => {
    if (lifecycleNotice === null) return;
    const timeout = window.setTimeout(() => setLifecycleNotice(null), 5200);
    return () => window.clearTimeout(timeout);
  }, [lifecycleNotice]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setModuleDrawer((current) => !current);
      } else if (event.key === "Escape") {
        setModuleDrawer(false);
        setRecipeReview(null);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const stale = Boolean(error && data);
  const panels = canvasState.layouts[workspace];
  const selectedSessionId = canvasState.selectedSessionByWorkspace[workspace];
  const workspaceName = workspaceNameFor(workspaceCatalog.spaces, workspace);
  const workspaceRecord = workspaceRecordFor(workspaceCatalog.spaces, workspace);
  const selectedSession = data?.agent_sessions.sessions.find(
    (session) => session.id === selectedSessionId,
  );

  useEffect(() => {
    if (!data || selectedSessionId !== undefined) return;
    const preferred = preferredSession(data.agent_sessions.sessions);
    if (!preferred) return;
    setCanvasState((current) => ({
      ...current,
      selectedSessionByWorkspace: {
        ...current.selectedSessionByWorkspace,
        [workspace]: preferred.id,
      },
    }));
  }, [data, selectedSessionId, workspace]);

  const activeTaskCount = data?.agent_sessions.sessions.filter(
    (session) => session.state === "running" ||
      session.state === "cancelling" ||
      session.state === "waiting_approval" ||
      session.state === "waiting_execution",
  ).length ?? 0;
  const agentPanelVisible = panels.some((panel) => panelContains(panel, "agent"));
  const selectedSessionMissing = Boolean(selectedSessionId && data && !selectedSession);
  const availableModules = useMemo(() => {
    const query = moduleQuery.trim().toLocaleLowerCase();
    return moduleDefinitions.filter((definition) =>
      !query || `${definition.title} ${definition.description}`.toLocaleLowerCase().includes(query),
    );
  }, [moduleQuery]);
  const migrationTargets = useMemo(
    () => workspaceCatalog.spaces.filter(
      (candidate) => candidate.id !== workspace,
    ),
    [workspace, workspaceCatalog.spaces],
  );
  const workspacePanelCounts = useMemo(() => workspaceCatalog.spaces.reduce(
    (counts, space) => {
      counts[space.id] = canvasState.layouts[space.id]?.length ?? 0;
      return counts;
    },
    {} as Partial<Record<WorkspaceId, number>>,
  ), [canvasState.layouts, workspaceCatalog.spaces]);
  const migrationPreview = useMemo<WorkspaceMigrationPreview | null>(() => {
    if (!migrationTarget || migrationTarget === workspace || !panels.length) return null;
    try {
      return buildWorkspaceMigration(
        workspace,
        panels,
        selectedSessionId,
        migrationTarget,
        canvasState.layouts[migrationTarget],
        canvasState.selectedSessionByWorkspace[migrationTarget],
      );
    } catch {
      return null;
    }
  }, [
    canvasState.layouts,
    canvasState.selectedSessionByWorkspace,
    migrationTarget,
    panels,
    selectedSessionId,
    workspace,
  ]);

  const currentWorkspaceSnapshot = useMemo<WorkspaceLayoutSnapshot>(() => ({
    layout: normalizeLayout(panels),
    ...(selectedSessionId ? { selectedSessionId } : {}),
  }), [panels, selectedSessionId]);

  const replaceWorkspaceSnapshot = useCallback((snapshot: WorkspaceLayoutSnapshot) => {
    const layout = normalizeLayout(snapshot.layout);
    setCanvasState((current) => {
      const selectedSessionByWorkspace = { ...current.selectedSessionByWorkspace };
      if (snapshot.selectedSessionId) {
        selectedSessionByWorkspace[workspace] = snapshot.selectedSessionId;
      } else {
        delete selectedSessionByWorkspace[workspace];
      }
      return {
        ...current,
        layouts: {
          ...current.layouts,
          [workspace]: layout,
        },
        selectedSessionByWorkspace,
      };
    });
  }, [workspace]);

  const applyWorkspaceSnapshot = useCallback((
    snapshot: WorkspaceLayoutSnapshot,
    recordHistory: boolean,
  ): boolean => {
    const next = {
      layout: normalizeLayout(snapshot.layout),
      ...(snapshot.selectedSessionId
        ? { selectedSessionId: snapshot.selectedSessionId }
        : {}),
    };
    if (workspaceSnapshotsEqual(currentWorkspaceSnapshot, next)) return false;
    if (recordHistory) {
      setWorkspaceHistory((current) =>
        recordWorkspaceHistory(current, workspace, currentWorkspaceSnapshot)
      );
    }
    replaceWorkspaceSnapshot(next);
    return true;
  }, [currentWorkspaceSnapshot, replaceWorkspaceSnapshot, workspace]);

  const updatePanels = useCallback((next: PanelLayout[]) => {
    applyWorkspaceSnapshot({
      layout: next,
      ...(selectedSessionId ? { selectedSessionId } : {}),
    }, true);
  }, [applyWorkspaceSnapshot, selectedSessionId]);

  const selectSession = useCallback((sessionId: string) => {
    setCanvasState((current) => ({
      ...current,
      selectedSessionByWorkspace: {
        ...current.selectedSessionByWorkspace,
        [workspace]: sessionId,
      },
    }));
  }, [workspace]);

  const changePanelContext = useCallback((
    panelId: string,
    moduleId: ModuleId,
    binding: PanelContextBinding,
  ) => {
    if (binding.mode === "pinned" && !binding.sessionId) return;
    updatePanels(panels.map((panel) =>
      panel.id === panelId ? setModuleContext(panel, moduleId, binding) : panel,
    ));
  }, [panels, updatePanels]);

  const resolvePanelContext = useCallback((panel: PanelLayout, moduleId: ModuleId) => {
    const binding = panelContext(panel, moduleId);
    if (!binding) return null;
    const effectiveSessionId = binding.mode === "follow"
      ? selectedSessionId
      : binding.sessionId;
    const session = effectiveSessionId
      ? data?.agent_sessions.sessions.find((candidate) => candidate.id === effectiveSessionId)
      : undefined;
    const state = !effectiveSessionId
      ? "unbound" as const
      : !data
        ? "loading" as const
        : !session
          ? "missing" as const
          : "ready" as const;
    return { binding, effectiveSessionId, session, state };
  }, [data, selectedSessionId]);

  const closePanel = useCallback((panelId: string, moduleId: ModuleId) => {
    const panel = panels.find((candidate) => candidate.id === panelId);
    if (!panel) return;
    const tabs = panelTabs(panel).filter((candidate) => candidate !== moduleId);
    setLifecycleNotice(
      moduleId === "approvals"
        ? "已关闭审批视图；审批请求不会被拒绝、取消或停止。"
        : "已关闭面板视图；不会停止关联任务。",
    );
    if (tabs.length === 0) {
      updatePanels(panels.filter((candidate) => candidate.id !== panelId));
      return;
    }
    updatePanels(panels.map((candidate) =>
      candidate.id === panelId
        ? removeModuleContext({ ...candidate, moduleId: tabs[0], tabs }, moduleId)
        : candidate,
    ));
  }, [panels, updatePanels]);

  const maximizePanel = useCallback((panelId: string) => {
    updatePanels(panels.map((panel) => {
      if (panel.id !== panelId) {
        if (!panel.maximized || !panel.restore) return panel;
        return { ...panel, ...panel.restore, maximized: false, restore: undefined };
      }
      if (panel.maximized && panel.restore) {
        return { ...panel, ...panel.restore, maximized: false, restore: undefined };
      }
      return {
        ...panel,
        maximized: true,
        restore: { x: panel.x, y: panel.y, w: panel.w, h: panel.h },
        x: 0,
        y: 0,
        w: 12,
        h: 12,
      };
    }));
  }, [panels, updatePanels]);

  const addModule = useCallback((moduleId: ModuleId) => {
    if (panels.some((panel) => panelContains(panel, moduleId))) {
      setDrawerMessage("该面板已经在当前工作区中。");
      return;
    }
    const slot = findFreeSlot(panels, moduleId);
    if (!slot) {
      setDrawerMessage("当前工作区没有足够空间。请先移动、缩放或关闭一个面板。");
      return;
    }
    updatePanels([...panels, slot]);
    setDrawerMessage(null);
    setModuleDrawer(false);
  }, [panels, updatePanels]);

  const openTerminalForSession = useCallback((sessionId: string) => {
    selectSession(sessionId);
    if (panels.some((panel) => panelContains(panel, "terminal"))) {
      setLifecycleNotice("Terminal 已在当前工作区，当前 Agent Session 已选中；若 Terminal 已锁定，请切换其上下文。");
      return;
    }
    addModule("terminal");
  }, [addModule, panels, selectSession]);

  const resetLayout = useCallback(() => {
    const next = defaultWorkspaceLayout(workspaceRecord?.template ?? "blank");
    updatePanels(next);
  }, [updatePanels, workspaceRecord]);

  const createWorkspace = useCallback((
    name: string,
    template: WorkspaceTemplateId,
  ) => {
    const record = createPersonalWorkspaceRecord(name, template);
    const layout = normalizeLayout(defaultWorkspaceLayout(template), {
      collapseMaximized: true,
    });
    setWorkspaceCatalog((current) => ({
      ...current,
      spaces: [...current.spaces, record],
    }));
    setCanvasState((current) => ({
      ...current,
      layouts: {
        ...current.layouts,
        [record.id]: layout,
      },
    }));
    setWorkspaceHistory((current) => ({
      ...current,
      [record.id]: { past: [], future: [] },
    }));
    setWorkspace(record.id);
    setMigrationTarget(null);
    setLifecycleNotice(`已创建并打开“${record.name}”。`);
  }, []);

  const renameWorkspace = useCallback((spaceId: WorkspaceId, name: string) => {
    const previousName = workspaceNameFor(workspaceCatalog.spaces, spaceId);
    setWorkspaceCatalog((current) => ({
      ...current,
      spaces: current.spaces.map((space) =>
        space.id === spaceId ? { ...space, name } : space
      ),
    }));
    setLifecycleNotice(`已将“${previousName}”重命名为“${name}”。`);
  }, [workspaceCatalog.spaces]);

  const deleteWorkspace = useCallback((spaceId: WorkspaceId) => {
    const target = workspaceCatalog.spaces.find((space) => space.id === spaceId);
    if (!target || target.kind !== "personal") return;
    const remaining = workspaceCatalog.spaces.filter((space) => space.id !== spaceId);
    const fallback = remaining.find((space) => space.id === "security") ?? remaining[0];
    setWorkspaceCatalog((current) => ({
      ...current,
      spaces: current.spaces.filter((space) => space.id !== spaceId),
    }));
    setCanvasState((current) => {
      const layouts = { ...current.layouts };
      const selectedSessionByWorkspace = { ...current.selectedSessionByWorkspace };
      delete layouts[spaceId];
      delete selectedSessionByWorkspace[spaceId];
      return { ...current, layouts, selectedSessionByWorkspace };
    });
    setWorkspaceHistory((current) => {
      const next = { ...current };
      delete next[spaceId];
      return next;
    });
    setMigrationTarget((current) => current === spaceId ? null : current);
    if (workspace === spaceId) {
      setWorkspace(fallback?.id ?? "security");
    }
    setLifecycleNotice(`已删除“${target.name}”；运行中的任务、终端和审批未受影响。`);
  }, [workspace, workspaceCatalog.spaces]);

  const undoLayout = useCallback(() => {
    const result = undoWorkspaceHistory(
      workspaceHistory,
      workspace,
      currentWorkspaceSnapshot,
    );
    if (!result) return;
    setWorkspaceHistory(result.histories);
    replaceWorkspaceSnapshot(result.snapshot);
    setLifecycleNotice(`已撤销“${workspaceName}”的上一次布局变更。`);
  }, [currentWorkspaceSnapshot, replaceWorkspaceSnapshot, workspace, workspaceHistory, workspaceName]);

  const redoLayout = useCallback(() => {
    const result = redoWorkspaceHistory(
      workspaceHistory,
      workspace,
      currentWorkspaceSnapshot,
    );
    if (!result) return;
    setWorkspaceHistory(result.histories);
    replaceWorkspaceSnapshot(result.snapshot);
    setLifecycleNotice(`已重做“${workspaceName}”的布局变更。`);
  }, [currentWorkspaceSnapshot, replaceWorkspaceSnapshot, workspace, workspaceHistory, workspaceName]);

  useEffect(() => {
    const onUndoKeyDown = (event: KeyboardEvent) => {
      if (moduleDrawer || recipeReview || commandPalette || layoutSettings) return;
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "z") return;
      const target = event.target;
      if (
        target instanceof Element &&
        target.closest("input, textarea, select, [contenteditable='true']")
      ) {
        return;
      }
      event.preventDefault();
      if (event.shiftKey) {
        redoLayout();
      } else {
        undoLayout();
      }
    };
    window.addEventListener("keydown", onUndoKeyDown);
    return () => window.removeEventListener("keydown", onUndoKeyDown);
  }, [commandPalette, layoutSettings, moduleDrawer, recipeReview, redoLayout, undoLayout]);

  useEffect(() => {
    const onCommandPalette = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setCommandPalette(false);
        setLayoutSettings(false);
        setModuleDrawer(false);
        return;
      }
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "k") return;
      event.preventDefault();
      openCommandPalette();
    };
    window.addEventListener("keydown", onCommandPalette);
    return () => window.removeEventListener("keydown", onCommandPalette);
  }, [openCommandPalette]);

  const exportCurrentWorkspace = useCallback(() => {
    try {
      const recipe = createWorkspaceRecipe(workspace, panels, selectedSessionId);
      downloadWorkspaceRecipe(recipe);
      setRecipeProblem(null);
      setLifecycleNotice(`已导出“${workspaceName}”Space Recipe。`);
    } catch (reason) {
      setRecipeProblem(reason instanceof Error ? reason.message : String(reason));
    }
  }, [panels, selectedSessionId, workspace, workspaceName]);

  const selectRecipeFile = useCallback(async (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.currentTarget;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    if (file.size > MAX_WORKSPACE_RECIPE_BYTES) {
      setRecipeReview(null);
      setRecipeProblem(`Workspace Recipe 不能超过 ${MAX_WORKSPACE_RECIPE_BYTES / 1024} KiB。`);
      return;
    }
    try {
      const parsed = parseWorkspaceRecipe(await file.text());
      setRecipeReview(parsed);
      setRecipeProblem(null);
    } catch (reason) {
      setRecipeReview(null);
      setRecipeProblem(reason instanceof Error ? reason.message : String(reason));
    }
  }, []);

  const applyRecipe = useCallback(() => {
    if (!recipeReview) return;
    const recipe = recipeReview.recipe;
    applyWorkspaceSnapshot({
      layout: normalizeLayout(recipe.layout, { collapseMaximized: true }),
      ...(recipe.selected_session_id
        ? { selectedSessionId: recipe.selected_session_id }
        : {}),
    }, true);
    setRecipeReview(null);
    setRecipeProblem(null);
    setLifecycleNotice(
      `已将“${workspaceNameFor(workspaceCatalog.spaces, recipe.workspace)}”Recipe 导入到“${workspaceName}”；任务与审批未受影响。`,
    );
  }, [applyWorkspaceSnapshot, recipeReview, workspace, workspaceCatalog.spaces, workspaceName]);

  const openMigration = useCallback(() => {
    const target = migrationTargets[0]?.id;
    if (!target) return;
    setRecipeReview(null);
    setMigrationTarget(target);
  }, [migrationTargets]);

  const applyMigration = useCallback(() => {
    if (!migrationPreview) return;
    const target = migrationPreview.target;
    const targetSnapshot: WorkspaceLayoutSnapshot = {
      layout: canvasState.layouts[target],
      ...(canvasState.selectedSessionByWorkspace[target]
        ? { selectedSessionId: canvasState.selectedSessionByWorkspace[target] }
        : {}),
    };
    const nextSnapshot = migrationPreview.snapshot;
    if (!workspaceSnapshotsEqual(targetSnapshot, nextSnapshot)) {
      setWorkspaceHistory((current) =>
        recordWorkspaceHistory(current, target, targetSnapshot)
      );
    }
    setCanvasState((current) => {
      const selectedSessionByWorkspace = { ...current.selectedSessionByWorkspace };
      if (nextSnapshot.selectedSessionId) {
        selectedSessionByWorkspace[target] = nextSnapshot.selectedSessionId;
      } else {
        delete selectedSessionByWorkspace[target];
      }
      return {
        ...current,
        layouts: {
          ...current.layouts,
          [target]: normalizeLayout(nextSnapshot.layout),
        },
        selectedSessionByWorkspace,
      };
    });
    setWorkspace(target);
    setMigrationTarget(null);
    setRecipeProblem(null);
    setLifecycleNotice(
      `已将“${workspaceName}”布局复制到“${workspaceNameFor(workspaceCatalog.spaces, target)}”；任务、终端和审批未被迁移或重启。`,
    );
  }, [canvasState, migrationPreview, workspace, workspaceCatalog.spaces, workspaceName]);

  const commandEntries = useMemo(() => {
    const commands = [
      {
        id: "command:refresh",
        kind: "command" as const,
        label: "刷新 Runtime 快照",
        description: "重新读取 Runtime、Core、MCP、审批和 Session 状态。",
        run: () => void refresh(),
      },
      {
        id: "command:undo",
        kind: "command" as const,
        label: "撤销布局变更",
        description: "撤销当前 Workspace 的上一次布局修改。",
        run: undoLayout,
      },
      {
        id: "command:redo",
        kind: "command" as const,
        label: "重做布局变更",
        description: "重做当前 Workspace 的上一次撤销。",
        run: redoLayout,
      },
      {
        id: "command:export",
        kind: "command" as const,
        label: "导出当前布局",
        description: "导出当前 Workspace Recipe JSON。",
        run: exportCurrentWorkspace,
      },
      {
        id: "command:import",
        kind: "command" as const,
        label: "导入布局",
        description: "选择并预览 Workspace Recipe JSON。",
        run: () => recipeFileInput.current?.click(),
      },
      {
        id: "command:migrate",
        kind: "command" as const,
        label: "复制布局到其他 Workspace",
        description: "预览并复制当前布局和 Session 引用。",
        run: openMigration,
      },
      {
        id: "command:restore",
        kind: "command" as const,
        label: "恢复场景模板",
        description: "用当前场景的推荐面板恢复布局。",
        run: resetLayout,
      },
      {
        id: "command:modules",
        kind: "command" as const,
        label: "打开面板目录",
        description: "只浏览和添加 Workspace 面板。",
        run: openModuleDrawer,
      },
    ];
    const modules = moduleDefinitions.map((module) => ({
      id: `module:${module.id}`,
      kind: "module" as const,
      label: `添加面板：${module.title}`,
      description: module.description,
      run: () => addModule(module.id),
    }));
    return [...commands, ...modules];
  }, [
    addModule,
    exportCurrentWorkspace,
    openMigration,
    openModuleDrawer,
    redoLayout,
    refresh,
    resetLayout,
    undoLayout,
  ]);

  const matchingEntries = commandEntries.filter((entry) => {
    const query = commandQuery.trim().toLocaleLowerCase();
    if (!query) return true;
    return `${entry.label} ${entry.description}`.toLocaleLowerCase().includes(query);
  });
  const matchingCommands = matchingEntries.filter((entry) => entry.kind === "command");
  const matchingModules = matchingEntries.filter((entry) => entry.kind === "module");
  const firstMatchingEntry = matchingEntries[0];

  const renderPanelBody = useCallback((panel: PanelLayout) => {
    if (!data) {
      return <div className="canvas-empty">等待 Harness Runtime 数据…</div>;
    }
    const resolvedContext = resolvePanelContext(panel, panel.moduleId);
    switch (panel.moduleId) {
      case "sessions":
        return (
          <SessionsPanel
            data={data}
            selectedSessionId={selectedSessionId}
            onSelectSession={selectSession}
            stale={stale}
          />
        );
      case "agent":
        return (
          <AgentPanel
            data={data}
            session={resolvedContext?.session}
            stale={stale}
            approvalControl={approvalControl && !stale}
            onChanged={refresh}
            onSelectSession={selectSession}
            onOpenTerminal={openTerminalForSession}
          />
        );
      case "approvals":
        return (
          <ApprovalsPanel
            data={data}
            session={resolvedContext?.session}
            sessionId={resolvedContext?.effectiveSessionId}
            stale={stale}
            canDecide={approvalControl && !stale}
            onChanged={refresh}
          />
        );
      case "terminal":
        return (
          <TerminalPanel
            session={resolvedContext?.session}
            sessionId={resolvedContext?.effectiveSessionId}
            stale={stale}
          />
        );
      case "mcp":
        return <MCPPanel data={data} />;
      case "activity":
        return <ActivityPanel data={data} session={resolvedContext?.session} stale={stale} />;
      case "diagnostics":
        return <DiagnosticsPanel data={data} stale={stale} />;
      default:
        return null;
    }
  }, [
    approvalControl,
    data,
    refresh,
    resolvePanelContext,
    openTerminalForSession,
    selectSession,
    stale,
  ]);

  const renderContextBar = useCallback((panel: PanelLayout, moduleId: ModuleId) => {
    if (!isContextualModule(moduleId)) return null;
    const resolved = resolvePanelContext(panel, moduleId);
    if (!resolved) return null;
    const { binding, effectiveSessionId, session, state } = resolved;
    const statusClass = state === "ready" && !stale
      ? "ready"
      : state === "unbound"
        ? "unbound"
        : "unknown";
    const statusText = state === "unbound"
      ? messages.context.unbound
      : state === "loading"
        ? messages.app.connecting
        : state === "missing"
          ? messages.context.missing
          : stale
            ? messages.context.stale
            : session
              ? agentStateLabel(session.state)
              : messages.common.unknown;
    return (
      <div className={`canvas-context-bar ${statusClass}`}>
        <span className={`canvas-context-mode ${binding.mode}`}>
          {binding.mode === "follow" ? messages.context.follow : messages.context.pinned}
        </span>
        <span className="canvas-context-id" title={effectiveSessionId}>
          {messages.context.session} {effectiveSessionId ? shortID(effectiveSessionId) : messages.common.none}
        </span>
        <span className={`canvas-context-state ${statusClass}`}>{statusText}</span>
        <div className="canvas-context-actions">
          {binding.mode === "follow" ? (
            <button
              type="button"
              disabled={!selectedSessionId}
              title={selectedSessionId ? messages.context.lock : messages.context.noSession}
              onClick={() => {
                if (!selectedSessionId) return;
                changePanelContext(panel.id, moduleId, {
                  mode: "pinned",
                  sessionId: selectedSessionId,
                });
              }}
            >
              {messages.context.lock}
            </button>
          ) : (
            <button
              type="button"
              onClick={() => changePanelContext(panel.id, moduleId, { mode: "follow" })}
            >
              {messages.context.followCurrent}
            </button>
          )}
        </div>
      </div>
    );
  }, [changePanelContext, resolvePanelContext, selectedSessionId, stale]);

  const runtimeState = error ? "unknown" : data?.runtime.state ?? "new";
  const runtimeLabel = runtimeState === "unknown" ? messages.state.unknown : stateCopy[runtimeState];
  const coreState = data?.core.core?.state ?? "unavailable";
  const coreLabel = data?.core.core
    ? coreStateLabel(data.core.core.state)
    : messages.core.unconfigured;
  const mcpStatus = mcpStatusSummary(data?.mcp);

  return (
    <div className="canvas-shell">
      <header className="canvas-topbar drag-region">
        <div className="canvas-brand">
          <span className="canvas-brand-mark" aria-hidden="true">H</span>
          <div>
            <strong>Termcp Harness</strong>
            <span>Agent Runtime Workspace</span>
          </div>
        </div>

        <SpaceSwitcher
          spaces={workspaceCatalog.spaces}
          activeSpaceId={workspace}
          panelCounts={workspacePanelCounts}
          onSelect={setWorkspace}
          onCreate={createWorkspace}
          onRename={renameWorkspace}
          onDelete={deleteWorkspace}
        />

        <div className="canvas-top-actions no-drag">
          <button type="button" className="canvas-primary-action" onClick={openModuleDrawer}>+ 添加面板</button>
          <button
            type="button"
            className="canvas-command-button"
            onClick={openCommandPalette}
          >
            搜索或运行命令
            <kbd>Ctrl K</kbd>
          </button>
        </div>

        <div className={`canvas-status-cluster state-${runtimeState}`} aria-live="polite">
          <span><i className="status-dot" />Runtime {runtimeLabel}</span>
          <span><i className={`status-dot ${coreState === "identified" ? "ready" : "warning"}`} />Core {coreLabel}</span>
          <span><i className={`status-dot ${mcpStatus.tone}`} />{mcpStatus.label}</span>
        </div>

        <WindowControls />
      </header>

      <main className="canvas-main">
        <div className="canvas-toolbar">
          <div>
            <strong>{workspaceName}</strong>
            <span>{panels.length} 个面板 · {lastChecked ? `更新于 ${lastChecked.toLocaleTimeString(messages.locale, { hour12: false })}` : "等待连接"}</span>
            <span className={`canvas-task-summary ${activeTaskCount ? "has-active" : ""}`}>
              <i className="status-dot" />
              {activeTaskCount
                ? agentPanelVisible
                  ? `${activeTaskCount} 个任务仍在运行或等待`
                  : `Agent 面板已关闭 · ${activeTaskCount} 个任务仍由 Runtime 管理`
                : "没有运行中或等待中的任务"}
            </span>
          </div>
          <div className="canvas-toolbar-actions">
            <label className="canvas-session-picker">
              <span>当前 Session</span>
              <select
                value={selectedSessionId ?? ""}
                onChange={(event) => selectSession(event.target.value)}
                disabled={!data?.agent_sessions.sessions.length}
              >
                <option value="" disabled>
                  {data ? "未选择 Session" : "等待 Session 快照"}
                </option>
                {selectedSessionMissing ? (
                  <option value={selectedSessionId}>
                    关联未知 · {shortID(selectedSessionId)}
                  </option>
                ) : null}
                {data?.agent_sessions.sessions.map((session) => (
                  <option key={session.id} value={session.id}>
                    {shortID(session.id)} · {agentStateLabel(session.state)}
                  </option>
                ))}
              </select>
            </label>
            <button
              type="button"
              disabled={!canUndoWorkspace(workspaceHistory, workspace)}
              title="撤销布局变更（Ctrl+Z）"
              onClick={undoLayout}
            >
              撤销
            </button>
            <button
              type="button"
              disabled={!canRedoWorkspace(workspaceHistory, workspace)}
              title="重做布局变更（Ctrl+Shift+Z）"
              onClick={redoLayout}
            >
              重做
            </button>
            <button type="button" onClick={() => void refresh()} disabled={loading}>{loading ? "刷新中…" : "刷新"}</button>
            <button type="button" onClick={openLayoutSettings}>布局设置…</button>
            <input
              ref={recipeFileInput}
              className="recipe-file-input"
              type="file"
              accept="application/json,.json"
              aria-label="选择 Workspace Recipe JSON 文件"
              onChange={(event) => void selectRecipeFile(event)}
            />
          </div>
        </div>

        {error ? (
          <div className="canvas-error-banner" role="alert">
            <div>
              <strong>{messages.app.unreachable}</strong>
              <p>{stale ? "当前显示最后一次成功读取的快照。" : messages.app.unreachableBody}</p>
              <details>
                <summary>{messages.app.technicalDetails}</summary>
                <code>{error}</code>
              </details>
            </div>
            <button type="button" onClick={() => void refresh()}>{messages.app.tryAgain}</button>
          </div>
        ) : null}

        {data?.recovery?.state === "partial" || data?.recovery?.state === "unverified" ? (
          <div
            className={`canvas-recovery-problem ${data.recovery.state}`}
            role={data.recovery.state === "partial" ? "alert" : "status"}
          >
            <strong>{recoveryStateLabel(data.recovery.state)}</strong>
            <span>
              {data.recovery.warning
                ?? (data.recovery.state === "partial"
                  ? "部分 Store 与恢复清单不一致，请先在诊断面板核对。"
                  : "各 Store 可独立读取，但尚未证明属于同一恢复代次。")}
            </span>
          </div>
        ) : null}

        {lifecycleNotice ? (
          <div className="canvas-lifecycle-notice" role="status">
            <span>{lifecycleNotice}</span>
            <button type="button" aria-label="关闭提示" onClick={() => setLifecycleNotice(null)}>×</button>
          </div>
        ) : null}

        {recipeProblem ? (
          <div className="canvas-recipe-problem" role="alert">
            <span>{recipeProblem}</span>
            <button type="button" aria-label="关闭导入错误" onClick={() => setRecipeProblem(null)}>×</button>
          </div>
        ) : null}

        {persistenceState === "failed" ? (
          <div className="canvas-persistence-problem" role="alert">
            <strong>本地保存失败</strong>
            <span>当前修改仍可使用，但关闭 Desktop 后可能丢失。请检查本机存储权限或可用空间。</span>
          </div>
        ) : null}

        {panels.length ? (
          <CanvasGrid
            panels={panels}
            onChange={updatePanels}
            onClose={closePanel}
            onMaximize={maximizePanel}
            renderPanel={renderPanelBody}
            renderContextBar={renderContextBar}
          />
        ) : (
          <section className="canvas-empty-board">
            <strong>当前 Workspace 没有面板</strong>
            <p>从面板目录添加视图，或恢复当前场景模板。</p>
            <div>
              <button type="button" onClick={openModuleDrawer}>添加面板</button>
              <button type="button" onClick={resetLayout}>恢复模板</button>
            </div>
          </section>
        )}
      </main>

      <footer className="canvas-statusbar">
        <span>
          {persistenceState === "checking"
            ? "正在检查本机保存…"
            : persistenceState === "failed"
              ? "本地保存失败 · 修改可能仅保留在本次会话"
              : "布局已保存在本机"}
          {" · "}面板关闭不会停止任务
          {activeTaskCount ? ` · ${activeTaskCount} 个任务仍由 Runtime 管理` : ""}
        </span>
        <span title={eventStreamError ?? undefined}>
          Events {eventStreamCopy[eventStreamState]} · {stale ? "最后快照" : error ? "状态未知" : "Harness Runtime 权威状态"}
        </span>
      </footer>

      {commandPalette ? (
        <div
          className="module-drawer-backdrop command-palette-backdrop"
          role="presentation"
          onMouseDown={() => setCommandPalette(false)}
        >
          <aside
            className="module-drawer command-palette no-drag"
            role="dialog"
            aria-modal="true"
            aria-label="搜索或运行命令"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <header>
              <div>
                <span>Command Palette</span>
                <h2>搜索或运行命令</h2>
              </div>
              <button type="button" aria-label="关闭命令面板" onClick={() => setCommandPalette(false)}>×</button>
            </header>
            <input
              autoFocus
              value={commandQuery}
              onChange={(event) => setCommandQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Escape") setCommandPalette(false);
                if (event.key === "Enter" && firstMatchingEntry) {
                  event.preventDefault();
                  setCommandPalette(false);
                  firstMatchingEntry.run();
                }
              }}
              placeholder="搜索命令或面板…"
              aria-label="搜索命令或面板"
            />
            <p className="command-palette-hint">这里搜索并执行操作；添加面板只浏览面板目录。</p>
            <div className="command-palette-results">
              {matchingEntries.length ? (
                <>
                  <section>
                    <h3>UI 命令</h3>
                    {matchingCommands.length ? matchingCommands.map((command) => (
                      <button
                        type="button"
                        key={command.id}
                        onClick={() => {
                          setCommandPalette(false);
                          command.run();
                        }}
                      >
                        <strong>{command.label}</strong>
                        <small>{command.description}</small>
                      </button>
                    )) : <p className="canvas-empty">没有匹配命令</p>}
                  </section>
                  <section>
                    <h3>面板</h3>
                    {matchingModules.length ? matchingModules.map((module) => (
                      <button
                        type="button"
                        key={module.id}
                        onClick={() => {
                          setCommandPalette(false);
                          module.run();
                        }}
                      >
                        <strong>{module.label}</strong>
                        <small>{module.description}</small>
                      </button>
                    )) : <p className="canvas-empty">没有匹配面板</p>}
                  </section>
                </>
              ) : <p className="canvas-empty">没有匹配的命令或面板。</p>}
            </div>
          </aside>
        </div>
      ) : null}

      {layoutSettings ? (
        <div
          className="module-drawer-backdrop"
          role="presentation"
          onMouseDown={() => setLayoutSettings(false)}
        >
          <aside
            className="module-drawer layout-settings no-drag"
            role="dialog"
            aria-modal="true"
            aria-labelledby="layout-settings-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <header>
              <div>
                <span>Workspace Settings</span>
                <h2 id="layout-settings-title">布局设置</h2>
              </div>
              <button type="button" aria-label="关闭布局设置" onClick={() => setLayoutSettings(false)}>×</button>
            </header>
            <section>
              <h3>布局精度</h3>
              <p>
                当前使用 {CANVAS_COLUMNS} × {CANVAS_ROWS} 网格、自动吸附和 {CANVAS_GAP} px 面板间距。
                面板通过拖动和边缘拉伸调整；布局会自动保存。
              </p>
            </section>
            <section>
              <h3>导入、导出与复制</h3>
              <p>这些操作只处理 Workspace 布局和 Session 引用，不会启动、停止或重绑任务。</p>
              <div className="layout-settings-actions">
                <button type="button" onClick={exportCurrentWorkspace} disabled={!panels.length}>导出当前</button>
                <button type="button" onClick={() => recipeFileInput.current?.click()}>导入布局</button>
                <button type="button" onClick={openMigration} disabled={!panels.length || !migrationTargets.length}>复制到…</button>
              </div>
            </section>
            <section>
              <h3>恢复</h3>
              <p>恢复当前场景模板会替换这个 Workspace 的布局，可通过撤销恢复。</p>
              <button type="button" onClick={resetLayout}>恢复模板</button>
            </section>
          </aside>
        </div>
      ) : null}

      {moduleDrawer ? (
        <div className="module-drawer-backdrop" role="presentation" onMouseDown={() => setModuleDrawer(false)}>
          <aside className="module-drawer no-drag" aria-label="面板目录" onMouseDown={(event) => event.stopPropagation()}>
            <header>
              <div>
                <span>Workspace Panels</span>
                <h2>添加面板</h2>
              </div>
              <button type="button" aria-label="关闭面板目录" onClick={() => setModuleDrawer(false)}>×</button>
            </header>
            <input
              autoFocus
              value={moduleQuery}
              onChange={(event) => setModuleQuery(event.target.value)}
              placeholder="搜索面板…"
              aria-label="搜索面板"
            />
            {drawerMessage ? <p className="canvas-warning">{drawerMessage}</p> : null}
            <div className="module-catalog">
              {availableModules.map((module) => {
                const opened = panels.some((panel) => panelContains(panel, module.id));
                return (
                  <article key={module.id} className={opened ? "is-open" : ""}>
                    <div>
                      <strong>{module.title}</strong>
                      <p>{module.description}</p>
                      <span>最小尺寸 {module.minW} × {module.minH}</span>
                    </div>
                    <button type="button" disabled={opened} onClick={() => addModule(module.id)}>
                      {opened ? "已添加" : "添加"}
                    </button>
                  </article>
                );
              })}
            </div>
          </aside>
        </div>
      ) : null}

      {recipeReview ? (
        <div
          className="module-drawer-backdrop recipe-review-backdrop"
          role="presentation"
          onMouseDown={() => setRecipeReview(null)}
        >
          <aside
            className="module-drawer recipe-review no-drag"
            role="dialog"
            aria-modal="true"
            aria-labelledby="recipe-review-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <header>
              <div>
                <span>Workspace Recipe v1</span>
                <h2 id="recipe-review-title">导入布局</h2>
              </div>
              <button type="button" aria-label="关闭导入预览" onClick={() => setRecipeReview(null)}>×</button>
            </header>
            <div className="recipe-review-summary">
              <p>
                来源“{workspaceNameFor(workspaceCatalog.spaces, recipeReview.recipe.workspace)}”，包含
                <strong> {recipeReview.recipe.layout.length} </strong>
                个面板。确认后只替换“{workspaceName}”当前布局。
              </p>
              <p className="canvas-note">
                导入不会启动、停止或重新绑定任何任务、终端或审批；不存在的 Session 引用会显示为关联未知。
              </p>
              {recipeReview.warnings.length ? (
                <section className="recipe-review-warnings" aria-label="导入警告">
                  <strong>已执行的修复</strong>
                  <ul>
                    {recipeReview.warnings.map((warning, index) => (
                      <li key={`${index}-${warning}`}>{warning}</li>
                    ))}
                  </ul>
                </section>
              ) : (
                <p className="recipe-review-clean">校验通过，没有需要修复的字段。</p>
              )}
            </div>
            <footer className="recipe-review-actions">
              <button type="button" autoFocus onClick={() => setRecipeReview(null)}>取消</button>
              <button type="button" className="recipe-review-confirm" onClick={applyRecipe}>
                替换当前布局
              </button>
            </footer>
          </aside>
        </div>
      ) : null}

      {migrationTarget && migrationPreview ? (
        <div
          className="module-drawer-backdrop recipe-review-backdrop"
          role="presentation"
          onMouseDown={() => setMigrationTarget(null)}
        >
          <aside
            className="module-drawer recipe-review no-drag"
            role="dialog"
            aria-modal="true"
            aria-labelledby="workspace-migration-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <header>
              <div>
                <span>Workspace Migration</span>
                <h2 id="workspace-migration-title">复制布局到其他 Workspace</h2>
              </div>
              <button type="button" aria-label="关闭迁移预览" onClick={() => setMigrationTarget(null)}>×</button>
            </header>
            <div className="recipe-review-summary">
              <label className="workspace-migration-target">
                <span>目标 Workspace</span>
                <select
                  value={migrationTarget}
                  onChange={(event) => setMigrationTarget(event.target.value as WorkspaceId)}
                >
                  {migrationTargets.map((target) => (
                    <option key={target.id} value={target.id}>{target.name}</option>
                  ))}
                </select>
              </label>
              <p>{migrationDescription(
                migrationPreview,
                workspaceNameFor(workspaceCatalog.spaces, migrationPreview.target),
              )}</p>
              <p className="canvas-note">
                只复制布局和 Session 引用。不会迁移 Agent、终端、工具调用或审批事实，也不会启动或停止任何任务。
              </p>
              {migrationPreview.replacedPanelCount ? (
                <p className="recipe-review-warnings">
                  目标布局将被替换；复制后可在目标 Workspace 使用“撤销”恢复其原布局。
                </p>
              ) : null}
              {migrationPreview.targetHadSelectedSession ? (
                <p>目标 Workspace 原来的 Session 选择会被来源 Workspace 的选择替换。</p>
              ) : null}
            </div>
            <footer className="recipe-review-actions">
              <button type="button" autoFocus onClick={() => setMigrationTarget(null)}>取消</button>
              <button type="button" className="recipe-review-confirm" onClick={applyMigration}>
                复制并切换
              </button>
            </footer>
          </aside>
        </div>
      ) : null}
    </div>
  );
}
