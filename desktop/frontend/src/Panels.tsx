import { useMemo, useState } from "react";
import { decideApproval } from "./api";
import { agentStateLabel, coreOwnershipLabel, coreStateLabel, executionStateLabel, mcpStateLabel, messages } from "./i18n";
import type { AgentSessionSnapshot, Bootstrap } from "./types";

function shortID(value?: string): string {
  if (!value) return messages.common.unknown;
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function formatTime(value?: string): string {
  if (!value) return messages.common.notRecorded;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return messages.common.unknown;
  return new Intl.DateTimeFormat(messages.locale, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date);
}

function preferredSession(sessions: AgentSessionSnapshot[]): AgentSessionSnapshot | undefined {
  return sessions.find((session) => session.state === "waiting_approval") ??
    sessions.find((session) => session.state === "waiting_execution") ??
    sessions.find((session) => session.state === "running") ??
    sessions[0];
}

function stateClass(state: string): string {
  if (state === "completed" || state === "succeeded" || state === "running") return "ready";
  if (state === "failed" || state === "blocked" || state === "expired") return "danger";
  if (state === "waiting_approval" || state === "waiting_execution" || state === "submitted") return "warning";
  return "unknown";
}

export function SessionsPanel({ data }: { data: Bootstrap }) {
  const sessions = data.agent_sessions.sessions;
  const counts = {
    running: sessions.filter((session) => session.state === "running").length,
    waiting: sessions.filter((session) => session.state === "waiting_approval" || session.state === "waiting_execution").length,
    failed: sessions.filter((session) => ["failed", "blocked", "expired", "unknown"].includes(session.state)).length,
  };
  return (
    <div className="canvas-module sessions-module">
      <div className="canvas-summary-row">
        <span><i className="status-dot ready" />运行中 <strong>{counts.running}</strong></span>
        <span><i className="status-dot warning" />待处理 <strong>{counts.waiting}</strong></span>
        <span><i className="status-dot danger" />异常 <strong>{counts.failed}</strong></span>
      </div>
      {sessions.length === 0 ? (
        <p className="canvas-empty">{messages.agent.emptyBody}</p>
      ) : (
        <ul className="canvas-list session-list">
          {sessions.map((session) => (
            <li key={session.id}>
              <div className="canvas-list-row">
                <strong title={session.id}>{shortID(session.id)}</strong>
                <span className={`state-badge ${stateClass(session.state)}`}>{agentStateLabel(session.state)}</span>
              </div>
              <div className="canvas-meta">
                <span>{session.current_call_id ? `call ${shortID(session.current_call_id)}` : messages.common.none}</span>
                <span>{session.current_execution ? executionStateLabel(session.current_execution.state) : messages.common.notRecorded}</span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function AgentPanel({ data }: { data: Bootstrap }) {
  const session = preferredSession(data.agent_sessions.sessions);
  const relatedEvents = data.events.events.filter((event) =>
    session && (event.subject_id === session.id || event.correlation_id === session.current_call_id),
  ).slice(-8);
  return (
    <div className="canvas-module agent-module">
      {!session ? (
        <p className="canvas-empty">{messages.agent.emptyBody}</p>
      ) : (
        <>
          <div className="agent-session-summary">
            <div>
              <span className={`state-badge ${stateClass(session.state)}`}>{agentStateLabel(session.state)}</span>
              <h3>{shortID(session.id)}</h3>
            </div>
            <span className="canvas-mono">{shortID(session.core_session_id)}</span>
          </div>
          <dl className="canvas-facts">
            <div><dt>Call</dt><dd>{shortID(session.current_call_id)}</dd></div>
            <div><dt>Approval</dt><dd>{session.current_approval_status ?? messages.common.none}</dd></div>
            <div><dt>Tool</dt><dd>{session.current_tool_status ?? messages.common.none}</dd></div>
            <div><dt>Completion</dt><dd>{session.current_completion ?? messages.common.notRecorded}</dd></div>
            <div><dt>Execution</dt><dd>{session.current_execution ? executionStateLabel(session.current_execution.state) : messages.common.none}</dd></div>
            <div><dt>Updated</dt><dd>{formatTime(session.finished_at ?? session.started_at)}</dd></div>
          </dl>
          <div className="canvas-section-label">最近轨迹</div>
          <ol className="mini-timeline">
            {relatedEvents.length ? relatedEvents.map((event) => (
              <li key={event.event_id}>
                <span>{formatTime(event.occurred_at)}</span>
                <strong>{event.type}</strong>
                <small>{event.class}</small>
              </li>
            )) : <li className="canvas-empty">当前 Session 暂无关联事件。</li>}
          </ol>
        </>
      )}
    </div>
  );
}

export function ApprovalsPanel({
  data,
  canDecide,
  onChanged,
}: {
  data: Bootstrap;
  canDecide: boolean;
  onChanged: () => Promise<void>;
}) {
  const approvals = data.approvals.approvals;
  const [deciding, setDeciding] = useState<string | null>(null);
  const [error, setError] = useState<{ message: string; detail?: string } | null>(null);
  const submit = async (id: string, revision: number, decision: "approve" | "reject") => {
    setDeciding(id);
    setError(null);
    try {
      const result = await decideApproval(id, revision, decision);
      if (result.outcome === "unknown") {
        setError({ message: messages.agent.decisionUnknown });
      } else if (result.warning) {
        setError({ message: messages.agent.decisionWarning, detail: result.warning });
      }
      await onChanged();
    } catch (reason) {
      setError({
        message: messages.agent.decisionFailed,
        detail: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setDeciding(null);
    }
  };
  return (
    <div className="canvas-module approvals-module">
      {data.approvals.correlation_capability === "unverified" ? (
        <p className="canvas-warning">{messages.agent.approvalCapabilityUnverified}</p>
      ) : null}
      {!canDecide ? <p className="canvas-warning">{messages.agent.approvalDisabled}</p> : null}
      {error ? (
        <div className="canvas-error" role="alert">
          <strong>{error.message}</strong>
          {error.detail ? <details><summary>技术详情</summary><code>{error.detail}</code></details> : null}
        </div>
      ) : null}
      {approvals.length === 0 ? (
        <p className="canvas-empty">{messages.agent.approvalsEmpty}</p>
      ) : (
        <ul className="canvas-list approval-list">
          {approvals.map((approval) => (
            <li key={approval.request.id} className={approval.request.status === "pending" ? "needs-attention" : ""}>
              <div className="canvas-list-row">
                <strong title={approval.request.id}>{shortID(approval.request.id)}</strong>
                <span className={`state-badge ${stateClass(approval.request.status)}`}>{approval.request.status}</span>
              </div>
              <p>{approval.reference.summary?.trim() || messages.agent.noSummary}</p>
              <div className="canvas-meta">
                <span>call {shortID(approval.request.call_id)}</span>
                <span>revision {approval.request.revision}</span>
                {approval.request.execution ? <span>{executionStateLabel(approval.request.execution.state)}</span> : null}
              </div>
              {approval.request.status === "pending" && canDecide && approval.reference.summary?.trim() ? (
                <div className="approval-actions">
                  <button type="button" className="approval-reject" disabled={deciding !== null} onClick={() => void submit(approval.request.id, approval.request.revision, "reject")}>
                    {messages.agent.reject}
                  </button>
                  <button type="button" className="approval-approve" disabled={deciding !== null} onClick={() => void submit(approval.request.id, approval.request.revision, "approve")}>
                    {deciding === approval.request.id ? messages.agent.deciding : messages.agent.approve}
                  </button>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function TerminalPanel({ data }: { data: Bootstrap }) {
  const session = preferredSession(data.agent_sessions.sessions);
  return (
    <div className="canvas-module terminal-module">
      <div className="terminal-placeholder">
        <span className="terminal-prompt">$</span>
        <div>
          <strong>Terminal 数据通道尚未接入</strong>
          <p>布局位置已保留。后续通过 Core WebSocket 接入保序 PTY 字节流。</p>
          <code>{session?.core_session_id ?? "等待可信 Core Session"}</code>
        </div>
      </div>
    </div>
  );
}

export function MCPPanel({ data }: { data: Bootstrap }) {
  return (
    <div className="canvas-module mcp-module">
      <div className="canvas-summary-row">
        <span>Host {data.mcp.control_plane_configured ? "已配置" : "未配置"}</span>
        <span>Servers {data.mcp.ready}/{data.mcp.total} 就绪</span>
      </div>
      {data.mcp.servers.length === 0 ? (
        <p className="canvas-empty">{messages.mcp.emptyBody}</p>
      ) : (
        <ul className="canvas-list mcp-list">
          {data.mcp.servers.map((server) => (
            <li key={server.id}>
              <div className="canvas-list-row">
                <strong>{server.name}</strong>
                <span className={`state-badge ${server.ready ? "ready" : "warning"}`}>
                  {server.ready ? messages.mcp.readyState : mcpStateLabel(server.connection)}
                </span>
              </div>
              <div className="canvas-meta">
                <span title={server.endpoint}>{server.endpoint}</span>
                <span>{server.capabilities?.tool_count ?? 0} tools</span>
                <span>{mcpStateLabel(server.enablement)}</span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function ActivityPanel({ data }: { data: Bootstrap }) {
  const [filter, setFilter] = useState<"all" | "control" | "diagnostic">("all");
  const events = useMemo(
    () => data.events.events.filter((event) => filter === "all" || event.class === filter).slice(-80).reverse(),
    [data.events.events, filter],
  );
  return (
    <div className="canvas-module activity-module">
      <div className="canvas-tabs compact">
        {(["all", "control", "diagnostic"] as const).map((value) => (
          <button type="button" key={value} className={filter === value ? "active" : ""} onClick={() => setFilter(value)}>
            {value === "all" ? "全部" : value === "control" ? "控制" : "诊断"}
          </button>
        ))}
      </div>
      <ol className="canvas-event-list">
        {events.map((event) => (
          <li key={event.event_id}>
            <span className="event-sequence">{String(event.sequence).padStart(4, "0")}</span>
            <span>{formatTime(event.occurred_at)}</span>
            <strong>{event.type}</strong>
            <span className={`event-class ${event.class}`}>{event.class}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

export function DiagnosticsPanel({ data, stale }: { data: Bootstrap; stale: boolean }) {
  const core = data.core.core;
  return (
    <div className="canvas-module diagnostics-module">
      {stale ? <p className="canvas-warning">{messages.ledger.lastKnownNote}</p> : null}
      <div className="diagnostic-grid">
        <section>
          <h3>Harness Runtime</h3>
          <dl className="canvas-facts">
            <div><dt>状态</dt><dd>{data.runtime.state}</dd></div>
            <div><dt>版本</dt><dd>{data.runtime.version}</dd></div>
            <div><dt>实例</dt><dd>{shortID(data.runtime.instance_id)}</dd></div>
          </dl>
        </section>
        <section>
          <h3>Termcp Core</h3>
          <dl className="canvas-facts">
            <div><dt>状态</dt><dd>{core ? coreStateLabel(core.state) : messages.core.unconfigured}</dd></div>
            <div><dt>所有权</dt><dd>{core ? coreOwnershipLabel(core.ownership) : messages.common.unknown}</dd></div>
            <div><dt>版本</dt><dd>{core?.version ?? messages.common.unknown}</dd></div>
            <div><dt>端点</dt><dd>{core?.endpoint ?? messages.common.notRecorded}</dd></div>
          </dl>
        </section>
      </div>
    </div>
  );
}
