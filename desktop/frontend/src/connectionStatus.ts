import type { CoreState, EventStreamState, RuntimeState } from "./types";

export type StatusTone = "neutral" | "warning" | "ready" | "danger";
export type MCPStatusTone = Exclude<StatusTone, "danger">;

export interface MCPStatusInput {
  total: number;
  ready: number;
  control_plane_configured: boolean;
}

export interface MCPStatusSummary {
  label: string;
  tone: MCPStatusTone;
}

export interface CoreStatusSummary {
  label: string;
  tone: StatusTone;
}

export function runtimeStatusTone(state: RuntimeState | "unknown"): StatusTone {
  switch (state) {
    case "running":
      return "ready";
    case "new":
    case "stopping":
      return "warning";
    case "failed":
      return "danger";
    case "stopped":
    case "unknown":
      return "neutral";
  }
}

export function runtimeStatusSummary(
  state: RuntimeState | "unknown",
  eventStreamState: EventStreamState,
): { state: RuntimeState | "unknown"; available: boolean } {
  if (eventStreamState === "reconnecting") {
    return { state: "unknown", available: false };
  }
  return { state, available: state === "running" };
}

export function coreStatusSummary(
  core: CoreState | undefined,
  runtimeAvailable: boolean,
): CoreStatusSummary {
  if (!runtimeAvailable || !core) {
    return { label: "Core 状态未知", tone: "neutral" };
  }
  if (!core.configured) {
    return { label: "Core 未配置", tone: "warning" };
  }
  if (!core.core) {
    return { label: "Core 尚未识别", tone: "warning" };
  }
  switch (core.core.state) {
    case "identified":
      return { label: "Core 已识别", tone: "ready" };
    case "conflict":
      return { label: "Core 身份冲突", tone: "danger" };
    case "unavailable":
      return { label: "Core 不可用", tone: "warning" };
    default:
      return { label: "Core 状态未知", tone: "neutral" };
  }
}

export function mcpStatusSummary(
  mcp: MCPStatusInput | null | undefined,
  runtimeAvailable = true,
): MCPStatusSummary {
  if (!runtimeAvailable || !mcp) {
    return { label: "MCP 状态未知", tone: "neutral" };
  }
  if (mcp.total <= 0) {
    return {
      label: mcp.control_plane_configured ? "MCP Servers 0" : "MCP 未配置",
      tone: "neutral",
    };
  }
  return {
    label: `MCP Servers ${mcp.ready}/${mcp.total}`,
    tone: mcp.ready >= mcp.total ? "ready" : "warning",
  };
}
