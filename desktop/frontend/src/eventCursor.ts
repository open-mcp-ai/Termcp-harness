export const EVENT_CURSOR_STORAGE_KEY = "termcp.harness.event-cursor.v1";

const EVENT_CURSOR_SCHEMA_VERSION = 1;

export interface EventCursorStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

interface StoredEventCursor {
  version: number;
  endpoint: string;
  runtime_instance_id: string;
  cursor: number;
}

function isSequence(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function readStoredEventCursor(storage: EventCursorStorage): StoredEventCursor | null {
  let raw: string | null;
  try {
    raw = storage.getItem(EVENT_CURSOR_STORAGE_KEY);
  } catch {
    return null;
  }
  if (!raw) return null;

  try {
    const value = JSON.parse(raw) as Partial<StoredEventCursor>;
    if (
      value.version !== EVENT_CURSOR_SCHEMA_VERSION ||
      typeof value.endpoint !== "string" ||
      typeof value.runtime_instance_id !== "string" ||
      !value.endpoint ||
      !value.runtime_instance_id ||
      !isSequence(value.cursor)
    ) {
      throw new Error("invalid event cursor");
    }
    return {
      version: value.version,
      endpoint: value.endpoint,
      runtime_instance_id: value.runtime_instance_id,
      cursor: value.cursor,
    };
  } catch {
    try {
      storage.removeItem(EVENT_CURSOR_STORAGE_KEY);
    } catch {
      // Storage may be unavailable; the caller still gets a safe cursor.
    }
    return null;
  }
}

export function persistEventCursor(
  storage: EventCursorStorage,
  endpoint: string,
  runtimeInstanceID: string,
  cursor: number,
): boolean {
  if (!endpoint || !runtimeInstanceID || !isSequence(cursor)) return false;
  const value: StoredEventCursor = {
    version: EVENT_CURSOR_SCHEMA_VERSION,
    endpoint,
    runtime_instance_id: runtimeInstanceID,
    cursor,
  };
  try {
    storage.setItem(EVENT_CURSOR_STORAGE_KEY, JSON.stringify(value));
    return true;
  } catch {
    return false;
  }
}

// A saved sequence is only reusable for the same Runtime instance. A future
// cursor is treated as corrupt because it could skip events from a newer log.
export function resolveEventCursor(
  storage: EventCursorStorage,
  endpoint: string,
  runtimeInstanceID: string,
  latestSequence: number,
): number {
  const latest = isSequence(latestSequence) ? latestSequence : 0;
  const stored = readStoredEventCursor(storage);
  if (
    stored &&
    stored.endpoint === endpoint &&
    stored.runtime_instance_id === runtimeInstanceID &&
    stored.cursor <= latest
  ) {
    return stored.cursor;
  }
  persistEventCursor(storage, endpoint, runtimeInstanceID, latest);
  return latest;
}
