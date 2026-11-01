import assert from "node:assert/strict";
import test from "node:test";

import { MAX_VISIBLE_EVENTS, mergeRealtimeEvents } from "../src/events.ts";

function event(sequence) {
  return {
    event_id: `event-${sequence}`,
    sequence,
    occurred_at: "2026-10-31T00:00:00Z",
    type: "runtime.started",
    class: "control",
  };
}

function stream(events, overrides = {}) {
  return {
    state: "connected",
    cursor: events.length ? events[events.length - 1].sequence : 0,
    latest_sequence: events.length ? events[events.length - 1].sequence : 0,
    dropped_diagnostics: 0,
    truncated: false,
    control_overflow: false,
    lagged: false,
    revision: 1,
    events,
    ...overrides,
  };
}

test("merges, orders and deduplicates realtime events", () => {
  const current = {
    events: [event(2), event(1)],
    latest_sequence: 2,
    dropped_diagnostics: 0,
    truncated: false,
    has_more: false,
    control_overflow: false,
  };
  const merged = mergeRealtimeEvents(current, stream([event(2), event(3)]));
  assert.deepEqual(merged.events.map((item) => item.sequence), [1, 2, 3]);
  assert.equal(merged.latest_sequence, 3);
  assert.equal(merged.truncated, false);
});

test("preserves retention warnings and caps the visible window", () => {
  const current = {
    events: [event(1)],
    latest_sequence: 1,
    dropped_diagnostics: 2,
    diagnostic_dropped_through: 1,
    truncated: true,
    has_more: false,
    control_overflow: false,
  };
  const incoming = Array.from(
    { length: MAX_VISIBLE_EVENTS + 5 },
    (_, index) => event(index + 2),
  );
  const merged = mergeRealtimeEvents(current, stream(incoming, {
    dropped_diagnostics: 3,
    diagnostic_dropped_through: 2,
    control_overflow: true,
  }));

  assert.equal(merged.events.length, MAX_VISIBLE_EVENTS);
  assert.equal(merged.events[0].sequence, 7);
  assert.equal(merged.dropped_diagnostics, 3);
  assert.equal(merged.diagnostic_dropped_through, 2);
  assert.equal(merged.truncated, true);
  assert.equal(merged.has_more, true);
  assert.equal(merged.control_overflow, true);
});
