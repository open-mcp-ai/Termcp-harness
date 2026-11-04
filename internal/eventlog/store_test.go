package eventlog

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFileStoreRestoresEventsAndSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := NewWithPersistence(store)
	if err != nil {
		t.Fatal(err)
	}
	appendEvent(t, journal, TypeRuntimeStarted, ClassControl)
	appendEvent(t, journal, Type("diagnostic.persisted"), ClassDiagnostic)

	restored, err := NewWithPersistence(store)
	if err != nil {
		t.Fatalf("restore Event Journal: %v", err)
	}
	window, err := restored.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != 2 || window.LatestSequence != 2 {
		t.Fatalf("restored window = %+v", window)
	}
	if window.Events[0].Type != TypeRuntimeStarted ||
		window.Events[1].Type != Type("diagnostic.persisted") {
		t.Fatalf("restored events = %+v", window.Events)
	}

	next := appendEvent(t, restored, TypeRuntimeStopping, ClassControl)
	if next.Sequence != 3 {
		t.Fatalf("next sequence = %d, want 3", next.Sequence)
	}
}

func TestPersistentJournalRestoresRetentionAndControlOverflow(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := newWithPersistence(store, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		appendEvent(t, journal, Type("diagnostic.retained"), ClassDiagnostic)
	}
	appendEvent(t, journal, TypeRuntimeStarted, ClassControl)
	appendEvent(t, journal, TypeRuntimeStopping, ClassControl)
	if _, err := journal.Append(Entry{Type: TypeRuntimeStopped, Class: ClassControl}); !errors.Is(err, ErrControlCapacityExceeded) {
		t.Fatalf("control overflow error = %v", err)
	}

	restored, err := NewWithPersistence(store)
	if err != nil {
		t.Fatalf("restore Event Journal: %v", err)
	}
	window, err := restored.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != 4 || window.LatestSequence != 5 {
		t.Fatalf("restored window = %+v", window)
	}
	if window.DroppedDiagnostics != 1 || window.DiagnosticDroppedThrough != 1 ||
		!window.Truncated || !window.ControlOverflow {
		t.Fatalf("restored retention = %+v", window)
	}
	if window.Events[0].Sequence != 2 || window.Events[1].Sequence != 3 ||
		window.Events[2].Sequence != 4 || window.Events[3].Sequence != 5 {
		t.Fatalf("restored event sequences = %+v", window.Events)
	}
}

func TestPersistentAppendFailureRollsBackMemoryState(t *testing.T) {
	persistence := &memoryPersistence{}
	journal, err := newWithPersistence(persistence, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	first := appendEvent(t, journal, Type("diagnostic.first"), ClassDiagnostic)
	second := appendEvent(t, journal, Type("diagnostic.second"), ClassDiagnostic)
	persistence.setFailure(true)
	if _, err := journal.Append(Entry{Type: Type("diagnostic.failed"), Class: ClassDiagnostic}); err == nil {
		t.Fatal("persistence failure did not fail append")
	}

	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != 1 || window.Events[0].Sequence != second.Sequence ||
		window.LatestSequence != second.Sequence ||
		window.DroppedDiagnostics != 1 || window.DiagnosticDroppedThrough != first.Sequence {
		t.Fatalf("rollback changed journal = %+v", window)
	}

	persistence.setFailure(false)
	third := appendEvent(t, journal, Type("diagnostic.third"), ClassDiagnostic)
	if third.Sequence != 3 {
		t.Fatalf("sequence after rollback = %d, want 3", third.Sequence)
	}
}

func TestPersistentControlAppendFailureRollsBackMemoryState(t *testing.T) {
	persistence := &memoryPersistence{fail: true}
	journal, err := newWithPersistence(persistence, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Append(Entry{Type: TypeRuntimeStarted, Class: ClassControl}); err == nil {
		t.Fatal("persistence failure did not fail control append")
	}
	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if window.LatestSequence != 0 || len(window.Events) != 0 {
		t.Fatalf("control rollback changed journal = %+v", window)
	}

	persistence.setFailure(false)
	first := appendEvent(t, journal, TypeRuntimeStarted, ClassControl)
	if first.Sequence != 1 {
		t.Fatalf("sequence after rollback = %d, want 1", first.Sequence)
	}
}

func TestPersistentControlCapacityFailureKeepsOverflowVisible(t *testing.T) {
	persistence := &memoryPersistence{}
	journal, err := newWithPersistence(persistence, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	appendEvent(t, journal, TypeRuntimeStarted, ClassControl)
	if _, err := journal.Append(Entry{Type: TypeRuntimeStopping, Class: ClassControl}); !errors.Is(err, ErrControlCapacityExceeded) {
		t.Fatalf("overflow error = %v", err)
	}

	restored, err := NewWithPersistence(persistence)
	if err != nil {
		t.Fatal(err)
	}
	window, err := restored.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !window.ControlOverflow || window.LatestSequence != 1 || len(window.Events) != 1 {
		t.Fatalf("restored overflow window = %+v", window)
	}
}

func TestPersistentCorruptStoreFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"state":{"next_sequence":1`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithPersistence(store); err == nil {
		t.Fatal("corrupt Event Journal store was accepted")
	}
}

func TestFileStoreRejectsOversizedState(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.maxBytes = 128
	state := PersistenceState{
		NextSequence:       2,
		ControlCapacity:    1,
		DiagnosticCapacity: 1,
		ControlEvents: []Event{{
			EventID: strings.Repeat("x", 128), Sequence: 1,
			OccurredAt: time.Now(), Type: TypeRuntimeStarted, Class: ClassControl,
		}},
	}
	if err := store.Save(state); err == nil {
		t.Fatal("oversized Event Journal store was accepted")
	}
}

func TestFileStoreCreatesOwnerOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX owner/group/other mode bits")
	}
	path := filepath.Join(t.TempDir(), "events.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(PersistenceState{
		NextSequence: 1, ControlCapacity: 1, DiagnosticCapacity: 1,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("store permissions = %o, want group/other disabled", info.Mode().Perm())
	}
}

func TestConcurrentPersistentAppendsKeepUniqueSequences(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := newWithPersistence(store, 32, 32)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			if _, err := journal.Append(Entry{
				Type: Type("diagnostic.concurrent"), Class: ClassDiagnostic,
			}); err != nil {
				t.Errorf("Append() error = %v", err)
			}
		}()
	}
	wait.Wait()

	restored, err := NewWithPersistence(store)
	if err != nil {
		t.Fatalf("restore Event Journal: %v", err)
	}
	window, err := restored.Query(0, workers)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != workers || window.LatestSequence != workers {
		t.Fatalf("restored window = %+v", window)
	}
	for i, event := range window.Events {
		if event.Sequence != Sequence(i+1) {
			t.Fatalf("event %d sequence = %d", i, event.Sequence)
		}
	}
}

type memoryPersistence struct {
	mu    sync.Mutex
	state *PersistenceState
	fail  bool
}

func (p *memoryPersistence) Load() (*PersistenceState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == nil {
		return nil, nil
	}
	state := clonePersistenceState(*p.state)
	return &state, nil
}

func (p *memoryPersistence) Save(state PersistenceState) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("persistence unavailable")
	}
	cloned := clonePersistenceState(state)
	p.state = &cloned
	return nil
}

func (p *memoryPersistence) setFailure(fail bool) {
	p.mu.Lock()
	p.fail = fail
	p.mu.Unlock()
}
