package toolcall

import (
	"errors"
	"sync"
	"testing"
)

func TestHappyPath(t *testing.T) {
	call := newCall(t, EffectReadOnly)

	assertTransition(t, call, 1, EventMarkReady, EvidenceValidationAndPolicy, StatusReady, 2)
	assertTransition(t, call, 2, EventDispatch, EvidenceDispatchContext, StatusDispatched, 3)
	assertTransition(t, call, 3, EventAccept, EvidenceServerAccepted, StatusAccepted, 4)
	assertTransition(t, call, 4, EventStart, EvidenceExecutionProgress, StatusRunning, 5)
	assertTransition(t, call, 5, EventSucceed, EvidenceTrustedResult, StatusSucceeded, 6)

	snapshot := call.Snapshot()
	if snapshot.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", snapshot.Attempts)
	}
	if !snapshot.Status.IsTerminal() {
		t.Fatalf("status %q should be terminal", snapshot.Status)
	}
}

func TestApprovalAndUnknownOutcome(t *testing.T) {
	call := newCall(t, EffectSideEffect)

	assertTransition(t, call, 1, EventRequireApproval, EvidencePolicyRequiresApproval, StatusApprovalPending, 2)
	assertTransition(t, call, 2, EventApprovalAccepted, EvidenceTrustedApproval, StatusReady, 3)
	assertTransition(t, call, 3, EventDispatch, EvidenceDispatchContext, StatusDispatched, 4)
	assertTransition(t, call, 4, EventOutcomeUnknown, EvidenceTransportAmbiguity, StatusUnknown, 5)

	// Late trusted evidence can resolve an ambiguous outcome. A stale revision
	// must not be accepted by accident.
	if _, err := call.Apply(4, EventSucceed, EvidenceTrustedResult); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale Apply() error = %v, want ErrStaleRevision", err)
	}
	assertTransition(t, call, 5, EventSucceed, EvidenceTrustedResult, StatusSucceeded, 6)
}

func TestTransportAmbiguityCannotBecomeFailure(t *testing.T) {
	call := newCall(t, EffectSideEffect)
	assertTransition(t, call, 1, EventMarkReady, EvidenceValidationAndPolicy, StatusReady, 2)
	assertTransition(t, call, 2, EventDispatch, EvidenceDispatchContext, StatusDispatched, 3)

	if _, err := call.Apply(3, EventFail, EvidenceTransportAmbiguity); !errors.Is(err, ErrEvidenceMismatch) {
		t.Fatalf("Apply() error = %v, want ErrEvidenceMismatch", err)
	}
	if got := call.Snapshot(); got.Status != StatusDispatched || got.Revision != 3 {
		t.Fatalf("state changed after rejected evidence: %+v", got)
	}

	assertTransition(t, call, 3, EventOutcomeUnknown, EvidenceTransportAmbiguity, StatusUnknown, 4)
}

func TestCannotSkipValidationOrPolicy(t *testing.T) {
	call := newCall(t, EffectReadOnly)
	if _, err := call.Apply(1, EventDispatch, EvidenceDispatchContext); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Apply() error = %v, want ErrInvalidTransition", err)
	}
	if got := call.Snapshot(); got.Status != StatusProposed || got.Revision != 1 {
		t.Fatalf("state changed after invalid transition: %+v", got)
	}
}

func TestTerminalStateCannotTransition(t *testing.T) {
	call := newCall(t, EffectReadOnly)
	assertTransition(t, call, 1, EventBlock, EvidencePolicyBlocked, StatusBlocked, 2)

	if _, err := call.Apply(2, EventMarkReady, EvidenceValidationAndPolicy); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Apply() error = %v, want ErrInvalidTransition", err)
	}
}

func TestOnlyOneTransitionWinsForSameRevision(t *testing.T) {
	call := newCall(t, EffectReadOnly)
	const workers = 16
	results := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			_, err := call.Apply(1, EventMarkReady, EvidenceValidationAndPolicy)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrStaleRevision) {
			t.Fatalf("concurrent Apply() error = %v, want ErrStaleRevision", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful transitions = %d, want 1", successes)
	}
	if got := call.Snapshot(); got.Status != StatusReady || got.Revision != 2 {
		t.Fatalf("final snapshot = %+v", got)
	}
}

func TestEvaluateAutoRetry(t *testing.T) {
	tests := []struct {
		name     string
		snapshot Snapshot
		policy   RetryPolicy
		context  RetryContext
		want     bool
	}{
		{
			name:     "read only unknown",
			snapshot: Snapshot{Status: StatusUnknown, Effect: EffectReadOnly, Attempts: 1},
			policy:   RetryPolicy{MaxAttempts: 2},
			want:     true,
		},
		{
			name:     "idempotent unknown",
			snapshot: Snapshot{Status: StatusUnknown, Effect: EffectIdempotentWrite, Attempts: 1},
			policy:   RetryPolicy{MaxAttempts: 2},
			want:     true,
		},
		{
			name:     "side effect unknown without safety evidence",
			snapshot: Snapshot{Status: StatusUnknown, Effect: EffectSideEffect, Attempts: 1},
			policy:   RetryPolicy{MaxAttempts: 2},
			want:     false,
		},
		{
			name:     "side effect unknown with idempotency key",
			snapshot: Snapshot{Status: StatusUnknown, Effect: EffectSideEffect, Attempts: 1},
			policy:   RetryPolicy{MaxAttempts: 2},
			context:  RetryContext{IdempotencyKey: "call-123"},
			want:     true,
		},
		{
			name:     "side effect confirmed not executed",
			snapshot: Snapshot{Status: StatusUnknown, Effect: EffectSideEffect, Attempts: 1},
			policy:   RetryPolicy{MaxAttempts: 2},
			context:  RetryContext{OutcomeConfirmedNotExecuted: true},
			want:     true,
		},
		{
			name:     "unspecified effect",
			snapshot: Snapshot{Status: StatusUnknown, Effect: EffectUnspecified, Attempts: 1},
			policy:   RetryPolicy{MaxAttempts: 2},
			want:     false,
		},
		{
			name:     "succeeded is not retryable",
			snapshot: Snapshot{Status: StatusSucceeded, Effect: EffectReadOnly, Attempts: 1},
			policy:   RetryPolicy{MaxAttempts: 2},
			want:     false,
		},
		{
			name:     "budget exhausted",
			snapshot: Snapshot{Status: StatusFailed, Effect: EffectReadOnly, Attempts: 2},
			policy:   RetryPolicy{MaxAttempts: 2},
			want:     false,
		},
		{
			name:     "no dispatched attempt",
			snapshot: Snapshot{Status: StatusUnknown, Effect: EffectReadOnly, Attempts: 0},
			policy:   RetryPolicy{MaxAttempts: 2},
			want:     false,
		},
		{
			name:     "implicit retry disabled",
			snapshot: Snapshot{Status: StatusFailed, Effect: EffectReadOnly, Attempts: 1},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := EvaluateAutoRetry(tt.snapshot, tt.policy, tt.context)
			if decision.Allowed != tt.want {
				t.Fatalf("Allowed = %v, want %v; reason = %q", decision.Allowed, tt.want, decision.Reason)
			}
			if decision.Reason == "" {
				t.Fatal("retry decision reason is empty")
			}
		})
	}
}

func TestNewValidatesRequiredIdentity(t *testing.T) {
	tests := []Spec{
		{},
		{ID: "call", SourceID: "server"},
		{ID: "call", ToolName: "tool"},
		{ID: "call", ToolName: "tool", SourceID: "server", Effect: Effect("invalid")},
	}
	for _, spec := range tests {
		if _, err := New(spec); err == nil {
			t.Fatalf("New(%+v) error = nil, want error", spec)
		}
	}
}

func newCall(t *testing.T, effect Effect) *Call {
	t.Helper()
	call, err := New(Spec{ID: "call-1", ToolName: "shell", SourceID: "mcp:test", Effect: effect})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return call
}

func assertTransition(t *testing.T, call *Call, revision Revision, event Event, evidence Evidence, wantStatus Status, wantRevision Revision) {
	t.Helper()
	snapshot, err := call.Apply(revision, event, evidence)
	if err != nil {
		t.Fatalf("Apply(%d, %s, %s) error = %v", revision, event, evidence, err)
	}
	if snapshot.Status != wantStatus || snapshot.Revision != wantRevision {
		t.Fatalf("snapshot = %+v, want status %s revision %d", snapshot, wantStatus, wantRevision)
	}
}
