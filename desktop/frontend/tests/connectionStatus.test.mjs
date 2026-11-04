import assert from "node:assert/strict";
import test from "node:test";

import {
  coreStatusSummary,
  mcpStatusSummary,
  runtimeStatusSummary,
  runtimeStatusTone,
} from "../src/connectionStatus.ts";

test("keeps Runtime, Core, and MCP status unknown when Runtime is unavailable", () => {
  assert.deepEqual(mcpStatusSummary(undefined, false), {
    label: "MCP 状态未知",
    tone: "neutral",
  });
  assert.deepEqual(mcpStatusSummary({
    total: 3,
    ready: 3,
    control_plane_configured: true,
  }, false), {
    label: "MCP 状态未知",
    tone: "neutral",
  });
  assert.deepEqual(coreStatusSummary({
    configured: true,
    core: {
      endpoint: "http://127.0.0.1:18765",
      state: "identified",
      ownership: "external",
      ownership_verified: false,
      daemon: true,
      observed_at: "2026-11-03T00:00:00Z",
      revision: 1,
      changed: false,
    },
  }, false), {
    label: "Core 状态未知",
    tone: "neutral",
  });
  assert.equal(runtimeStatusTone("unknown"), "neutral");
});

test("does not keep a stale online Runtime status when the event stream is reconnecting", () => {
  assert.deepEqual(runtimeStatusSummary("running", "connected"), {
    state: "running",
    available: true,
  });
  assert.deepEqual(runtimeStatusSummary("running", "lagged"), {
    state: "running",
    available: true,
  });
  assert.deepEqual(runtimeStatusSummary("running", "reconnecting"), {
    state: "unknown",
    available: false,
  });
  assert.deepEqual(runtimeStatusSummary("unknown", "connected"), {
    state: "unknown",
    available: false,
  });
});

test("maps Runtime states to honest status colors", () => {
  assert.equal(runtimeStatusTone("running"), "ready");
  assert.equal(runtimeStatusTone("new"), "warning");
  assert.equal(runtimeStatusTone("stopping"), "warning");
  assert.equal(runtimeStatusTone("failed"), "danger");
  assert.equal(runtimeStatusTone("stopped"), "neutral");
});

test("distinguishes Core configuration and identity states", () => {
  assert.deepEqual(coreStatusSummary(undefined, true), {
    label: "Core 状态未知",
    tone: "neutral",
  });
  assert.deepEqual(coreStatusSummary({ configured: false }, true), {
    label: "Core 未配置",
    tone: "warning",
  });
  assert.deepEqual(coreStatusSummary({ configured: true }, true), {
    label: "Core 尚未识别",
    tone: "warning",
  });
  assert.deepEqual(coreStatusSummary({
    configured: true,
    core: {
      endpoint: "http://127.0.0.1:18765",
      state: "conflict",
      ownership: "unverified",
      ownership_verified: false,
      daemon: true,
      observed_at: "2026-11-03T00:00:00Z",
      revision: 2,
      changed: true,
    },
  }, true), {
    label: "Core 身份冲突",
    tone: "danger",
  });
});

test("uses a neutral state when no MCP servers are configured", () => {
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
