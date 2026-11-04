export interface AgentSessionSummary {
  active: number;
  waiting: number;
  abnormal: number;
  unknown: number;
}

export function summarizeAgentSessionStates(
  states: readonly string[],
): AgentSessionSummary {
  const summary: AgentSessionSummary = {
    active: 0,
    waiting: 0,
    abnormal: 0,
    unknown: 0,
  };
  for (const state of states) {
    if (state === "running" || state === "cancelling") {
      summary.active += 1;
    } else if (state === "waiting_approval" || state === "waiting_execution") {
      summary.waiting += 1;
    } else if (state === "failed" || state === "blocked" || state === "expired") {
      summary.abnormal += 1;
    } else if (state === "unknown") {
      summary.unknown += 1;
    }
  }
  return summary;
}
