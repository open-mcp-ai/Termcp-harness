import assert from "node:assert/strict";
import test from "node:test";

import { summarizeAgentSessionStates } from "../src/sessionSummary.ts";

test("keeps unknown sessions separate from failed, blocked, and expired sessions", () => {
  const summary = summarizeAgentSessionStates([
    "new",
    "running",
    "cancelling",
    "waiting_approval",
    "waiting_execution",
    "failed",
    "blocked",
    "expired",
    "unknown",
    "cancelled",
    "completed",
  ]);

  assert.deepEqual(summary, {
    active: 2,
    waiting: 2,
    abnormal: 3,
    unknown: 1,
  });
});
