import assert from "node:assert/strict";
import test from "node:test";

import { mcpStatusSummary } from "../src/connectionStatus.ts";

test("uses a neutral state before data and when no MCP servers are configured", () => {
  assert.deepEqual(mcpStatusSummary(undefined), {
    label: "MCP Servers —",
    tone: "neutral",
  });
  assert.deepEqual(mcpStatusSummary({
    total: 0,
    ready: 0,
    control_plane_configured: false,
  }), {
    label: "MCP 未配置",
    tone: "neutral",
  });
  assert.deepEqual(mcpStatusSummary({
    total: 0,
    ready: 0,
    control_plane_configured: true,
  }), {
    label: "MCP Servers 0",
    tone: "neutral",
  });
});

test("distinguishes partial and fully ready MCP servers", () => {
  assert.deepEqual(mcpStatusSummary({
    total: 3,
    ready: 1,
    control_plane_configured: true,
  }), {
    label: "MCP Servers 1/3",
    tone: "warning",
  });
  assert.deepEqual(mcpStatusSummary({
    total: 3,
    ready: 3,
    control_plane_configured: true,
  }), {
    label: "MCP Servers 3/3",
    tone: "ready",
  });
});
