import assert from "node:assert/strict";
import test from "node:test";

import {
  WORKSPACE_CATALOG_STORAGE_KEY,
  createPersonalWorkspaceRecord,
  defaultWorkspaceCatalog,
  defaultWorkspaceLayout,
  normalizeLayout,
  personalWorkspaceCount,
  readStoredCanvasState,
  readStoredWorkspaceCatalog,
  workspaceNameError,
  writeStoredCanvasState,
  writeStoredWorkspaceCatalog,
} from "../src/canvas.ts";
import {
  WORKSPACE_HISTORY_STORAGE_KEY,
  emptyWorkspaceHistoryMap,
  readStoredWorkspaceHistory,
  recordWorkspaceHistory,
  writeStoredWorkspaceHistory,
} from "../src/workspaceHistory.ts";

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

test("starts with four presets and one editable personal Space", () => {
  const catalog = defaultWorkspaceCatalog();

  assert.deepEqual(
    catalog.spaces.filter((space) => space.kind === "preset").map((space) => space.id),
    ["daily", "security", "mcp", "diagnostics"],
  );
  assert.equal(personalWorkspaceCount(catalog.spaces), 1);
  assert.equal(catalog.spaces.find((space) => space.kind === "personal").id, "custom");
});

test("creates multiple personal Spaces with safe identities and unique-name validation", () => {
  const catalog = defaultWorkspaceCatalog();
  const first = createPersonalWorkspaceRecord("  发布   值守  ", "security", 100);
  const second = createPersonalWorkspaceRecord("发布 值守", "blank", 101);

  assert.equal(first.name, "发布 值守");
  assert.match(first.id, /^space-[a-z0-9-]+$/);
  assert.equal(first.kind, "personal");
  assert.notEqual(first.id, second.id);
  assert.equal(
    workspaceNameError(second.name, [...catalog.spaces, first]),
    "已经有同名 Space，请换一个名称。",
  );
});

test("persists a catalog after deleting the original personal Space", () => {
  const storage = memoryStorage();
  const catalog = defaultWorkspaceCatalog();
  const created = createPersonalWorkspaceRecord("发布值守", "daily", 100);
  const withoutLegacy = {
    version: 1,
    spaces: [
      ...catalog.spaces.filter((space) => space.kind === "preset"),
      created,
    ],
  };

  assert.equal(writeStoredWorkspaceCatalog(storage, withoutLegacy), true);
  const restored = readStoredWorkspaceCatalog(storage);

  assert.equal(personalWorkspaceCount(restored.spaces), 1);
  assert.equal(restored.spaces.find((space) => space.kind === "personal").id, created.id);
  assert.ok(storage.value(WORKSPACE_CATALOG_STORAGE_KEY));
});

test("persists independent layouts and undo history for generated Spaces", () => {
  const storage = memoryStorage();
  const previousLocalStorage = globalThis.localStorage;
  globalThis.localStorage = storage;
  const catalog = defaultWorkspaceCatalog();
  const created = createPersonalWorkspaceRecord("实验空间", "daily", 100);
  const spaces = [...catalog.spaces, created];

  try {
    assert.equal(writeStoredCanvasState({
      version: 2,
      layouts: {
        daily: normalizeLayout(defaultWorkspaceLayout("daily")),
        security: normalizeLayout(defaultWorkspaceLayout("security")),
        mcp: normalizeLayout(defaultWorkspaceLayout("mcp")),
        diagnostics: normalizeLayout(defaultWorkspaceLayout("diagnostics")),
        custom: normalizeLayout(defaultWorkspaceLayout("custom")),
        [created.id]: normalizeLayout(defaultWorkspaceLayout("daily").slice(0, 1)),
      },
      selectedSessionByWorkspace: {
        [created.id]: "session-custom",
      },
    }, spaces), true);
    const restoredCanvas = readStoredCanvasState(spaces);
    assert.equal(restoredCanvas.layouts[created.id].length, 1);
    assert.equal(restoredCanvas.selectedSessionByWorkspace[created.id], "session-custom");

    let histories = emptyWorkspaceHistoryMap(spaces);
    histories = recordWorkspaceHistory(histories, created.id, {
      layout: defaultWorkspaceLayout("daily").slice(0, 1),
    });
    assert.equal(writeStoredWorkspaceHistory(storage, histories, spaces), true);
    const restoredHistory = readStoredWorkspaceHistory(storage, spaces);
    assert.equal(restoredHistory[created.id].past.length, 1);
    assert.ok(storage.value(WORKSPACE_HISTORY_STORAGE_KEY));
  } finally {
    globalThis.localStorage = previousLocalStorage;
  }
});

test("reports layout persistence failure instead of claiming success", () => {
  const catalog = defaultWorkspaceCatalog();
  const state = {
    version: 2,
    layouts: {
      daily: normalizeLayout(defaultWorkspaceLayout("daily")),
      security: normalizeLayout(defaultWorkspaceLayout("security")),
      mcp: normalizeLayout(defaultWorkspaceLayout("mcp")),
      diagnostics: normalizeLayout(defaultWorkspaceLayout("diagnostics")),
      custom: normalizeLayout(defaultWorkspaceLayout("custom")),
    },
    selectedSessionByWorkspace: {},
  };
  const failingStorage = {
    getItem() {
      return null;
    },
    setItem() {
      throw new Error("storage disabled");
    },
  };

  assert.equal(writeStoredCanvasState(state, catalog.spaces, failingStorage), false);
});

test("rejects duplicate persisted personal Space IDs and caps malformed input", () => {
  const storage = memoryStorage();
  const created = createPersonalWorkspaceRecord("唯一 Space", "blank", 100);
  storage.setItem(WORKSPACE_CATALOG_STORAGE_KEY, JSON.stringify({
    version: 1,
    spaces: [
      { ...created, kind: "preset" },
      created,
      { ...created, id: "space-invalid id", name: "无效 Space" },
      { ...created, id: "space-valid-again", name: "   " },
    ],
  }));

  const restored = readStoredWorkspaceCatalog(storage);
  assert.equal(personalWorkspaceCount(restored.spaces), 1);
  assert.equal(restored.spaces.find((space) => space.kind === "personal").name, "唯一 Space");
});
