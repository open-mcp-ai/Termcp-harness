import { useEffect, useMemo, useRef, useState } from "react";
import {
  approvalControlAvailable,
  decideApproval,
  desktopAvailable,
  loadBootstrap,
  windowAction,
} from "./api";
import {
  agentStateLabel,
  coreOwnershipLabel,
  coreStateLabel,
  executionStateLabel,
  mcpFailureLabel,
  mcpStateLabel,
  messages,
} from "./i18n";
import type { Bootstrap, HarnessEvent, RuntimeState } from "./types";

type Filter = "all" | "control" | "diagnostic";

const stateCopy: Record<RuntimeState, string> = messages.state;

function Icon({ name }: { name: "ledger" | "agent" | "core" | "runtime" | "mcp" | "refresh" | "min" | "max" | "close" }) {
  const paths = {
    ledger: <path d="M5 4h14v16H5zM8 8h8M8 12h8M8 16h5" />,
    agent: <path d="M8 4h8v4H8zM5 8h14v12H5zM8 12h3v3H8zM13 12h3v3h-3zM9 18h6" />,
    core: <path d="M12 3 4 7v10l8 4 8-4V7zM4 7l8 4 8-4M12 11v10" />,
    runtime: <path d="M7 4h10l3 3v10l-3 3H7l-3-3V7zM8 8h8v8H8z" />,
    mcp: <path d="M6 7h5v5H6zM13 12h5v5h-5zM11 9h2v5h-2z" />,
    refresh: <path d="M19 8V4l-2 2a8 8 0 1 0 2.3 7M19 4h-4" />,
    min: <path d="M6 12h12" />,
    max: <path d="M6 6h12v12H6z" />,
    close: <path d="m7 7 10 10M17 7 7 17" />,
  };
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24">
      {paths[name]}
    </svg>
  );
}

function WindowControls() {
  if (!desktopAvailable()) return null;
  return (
    <div className="window-controls no-drag" aria-label={messages.window.controls}>
      <button type="button" aria-label={messages.window.minimise} onClick={() => void windowAction("minimise")}>
        <Icon name="min" />
      </button>
      <button type="button" aria-label={messages.window.maximise} onClick={() => void windowAction("maximise")}>
        <Icon name="max" />
      </button>
      <button type="button" className="window-close" aria-label={messages.window.close} onClick={() => void windowAction("close")}>
        <Icon name="close" />
      </button>
    </div>
  );
}

function formatTime(value?: string): string {
  if (!value) return messages.common.notRecorded;
  const time = new Date(value);
  if (Number.isNaN(time.getTime())) return messages.common.unknown;
  return new Intl.DateTimeFormat(messages.locale, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(time);
}

function formatDate(value?: string): string {
  if (!value) return messages.common.notRecorded;
  const time = new Date(value);
  if (Number.isNaN(time.getTime())) return messages.common.unknown;
  return new Intl.DateTimeFormat(messages.locale, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(time);
}

function shortID(value?: string): string {
  if (!value) return messages.common.unknown;
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function eventLabel(type: string): string {
  const [group, ...rest] = type.split(".");
  return rest.length ? `${group} / ${rest.join(".")}` : type;
}

function EventRow({ event, expanded, onToggle }: { event: HarnessEvent; expanded: boolean; onToggle: () => void }) {
  const detail = event.data == null ? "" : JSON.stringify(event.data, null, 2);
  return (
    <li className={`event-row event-${event.class}`}>
      <button type="button" className="event-row-main" onClick={onToggle} aria-expanded={expanded}>
        <span className="event-sequence">{String(event.sequence).padStart(4, "0")}</span>
        <span className="event-marker" aria-hidden="true" />
        <span className="event-time">{formatTime(event.occurred_at)}</span>
        <span className="event-body">
          <span className="event-type">{eventLabel(event.type)}</span>
          <span className="event-meta">
            <span>{event.class === "control" ? messages.event.control : messages.event.diagnostic}</span>
            {event.subject_id ? <span title={event.subject_id}>{messages.event.subject} {shortID(event.subject_id)}</span> : null}
            {event.correlation_id ? <span title={event.correlation_id}>{messages.event.correlation} {shortID(event.correlation_id)}</span> : null}
          </span>
        </span>
        <span className="event-disclosure" aria-hidden="true">{expanded ? "−" : "+"}</span>
      </button>
      {expanded ? (
        <div className="event-detail">
          <dl>
            <div><dt>{messages.event.eventID}</dt><dd title={event.event_id}>{event.event_id}</dd></div>
            <div><dt>{messages.event.occurred}</dt><dd>{formatDate(event.occurred_at)}</dd></div>
            <div><dt>{messages.event.localSequence}</dt><dd>{event.sequence}</dd></div>
          </dl>
          {detail ? <pre>{detail}</pre> : <p className="quiet">{messages.event.noPayload}</p>}
        </div>
      ) : null}
    </li>
  );
}

function Ledger({
  data,
  filter,
  setFilter,
  stale,
}: {
  data: Bootstrap;
  filter: Filter;
  setFilter: (filter: Filter) => void;
  stale: boolean;
}) {
  const [expanded, setExpanded] = useState<string | null>(null);
  const listRef = useRef<HTMLOListElement>(null);
  const lastSequence = useRef(0);
  const events = useMemo(
    () => data.events.events.filter((event) => filter === "all" || event.class === filter),
    [data.events.events, filter],
  );

  useEffect(() => {
    const newest = data.events.events.at(-1)?.sequence ?? 0;
    if (newest > lastSequence.current && listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight;
    }
    lastSequence.current = newest;
  }, [data.events.events]);

  return (
    <section className="ledger-panel" id="ledger" aria-labelledby="ledger-title">
      <header className="panel-heading ledger-heading">
        <div>
          <p className="context-line">{stale ? messages.ledger.contextStale : messages.ledger.context}</p>
          <h2 id="ledger-title">{messages.ledger.title}</h2>
        </div>
        <div className="ledger-summary" aria-label={messages.ledger.summaryLabel}>
          <span><strong>{data.events.events.length}</strong> {messages.ledger.retained}</span>
          <span><strong>{data.events.latest_sequence}</strong> {messages.ledger.latestSequence}</span>
        </div>
        <div className="filter-control" role="group" aria-label={messages.ledger.filterLabel}>
          {(["all", "control", "diagnostic"] as const).map((value) => (
            <button
              type="button"
              key={value}
              className={filter === value ? "active" : ""}
              aria-pressed={filter === value}
              onClick={() => setFilter(value)}
            >
              {value === "all" ? messages.ledger.filterAll : value === "control" ? messages.ledger.filterControl : messages.ledger.filterDiagnostic}
            </button>
          ))}
        </div>
      </header>

      {stale ? (
        <div className="snapshot-notice" role="status">
          {messages.ledger.lastKnownNote}
        </div>
      ) : null}

      {data.events.truncated || data.events.control_overflow ? (
        <div className="retention-warning" role="status">
          <strong>{messages.ledger.historyIncomplete}</strong>
          {data.events.truncated
            ? ` ${messages.ledger.diagnosticEvicted(data.events.dropped_diagnostics, data.events.diagnostic_dropped_through ?? 0)}`
            : ` ${messages.ledger.controlOverflow}`}
        </div>
      ) : null}

      <ol className="event-list" ref={listRef} aria-live="polite">
        {events.length ? (
          events.map((event) => (
            <EventRow
              key={event.event_id}
              event={event}
              expanded={expanded === event.event_id}
              onToggle={() => setExpanded(expanded === event.event_id ? null : event.event_id)}
            />
          ))
        ) : (
          <li className="ledger-empty">
            <span className="empty-orbit" aria-hidden="true" />
            <h3>{filter === "all" ? messages.ledger.emptyAll : filter === "control" ? messages.ledger.emptyControl : messages.ledger.emptyDiagnostic}</h3>
            <p>{messages.ledger.emptyBody}</p>
          </li>
        )}
      </ol>
      <footer className="ledger-foot">
        <span>{messages.ledger.sequenceNote}</span>
        <span>{data.events.has_more ? messages.ledger.olderOutside : messages.ledger.complete}</span>
      </footer>
    </section>
  );
}

function RuntimeInspector({ data, stale }: { data: Bootstrap; stale: boolean }) {
  const state = data.runtime.state;
  const displayState = stale ? "unknown" : state;
  return (
    <section className={`inspector-section ${stale ? "is-stale" : ""}`} id="runtime" aria-labelledby="runtime-title">
      <header className="inspector-heading">
        <div>
          <p className="context-line">{stale ? messages.runtime.contextStale : messages.runtime.context}</p>
          <h2 id="runtime-title">{messages.runtime.title}</h2>
        </div>
        <span className={`state-pill state-${displayState}`}>
          <span className="state-dot" aria-hidden="true" />
          {stale ? messages.state.unknown : stateCopy[state]}
        </span>
      </header>
      {stale ? <div className="snapshot-notice">{messages.runtime.lastKnownNote}</div> : null}
      <dl className="fact-list">
        <div><dt>{messages.runtime.instance}</dt><dd title={data.runtime.instance_id}>{shortID(data.runtime.instance_id)}</dd></div>
        <div><dt>{messages.runtime.version}</dt><dd>{data.runtime.version}</dd></div>
        <div><dt>{messages.runtime.started}</dt><dd>{formatDate(data.runtime.started_at)}</dd></div>
        <div><dt>{messages.runtime.health}</dt><dd>{data.health.status === "ok" ? messages.runtime.healthOk : messages.runtime.healthUnavailable}</dd></div>
      </dl>
      {!stale ? <p className="inspector-note">{messages.runtime.observer}</p> : null}
    </section>
  );
}

function CoreInspector({ data, stale }: { data: Bootstrap; stale: boolean }) {
  const core = data.core.core;
  const displayState = stale ? "unknown" : core?.state ?? "unavailable";
  return (
    <section className={`inspector-section ${stale ? "is-stale" : ""}`} id="core" aria-labelledby="core-title">
      <header className="inspector-heading">
        <div>
          <p className="context-line">{stale ? messages.core.contextStale : messages.core.context}</p>
          <h2 id="core-title">{messages.core.title}</h2>
        </div>
        {core ? (
          <span className={`state-pill state-${displayState}`}>
            <span className="state-dot" aria-hidden="true" />
            {stale ? messages.state.unknown : coreStateLabel(core.state)}
          </span>
        ) : (
          <span className={`count-badge ${stale ? "stale" : ""}`}>{messages.core.unconfigured}</span>
        )}
      </header>
      {stale ? <div className="snapshot-notice">{messages.core.lastKnownNote}</div> : null}
      {!data.core.configured ? (
        <div className="compact-empty">
          <strong>{messages.core.unconfigured}</strong>
          <p>{messages.core.unconfiguredBody}</p>
        </div>
      ) : !core ? (
        <div className="compact-empty">
          <strong>{messages.core.notIdentified}</strong>
          <p>{messages.core.notIdentifiedBody}</p>
        </div>
      ) : (
        <>
          <dl className="fact-list">
            <div><dt>{messages.core.endpoint}</dt><dd title={core.endpoint}>{core.endpoint}</dd></div>
            <div><dt>{messages.core.ownership}</dt><dd>{coreOwnershipLabel(core.ownership)}</dd></div>
            <div><dt>{messages.core.version}</dt><dd>{core.version ?? messages.common.unknown}</dd></div>
            <div><dt>{messages.core.pid}</dt><dd>{core.pid ?? messages.common.unknown}</dd></div>
            <div><dt>{messages.core.started}</dt><dd>{formatDate(core.started_at)}</dd></div>
            <div><dt>{messages.core.instance}</dt><dd title={core.instance_key}>{shortID(core.instance_key)}</dd></div>
            <div><dt>{messages.core.daemon}</dt><dd>{core.daemon ? messages.core.daemonMode : messages.core.standaloneMode}</dd></div>
          </dl>
          {core.last_error ? (
            <div className="compact-empty">
              <strong>{messages.core.lastError}</strong>
              <p>{core.last_error}</p>
            </div>
          ) : null}
          {!stale ? (
            <p className="inspector-note">
              {core.ownership === "managed"
                ? messages.core.managedNote
                : core.ownership === "external"
                  ? messages.core.externalNote
                  : messages.core.unverifiedNote}
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}

function MCPInspector({ data, stale }: { data: Bootstrap; stale: boolean }) {
  return (
    <section className={`inspector-section ${stale ? "is-stale" : ""}`} id="mcp" aria-labelledby="mcp-title">
      <header className="inspector-heading">
        <div>
          <p className="context-line">{stale ? messages.mcp.contextStale : messages.mcp.context}</p>
          <h2 id="mcp-title">{messages.mcp.title}</h2>
        </div>
        <span className={`count-badge ${stale ? "stale" : ""}`}>
          {stale ? messages.mcp.lastKnownCount(data.mcp.ready, data.mcp.total) : `${data.mcp.ready}/${data.mcp.total} ${messages.mcp.ready}`}
        </span>
      </header>
      {stale ? <div className="snapshot-notice">{messages.mcp.lastKnownNote}</div> : null}
      {!data.mcp.control_plane_configured ? (
        <div className="compact-empty">
          <strong>{messages.mcp.controlUnavailable}</strong>
          <p>{messages.mcp.controlUnavailableBody}</p>
        </div>
      ) : data.mcp.servers.length === 0 ? (
        <div className="compact-empty">
          <strong>{messages.mcp.empty}</strong>
          <p>{messages.mcp.emptyBody}</p>
        </div>
      ) : (
        <ul className="server-list">
          {data.mcp.servers.map((server) => (
            <li key={server.id}>
              <div className="server-title">
                <strong>{server.name}</strong>
                <span className={stale ? "unknown" : server.ready ? "ready" : "not-ready"}>
                  {stale ? messages.mcp.lastKnownReady : server.ready ? messages.mcp.readyState : mcpStateLabel(server.connection)}
                </span>
              </div>
              <p>{server.endpoint}</p>
              <div className="server-facts">
                <span>{mcpStateLabel(server.registration)}</span>
                <span>{mcpStateLabel(server.capability_state)}</span>
                <span>{mcpStateLabel(server.enablement)}</span>
                {server.capability_state === "loaded" ? <span>{messages.mcp.toolCount(server.capabilities?.tool_count ?? 0)}</span> : null}
                {server.failure_reason ? <span>{mcpFailureLabel(server.failure_reason)}</span> : null}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function AgentInspector({
  data,
  stale,
  canDecide,
  onChanged,
}: {
  data: Bootstrap;
  stale: boolean;
  canDecide: boolean;
  onChanged: () => Promise<void>;
}) {
  const sessions = data.agent_sessions.sessions;
  const approvals = data.approvals.approvals;
  const [deciding, setDeciding] = useState<string | null>(null);
  const [decisionError, setDecisionError] = useState<{ message: string; detail?: string } | null>(null);
  const approvalCapabilityNotice =
    data.approvals.correlation_capability === "unverified"
      ? messages.agent.approvalCapabilityUnverified
      : null;

  const submitDecision = async (id: string, revision: number, decision: "approve" | "reject") => {
    setDeciding(id);
    setDecisionError(null);
    try {
      const result = await decideApproval(id, revision, decision);
      if (result.outcome === "unknown") {
        setDecisionError({ message: messages.agent.decisionUnknown });
      } else if (result.warning) {
        setDecisionError({ message: messages.agent.decisionWarning, detail: result.warning });
      }
      await onChanged();
    } catch (reason) {
      setDecisionError({
        message: messages.agent.decisionFailed,
        detail: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setDeciding(null);
    }
  };

  return (
    <section className={`inspector-section ${stale ? "is-stale" : ""}`} id="agent" aria-labelledby="agent-title">
      <header className="inspector-heading">
        <div>
          <p className="context-line">{stale ? messages.agent.contextStale : messages.agent.context}</p>
          <h2 id="agent-title">{messages.agent.title}</h2>
        </div>
        <span className={`count-badge ${stale ? "stale" : ""}`}>{sessions.length}</span>
      </header>
      {stale ? <div className="snapshot-notice">{messages.agent.lastKnownNote}</div> : null}
      {!data.agent_sessions.configured ? (
        <div className="compact-empty">
          <strong>{messages.agent.empty}</strong>
          <p>{messages.agent.emptyBody}</p>
        </div>
      ) : sessions.length === 0 ? (
        <div className="compact-empty">
          <strong>{messages.agent.empty}</strong>
          <p>{messages.agent.emptyBody}</p>
        </div>
      ) : (
        <ul className="server-list">
          {sessions.map((session) => (
            <li key={session.id}>
              <div className="server-title">
                <strong title={session.id}>{shortID(session.id)}</strong>
                <span className={stale ? "unknown" : session.state === "completed" ? "ready" : "not-ready"}>
                  {stale ? messages.common.unknown : agentStateLabel(session.state)}
                </span>
              </div>
              <div className="server-facts">
                {session.current_call_id ? <span>{messages.agent.call}: {shortID(session.current_call_id)}</span> : null}
                {session.current_approval_status ? <span>{messages.agent.approval}: {session.current_approval_status}</span> : null}
                {session.current_tool_status ? <span>{messages.agent.tool}: {session.current_tool_status}</span> : null}
                {session.current_completion ? <span>{messages.agent.completion}: {session.current_completion}</span> : null}
                {session.current_execution ? (
                  <span>{messages.agent.execution}: {executionStateLabel(session.current_execution.state)}</span>
                ) : null}
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className="agent-approvals">
        <strong>{messages.agent.approvals}</strong>
        {approvalCapabilityNotice ? (
          <p className="agent-capability-warning">{approvalCapabilityNotice}</p>
        ) : null}
        {!canDecide ? <p className="quiet">{messages.agent.approvalDisabled}</p> : null}
        {decisionError ? (
          <>
            <p className="agent-decision-error" role="alert">{decisionError.message}</p>
            {decisionError.detail ? (
              <details className="agent-decision-details">
                <summary>{messages.app.technicalDetails}</summary>
                <pre>{decisionError.detail}</pre>
              </details>
            ) : null}
          </>
        ) : null}
        {approvals.length === 0 ? (
          <p className="quiet">{messages.agent.approvalsEmpty}</p>
        ) : (
          <ul className="server-list">
            {approvals.map((approval) => (
              <li key={approval.request.id}>
                <div className="server-title">
                  <strong title={approval.request.id}>{shortID(approval.request.id)}</strong>
                  <span className="not-ready">{approval.request.status}</span>
                </div>
                <div className="server-facts">
                  <span>{messages.agent.call}: {shortID(approval.request.call_id)}</span>
                  {approval.request.execution ? (
                    <span>{messages.agent.execution}: {executionStateLabel(approval.request.execution.state)}</span>
                  ) : null}
                </div>
                <p className="agent-approval-summary">
                  {approval.reference.summary?.trim() || messages.agent.noSummary}
                </p>
                {canDecide && approval.request.status === "pending" && !approval.reference.summary?.trim() ? (
                  <p className="agent-decision-error" role="alert">{messages.agent.noSummary}</p>
                ) : null}
                {canDecide && approval.request.status === "pending" && approval.reference.summary?.trim() ? (
                  <div className="agent-approval-actions">
                    <button
                      type="button"
                      className="approval-approve"
                      disabled={deciding !== null}
                      onClick={() => void submitDecision(approval.request.id, approval.request.revision, "approve")}
                    >
                      {deciding === approval.request.id ? messages.agent.deciding : messages.agent.approve}
                    </button>
                    <button
                      type="button"
                      className="approval-reject"
                      disabled={deciding !== null}
                      onClick={() => void submitDecision(approval.request.id, approval.request.revision, "reject")}
                    >
                      {messages.agent.reject}
                    </button>
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

function App() {
  const [data, setData] = useState<Bootstrap | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [lastChecked, setLastChecked] = useState<Date | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [approvalControl, setApprovalControl] = useState(false);
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
    void approvalControlAvailable()
      .then(setApprovalControl)
      .catch(() => setApprovalControl(false));
  }, []);

  const stale = Boolean(error && data);
  const state = error ? "unknown" : data?.runtime.state ?? "new";
  return (
    <div className="workspace-shell">
      <header className="titlebar drag-region">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">H</span>
          <div>
            <strong>Termcp Harness</strong>
            <span>{messages.brand.tagline}</span>
          </div>
        </div>
        <div className={`connection-strip state-${state}`} aria-live="polite">
          <span className="state-dot" aria-hidden="true" />
          <span>{error ? messages.state.unknown : data ? stateCopy[data.runtime.state] : messages.app.connecting}</span>
          <code>{data?.endpoint ?? "127.0.0.1:8787"}</code>
        </div>
        <WindowControls />
      </header>

      <div className="workspace-grid">
        <nav className="rail no-drag" aria-label={messages.nav.label}>
          <a href="#ledger" className="rail-link active" aria-label={messages.nav.ledger} title={messages.nav.ledger}>
            <Icon name="ledger" />
          </a>
          <a href="#runtime" className="rail-link" aria-label={messages.nav.runtime} title={messages.nav.runtime}>
            <Icon name="runtime" />
          </a>
          <a href="#agent" className="rail-link" aria-label={messages.nav.agent} title={messages.nav.agent}>
            <Icon name="agent" />
          </a>
          <a href="#core" className="rail-link" aria-label={messages.nav.core} title={messages.nav.core}>
            <Icon name="core" />
          </a>
          <a href="#mcp" className="rail-link" aria-label={messages.nav.mcp} title={messages.nav.mcp}>
            <Icon name="mcp" />
          </a>
          <span className="rail-seam" aria-hidden="true" />
          <div className="rail-mark" aria-hidden="true"><span>01</span><i /></div>
          <p className="rail-note">{messages.nav.railNote.split("\n").map((line, index) => <span key={line}>{index ? <br /> : null}{line}</span>)}</p>
        </nav>

        <main className="workspace-main" id="overview">
          <div className="workspace-intro">
            <div>
              <p className="context-line">{messages.app.context}</p>
              <h1>{messages.app.headline.split("\n").map((line, index) => <span key={line}>{index ? <br /> : null}{line}</span>)}</h1>
            </div>
            <div className="refresh-cluster">
              <span>{lastChecked ? `${messages.app.checked} ${formatTime(lastChecked.toISOString())}` : messages.app.waiting}</span>
              <button type="button" className={`refresh-button ${loading ? "loading" : ""}`} onClick={() => void refresh()}>
                <Icon name="refresh" />
                <span>{messages.app.refresh}</span>
              </button>
            </div>
          </div>

          {error ? (
            <div className="error-banner" role="alert">
              <div>
                <strong>{messages.app.unreachable}</strong>
                <p>{messages.app.unreachableBody}</p>
                <details>
                  <summary>{messages.app.technicalDetails}</summary>
                  <code>{error}</code>
                  <code>go run ./cmd/termcp-harness serve --listen 127.0.0.1:8787</code>
                </details>
              </div>
              <button type="button" onClick={() => void refresh()}>{messages.app.tryAgain}</button>
            </div>
          ) : null}

          {data ? (
            <Ledger data={data} filter={filter} setFilter={setFilter} stale={stale} />
          ) : error ? (
            <section className="ledger-panel error-panel" aria-live="polite">
              <span className="empty-orbit" aria-hidden="true" />
              <h2>{messages.app.unavailableTitle}</h2>
              <p>{messages.app.unavailableBody}</p>
            </section>
          ) : (
            <section className="ledger-panel loading-panel" aria-live="polite">
              <div className="loading-rule"><span /></div>
              <h2>{desktopAvailable() ? messages.app.openingLedger : messages.app.waitingBridge}</h2>
              <p>{desktopAvailable() ? messages.app.reading : messages.app.runThroughDesktop}</p>
            </section>
          )}
        </main>

        <aside className="inspector no-drag" aria-label={messages.app.runtimeDetails}>
          {data ? (
            <>
              <AgentInspector
                data={data}
                stale={stale}
                canDecide={approvalControl && !stale}
                onChanged={refresh}
              />
              <CoreInspector data={data} stale={stale} />
              <RuntimeInspector data={data} stale={stale} />
              <MCPInspector data={data} stale={stale} />
            </>
          ) : (
            <div className="inspector-placeholder">
              <span className="empty-orbit" aria-hidden="true" />
              <p>{messages.app.inspectorPlaceholder}</p>
            </div>
          )}
          <footer className="inspector-footer">
            <span>{messages.app.readOnly}</span>
            <span>{messages.app.controlAPI}</span>
          </footer>
        </aside>
      </div>

      <footer className="statusbar">
        <span className="status-primary">
          <i aria-hidden="true" />
          {error ? messages.state.unknown : data ? messages.app.authoritative : messages.app.noSnapshot}
        </span>
        <span>{messages.app.observerNote}</span>
      </footer>
    </div>
  );
}

export default App;
