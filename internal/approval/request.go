package approval

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Status is separate from the tool call status. In particular, submitting a
// rejection is not the same as the server-confirmed rejected state.
type Status string

const (
	StatusPending    Status = "pending"
	StatusSubmitting Status = "submitting"
	StatusApproved   Status = "approved"
	StatusRejected   Status = "rejected"
	StatusExpired    Status = "expired"
	StatusCancelled  Status = "cancelled"
	StatusUnknown    Status = "unknown"
)

type Decision string

const (
	DecisionNone    Decision = "none"
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
)

type Event string

const (
	EventSubmitApprove   Event = "submit_approve"
	EventSubmitReject    Event = "submit_reject"
	EventConfirmApproved Event = "confirm_approved"
	EventConfirmRejected Event = "confirm_rejected"
	EventMarkUnknown     Event = "mark_unknown"
	EventExpireLocal     Event = "expire_local"
	EventExpireConfirmed Event = "expire_confirmed"
	EventCancelLocal     Event = "cancel_local"
	EventCancelConfirmed Event = "cancel_confirmed"
)

type Revision uint64

var (
	ErrInvalidTransition = errors.New("invalid approval transition")
	ErrDecisionMismatch  = errors.New("approval decision mismatch")
	ErrStaleRevision     = errors.New("stale approval revision")
)

type Snapshot struct {
	ID       string   `json:"id"`
	CallID   string   `json:"call_id"`
	Status   Status   `json:"status"`
	Decision Decision `json:"decision"`
	Revision Revision `json:"revision"`
}

type Request struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

type transitionKey struct {
	from  Status
	event Event
}

var transitions = map[transitionKey]Status{
	{StatusPending, EventSubmitApprove}: StatusSubmitting,
	{StatusPending, EventSubmitReject}:  StatusSubmitting,
	{StatusPending, EventExpireLocal}:   StatusExpired,
	{StatusPending, EventCancelLocal}:   StatusCancelled,

	{StatusSubmitting, EventConfirmApproved}: StatusApproved,
	{StatusSubmitting, EventConfirmRejected}: StatusRejected,
	{StatusSubmitting, EventMarkUnknown}:     StatusUnknown,
	{StatusSubmitting, EventExpireConfirmed}: StatusExpired,
	{StatusSubmitting, EventCancelConfirmed}: StatusCancelled,

	{StatusUnknown, EventConfirmApproved}: StatusApproved,
	{StatusUnknown, EventConfirmRejected}: StatusRejected,
	{StatusUnknown, EventExpireConfirmed}: StatusExpired,
	{StatusUnknown, EventCancelConfirmed}: StatusCancelled,
}

func New(id, callID string) (*Request, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("approval ID is required")
	}
	if strings.TrimSpace(callID) == "" {
		return nil, errors.New("tool call ID is required")
	}

	return &Request{snapshot: Snapshot{
		ID:       id,
		CallID:   callID,
		Status:   StatusPending,
		Decision: DecisionNone,
		Revision: 1,
	}}, nil
}

// Apply performs a compare-and-swap transition so an old decision result
// cannot overwrite a newer approval state.
func (r *Request) Apply(expected Revision, event Event) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if expected != r.snapshot.Revision {
		return r.snapshot, fmt.Errorf("%w: expected %d, current %d", ErrStaleRevision, expected, r.snapshot.Revision)
	}

	next, ok := transitions[transitionKey{from: r.snapshot.Status, event: event}]
	if !ok {
		return r.snapshot, fmt.Errorf("%w: %s + %s", ErrInvalidTransition, r.snapshot.Status, event)
	}

	switch event {
	case EventSubmitApprove, EventConfirmApproved:
		if r.snapshot.Decision == DecisionReject {
			return r.snapshot, fmt.Errorf("%w: requested %s, got approval", ErrDecisionMismatch, r.snapshot.Decision)
		}
		r.snapshot.Decision = DecisionApprove
	case EventSubmitReject, EventConfirmRejected:
		if r.snapshot.Decision == DecisionApprove {
			return r.snapshot, fmt.Errorf("%w: requested %s, got rejection", ErrDecisionMismatch, r.snapshot.Decision)
		}
		r.snapshot.Decision = DecisionReject
	}

	r.snapshot.Status = next
	r.snapshot.Revision++
	return r.snapshot, nil
}

func (r *Request) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

func (s Status) IsTerminal() bool {
	switch s {
	case StatusApproved, StatusRejected, StatusExpired, StatusCancelled:
		return true
	default:
		return false
	}
}
