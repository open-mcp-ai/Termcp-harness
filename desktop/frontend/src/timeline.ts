import type {
  AgentSessionSnapshot,
  ApprovalStatus,
  HarnessEvent,
  TrackedApproval,
} from "./types";

export type ApprovalTimelineMatch = "approval_id" | "call_id" | "correlation_id";

export interface ApprovalTimelineEntry {
  event: HarnessEvent;
  matchedBy: ApprovalTimelineMatch;
}

export function approvalTimeline(
  events: HarnessEvent[],
  approval: TrackedApproval,
): ApprovalTimelineEntry[] {
  const approvalId = approval.request.id;
  const callId = approval.request.call_id;
  const entries: ApprovalTimelineEntry[] = [];

  for (const event of events) {
    const subjectMatchesApproval = event.subject_id === approvalId;
    const subjectMatchesCall = event.subject_id === callId;
    const correlationMatchesCall = event.correlation_id === callId;
    const conflictingApprovalCorrelation = subjectMatchesApproval &&
      Boolean(event.correlation_id) &&
      !correlationMatchesCall;
    if (conflictingApprovalCorrelation) continue;

    let matchedBy: ApprovalTimelineMatch | undefined;
    if (subjectMatchesApproval) {
      matchedBy = "approval_id";
    } else if (subjectMatchesCall) {
      matchedBy = "call_id";
    } else if (correlationMatchesCall) {
      matchedBy = "correlation_id";
    }
    if (matchedBy) entries.push({ event, matchedBy });
  }

  return entries.sort((left, right) => left.event.sequence - right.event.sequence);
}

export function canRefreshWaitingExecution(
  session: AgentSessionSnapshot | undefined,
  approvalControlAvailable: boolean,
): boolean {
  return Boolean(
    session &&
    approvalControlAvailable &&
    session.state === "waiting_execution" &&
    session.current_approval_id &&
    session.current_approval_status === "approved",
  );
}

export function canReconcileApproval(
  status: ApprovalStatus,
  approvalControlAvailable: boolean,
): boolean {
  return approvalControlAvailable && status === "unknown";
}
