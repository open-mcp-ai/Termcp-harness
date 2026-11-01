import type { EventStreamPollResult, EventWindow, HarnessEvent } from "./types";

export const MAX_VISIBLE_EVENTS = 1000;

export function mergeRealtimeEvents(
  current: EventWindow,
  incoming: EventStreamPollResult,
): EventWindow {
  const bySequence = new Map<number, HarnessEvent>();
  for (const event of current.events) bySequence.set(event.sequence, event);
  for (const event of incoming.events) bySequence.set(event.sequence, event);

  const merged = Array.from(bySequence.values())
    .sort((left, right) => left.sequence - right.sequence);
  const events = merged.slice(-MAX_VISIBLE_EVENTS);
  const latestSequence = Math.max(
    current.latest_sequence,
    incoming.latest_sequence,
    events.length ? events[events.length - 1].sequence : 0,
  );
  return {
    events,
    latest_sequence: latestSequence,
    dropped_diagnostics: Math.max(
      current.dropped_diagnostics,
      incoming.dropped_diagnostics,
    ),
    diagnostic_dropped_through: Math.max(
      current.diagnostic_dropped_through ?? 0,
      incoming.diagnostic_dropped_through ?? 0,
    ) || undefined,
    truncated: current.truncated || incoming.truncated,
    has_more: current.has_more || merged.length > events.length,
    control_overflow: current.control_overflow || incoming.control_overflow,
  };
}
