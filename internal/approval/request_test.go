package approval

import (
	"errors"
	"testing"
)

func TestRejectSubmissionIsNotRejectedUntilConfirmed(t *testing.T) {
	request := newRequest(t)

	snapshot, err := request.Apply(1, EventSubmitReject)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if snapshot.Status != StatusSubmitting || snapshot.Decision != DecisionReject {
		t.Fatalf("submission snapshot = %+v", snapshot)
	}
	if snapshot.Status.IsTerminal() {
		t.Fatal("submitting rejection must not be terminal")
	}

	snapshot, err = request.Apply(2, EventConfirmRejected)
	if err != nil {
		t.Fatalf("confirm Apply() error = %v", err)
	}
	if snapshot.Status != StatusRejected || snapshot.Decision != DecisionReject {
		t.Fatalf("confirmed snapshot = %+v", snapshot)
	}
}

func TestSubmissionTimeoutIsUnknownThenResolved(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Apply(1, EventSubmitApprove); err != nil {
		t.Fatalf("submit Apply() error = %v", err)
	}
	if _, err := request.Apply(2, EventMarkUnknown); err != nil {
		t.Fatalf("unknown Apply() error = %v", err)
	}

	if got := request.Snapshot(); got.Status != StatusUnknown || got.Status.IsTerminal() {
		t.Fatalf("unknown snapshot = %+v", got)
	}

	snapshot, err := request.Apply(3, EventConfirmApproved)
	if err != nil {
		t.Fatalf("confirm Apply() error = %v", err)
	}
	if snapshot.Status != StatusApproved {
		t.Fatalf("resolved snapshot = %+v", snapshot)
	}
}

func TestConfirmationCannotContradictRequest(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Apply(1, EventSubmitApprove); err != nil {
		t.Fatalf("submit Apply() error = %v", err)
	}

	if _, err := request.Apply(2, EventConfirmRejected); !errors.Is(err, ErrDecisionMismatch) {
		t.Fatalf("Apply() error = %v, want ErrDecisionMismatch", err)
	}
	if got := request.Snapshot(); got.Status != StatusSubmitting || got.Revision != 2 {
		t.Fatalf("state changed after mismatch: %+v", got)
	}
}

func TestStaleApprovalRevisionRejected(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Apply(1, EventSubmitReject); err != nil {
		t.Fatalf("submit Apply() error = %v", err)
	}
	if _, err := request.Apply(1, EventSubmitReject); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale Apply() error = %v, want ErrStaleRevision", err)
	}
}

func TestApprovalTerminalStateCannotTransition(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Apply(1, EventCancelLocal); err != nil {
		t.Fatalf("cancel Apply() error = %v", err)
	}
	if _, err := request.Apply(2, EventSubmitApprove); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Apply() error = %v, want ErrInvalidTransition", err)
	}
}

func TestPendingExpirationAndSubmittingTimeoutAreDistinct(t *testing.T) {
	pending := newRequest(t)
	if snapshot, err := pending.Apply(1, EventExpireLocal); err != nil || snapshot.Status != StatusExpired {
		t.Fatalf("pending expiration = %+v, error = %v", snapshot, err)
	}

	submitting := newRequest(t)
	if _, err := submitting.Apply(1, EventSubmitApprove); err != nil {
		t.Fatalf("submit Apply() error = %v", err)
	}
	if _, err := submitting.Apply(2, EventExpireLocal); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("submitting local expiration error = %v, want ErrInvalidTransition", err)
	}
	if snapshot, err := submitting.Apply(2, EventMarkUnknown); err != nil || snapshot.Status != StatusUnknown {
		t.Fatalf("submitting timeout = %+v, error = %v", snapshot, err)
	}
}

func TestNewValidatesIdentity(t *testing.T) {
	tests := [][2]string{{}, {"approval", ""}, {"", "call"}}
	for _, values := range tests {
		if _, err := New(values[0], values[1]); err == nil {
			t.Fatalf("New(%q, %q) error = nil, want error", values[0], values[1])
		}
	}
}

func newRequest(t *testing.T) *Request {
	t.Helper()
	request, err := New("approval-1", "call-1")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return request
}
