import assert from "node:assert/strict";
import test from "node:test";

import {
  approvalTimeline,
  canReconcileApproval,
  canRefreshWaitingExecution,
} from "../src/timeline.ts";

function event(sequence, overrides = {}) {
  return {
    event_id: `event-${sequence}`,
    sequence,
    occurred_at: "2026-10-31T00:00:00Z",
    type: "approval.request.tracked",
    class: "control",
    subject_id: "approval-1",
    correlation_id: "call-1",
    ...overrides,
  };
}

const approval = {
  request: {
    id: "approval-1",
    call_id: "call-1",
    status: "approved",
    decision: "approve",
    revision: 3,
  },
  reference: {
    correlation: "trusted",
    approval_id: "approval-1",
  },
};

test("builds an exact, ordered approval timeline without inferred matches", () => {
  const entries = approvalTimeline([
    event(5, { subject_id: "session-1", correlation_id: "call-1" }),
    event(2, { subject_id: "call-1", correlation_id: undefined }),
    event(9, { subject_id: "other-approval", correlation_id: "other-call" }),
    event(3, { subject_id: "approval-1", correlation_id: "other-call" }),
    event(1),
  ], approval);

  assert.deepEqual(entries.map((entry) => entry.event.sequence), [1, 2, 5]);
  assert.deepEqual(entries.map((entry) => entry.matchedBy), [
    "approval_id",
    "call_id",
    "correlation_id",
  ]);
});

test("offers waiting_execution refresh only for trusted approved evidence", () => {
  const session = {
    id: "session-1",
    state: "waiting_execution",
    revision: 5,
    current_approval_id: "approval-1",
    current_approval_status: "approved",
  };
  assert.equal(canRefreshWaitingExecution(session, true), true);
  assert.equal(canRefreshWaitingExecution(session, false), false);
  assert.equal(canRefreshWaitingExecution({ ...session, state: "waiting_approval" }, true), false);
  assert.equal(canRefreshWaitingExecution({ ...session, current_approval_status: "unknown" }, true), false);
  assert.equal(canRefreshWaitingExecution({ ...session, current_approval_id: undefined }, true), false);
});

test("offers read-only reconciliation only for unknown approvals with control", () => {
  assert.equal(canReconcileApproval("unknown", true), true);
  assert.equal(canReconcileApproval("unknown", false), false);
  assert.equal(canReconcileApproval("pending", true), false);
  assert.equal(canReconcileApproval("approved", true), false);
});
