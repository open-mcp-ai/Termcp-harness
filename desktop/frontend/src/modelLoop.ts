import type {
  AgentSessionSnapshot,
  AgentSessionState,
  ModelLoopRecoveryStage,
  ModelLoopRecoverySummary,
} from "./types";

export type ModelLoopResultKind = "completed" | "waiting" | "failed" | "other";

export function modelLoopResultKind(state: AgentSessionState): ModelLoopResultKind {
  if (state === "completed") return "completed";
  if (state === "waiting_approval" || state === "waiting_execution") return "waiting";
  if (state === "failed" || state === "blocked" || state === "cancelled" || state === "expired" || state === "unknown") {
    return "failed";
  }
  return "other";
}

export function validModelBudgets(maxIterations: number, maxToolCalls: number): boolean {
  return Number.isInteger(maxIterations) && maxIterations >= 1 && maxIterations <= 32 &&
    Number.isInteger(maxToolCalls) && maxToolCalls >= 1 && maxToolCalls <= 64;
}

export function visibleModelResult<T extends { agent_session_id: string }>(
  result: T | null,
  selectedSessionId?: string,
): T | null {
  if (!result) return null;
  if (!selectedSessionId) return result;
  return result.agent_session_id === selectedSessionId ? result : null;
}

export function modelRecoveryStageLabel(stage: ModelLoopRecoveryStage): string {
  switch (stage) {
    case "model_requesting":
      return "模型请求边界";
    case "tools_ready":
      return "工具待执行边界";
    case "tool_dispatching":
      return "工具执行中";
    case "tool_observed":
      return "工具结果待持久化";
    case "waiting_approval":
      return "等待审批";
    case "waiting_execution":
      return "等待执行确认";
    default:
      return "检查点无效";
  }
}

export function modelRecoveryBlockedReason(code: string | undefined): string {
  switch (code) {
    case "tool_execution_may_have_occurred":
      return "工具可能已经执行，禁止自动重放。";
    case "approval_outcome_unknown":
      return "审批结果未确认，需要先核对服务端状态。";
    case "tool_execution_unconfirmed":
      return "工具执行结果未确认，需要可信结果或只读核对。";
    case "checkpoint_invalid":
      return "检查点结构无效，已 fail-closed。";
    default:
      return "当前阶段不允许自动恢复。";
  }
}

export function modelRecoverySummaryText(summary: ModelLoopRecoverySummary): string {
  const boundary = summary.safe_boundary
    ? "位于安全边界；只能由用户显式调用人工恢复。"
    : modelRecoveryBlockedReason(summary.blocked_reason);
  return `${modelRecoveryStageLabel(summary.stage)}：${boundary}`;
}

export function modelRecoveryNeedsAction(session?: AgentSessionSnapshot): boolean {
  if (!session) return false;
  return session.state === "unknown" &&
    session.recovered === true &&
    session.recovery_reason === "runtime_restart" &&
    session.model_loop_recovery?.safe_boundary === true &&
    (session.model_loop_recovery.stage === "model_requesting" ||
      session.model_loop_recovery.stage === "tools_ready");
}

export function canResumeModelLoop(
  session: AgentSessionSnapshot | undefined,
  controlAvailable: boolean,
): boolean {
  return controlAvailable && modelRecoveryNeedsAction(session);
}
