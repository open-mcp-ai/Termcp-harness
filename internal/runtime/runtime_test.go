package runtime

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRuntimeLifecycle(t *testing.T) {
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	rt, err := New("test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	rt.now = func() time.Time { return now }

	initial := rt.Snapshot()
	if initial.State != StateNew {
		t.Fatalf("initial state = %q, want %q", initial.State, StateNew)
	}
	if initial.InstanceID == "" {
		t.Fatal("initial instance ID is empty")
	}

	if err := rt.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	running := rt.Snapshot()
	if running.State != StateRunning {
		t.Fatalf("running state = %q, want %q", running.State, StateRunning)
	}
	if running.StartedAt == nil || !running.StartedAt.Equal(now) {
		t.Fatalf("StartedAt = %v, want %v", running.StartedAt, now)
	}

	now = now.Add(time.Minute)
	if err := rt.MarkStopping(); err != nil {
		t.Fatalf("MarkStopping() error = %v", err)
	}
	if err := rt.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stopped := rt.Snapshot()
	if stopped.State != StateStopped {
		t.Fatalf("stopped state = %q, want %q", stopped.State, StateStopped)
	}
	if stopped.StoppedAt == nil || !stopped.StoppedAt.Equal(now) {
		t.Fatalf("StoppedAt = %v, want %v", stopped.StoppedAt, now)
	}
}

func TestRuntimeRejectsInvalidTransitions(t *testing.T) {
	rt, err := New("test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := rt.MarkStopping(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("MarkStopping() error = %v, want ErrInvalidTransition", err)
	}
	if err := rt.Stop(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Stop() error = %v, want ErrInvalidTransition", err)
	}
	if err := rt.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := rt.Start(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second Start() error = %v, want ErrInvalidTransition", err)
	}
}

func TestSnapshotCopiesTimes(t *testing.T) {
	rt, err := New("test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rt.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	first := rt.Snapshot()
	*first.StartedAt = first.StartedAt.Add(time.Hour)
	second := rt.Snapshot()
	if second.StartedAt.Equal(*first.StartedAt) {
		t.Fatal("mutating a snapshot changed Runtime state")
	}
}

func TestConcurrentSnapshots(t *testing.T) {
	rt, err := New("test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rt.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			if snapshot := rt.Snapshot(); snapshot.State != StateRunning {
				t.Errorf("snapshot state = %q, want %q", snapshot.State, StateRunning)
			}
		}()
	}
	wg.Wait()
}
