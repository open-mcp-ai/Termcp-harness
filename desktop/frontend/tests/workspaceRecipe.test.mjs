import assert from "node:assert/strict";
import test from "node:test";

import {
  defaultWorkspaceLayout,
  normalizeLayout,
} from "../src/canvas.ts";
import {
  MAX_WORKSPACE_RECIPE_BYTES,
  WORKSPACE_RECIPE_FORMAT,
  WORKSPACE_RECIPE_VERSION,
  WorkspaceRecipeError,
  createWorkspaceRecipe,
  parseWorkspaceRecipe,
  serializeWorkspaceRecipe,
  workspaceRecipeFilename,
} from "../src/workspaceRecipe.ts";

test("round-trips a workspace recipe with layout and Session state", () => {
  const layout = defaultWorkspaceLayout("security");
  const recipe = createWorkspaceRecipe("security", layout, " agent-session-1 ");
  const parsed = parseWorkspaceRecipe(serializeWorkspaceRecipe(recipe));

  assert.equal(parsed.recipe.format, WORKSPACE_RECIPE_FORMAT);
  assert.equal(parsed.recipe.version, WORKSPACE_RECIPE_VERSION);
  assert.equal(parsed.recipe.workspace, "security");
  assert.equal(parsed.recipe.selected_session_id, "agent-session-1");
  assert.deepEqual(
    parsed.recipe.layout.map((panel) => [panel.moduleId, panel.x, panel.y, panel.w, panel.h]),
    normalizeLayout(layout, { collapseMaximized: true })
      .map((panel) => [panel.moduleId, panel.x, panel.y, panel.w, panel.h]),
  );
  assert.deepEqual(parsed.warnings, []);
});

test("isolates a damaged panel instead of rejecting the whole recipe", () => {
  const layout = defaultWorkspaceLayout("daily");
  const parsed = parseWorkspaceRecipe(JSON.stringify({
    format: WORKSPACE_RECIPE_FORMAT,
    version: WORKSPACE_RECIPE_VERSION,
    workspace: "daily",
    layout: [null, { moduleId: "not-a-module" }, layout[0]],
  }));

  assert.equal(parsed.recipe.layout.length, 1);
  assert.equal(parsed.recipe.layout[0].moduleId, "sessions");
  assert.equal(parsed.warnings.length, 2);
  assert.match(parsed.warnings[0], /第 1 个面板/);
});

test("preserves the global Core Sessions panel", () => {
  const parsed = parseWorkspaceRecipe(JSON.stringify({
    format: WORKSPACE_RECIPE_FORMAT,
    version: WORKSPACE_RECIPE_VERSION,
    workspace: "daily",
    layout: [{
      id: "panel-core-sessions-1",
      moduleId: "core-sessions",
      x: 0,
      y: 0,
      w: 6,
      h: 6,
    }],
  }));

  assert.equal(parsed.recipe.layout.length, 1);
  assert.equal(parsed.recipe.layout[0].moduleId, "core-sessions");
  assert.deepEqual(parsed.warnings, []);
});

test("drops duplicate modules and repairs invalid pinned context", () => {
  const layout = defaultWorkspaceLayout("daily");
  const agent = layout.find((panel) => panel.moduleId === "agent");
  assert.ok(agent);
  const parsed = parseWorkspaceRecipe(JSON.stringify({
    format: WORKSPACE_RECIPE_FORMAT,
    version: WORKSPACE_RECIPE_VERSION,
    workspace: "daily",
    layout: [
      {
        ...agent,
        contextByModule: {
          agent: { mode: "pinned", sessionId: "" },
        },
      },
      layout[0],
      { ...layout[1], x: 0, y: 0 },
    ],
  }));

  assert.equal(parsed.recipe.layout.length, 2);
  assert.deepEqual(parsed.recipe.layout[0].contextByModule?.agent, { mode: "follow" });
  assert.ok(parsed.warnings.some((warning) => warning.includes("锁定 Session 无效")));
  assert.ok(parsed.warnings.some((warning) => warning.includes("重复占用")));
});

test("rejects overlapping layouts with an explicit error", () => {
  const layout = defaultWorkspaceLayout("daily");
  const recipe = {
    format: WORKSPACE_RECIPE_FORMAT,
    version: WORKSPACE_RECIPE_VERSION,
    workspace: "daily",
    layout: [
      { ...layout[0], x: 0, y: 0, w: 6, h: 6 },
      { ...layout[1], x: 0, y: 0, w: 6, h: 6 },
    ],
  };

  assertRecipeError(() => parseWorkspaceRecipe(JSON.stringify(recipe)), "overlapping_panels");
});

test("rejects unsupported formats, versions and oversized files", () => {
  const base = createWorkspaceRecipe("daily", defaultWorkspaceLayout("daily"));

  assertRecipeError(
    () => parseWorkspaceRecipe(JSON.stringify({ ...base, format: "other" })),
    "unsupported_format",
  );
  assertRecipeError(
    () => parseWorkspaceRecipe(JSON.stringify({ ...base, version: 99 })),
    "unsupported_version",
  );
  assertRecipeError(
    () => parseWorkspaceRecipe("x".repeat(MAX_WORKSPACE_RECIPE_BYTES + 1)),
    "too_large",
  );
});

test("creates a filesystem-safe filename", () => {
  assert.equal(
    workspaceRecipeFilename("security", new Date(2026, 9, 31, 9, 5)),
    "termcp-workspace-security-20261031-0905.json",
  );
});

function assertRecipeError(callback, code) {
  assert.throws(callback, (error) => {
    assert.ok(error instanceof WorkspaceRecipeError);
    assert.equal(error.code, code);
    return true;
  });
}
