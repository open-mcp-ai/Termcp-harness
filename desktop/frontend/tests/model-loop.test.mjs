import assert from "node:assert/strict";
import test from "node:test";

import {
  canCancelAgentSession,
  canResumeModelLoop,
  modelLoopResultKind,
  modelRecoveryNeedsAction,
  modelRecoverySummaryText,
  validModelBudgets,
  visibleModelResult,
} from "../src/modelLoop.ts";

test("classifies model loop states without collapsing waiting and failure", () => {
  assert.equal(modelLoopResultKind("completed"), "completed");
  assert.equal(modelLoopResultKind("waiting_approval"), "waiting");
  assert.equal(modelLoopResultKind("waiting_execution"), "waiting");
  assert.equal(modelLoopResultKind("failed"), "failed");
  assert.equal(modelLoopResultKind("blocked"), "failed");
  assert.equal(modelLoopResultKind("cancelled"), "failed");
  assert.equal(modelLoopResultKind("expired"), "failed");
  assert.equal(modelLoopResultKind("unknown"), "failed");
  assert.equal(modelLoopResultKind("cancelling"), "other");
  assert.equal(modelLoopResultKind("running"), "other");
  assert.equal(modelLoopResultKind("new"), "other");
});

test("offers task cancellation only for controllable non-terminal states", () => {
  for (const state of ["new", "running", "waiting_approval", "waiting_execution"]) {
    assert.equal(canCancelAgentSession({ state }, true), true, state);
  }
  for (const state of ["cancelling", "completed", "cancelled", "unknown"]) {
    assert.equal(canCancelAgentSession({ state }, true), false, state);
  }
  assert.equal(canCancelAgentSession({ state: "running" }, false), false);
});

test("validates model budgets before invoking the fixed bridge", () => {
  assert.equal(validModelBudgets(8, 16), true);
  assert.equal(validModelBudgets(1, 1), true);
  assert.equal(validModelBudgets(32, 64), true);
  assert.equal(validModelBudgets(0, 16), false);
  assert.equal(validModelBudgets(33, 16), false);
  assert.equal(validModelBudgets(8, 0), false);
  assert.equal(validModelBudgets(8, 65), false);
  assert.equal(validModelBudgets(8.5, 16), false);
});

test("shows a model result only for its owning session", () => {
  const result = { agent_session_id: "session-a", output: "done" };
  assert.equal(visibleModelResult(result, undefined), result);
  assert.equal(visibleModelResult(result, "session-a"), result);
  assert.equal(visibleModelResult(result, "session-b"), null);
  assert.equal(visibleModelResult(null, "session-a"), null);
});

test("keeps safe recovery boundaries distinct from blocked tool execution", () => {
  const base = {
    iteration: 2,
    tool_calls: 1,
    pending_tool_calls: 1,
    checkpoint_revision: 7,
    updated_at: "2026-10-31T12:00:00Z",
  };
  assert.match(
    modelRecoverySummaryText({
      ...base,
      stage: "tools_ready",
      safe_boundary: true,
    }),
    /工具待执行边界.*人工恢复/,
  );
  assert.match(
    modelRecoverySummaryText({
      ...base,
      stage: "tool_dispatching",
      safe_boundary: false,
      blocked_reason: "tool_execution_may_have_occurred",
    }),
    /禁止自动重放/,
  );
});

test("offers model recovery only for unclaimed runtime-restart safe checkpoints", () => {
  const session = {
    id: "session-1",
    state: "unknown",
    revision: 8,
    recovered: true,
    recovery_reason: "runtime_restart",
    model_loop_recovery: {
      stage: "tools_ready",
      safe_boundary: true,
      iteration: 1,
      tool_calls: 0,
      pending_tool_calls: 1,
      checkpoint_revision: 7,
      updated_at: "2026-10-31T12:00:00Z",
    },
  };
  assert.equal(modelRecoveryNeedsAction(session), true);
  assert.equal(canResumeModelLoop(session, true), true);
  assert.equal(canResumeModelLoop(session, false), false);
  assert.equal(modelRecoveryNeedsAction({
    ...session,
    model_loop_recovery: { ...session.model_loop_recovery, safe_boundary: false },
  }), false);
  assert.equal(modelRecoveryNeedsAction({ ...session, recovery_reason: "manual" }), false);
});
