import type {
  ApprovalDecisionResult,
  ApprovalReconciliationResult,
  ApprovalRefreshResult,
  AgentSessionSnapshot,
  AgentSessionTranscript,
  Bootstrap,
  DesktopBridge,
  EventWindow,
  EventStreamPollResult,
  EventStreamSnapshot,
  ModelLoopLaunchResult,
  TerminalOpenResult,
  TerminalPollResult,
  TerminalShellsResult,
} from "./types";

function bridge(): DesktopBridge {
  const app = window.go?.main?.App;
  if (!app) {
    throw new Error("Desktop bridge is unavailable. Open this workspace through Wails.");
  }
  return app;
}

export function desktopAvailable(): boolean {
  return Boolean(window.go?.main?.App);
}

export async function loadBootstrap(): Promise<Bootstrap> {
  return bridge().Bootstrap();
}

export async function loadEvents(after: number, limit = 200): Promise<EventWindow> {
  return bridge().Events(after, limit);
}

export async function openEventStream(after: number): Promise<EventStreamSnapshot> {
  return bridge().OpenEventStream(after);
}

export async function pollEventStream(): Promise<EventStreamPollResult> {
  return bridge().PollEventStream();
}

export async function closeEventStream(): Promise<void> {
  return bridge().CloseEventStream();
}

export async function approvalControlAvailable(): Promise<boolean> {
  return bridge().ApprovalControlAvailable();
}

export async function decideApproval(
  id: string,
  revision: number,
  decision: "approve" | "reject",
): Promise<ApprovalDecisionResult> {
  return bridge().DecideApproval(id, revision, decision);
}

export async function refreshApprovalExecution(id: string): Promise<ApprovalRefreshResult> {
  return bridge().RefreshApprovalExecution(id);
}

export async function reconcileApproval(id: string): Promise<ApprovalReconciliationResult> {
  return bridge().ReconcileApproval(id);
}

export async function startModelLoop(
  coreSessionId: string,
  input: string,
  maxIterations: number,
  maxToolCalls: number,
): Promise<ModelLoopLaunchResult> {
  return bridge().StartModelLoop(coreSessionId, input, maxIterations, maxToolCalls);
}

export async function resumeModelLoop(agentSessionId: string): Promise<ModelLoopLaunchResult> {
  return bridge().ResumeModelLoop(agentSessionId);
}

export async function cancelAgentSession(
  agentSessionId: string,
  expectedRevision: number,
): Promise<{ session: AgentSessionSnapshot }> {
  return bridge().CancelAgentSession(agentSessionId, expectedRevision);
}

export async function loadAgentSessionTranscript(id: string): Promise<AgentSessionTranscript> {
  const response = await bridge().AgentSessionTranscript(id);
  return response.transcript;
}

export async function loadTerminalShells(agentSessionId: string): Promise<TerminalShellsResult> {
  return bridge().TerminalShells(agentSessionId);
}

export async function openTerminal(
  agentSessionId: string,
  shellId: string,
  rows: number,
  cols: number,
): Promise<TerminalOpenResult> {
  return bridge().TerminalOpen(agentSessionId, shellId, rows, cols);
}

export async function pollTerminal(id: string): Promise<TerminalPollResult> {
  return bridge().TerminalPoll(id);
}

export async function sendTerminalInput(id: string, data: string): Promise<void> {
  return bridge().TerminalInput(id, data);
}

export async function resizeTerminal(id: string, rows: number, cols: number): Promise<void> {
  return bridge().TerminalResize(id, rows, cols);
}

export async function closeTerminal(id: string): Promise<void> {
  return bridge().TerminalClose(id);
}

export async function windowAction(action: "minimise" | "maximise" | "close"): Promise<void> {
  const app = bridge();
  if (action === "minimise") {
    await app.WindowMinimise();
  } else if (action === "maximise") {
    await app.WindowToggleMaximise();
  } else {
    await app.WindowClose();
  }
}
