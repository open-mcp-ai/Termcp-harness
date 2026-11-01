export type MCPStatusTone = "neutral" | "warning" | "ready";

export interface MCPStatusInput {
  total: number;
  ready: number;
  control_plane_configured: boolean;
}

export interface MCPStatusSummary {
  label: string;
  tone: MCPStatusTone;
}

export function mcpStatusSummary(
  mcp: MCPStatusInput | null | undefined,
): MCPStatusSummary {
  if (!mcp) {
    return { label: "MCP Servers —", tone: "neutral" };
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
