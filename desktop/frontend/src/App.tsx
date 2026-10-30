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
  moduleDefinitions,
  readStoredLayouts,
  type ModuleId,
  type PanelLayout,
  type WorkspaceId,
  workspaceLabels,
  writeStoredLayout,
} from "./canvas";
import { messages } from "./i18n";
import {
  ActivityPanel,
  AgentPanel,
  ApprovalsPanel,
  DiagnosticsPanel,
  MCPPanel,
  SessionsPanel,
  TerminalPanel,
} from "./Panels";
import type { Bootstrap, RuntimeState } from "./types";

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

function initialLayouts(): Record<WorkspaceId, PanelLayout[]> {
  const stored = readStoredLayouts();
  return (Object.keys(workspaceLabels) as WorkspaceId[]).reduce(
    (result, workspace) => {
      result[workspace] = stored[workspace] ?? defaultWorkspaceLayout(workspace);
      return result;
    },
    {} as Record<WorkspaceId, PanelLayout[]>,
  );
}

export default function App() {
  const [data, setData] = useState<Bootstrap | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [lastChecked, setLastChecked] = useState<Date | null>(null);
  const [approvalControl, setApprovalControl] = useState(false);
  const [workspace, setWorkspace] = useState<WorkspaceId>(initialWorkspace);
  const [layouts, setLayouts] = useState<Record<WorkspaceId, PanelLayout[]>>(initialLayouts);
  const [moduleDrawer, setModuleDrawer] = useState(false);
  const [moduleQuery, setModuleQuery] = useState("");
  const [drawerMessage, setDrawerMessage] = useState<string | null>(null);
  const inFlight = useRef(false);

  const refresh = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setLoading(true);
    try {
      const next = await loadBootstrap();
      setData((current) => {
        if (current && next.events.latest_sequence < current.events.latest_sequence) return current;
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
  const panels = layouts[workspace];
  const availableModules = useMemo(() => {
    const query = moduleQuery.trim().toLocaleLowerCase();
    return moduleDefinitions.filter((definition) =>
      !query || `${definition.title} ${definition.description}`.toLocaleLowerCase().includes(query),
    );
  }, [moduleQuery]);

  const updatePanels = (next: PanelLayout[]) => {
    setLayouts((current) => ({ ...current, [workspace]: next }));
    writeStoredLayout(workspace, next);
  };

  const closePanel = (moduleId: ModuleId) => {
    updatePanels(panels.filter((panel) => panel.moduleId !== moduleId));
  };

  const maximizePanel = (moduleId: ModuleId) => {
    updatePanels(panels.map((panel) => {
      if (panel.moduleId !== moduleId) {
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
    if (panels.some((panel) => panel.moduleId === moduleId)) {
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
    switch (panel.moduleId) {
      case "sessions":
        return <SessionsPanel data={data} />;
      case "agent":
        return <AgentPanel data={data} />;
      case "approvals":
        return <ApprovalsPanel data={data} canDecide={approvalControl && !stale} onChanged={refresh} />;
      case "terminal":
        return <TerminalPanel data={data} />;
      case "mcp":
        return <MCPPanel data={data} />;
      case "activity":
        return <ActivityPanel data={data} />;
      case "diagnostics":
        return <DiagnosticsPanel data={data} stale={stale} />;
      default:
        return null;
    }
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
          </div>
          <div className="canvas-toolbar-actions">
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

        {panels.length ? (
          <CanvasGrid
            panels={panels}
            onChange={updatePanels}
            onClose={closePanel}
            onMaximize={maximizePanel}
            renderPanel={renderPanelBody}
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
        <span>布局自动保存在本机 · 面板关闭不会停止任务</span>
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
                const opened = panels.some((panel) => panel.moduleId === module.id);
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
