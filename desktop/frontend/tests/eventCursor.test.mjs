import assert from "node:assert/strict";
import test from "node:test";

import {
  EVENT_CURSOR_STORAGE_KEY,
  persistEventCursor,
  resolveEventCursor,
} from "../src/eventCursor.ts";

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem(key) {
      return values.get(key) ?? null;
    },
    setItem(key, value) {
      values.set(key, value);
    },
    removeItem(key) {
      values.delete(key);
    },
    value(key) {
      return values.get(key);
    },
  };
}

test("resumes a saved cursor for the same Runtime instance", () => {
  const storage = memoryStorage();
  assert.equal(
    persistEventCursor(storage, "http://127.0.0.1:8787", "runtime-1", 41),
    true,
  );

  assert.equal(
    resolveEventCursor(storage, "http://127.0.0.1:8787", "runtime-1", 90),
    41,
  );
});

test("resets a saved cursor when the Runtime instance changes", () => {
  const storage = memoryStorage();
  persistEventCursor(storage, "http://127.0.0.1:8787", "runtime-1", 41);

  assert.equal(
    resolveEventCursor(storage, "http://127.0.0.1:8787", "runtime-2", 7),
    7,
  );
  assert.deepEqual(JSON.parse(storage.value(EVENT_CURSOR_STORAGE_KEY)), {
    version: 1,
    endpoint: "http://127.0.0.1:8787",
    runtime_instance_id: "runtime-2",
    cursor: 7,
  });
});

test("rejects corrupt and future cursors without losing the latest snapshot", () => {
  const corrupt = memoryStorage({
    [EVENT_CURSOR_STORAGE_KEY]: "{not-json",
  });
  assert.equal(
    resolveEventCursor(corrupt, "http://127.0.0.1:8787", "runtime-1", 12),
    12,
  );
  assert.equal(corrupt.getItem(EVENT_CURSOR_STORAGE_KEY), JSON.stringify({
    version: 1,
    endpoint: "http://127.0.0.1:8787",
    runtime_instance_id: "runtime-1",
    cursor: 12,
  }));

  const future = memoryStorage();
  persistEventCursor(future, "http://127.0.0.1:8787", "runtime-1", 100);
  assert.equal(
    resolveEventCursor(future, "http://127.0.0.1:8787", "runtime-1", 20),
    20,
  );
});

test("does not persist invalid sequence values", () => {
  const storage = memoryStorage();
  assert.equal(
    persistEventCursor(storage, "http://127.0.0.1:8787", "runtime-1", -1),
    false,
  );
  assert.equal(storage.getItem(EVENT_CURSOR_STORAGE_KEY), null);
});
