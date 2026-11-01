import { Terminal as XtermTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelAgentSession,
  closeTerminal,
  decideApproval,
  desktopAvailable,
  loadAgentSessionTranscript,
  loadTerminalShells,
  openTerminal,
  pollTerminal,
  reconcileApproval,
  refreshApprovalExecution,
  resumeModelLoop,
  resizeTerminal,
  sendTerminalInput,
  startModelLoop,
} from "./api";
import {
  agentStateLabel,
  approvalStateLabel,
  coreOwnershipLabel,
  coreStateLabel,
  executionStateLabel,
  mcpStateLabel,
  messages,
} from "./i18n";
import {
  approvalTimeline,
  canReconcileApproval,
  canRefreshWaitingExecution,
} from "./timeline";
import {
  modelLoopResultKind,
  modelRecoveryNeedsAction,
  modelRecoverySummaryText,
  canResumeModelLoop,
  canCancelAgentSession,
  validModelBudgets,
  visibleModelResult,
} from "./modelLoop";
import { summarizeAgentSessionStates } from "./sessionSummary";
import type {
  AgentSessionSnapshot,
  AgentSessionTranscript,
  ApprovalDecisionResult,
  ApprovalStatus,
  Bootstrap,
  HarnessEvent,
  ModelLoopLaunchResult,
  TerminalShell,
  TerminalState,
  TrackedApproval,
} from "./types";

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

function stateClass(state: string): string {
  if (state === "completed" || state === "succeeded" || state === "running") return "ready";
  if (state === "failed" || state === "blocked" || state === "expired") return "danger";
  if (state === "waiting_approval" || state === "waiting_execution" || state === "cancelling" || state === "submitted") return "warning";
  return "unknown";
}

function eventBelongsToSession(event: HarnessEvent, session: AgentSessionSnapshot): boolean {
  return event.subject_id === session.id ||
    Boolean(session.current_approval_id && event.subject_id === session.current_approval_id) ||
    Boolean(session.current_call_id && event.correlation_id === session.current_call_id);
}

function approvalBelongsToSession(approval: TrackedApproval, session: AgentSessionSnapshot): boolean {
  if (approval.request.id === session.current_approval_id) return true;
  if (approval.request.call_id && approval.request.call_id === session.current_call_id) return true;
  return approval.reference.correlation === "trusted" &&
    Boolean(session.core_session_id) &&
    approval.reference.session_id === session.core_session_id;
}

function ModelComposer({
  session,
  approvalControl,
  stale,
  onChanged,
  onSelectSession,
}: {
  session?: AgentSessionSnapshot;
  approvalControl: boolean;
  stale: boolean;
  onChanged: () => Promise<void>;
  onSelectSession: (sessionId: string) => void;
}) {
  const [input, setInput] = useState("");
  const [coreSessionId, setCoreSessionId] = useState("");
  const [maxIterations, setMaxIterations] = useState(8);
  const [maxToolCalls, setMaxToolCalls] = useState(16);
  const [composerOpen, setComposerOpen] = useState(!session);
  const [starting, setStarting] = useState(false);
  const [result, setResult] = useState<ModelLoopLaunchResult | null>(null);
  const [transcript, setTranscript] = useState<AgentSessionTranscript | null>(null);
  const [transcriptFailed, setTranscriptFailed] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const unavailable = !approvalControl || stale;
  const budgetsValid = validModelBudgets(maxIterations, maxToolCalls);
  const canSubmit = !unavailable && !starting && budgetsValid && input.trim().length > 0;
  const matchedResult = visibleModelResult(result, session?.id);
  const visibleResult = matchedResult && session?.id === matchedResult.agent_session_id
    ? { ...matchedResult, session }
    : matchedResult;
  const resultKind = visibleResult ? modelLoopResultKind(visibleResult.session.state) : "other";
  const persistedOutputVisible = !visibleResult && Boolean(transcript?.output);

  useEffect(() => {
    setTranscript(null);
    setTranscriptFailed(false);
    if (!session?.id || !approvalControl) return;
    let cancelled = false;
    void loadAgentSessionTranscript(session.id)
      .then((next) => {
        if (!cancelled) setTranscript(next);
      })
      .catch(() => {
        if (!cancelled && (session.output_bytes ?? 0) > 0) setTranscriptFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [session?.id, session?.revision, approvalControl]);

  const submit = async () => {
    if (!canSubmit) {
      if (!input.trim()) setError(messages.agent.modelComposerInputRequired);
      return;
    }
    setStarting(true);
    setResult(null);
    setError(null);
    try {
      const next = await startModelLoop(coreSessionId, input, maxIterations, maxToolCalls);
      setResult(next);
      setInput("");
      onSelectSession(next.agent_session_id);
      try {
        await onChanged();
      } catch {
        setError(messages.agent.modelComposerRefreshWarning);
      }
    } catch {
      setError(messages.agent.modelComposerFailed);
    } finally {
      setStarting(false);
    }
  };

  return (
    <details
      className="model-composer"
      open={composerOpen}
      onToggle={(event) => setComposerOpen(event.currentTarget.open)}
    >
      <summary>
        <span className="model-composer-signal" aria-hidden="true" />
        <span>
          <strong>{messages.agent.modelComposerTitle}</strong>
          <small>{messages.agent.modelComposerBody}</small>
        </span>
      </summary>
      <form
        className="model-composer-form"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <label className="model-prompt-field">
          <span>{messages.agent.modelComposerPrompt}</span>
          <textarea
            value={input}
            rows={4}
            maxLength={256 * 1024}
            placeholder={messages.agent.modelComposerPlaceholder}
            disabled={unavailable || starting}
            onChange={(event) => setInput(event.target.value)}
          />
        </label>
        <details className="model-composer-advanced">
          <summary>{messages.agent.modelComposerAdvanced}</summary>
          <div className="model-budget-grid">
            <label>
              <span>{messages.agent.modelComposerCoreSession}</span>
              <input
                value={coreSessionId}
                maxLength={256}
                disabled={unavailable || starting}
                placeholder={session?.core_session_id ?? messages.common.none}
                onChange={(event) => setCoreSessionId(event.target.value)}
              />
            </label>
            <label>
              <span>{messages.agent.modelTurns}</span>
              <input
                type="number"
                min={1}
                max={32}
                value={maxIterations}
                disabled={unavailable || starting}
                onChange={(event) => setMaxIterations(Number(event.target.value))}
              />
            </label>
            <label>
              <span>{messages.agent.modelTools}</span>
              <input
                type="number"
                min={1}
                max={64}
                value={maxToolCalls}
                disabled={unavailable || starting}
                onChange={(event) => setMaxToolCalls(Number(event.target.value))}
              />
            </label>
          </div>
          <p className="model-field-hint">{messages.agent.modelComposerCoreSessionHint}</p>
        </details>
        {unavailable ? (
          <p className="canvas-warning">
            {stale ? messages.agent.modelComposerStale : messages.agent.modelComposerUnavailable}
          </p>
        ) : null}
        {!budgetsValid ? <p className="canvas-warning">{messages.agent.modelBudgetInvalid}</p> : null}
        <div className="model-composer-actions">
          <span aria-live="polite">
            {starting ? messages.agent.modelComposerRunning : ""}
          </span>
          <button type="submit" disabled={!canSubmit}>
            {starting ? messages.agent.modelComposerRunning : messages.agent.modelComposerRun}
          </button>
        </div>
      </form>
      {error ? <p className="canvas-warning">{error}</p> : null}
      {visibleResult ? (
        <section className="model-result" aria-live="polite">
          <div className="model-result-head">
            <strong>{messages.agent.modelOutputTitle}</strong>
            <span className={`state-badge ${stateClass(visibleResult.session.state)}`}>
              {agentStateLabel(visibleResult.session.state)}
            </span>
          </div>
          <p className="model-result-meta">
            session {shortID(visibleResult.agent_session_id)}
            {" · "}
            {visibleResult.session.model_turns ?? 0} {messages.agent.modelTurns}
            {" · "}
            {messages.agent.modelOutputTools(visibleResult.calls.length)}
          </p>
          {visibleResult.session.state === "completed" && visibleResult.output ? (
            <div className="model-output-text">{visibleResult.output}</div>
          ) : null}
          {visibleResult.session.state === "completed" && !visibleResult.output ? (
            <p className="canvas-warning">{messages.agent.modelOutputEmpty}</p>
          ) : null}
          {resultKind === "waiting" ? (
            <p className="model-result-waiting">
              {messages.agent.modelOutputWaiting(agentStateLabel(visibleResult.session.state))}
            </p>
          ) : null}
          {resultKind === "failed" ? (
            <p className="canvas-warning">
              {messages.agent.modelOutputFailed(agentStateLabel(visibleResult.session.state))}
            </p>
          ) : null}
          <p className="model-result-note">{messages.agent.modelOutputEphemeral}</p>
        </section>
      ) : null}
      {persistedOutputVisible && transcript ? (
        <section className="model-result persisted" aria-live="polite">
          <div className="model-result-head">
            <strong>{messages.agent.modelOutputPersistedTitle}</strong>
            <span className={`state-badge ${stateClass(transcript.session.state)}`}>
              {agentStateLabel(transcript.session.state)}
            </span>
          </div>
          <p className="model-result-meta">session {shortID(transcript.session.id)}</p>
          <div className="model-output-text">{transcript.output}</div>
          <p className="model-result-note">
            {messages.agent.modelOutputPersistedNote(transcript.messages.length)}
          </p>
        </section>
      ) : null}
      {transcriptFailed ? <p className="canvas-warning">{messages.agent.transcriptLoadFailed}</p> : null}
    </details>
  );
}

function ApprovalTimeline({
  approval,
  events,
}: {
  approval: TrackedApproval;
  events: HarnessEvent[];
}) {
  const entries = useMemo(
    () => approvalTimeline(events, approval).slice(-12),
    [events, approval],
  );
  return (
    <details className="approval-timeline">
      <summary>{messages.agent.timelineTitle}</summary>
      <p className="canvas-note">{messages.agent.timelineWindowNote}</p>
      {entries.length ? (
        <ol className="mini-timeline approval-timeline-list">
          {entries.map(({ event, matchedBy }) => (
            <li key={event.event_id}>
              <span>{String(event.sequence).padStart(4, "0")} · {formatTime(event.occurred_at)}</span>
              <strong>{event.type}</strong>
              <small>
                {matchedBy === "approval_id"
                  ? messages.agent.timelineMatchedApproval
                  : matchedBy === "call_id"
                    ? messages.agent.timelineMatchedCall
                    : messages.agent.timelineMatchedCorrelation}
              </small>
            </li>
          ))}
        </ol>
      ) : <p className="canvas-empty">{messages.agent.timelineEmpty}</p>}
    </details>
  );
}

function SessionsPanelView({
  data,
  selectedSessionId,
  onSelectSession,
  stale,
}: {
  data: Bootstrap;
  selectedSessionId?: string;
  onSelectSession: (sessionId: string) => void;
  stale: boolean;
}) {
  const sessions = data.agent_sessions.sessions;
  const counts = summarizeAgentSessionStates(sessions.map((session) => session.state));
  return (
    <div className="canvas-module sessions-module">
      {stale ? <p className="canvas-warning">{messages.agent.lastKnownNote}</p> : null}
      <div className="canvas-summary-row session-state-strip" role="group" aria-label="Session 状态汇总">
        <span
          title="运行中或正在停止"
          aria-label={`活动 ${counts.active} 个：运行中或正在停止`}
        >
          活动 <strong>{counts.active}</strong>
        </span>
        <span
          title="等待审批或等待执行结果"
          aria-label={`等待 ${counts.waiting} 个：等待审批或等待执行结果`}
        >
          等待 <strong>{counts.waiting}</strong>
        </span>
        <span
          title="失败、已阻断或已过期"
          aria-label={`异常 ${counts.abnormal} 个：失败、已阻断或已过期`}
        >
          异常 <strong>{counts.abnormal}</strong>
        </span>
        <span
          title="结果无法确认，既不是失败也不是已执行"
          aria-label={`未知 ${counts.unknown} 个：结果无法确认，既不是失败也不是已执行`}
        >
          未知 <strong>{counts.unknown}</strong>
        </span>
      </div>
      {sessions.length === 0 ? (
        <p className="canvas-empty">{messages.agent.emptyBody}</p>
      ) : (
        <ul className="canvas-list session-list">
          {sessions.map((session) => (
            <li key={session.id} className={session.id === selectedSessionId ? "is-selected" : ""}>
              <button
                type="button"
                className="session-select-button"
                aria-pressed={session.id === selectedSessionId}
                onClick={() => onSelectSession(session.id)}
              >
                <div className="canvas-list-row">
                  <strong title={session.id}>
                    {session.id === selectedSessionId ? <i className="context-current-dot" aria-hidden="true" /> : null}
                    {shortID(session.id)}
                  </strong>
                  <span className={`state-badge ${stateClass(session.state)}`}>{agentStateLabel(session.state)}</span>
                </div>
                <div className="canvas-meta">
                  <span>{session.current_call_id ? `call ${shortID(session.current_call_id)}` : messages.common.none}</span>
                  <span>{session.current_execution ? executionStateLabel(session.current_execution.state) : messages.common.notRecorded}</span>
                </div>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function AgentPanelView({
  data,
  session,
  stale,
  approvalControl,
  onChanged,
  onSelectSession,
  onOpenTerminal,
}: {
  data: Bootstrap;
  session?: AgentSessionSnapshot;
  stale: boolean;
  approvalControl: boolean;
  onChanged: () => Promise<void>;
  onSelectSession: (sessionId: string) => void;
  onOpenTerminal: (sessionId: string) => void;
}) {
  const [refreshingExecution, setRefreshingExecution] = useState(false);
  const [resumingModelLoop, setResumingModelLoop] = useState(false);
  const [recoveryMessage, setRecoveryMessage] = useState<string | null>(null);
  const [recoveryError, setRecoveryError] = useState<string | null>(null);
  const [modelRecoveryReceipt, setModelRecoveryReceipt] = useState<string | null>(null);
  const [modelRecoveryOutput, setModelRecoveryOutput] = useState("");
  const [modelRecoveryError, setModelRecoveryError] = useState<string | null>(null);
  const [cancellingTask, setCancellingTask] = useState(false);
  const [cancelReceipt, setCancelReceipt] = useState<string | null>(null);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const relatedEvents = data.events.events.filter((event) =>
    session && eventBelongsToSession(event, session),
  ).slice(-8);
  const canRecover = canRefreshWaitingExecution(session, approvalControl && !stale);
  const modelRecoveryCandidate = modelRecoveryNeedsAction(session);
  const canResumeModel = canResumeModelLoop(session, approvalControl && !stale);
  const canCancel = canCancelAgentSession(session, approvalControl && !stale);

  useEffect(() => {
    setRecoveryMessage(null);
    setRecoveryError(null);
    setModelRecoveryReceipt(null);
    setModelRecoveryOutput("");
    setModelRecoveryError(null);
    setCancelReceipt(null);
    setCancelError(null);
  }, [session?.id, session?.current_call_id, session?.current_approval_id, session?.state]);

  const recoverExecution = async () => {
    if (!session?.current_approval_id || refreshingExecution) return;
    setRefreshingExecution(true);
    setRecoveryMessage(null);
    setRecoveryError(null);
    try {
      const result = await refreshApprovalExecution(session.current_approval_id);
      const execution = result.approval.request.execution;
      const state = execution ? executionStateLabel(execution.state) : messages.common.unknown;
      setRecoveryMessage(
        result.complete
          ? messages.agent.executionRecoveryComplete(state)
          : messages.agent.executionRecoveryWaiting(state),
      );
      if (result.warning) setRecoveryError(messages.agent.executionRecoveryWarning);
      await onChanged();
    } catch {
      setRecoveryError(messages.agent.executionRecoveryFailed);
    } finally {
      setRefreshingExecution(false);
    }
  };
  const resumeModel = async () => {
    if (!session || resumingModelLoop) return;
    setResumingModelLoop(true);
    setModelRecoveryReceipt(null);
    setModelRecoveryOutput("");
    setModelRecoveryError(null);
    try {
      const result = await resumeModelLoop(session.id);
      const kind = modelLoopResultKind(result.session.state);
      if (kind === "completed") {
        setModelRecoveryReceipt(messages.agent.modelRecoveryCompleted);
        setModelRecoveryOutput(result.output ?? "");
      } else if (kind === "waiting") {
        setModelRecoveryReceipt(messages.agent.modelRecoveryWaiting(result.session.state));
      } else {
        setModelRecoveryError(messages.agent.modelRecoveryFailed);
      }
      await onChanged();
    } catch {
      setModelRecoveryError(messages.agent.modelRecoveryFailed);
      await onChanged();
    } finally {
      setResumingModelLoop(false);
    }
  };
  const cancelTask = async () => {
    if (!session || cancellingTask || !window.confirm(messages.agent.cancelTaskConfirm)) return;
    setCancellingTask(true);
    setCancelReceipt(null);
    setCancelError(null);
    try {
      const result = await cancelAgentSession(session.id, session.revision);
      const next = result.session;
      if (next.state === "cancelled") {
        setCancelReceipt(
          next.cancellation?.approval_pending
            ? messages.agent.cancelTaskApprovalPending
            : next.cancellation?.remote_outcome_unknown
              ? messages.agent.cancelTaskRemoteUnknown
              : messages.agent.cancelTaskSafe,
        );
      } else if (next.state === "cancelling") {
        setCancelReceipt(messages.agent.cancellationRequested);
      } else {
        setCancelError(messages.agent.cancelTaskRejected);
      }
      await onChanged();
    } catch {
      setCancelError(messages.agent.cancelTaskRejected);
      await onChanged();
    } finally {
      setCancellingTask(false);
    }
  };
  return (
    <div className="canvas-module agent-module">
      {stale ? <p className="canvas-warning">{messages.agent.lastKnownNote}</p> : null}
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
          <section className="agent-cancel">
            <div>
              <strong>{messages.agent.cancelTaskTitle}</strong>
              <span>{messages.agent.cancelTaskBody}</span>
            </div>
            {canCancel ? (
              <button type="button" disabled={cancellingTask} onClick={() => void cancelTask()}>
                {cancellingTask ? messages.agent.cancelTaskPending : messages.agent.cancelTaskAction}
              </button>
            ) : null}
            {session.core_session_id ? (
              <button type="button" onClick={() => onOpenTerminal(session.id)}>
                {messages.agent.takeoverTerminalAction}
              </button>
            ) : null}
            {session.state === "cancelling" ? <p>{messages.agent.cancellationRequested}</p> : null}
            {cancelReceipt ? <p className="execution-recovery-receipt">{cancelReceipt}</p> : null}
            {cancelError ? <p className="canvas-warning">{cancelError}</p> : null}
          </section>
          {session.recovered ? (
            <p className="canvas-warning">{messages.agent.sessionRecovered(session.recovery_reason)}</p>
          ) : null}
          {session.model_loop_recovery ? (
            <section
              className={`model-recovery-checkpoint ${
                session.model_loop_recovery.safe_boundary ? "safe" : "blocked"
              }`}
            >
              <div>
                <strong>Loop checkpoint</strong>
                <span>{formatTime(session.model_loop_recovery.updated_at)}</span>
              </div>
              <p>{modelRecoverySummaryText(session.model_loop_recovery)}</p>
              <small>
                iteration {session.model_loop_recovery.iteration}
                {" · "}
                tools {session.model_loop_recovery.tool_calls}
                {" · "}
                pending {session.model_loop_recovery.pending_tool_calls}
                {session.model_loop_recovery.current_call_id
                  ? ` · call ${shortID(session.model_loop_recovery.current_call_id)}`
                  : ""}
              </small>
              {modelRecoveryCandidate ? (
                <div className="model-recovery-action">
                  <p>{messages.agent.modelRecoveryResumeBody}</p>
                  {canResumeModel ? (
                    <button type="button" disabled={resumingModelLoop} onClick={() => void resumeModel()}>
                      {resumingModelLoop
                        ? messages.agent.modelRecoveryResuming
                        : messages.agent.modelRecoveryResumeAction}
                    </button>
                  ) : (
                    <p className="canvas-warning">{messages.agent.modelRecoveryResumeDisabled}</p>
                  )}
                  {modelRecoveryReceipt ? <p className="execution-recovery-receipt">{modelRecoveryReceipt}</p> : null}
                  {modelRecoveryOutput ? <pre className="model-recovery-output">{modelRecoveryOutput}</pre> : null}
                  {modelRecoveryError ? <p className="canvas-warning">{modelRecoveryError}</p> : null}
                </div>
              ) : null}
            </section>
          ) : null}
          <dl className="canvas-facts">
            <div><dt>Call</dt><dd>{shortID(session.current_call_id)}</dd></div>
            <div><dt>Approval</dt><dd>{session.current_approval_status ? approvalStateLabel(session.current_approval_status) : messages.common.none}</dd></div>
            <div><dt>Tool</dt><dd>{session.current_tool_status ?? messages.common.none}</dd></div>
            <div><dt>Completion</dt><dd>{session.current_completion ?? messages.common.notRecorded}</dd></div>
            <div><dt>Execution</dt><dd>{session.current_execution ? executionStateLabel(session.current_execution.state) : messages.common.none}</dd></div>
            <div><dt>{messages.agent.modelTurns}</dt><dd>{session.model_turns ?? messages.common.notRecorded}</dd></div>
            <div><dt>{messages.agent.modelTools}</dt><dd>{session.model_tool_calls ?? messages.common.notRecorded}</dd></div>
            <div><dt>Updated</dt><dd>{formatTime(session.finished_at ?? session.started_at)}</dd></div>
          </dl>
          {session.state === "waiting_execution" ? (
            <div className="execution-recovery">
              <strong>{messages.agent.executionRecoveryTitle}</strong>
              <p>{messages.agent.executionRecoveryBody}</p>
              {canRecover ? (
                <button type="button" disabled={refreshingExecution} onClick={() => void recoverExecution()}>
                  {refreshingExecution
                    ? messages.agent.executionRecovering
                    : messages.agent.executionRecoveryAction}
                </button>
              ) : (
                <p className="canvas-warning">{messages.agent.executionRecoveryDisabled}</p>
              )}
              {recoveryMessage ? <p className="execution-recovery-receipt">{recoveryMessage}</p> : null}
              {recoveryError ? <p className="canvas-warning">{recoveryError}</p> : null}
            </div>
          ) : null}
        </>
      )}
      <ModelComposer
        session={session}
        approvalControl={approvalControl}
        stale={stale}
        onChanged={onChanged}
        onSelectSession={onSelectSession}
      />
      {session ? (
        <>
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
      ) : null}
    </div>
  );
}

function ApprovalStatusLine({
  tone,
  label,
  summary,
  detail,
}: {
  tone: "info" | "warning";
  label: string;
  summary: string;
  detail: string;
}) {
  return (
    <div className={`approval-status-line ${tone}`}>
      <span className="approval-status-dot" aria-hidden="true" />
      <strong>{label}</strong>
      <span>{summary}</span>
      <span className="approval-help" tabIndex={0} aria-label={`${label}：${detail}`}>
        ?
        <span role="tooltip">{detail}</span>
      </span>
    </div>
  );
}

function ApprovalsPanelView({
  data,
  session,
  sessionId,
  stale,
  canDecide,
  onChanged,
}: {
  data: Bootstrap;
  session?: AgentSessionSnapshot;
  sessionId?: string;
  stale: boolean;
  canDecide: boolean;
  onChanged: () => Promise<void>;
}) {
  const [deciding, setDeciding] = useState<{
    id: string;
    decision: "approve" | "reject";
  } | null>(null);
  const [reconciling, setReconciling] = useState<string | null>(null);
  const [receipts, setReceipts] = useState<Record<string, {
    outcome: ApprovalDecisionResult["outcome"];
    status: ApprovalStatus;
    revision: number;
  }>>({});
  const [showAllOverride, setShowAllOverride] = useState<boolean | null>(null);
  const [error, setError] = useState<{ message: string; detail?: string } | null>(null);
  const showAll = showAllOverride ?? !session;
  const approvals = useMemo(
    () => showAll || !session
      ? data.approvals.approvals
      : data.approvals.approvals.filter((approval) => approvalBelongsToSession(approval, session)),
    [data.approvals.approvals, session, showAll],
  );
  const submit = async (id: string, revision: number, decision: "approve" | "reject") => {
    setDeciding({ id, decision });
    setError(null);
    try {
      const result = await decideApproval(id, revision, decision);
      setReceipts((current) => ({
        ...current,
        [id]: {
          outcome: result.outcome,
          status: result.outcome === "unknown" ? "unknown" : result.approval.status,
          revision: result.approval.revision,
        },
      }));
      if (result.outcome === "unknown") {
        setError({ message: messages.agent.decisionUnknown });
      } else if (result.warning) {
        setError({ message: messages.agent.decisionWarning, detail: result.warning });
      }
      await onChanged();
    } catch (reason) {
      setReceipts((current) => ({
        ...current,
        [id]: { outcome: "unknown", status: "unknown", revision },
      }));
      setError({
        message: messages.agent.decisionUnknown,
        detail: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setDeciding(null);
    }
  };
  const reconcile = async (id: string) => {
    setReconciling(id);
    setError(null);
    try {
      const result = await reconcileApproval(id);
      setReceipts((current) => ({
        ...current,
        [id]: {
          outcome: result.approval.request.status === "unknown" ? "unknown" : "confirmed",
          status: result.approval.request.status,
          revision: result.approval.request.revision,
        },
      }));
      if (!result.resolved) {
        setError({ message: messages.agent.approvalReconcilePending });
      } else if (result.warning) {
        setError({ message: messages.agent.approvalReconcileWarning, detail: result.warning });
      }
      await onChanged();
    } catch (reason) {
      setError({
        message: messages.agent.approvalReconcileFailed,
        detail: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setReconciling(null);
    }
  };
  return (
    <div className="canvas-module approvals-module">
      <div className="approval-status-list" aria-label="审批状态说明">
        <ApprovalStatusLine
          tone="info"
          label="状态来源"
          summary="Runtime 快照，等待 Core 回执"
          detail={messages.agent.approvalSnapshot}
        />
        {data.approvals.correlation_capability === "unverified" ? (
          <ApprovalStatusLine
            tone="warning"
            label="审批关联"
            summary="待观察 approval_id"
            detail={messages.agent.approvalCapabilityUnverified}
          />
        ) : null}
        {!canDecide ? (
          <ApprovalStatusLine
            tone="warning"
            label="操作权限"
            summary="只读，未加载 token"
            detail={messages.agent.approvalDisabled}
          />
        ) : null}
      </div>
      {stale ? <p className="canvas-warning">{messages.agent.lastKnownNote}</p> : null}
      {sessionId ? (
        <div className="approval-filter-row">
          <strong>{showAll ? messages.agent.approvalAll : messages.agent.approvalRelated}</strong>
          <button type="button" onClick={() => setShowAllOverride(!showAll)}>
            {showAll ? messages.agent.approvalShowRelated : messages.agent.approvalShowAll}
          </button>
        </div>
      ) : null}
      {error ? (
        <div className="canvas-error" role="alert">
          <strong>{error.message}</strong>
          {error.detail ? <details><summary>技术详情</summary><code>{error.detail}</code></details> : null}
        </div>
      ) : null}
      {approvals.length === 0 ? (
        <p className="canvas-empty">
          {sessionId && !showAll ? messages.agent.approvalNoRelated : messages.agent.approvalsEmpty}
        </p>
      ) : (
        <ul className="canvas-list approval-list">
          {approvals.map((approval) => {
            const receipt = receipts[approval.request.id];
            const status = receipt?.outcome === "unknown"
              ? "unknown"
              : receipt && receipt.revision >= approval.request.revision
                ? receipt.status
                : approval.request.status;
            const receiptKind = receipt?.outcome === "unknown"
              ? "unknown"
              : receipt && receipt.revision >= approval.request.revision
                ? "confirmed"
                : "snapshot";
            const submitting = deciding?.id === approval.request.id;
            const isReconciling = reconciling === approval.request.id;
            return (
              <li key={approval.request.id} className={status === "pending" ? "needs-attention" : ""}>
                <div className="canvas-list-row">
                  <strong title={approval.request.id}>{shortID(approval.request.id)}</strong>
                  <span className={`state-badge ${stateClass(status)}`}>{approvalStateLabel(status)}</span>
                </div>
                <p>{approval.reference.summary?.trim() || messages.agent.noSummary}</p>
                {approval.request.recovered ? (
                  <p className="canvas-warning">
                    {messages.agent.approvalRecovered(
                      approval.request.recovery_reason,
                      approval.request.status,
                    )}
                  </p>
                ) : null}
                <div className="canvas-meta">
                  <span>call {shortID(approval.request.call_id)}</span>
                  <span>revision {approval.request.revision}</span>
                  {approval.request.execution ? <span>{executionStateLabel(approval.request.execution.state)}</span> : null}
                </div>
                <div className={`approval-receipt ${receiptKind}`}>
                  {receiptKind === "confirmed"
                    ? messages.agent.approvalReceiptConfirmed
                    : receiptKind === "unknown"
                      ? messages.agent.approvalReceiptUnknown
                      : messages.agent.approvalReceiptSnapshot}
                </div>
                <ApprovalTimeline approval={approval} events={data.events.events} />
                {status === "pending" && canDecide && approval.reference.summary?.trim() ? (
                  <div className="approval-actions">
                    <button
                      type="button"
                      className="approval-reject"
                      disabled={deciding !== null || reconciling !== null}
                      onClick={() => void submit(approval.request.id, approval.request.revision, "reject")}
                    >
                      {submitting && deciding.decision === "reject"
                        ? messages.agent.approvalSubmittingReject
                        : messages.agent.reject}
                    </button>
                    <button
                      type="button"
                      className="approval-approve"
                      disabled={deciding !== null || reconciling !== null}
                      onClick={() => void submit(approval.request.id, approval.request.revision, "approve")}
                    >
                      {submitting && deciding.decision === "approve"
                        ? messages.agent.approvalSubmittingApprove
                        : messages.agent.approve}
                    </button>
                  </div>
                ) : null}
                {canReconcileApproval(status, canDecide) ? (
                  <div className="approval-actions">
                    <button
                      type="button"
                      disabled={deciding !== null || reconciling !== null}
                      onClick={() => void reconcile(approval.request.id)}
                    >
                      {isReconciling
                        ? messages.agent.approvalReconciling
                        : messages.agent.approvalReconcileAction}
                    </button>
                  </div>
                ) : null}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function terminalStateLabel(state: TerminalState): string {
  switch (state) {
    case "connecting":
      return "正在连接";
    case "reconnecting":
      return "正在自动重连";
    case "connected":
      return "已连接";
    case "ended":
      return "Shell 已结束";
    default:
      return "连接未知";
  }
}

function TerminalView({
  agentSessionId,
  shell,
  stale,
}: {
  agentSessionId: string;
  shell: TerminalShell;
  stale: boolean;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const bridgeIDRef = useRef("");
  const [phase, setPhase] = useState<TerminalState>("connecting");
  const [activity, setActivity] = useState("");
  const [terminalError, setTerminalError] = useState<string | null>(null);
  const [historyTruncated, setHistoryTruncated] = useState(false);
  const [droppedBytes, setDroppedBytes] = useState(0);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    const host = hostRef.current;
    if (!host || !desktopAvailable()) return;

    setPhase("connecting");
    setActivity("");
    setTerminalError(null);
    setHistoryTruncated(false);
    setDroppedBytes(0);

    let disposed = false;
    let pollTimer = 0;
    let opened = false;
    let resizeTimer = 0;
    const terminal = new XtermTerminal({
      cursorBlink: true,
      convertEol: false,
      scrollback: 12000,
      fontFamily: '"Cascadia Mono", Consolas, monospace',
      fontSize: 12,
      lineHeight: 1.22,
      theme: {
        background: "#06141e",
        foreground: "#d8e6ec",
        cursor: "#7e93ff",
        selectionBackground: "rgba(91, 112, 237, 0.35)",
        black: "#071821",
        red: "#e07870",
        green: "#65c9aa",
        yellow: "#d7a757",
        blue: "#7e93ff",
        magenta: "#b59be8",
        cyan: "#62c2cf",
        white: "#d8e6ec",
      },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(host);

    const fitAndResize = () => {
      try {
        fit.fit();
      } catch {
        // The panel can be between grid layouts with no measurable size.
      }
      if (!opened || !bridgeIDRef.current) return;
      void resizeTerminal(bridgeIDRef.current, terminal.rows, terminal.cols).catch((reason) => {
        if (!disposed) setTerminalError(reason instanceof Error ? reason.message : String(reason));
      });
    };

    const resizeObserver = new ResizeObserver(() => {
      window.clearTimeout(resizeTimer);
      resizeTimer = window.setTimeout(fitAndResize, 100);
    });
    resizeObserver.observe(host);
    fitAndResize();

    const dataSubscription = terminal.onData((data) => {
      if (!opened || !bridgeIDRef.current) return;
      void sendTerminalInput(bridgeIDRef.current, data).catch((reason) => {
        if (!disposed) setTerminalError(reason instanceof Error ? reason.message : String(reason));
      });
    });

    const schedulePoll = (delay: number) => {
      if (disposed) return;
      window.clearTimeout(pollTimer);
      pollTimer = window.setTimeout(() => void poll(), delay);
    };

    const poll = async () => {
      const bridgeID = bridgeIDRef.current;
      if (!bridgeID || disposed) return;
      try {
        const result = await pollTerminal(bridgeID);
        if (disposed) return;
        if (result.output) terminal.write(result.output);
        setPhase(result.state);
        setActivity(result.activity_source ?? "");
        setDroppedBytes(result.dropped_output_bytes);
        if (result.last_error) setTerminalError(result.last_error);
        if (result.state === "connected") {
          setTerminalError(null);
          schedulePoll(result.output ? 16 : 45);
        } else if (result.state === "reconnecting") {
          setTerminalError(result.last_error ?? "Core 连接中断，正在自动重连并核对输出缺口。");
          schedulePoll(100);
        } else if (result.state === "unknown") {
          setTerminalError((current) => current ?? "Core 连接已断开，远端 Shell 状态未知。");
        }
      } catch (reason) {
        if (disposed) return;
        setPhase("unknown");
        setTerminalError(reason instanceof Error ? reason.message : String(reason));
      }
    };

    void openTerminal(agentSessionId, shell.id, terminal.rows, terminal.cols)
      .then((result) => {
        if (disposed) {
          void closeTerminal(result.id).catch(() => undefined);
          return;
        }
        bridgeIDRef.current = result.id;
        opened = true;
        setHistoryTruncated(result.history_truncated);
        if (result.history) terminal.write(result.history);
        setPhase(result.state);
        setTerminalError(null);
        fitAndResize();
        schedulePoll(0);
      })
      .catch((reason) => {
        if (disposed) return;
        setPhase("unknown");
        setTerminalError(reason instanceof Error ? reason.message : String(reason));
      });

    return () => {
      disposed = true;
      window.clearTimeout(pollTimer);
      window.clearTimeout(resizeTimer);
      resizeObserver.disconnect();
      dataSubscription.dispose();
      const bridgeID = bridgeIDRef.current;
      bridgeIDRef.current = "";
      opened = false;
      if (bridgeID) void closeTerminal(bridgeID).catch(() => undefined);
      terminal.dispose();
    };
  }, [agentSessionId, shell.id, generation]);

  const activityText = activity === "ai"
    ? "Agent 正在输入"
    : activity === "api"
      ? "人工输入中"
      : phase === "connected"
        ? "等待输入"
        : terminalStateLabel(phase);

  return (
    <div className="terminal-runtime">
      <div className="terminal-toolbar">
        <span className={`state-badge ${phase === "connected" ? "ready" : phase === "ended" ? "unknown" : "warning"}`}>
          {terminalStateLabel(phase)}
        </span>
        <span className={`terminal-activity ${activity ? "active" : ""}`}>{activityText}</span>
        {stale ? <span className="terminal-stale">Harness 快照已过期</span> : null}
        <span className="terminal-shell-id" title={shell.id}>{shortID(shell.id)}</span>
      </div>
      {historyTruncated ? (
        <p className="canvas-warning">仅恢复 Core 保留输出的最后 512 KiB；更早历史未载入。</p>
      ) : null}
      {droppedBytes > 0 ? (
        <p className="canvas-warning" role="alert">
          Terminal 渲染缓冲溢出了 {droppedBytes} 字节，当前显示存在可识别缺口。
        </p>
      ) : null}
      {terminalError ? (
        <div className="canvas-error terminal-error" role="alert">
          <span>{terminalError}</span>
          <button type="button" onClick={() => setGeneration((current) => current + 1)}>重新连接</button>
        </div>
      ) : null}
      <div
        className="terminal-host"
        ref={hostRef}
        aria-label={`Core Terminal ${shell.name ?? shell.id}`}
      />
    </div>
  );
}

function TerminalPanelView({
  session,
  sessionId,
  stale,
}: {
  session?: AgentSessionSnapshot;
  sessionId?: string;
  stale: boolean;
}) {
  const [shells, setShells] = useState<TerminalShell[]>([]);
  const [selectedShellID, setSelectedShellID] = useState("");
  const [loading, setLoading] = useState(false);
  const [shellError, setShellError] = useState<string | null>(null);
  const available = desktopAvailable();
  const coreSessionID = session?.core_session_id;

  useEffect(() => {
    setShells([]);
    setSelectedShellID("");
    setShellError(null);
    if (!available || !sessionId || !coreSessionID) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    void loadTerminalShells(sessionId)
      .then((result) => {
        if (cancelled) return;
        setShells(result.shells);
        const preferred = result.shells.find((shell) => shell.status === "running");
        setSelectedShellID(preferred?.id ?? "");
        setShellError(preferred ? null : "Core Session 当前没有 running Shell；只读历史视图尚未接入。");
      })
      .catch((reason) => {
        if (cancelled) return;
        setShellError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [available, sessionId, coreSessionID]);

  const selectedShell = shells.find((shell) => shell.id === selectedShellID);
  const shellLabel = (shell: TerminalShell) =>
    `${shell.index || "?"} · ${shell.name || shortID(shell.id)} · ${shell.status}`;

  let body;
  if (!available) {
    body = (
      <div className="terminal-placeholder">
        <div>
          <span className="terminal-placeholder-label">Terminal bridge</span>
          <strong>终端桥仅在 Wails Desktop 中可用</strong>
          <p>浏览器预览不会获得 Core 地址、WebSocket 句柄或终端输入桥。</p>
        </div>
      </div>
    );
  } else if (!session || !sessionId) {
    body = (
      <div className="terminal-placeholder">
        <div>
          <span className="terminal-placeholder-label">No trusted shell</span>
          <strong>等待可信 Agent Session</strong>
          <p>终端只绑定具有明确 Core Session 关联的 Agent Session。</p>
        </div>
      </div>
    );
  } else if (!coreSessionID) {
    body = (
      <div className="terminal-placeholder">
        <div>
          <span className="terminal-placeholder-label">Correlation unknown</span>
          <strong>Core Session 关联未知</strong>
          <p>不会按当前选择、终端标题或时间推断 Shell 归属。</p>
        </div>
      </div>
    );
  } else if (loading) {
    body = <div className="canvas-empty">正在读取 Core Session 的 Shell 列表…</div>;
  } else if (shellError) {
    body = <div className="canvas-error terminal-error" role="alert">{shellError}</div>;
  } else if (selectedShell) {
    body = <TerminalView agentSessionId={sessionId} shell={selectedShell} stale={stale} />;
  } else {
    body = <div className="canvas-empty">Core Session 当前没有可接入的 Shell。</div>;
  }

  return (
    <div className="canvas-module terminal-module">
      <div className="terminal-context">
        <span>Core Session</span>
        <code title={coreSessionID}>{shortID(coreSessionID)}</code>
        <label>
          Shell
          <select
            value={selectedShellID}
            onChange={(event) => setSelectedShellID(event.target.value)}
            disabled={loading || shells.length < 2}
          >
            {shells.length === 0 ? <option value="">未发现 Shell</option> : null}
            {shells.map((shell) => (
              <option key={shell.id} value={shell.id} disabled={shell.status !== "running"}>
                {shellLabel(shell)}
              </option>
            ))}
          </select>
        </label>
      </div>
      <div className="terminal-body">{body}</div>
    </div>
  );
}

function MCPPanelView({ data }: { data: Bootstrap }) {
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
                <span title={server.endpoint}>{server.transport}{server.endpoint ? ` · ${server.endpoint}` : ""}</span>
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

function ActivityPanelView({
  data,
  session,
  stale,
}: {
  data: Bootstrap;
  session?: AgentSessionSnapshot;
  stale: boolean;
}) {
  const [filter, setFilter] = useState<"all" | "control" | "diagnostic">("all");
  const [showAllContext, setShowAllContext] = useState(false);
  const events = useMemo(
    () => data.events.events
      .filter((event) => filter === "all" || event.class === filter)
      .filter((event) => !session || showAllContext || eventBelongsToSession(event, session))
      .slice(-80)
      .reverse(),
    [data.events.events, filter, session, showAllContext],
  );
  return (
    <div className="canvas-module activity-module">
      {stale ? <p className="canvas-warning">{messages.agent.lastKnownNote}</p> : null}
      <p className="canvas-note">{messages.ledger.sequenceNote}</p>
      <div className="activity-context-tools">
        <span>{session ? (showAllContext ? "显示全部事件" : "仅显示当前 Session 的可信关联事件") : "未绑定 Session，显示全部事件"}</span>
        {session ? (
          <button type="button" onClick={() => setShowAllContext((current) => !current)}>
            {showAllContext ? "只看当前 Session" : "显示全部事件"}
          </button>
        ) : null}
      </div>
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
            <div className="event-main">
              <strong>{event.type}</strong>
              <small>
                subject {shortID(event.subject_id)}
                {" · "}
                correlation {event.correlation_id ? shortID(event.correlation_id) : "关联未知"}
              </small>
            </div>
            <span className={`event-class ${event.class}`}>{event.class}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

function DiagnosticsPanelView({ data, stale }: { data: Bootstrap; stale: boolean }) {
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

export const SessionsPanel = memo(SessionsPanelView);
export const AgentPanel = memo(AgentPanelView);
export const ApprovalsPanel = memo(ApprovalsPanelView);
export const TerminalPanel = memo(TerminalPanelView);
export const MCPPanel = memo(MCPPanelView);
export const ActivityPanel = memo(ActivityPanelView);
export const DiagnosticsPanel = memo(DiagnosticsPanelView);
