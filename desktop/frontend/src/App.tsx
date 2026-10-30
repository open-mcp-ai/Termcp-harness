import { useEffect, useMemo, useRef, useState } from "react";
import {
  approvalControlAvailable,
  desktopAvailable,
  loadBootstrap,
  windowAction,
} from "./api";
import CanvasGrid from "./CanvasGrid";
import {
  defaultWorkspaceLayout,
  findFreeSlot,
  isContextualModule,
  moduleDefinitions,
  normalizeLayout,
  panelContains,
  panelContext,
  panelTabs,
  readStoredCanvasState,
  removeModuleContext,
  setModuleContext,
  type ModuleId,
  type PanelContextBinding,
  type PanelLayout,
  type StoredCanvasState,
  type WorkspaceId,
  workspaceLabels,
  writeStoredCanvasState,
} from "./canvas";
import { agentStateLabel, messages } from "./i18n";
import {
  ActivityPanel,
  AgentPanel,
  ApprovalsPanel,
  DiagnosticsPanel,
  MCPPanel,
  SessionsPanel,
  TerminalPanel,
} from "./Panels";
import type { AgentSessionSnapshot, Bootstrap, RuntimeState } from "./types";

const stateCopy: Record<RuntimeState, string> = messages.state;
const WORKSPACE_STORAGE_KEY = "termcp.canvas.workspace";

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

function initialWorkspace(): WorkspaceId {
  const stored = localStorage.getItem(WORKSPACE_STORAGE_KEY);
  return stored && stored in workspaceLabels ? stored as WorkspaceId : "security";
}

function preferredSession(sessions: AgentSessionSnapshot[]): AgentSessionSnapshot | undefined {
  return sessions.find((session) => session.state === "waiting_approval") ??
    sessions.find((session) => session.state === "waiting_execution") ??
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
  const [workspace, setWorkspace] = useState<WorkspaceId>(initialWorkspace);
  const [canvasState, setCanvasState] = useState<StoredCanvasState>(readStoredCanvasState);
  const [moduleDrawer, setModuleDrawer] = useState(false);
  const [moduleQuery, setModuleQuery] = useState("");
  const [drawerMessage, setDrawerMessage] = useState<string | null>(null);
  const [lifecycleNotice, setLifecycleNotice] = useState<string | null>(null);
  const inFlight = useRef(false);

  const refresh = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setLoading(true);
    try {
      const next = await loadBootstrap();
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
  };

  useEffect(() => {
    void refresh();
    const interval = window.setInterval(() => void refresh(), 2500);
    return () => window.clearInterval(interval);
  }, []);

  useEffect(() => {
    if (!desktopAvailable()) return;
    void approvalControlAvailable().then(setApprovalControl).catch(() => setApprovalControl(false));
  }, []);

  useEffect(() => {
    localStorage.setItem(WORKSPACE_STORAGE_KEY, workspace);
  }, [workspace]);

  useEffect(() => {
    writeStoredCanvasState(canvasState);
  }, [canvasState]);

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
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const stale = Boolean(error && data);
  const panels = canvasState.layouts[workspace];
  const selectedSessionId = canvasState.selectedSessionByWorkspace[workspace];
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

  const updatePanels = (next: PanelLayout[]) => {
    setCanvasState((current) => ({
      ...current,
      layouts: {
        ...current.layouts,
        [workspace]: normalizeLayout(next),
      },
    }));
  };

  const selectSession = (sessionId: string) => {
    setCanvasState((current) => ({
      ...current,
      selectedSessionByWorkspace: {
        ...current.selectedSessionByWorkspace,
        [workspace]: sessionId,
      },
    }));
  };

  const changePanelContext = (
    panelId: string,
    moduleId: ModuleId,
    binding: PanelContextBinding,
  ) => {
    if (binding.mode === "pinned" && !binding.sessionId) return;
    updatePanels(panels.map((panel) =>
      panel.id === panelId ? setModuleContext(panel, moduleId, binding) : panel,
    ));
  };

  const resolvePanelContext = (panel: PanelLayout, moduleId: ModuleId) => {
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
  };

  const closePanel = (panelId: string, moduleId: ModuleId) => {
    const panel = panels.find((candidate) => candidate.id === panelId);
    if (!panel) return;
    const tabs = panelTabs(panel).filter((candidate) => candidate !== moduleId);
    setLifecycleNotice(
      moduleId === "approvals"
        ? "已关闭审批视图；审批请求不会被拒绝、取消或停止。"
        : "已关闭面板视图；不会停止关联任务或卸载模块。",
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
  };

  const maximizePanel = (panelId: string) => {
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
  };

  const addModule = (moduleId: ModuleId) => {
    if (panels.some((panel) => panelContains(panel, moduleId))) {
      setDrawerMessage("该模块已经在当前工作区中。");
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
  };

  const resetLayout = () => {
    const next = defaultWorkspaceLayout(workspace);
    updatePanels(next);
  };

  const renderPanelBody = (panel: PanelLayout) => {
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
        return <AgentPanel data={data} session={resolvedContext?.session} stale={stale} />;
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
        return <TerminalPanel session={resolvedContext?.session} stale={stale} />;
      case "mcp":
        return <MCPPanel data={data} />;
      case "activity":
        return <ActivityPanel data={data} session={resolvedContext?.session} stale={stale} />;
      case "diagnostics":
        return <DiagnosticsPanel data={data} stale={stale} />;
      default:
        return null;
    }
  };

  const renderContextBar = (panel: PanelLayout, moduleId: ModuleId) => {
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
  };

  const runtimeState = error ? "unknown" : data?.runtime.state ?? "new";
  const runtimeLabel = runtimeState === "unknown" ? messages.state.unknown : stateCopy[runtimeState];
  const coreState = data?.core.core?.state ?? "unavailable";
  const coreLabel = data?.core.core ? data.core.core.state : messages.core.unconfigured;
  const mcpText = data ? `MCP Servers ${data.mcp.ready}/${data.mcp.total}` : "MCP Servers —";

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

        <label className="workspace-switcher no-drag">
          <span>Workspace</span>
          <select value={workspace} onChange={(event) => setWorkspace(event.target.value as WorkspaceId)}>
            {(Object.keys(workspaceLabels) as WorkspaceId[]).map((id) => (
              <option key={id} value={id}>{workspaceLabels[id]}</option>
            ))}
          </select>
        </label>

        <div className="canvas-top-actions no-drag">
          <button type="button" className="canvas-primary-action" onClick={() => setModuleDrawer(true)}>+ 添加模块</button>
          <button type="button" className="canvas-command-button" onClick={() => setModuleDrawer(true)}>
            搜索模块、命令
            <kbd>Ctrl K</kbd>
          </button>
        </div>

        <div className={`canvas-status-cluster state-${runtimeState}`} aria-live="polite">
          <span><i className="status-dot" />Runtime {runtimeLabel}</span>
          <span><i className={`status-dot ${coreState === "identified" ? "ready" : "warning"}`} />Core {coreLabel}</span>
          <span><i className={`status-dot ${data && data.mcp.ready === data.mcp.total ? "ready" : "warning"}`} />{mcpText}</span>
        </div>

        <WindowControls />
      </header>

      <main className="canvas-main">
        <div className="canvas-toolbar">
          <div>
            <strong>{workspaceLabels[workspace]}</strong>
            <span>{panels.length} 个模块 · {lastChecked ? `更新于 ${lastChecked.toLocaleTimeString(messages.locale, { hour12: false })}` : "等待连接"}</span>
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
            <button type="button" onClick={() => void refresh()} disabled={loading}>{loading ? "刷新中…" : "刷新"}</button>
            <button type="button" onClick={resetLayout}>恢复模板</button>
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

        {lifecycleNotice ? (
          <div className="canvas-lifecycle-notice" role="status">
            <span>{lifecycleNotice}</span>
            <button type="button" aria-label="关闭提示" onClick={() => setLifecycleNotice(null)}>×</button>
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
            <strong>当前 Workspace 没有模块</strong>
            <p>从模块目录添加面板，或恢复当前场景模板。</p>
            <div>
              <button type="button" onClick={() => setModuleDrawer(true)}>添加模块</button>
              <button type="button" onClick={resetLayout}>恢复模板</button>
            </div>
          </section>
        )}
      </main>

      <footer className="canvas-statusbar">
        <span>
          布局自动保存在本机 · 面板关闭不会停止任务
          {activeTaskCount ? ` · ${activeTaskCount} 个任务仍由 Runtime 管理` : ""}
        </span>
        <span>{stale ? "最后快照" : error ? "状态未知" : "Harness Runtime 权威状态"}</span>
      </footer>

      {moduleDrawer ? (
        <div className="module-drawer-backdrop" role="presentation" onMouseDown={() => setModuleDrawer(false)}>
          <aside className="module-drawer no-drag" aria-label="模块目录" onMouseDown={(event) => event.stopPropagation()}>
            <header>
              <div>
                <span>Workspace Modules</span>
                <h2>添加模块</h2>
              </div>
              <button type="button" aria-label="关闭模块目录" onClick={() => setModuleDrawer(false)}>×</button>
            </header>
            <input
              autoFocus
              value={moduleQuery}
              onChange={(event) => setModuleQuery(event.target.value)}
              placeholder="搜索模块…"
              aria-label="搜索模块"
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
    </div>
  );
}
