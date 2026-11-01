import assert from "node:assert/strict";
import test from "node:test";

import { defaultWorkspaceLayout } from "../src/canvas.ts";
import {
  WorkspaceMigrationError,
  buildWorkspaceMigration,
  migrationDescription,
} from "../src/workspaceMigration.ts";

test("builds a cross-Workspace copy preview without mutating source", () => {
  const source = defaultWorkspaceLayout("security");
  const target = defaultWorkspaceLayout("daily");
  const preview = buildWorkspaceMigration(
    "security",
    source,
    "session-1",
    "mcp",
    target,
    "session-2",
  );

  assert.equal(preview.source, "security");
  assert.equal(preview.target, "mcp");
  assert.equal(preview.snapshot.layout.length, source.length);
  assert.equal(preview.snapshot.selectedSessionId, "session-1");
  assert.equal(preview.replacedPanelCount, target.length);
  assert.equal(preview.targetHadSelectedSession, true);
  assert.match(migrationDescription(preview), /“MCP 开发”/);

  preview.snapshot.layout[0].x = 9;
  assert.notEqual(source[0].x, 9);
});

test("rejects copying a Workspace onto itself", () => {
  assert.throws(
    () => buildWorkspaceMigration(
      "daily",
      defaultWorkspaceLayout("daily"),
      undefined,
      "daily",
      [],
      undefined,
    ),
    (error) => error instanceof WorkspaceMigrationError && error.code === "same_workspace",
  );
});

test("rejects an empty source Workspace", () => {
  assert.throws(
    () => buildWorkspaceMigration("daily", [], undefined, "security", [], undefined),
    (error) => error instanceof WorkspaceMigrationError && error.code === "empty_source",
  );
});
