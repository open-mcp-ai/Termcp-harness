import type { ApprovalDecisionResult, Bootstrap, DesktopBridge, EventWindow } from "./types";

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
