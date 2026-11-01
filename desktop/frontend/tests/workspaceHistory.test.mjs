import assert from "node:assert/strict";
import test from "node:test";

import { defaultWorkspaceLayout } from "../src/canvas.ts";
import {
  WORKSPACE_HISTORY_STORAGE_KEY,
  canRedoWorkspace,
  canUndoWorkspace,
  emptyWorkspaceHistoryMap,
  readStoredWorkspaceHistory,
  recordWorkspaceHistory,
  redoWorkspaceHistory,
  undoWorkspaceHistory,
  workspaceSnapshotsEqual,
  writeStoredWorkspaceHistory,
} from "../src/workspaceHistory.ts";

function snapshot(count = 3, selectedSessionId) {
  return {
    layout: defaultWorkspaceLayout("daily").slice(0, count),
    ...(selectedSessionId ? { selectedSessionId } : {}),
  };
}

function memoryStorage() {
  const values = new Map();
  return {
    getItem(key) {
      return values.get(key) ?? null;
    },
    setItem(key, value) {
      values.set(key, value);
    },
    value(key) {
      return values.get(key);
    },
  };
}

test("records, undoes and redoes a Workspace snapshot", () => {
  const before = snapshot(3, "session-1");
  const after = snapshot(2);
  let histories = emptyWorkspaceHistoryMap();
  histories = recordWorkspaceHistory(histories, "daily", before);

  const undone = undoWorkspaceHistory(histories, "daily", after);
  assert.ok(undone);
  assert.ok(workspaceSnapshotsEqual(undone.snapshot, before));
  assert.equal(canRedoWorkspace(undone.histories, "daily"), true);

  const redone = redoWorkspaceHistory(undone.histories, "daily", undone.snapshot);
  assert.ok(redone);
  assert.ok(workspaceSnapshotsEqual(redone.snapshot, after));
  assert.equal(canUndoWorkspace(redone.histories, "daily"), true);
});

test("a new edit clears redo and Workspace histories stay isolated", () => {
  let histories = emptyWorkspaceHistoryMap();
  histories = recordWorkspaceHistory(histories, "daily", snapshot(3));
  const undone = undoWorkspaceHistory(histories, "daily", snapshot(2));
  assert.ok(undone);
  histories = recordWorkspaceHistory(undone.histories, "daily", snapshot(1));

  assert.equal(canRedoWorkspace(histories, "daily"), false);
  assert.equal(canUndoWorkspace(histories, "security"), false);
  assert.equal(canUndoWorkspace(histories, "daily"), true);
});

test("caps history and future stacks", () => {
  let histories = emptyWorkspaceHistoryMap();
  for (let index = 0; index < 8; index += 1) {
    histories = recordWorkspaceHistory(histories, "daily", snapshot(index % 3 + 1), 3);
  }
  assert.equal(histories.daily.past.length, 3);

  const undone = undoWorkspaceHistory(histories, "daily", snapshot(3), 3);
  assert.ok(undone);
  const redone = redoWorkspaceHistory(undone.histories, "daily", undone.snapshot, 3);
  assert.ok(redone);
  assert.equal(redone.histories.daily.future.length, 0);
});

test("persists valid history and strips unknown fields", () => {
  const storage = memoryStorage();
  let histories = emptyWorkspaceHistoryMap();
  histories = recordWorkspaceHistory(histories, "daily", {
    layout: [{
      ...defaultWorkspaceLayout("daily")[0],
      unsupported: "must not persist",
    }],
    selectedSessionId: "session-1",
  });
  assert.equal(writeStoredWorkspaceHistory(storage, histories), true);

  const restored = readStoredWorkspaceHistory(storage);
  assert.equal(restored.daily.past.length, 1);
  assert.equal(restored.daily.past[0].selectedSessionId, "session-1");
  assert.equal("unsupported" in restored.daily.past[0].layout[0], false);
});

test("corrupt or partially malformed history fails closed per snapshot", () => {
  const corrupt = memoryStorage();
  corrupt.setItem(WORKSPACE_HISTORY_STORAGE_KEY, "{not-json");
  assert.deepEqual(readStoredWorkspaceHistory(corrupt), emptyWorkspaceHistoryMap());

  const partial = memoryStorage();
  partial.setItem(WORKSPACE_HISTORY_STORAGE_KEY, JSON.stringify({
    version: 1,
    histories: {
      daily: {
        past: [null, { layout: defaultWorkspaceLayout("daily").slice(0, 1) }],
        future: [{ layout: "invalid" }],
      },
      security: "invalid",
    },
  }));
  const restored = readStoredWorkspaceHistory(partial);
  assert.equal(restored.daily.past.length, 1);
  assert.equal(restored.daily.future.length, 0);
  assert.equal(restored.security.past.length, 0);
});
