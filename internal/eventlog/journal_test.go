package eventlog

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAppendAndQueryInLocalSequenceOrder(t *testing.T) {
	journal := newTestJournal(4, 4)
	appendEvent(t, journal, TypeRuntimeStarted, ClassControl)
	appendEvent(t, journal, Type("diagnostic.one"), ClassDiagnostic)
	appendEvent(t, journal, TypeRuntimeStopping, ClassControl)

	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(window.Events) != 3 || window.LatestSequence != 3 {
		t.Fatalf("window = %+v", window)
	}
	for i, event := range window.Events {
		if event.Sequence != Sequence(i+1) {
			t.Fatalf("event %d sequence = %d", i, event.Sequence)
		}
	}
	if window.Truncated || window.ControlOverflow || window.HasMore {
		t.Fatalf("unexpected retention flags = %+v", window)
	}
}

func TestDiagnosticEvictionIsVisible(t *testing.T) {
	journal := newTestJournal(4, 2)
	appendEvent(t, journal, Type("diagnostic.one"), ClassDiagnostic)
	appendEvent(t, journal, Type("diagnostic.two"), ClassDiagnostic)
	appendEvent(t, journal, Type("diagnostic.three"), ClassDiagnostic)

	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(window.Events) != 2 || window.Events[0].Sequence != 2 || window.Events[1].Sequence != 3 {
		t.Fatalf("events = %+v", window.Events)
	}
	if window.DroppedDiagnostics != 1 || window.DiagnosticDroppedThrough != 1 || !window.Truncated {
		t.Fatalf("retention = %+v", window)
	}

	replay, err := journal.Query(1, 10)
	if err != nil {
		t.Fatalf("Query(after=1) error = %v", err)
	}
	if replay.Truncated {
		t.Fatalf("Truncated = true after cursor covers dropped prefix: %+v", replay)
	}
}

func TestControlEventsAreNeverEvicted(t *testing.T) {
	journal := newTestJournal(2, 4)
	appendEvent(t, journal, TypeRuntimeStarted, ClassControl)
	appendEvent(t, journal, TypeRuntimeStopping, ClassControl)

	_, err := journal.Append(Entry{Type: TypeRuntimeStopped, Class: ClassControl})
	if !errors.Is(err, ErrControlCapacityExceeded) {
		t.Fatalf("Append() error = %v, want ErrControlCapacityExceeded", err)
	}
	window, queryErr := journal.Query(0, 10)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	if len(window.Events) != 2 || !window.ControlOverflow {
		t.Fatalf("window = %+v", window)
	}
	if window.LatestSequence != 2 {
		t.Fatalf("LatestSequence = %d, want 2", window.LatestSequence)
	}
}

func TestQueryCursorLimitAndHasMore(t *testing.T) {
	journal := newTestJournal(10, 10)
	for range 5 {
		appendEvent(t, journal, Type("diagnostic.test"), ClassDiagnostic)
	}

	window, err := journal.Query(2, 2)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(window.Events) != 2 || window.Events[0].Sequence != 3 || window.Events[1].Sequence != 4 || !window.HasMore {
		t.Fatalf("window = %+v", window)
	}
	last, err := journal.Query(5, 2)
	if err != nil {
		t.Fatalf("Query(last) error = %v", err)
	}
	if len(last.Events) != 0 || last.HasMore {
		t.Fatalf("last window = %+v", last)
	}
	if _, err := journal.Query(0, 0); err == nil {
		t.Fatal("Query(limit=0) error = nil")
	}
}

func TestInvalidEntriesAreRejectedWithoutConsumingSequence(t *testing.T) {
	journal := newTestJournal(4, 4)
	tests := []Entry{
		{Class: ClassControl},
		{Type: Type("test"), Class: Class("unknown")},
		{Type: Type("test"), Class: ClassDiagnostic, Data: json.RawMessage(`{`)},
		{Type: Type("test"), Class: ClassDiagnostic, Data: json.RawMessage(make([]byte, MaxPayloadBytes+1))},
	}
	for _, entry := range tests {
		if _, err := journal.Append(entry); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("Append(%+v) error = %v, want ErrInvalidEvent", entry, err)
		}
	}
	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if window.LatestSequence != 0 || len(window.Events) != 0 {
		t.Fatalf("journal changed after invalid entries: %+v", window)
	}
}

func TestConcurrentAppendProducesUniqueSequences(t *testing.T) {
	journal := newTestJournal(256, 256)
	const workers = 64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			if _, err := journal.Append(Entry{Type: Type("diagnostic.concurrent"), Class: ClassDiagnostic}); err != nil {
				t.Errorf("Append() error = %v", err)
			}
		}()
	}
	wg.Wait()

	window, err := journal.Query(0, workers)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(window.Events) != workers || window.LatestSequence != workers {
		t.Fatalf("window = %+v", window)
	}
	seen := make(map[Sequence]bool, workers)
	for _, event := range window.Events {
		if seen[event.Sequence] {
			t.Fatalf("duplicate sequence %d", event.Sequence)
		}
		seen[event.Sequence] = true
	}
}

func TestEventPayloadIsCopiedAtJournalBoundaries(t *testing.T) {
	journal := newTestJournal(2, 2)
	entry := Entry{Type: Type("diagnostic.payload"), Class: ClassDiagnostic, Data: json.RawMessage(`{"value":"stored"}`)}
	event, err := journal.Append(entry)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	event.Data[0] = '['
	entry.Data[0] = '['

	window, err := journal.Query(0, 1)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if string(window.Events[0].Data) != `{"value":"stored"}` {
		t.Fatalf("stored payload was mutated: %s", window.Events[0].Data)
	}
	window.Events[0].Data[0] = '['
	again, err := journal.Query(0, 1)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if string(again.Events[0].Data) != `{"value":"stored"}` {
		t.Fatalf("query payload was mutated through a prior window: %s", again.Events[0].Data)
	}
}

func newTestJournal(controlCapacity, diagnosticCapacity int) *Journal {
	journal := New()
	journal.control = newEventRing(controlCapacity)
	journal.diagnostic = newEventRing(diagnosticCapacity)
	journal.now = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }
	return journal
}

func appendEvent(t *testing.T, journal *Journal, eventType Type, class Class) Event {
	t.Helper()
	event, err := journal.Append(Entry{
		Type:      eventType,
		Class:     class,
		SubjectID: "subject-1",
		Data:      json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	return event
}
