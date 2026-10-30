package eventlog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	DefaultControlCapacity    = 4096
	DefaultDiagnosticCapacity = 8192
	MaxPayloadBytes           = 64 << 10
)

type Class string

const (
	ClassControl    Class = "control"
	ClassDiagnostic Class = "diagnostic"
)

type Type string

const (
	TypeRuntimeStarted            Type = "runtime.started"
	TypeRuntimeStopping           Type = "runtime.stopping"
	TypeRuntimeStopped            Type = "runtime.stopped"
	TypeMCPRegistered             Type = "mcp.server.registered"
	TypeMCPConnecting             Type = "mcp.server.connecting"
	TypeMCPConnected              Type = "mcp.server.connected"
	TypeMCPCapabilitiesLoading    Type = "mcp.server.capabilities_loading"
	TypeMCPCapabilitiesLoaded     Type = "mcp.server.capabilities_loaded"
	TypeMCPCapabilitiesStale      Type = "mcp.server.capabilities_stale"
	TypeMCPEnabled                Type = "mcp.server.enabled"
	TypeMCPDisabled               Type = "mcp.server.disabled"
	TypeMCPDisconnected           Type = "mcp.server.disconnected"
	TypeMCPUnavailable            Type = "mcp.server.unavailable"
	TypeCoreInstanceBound         Type = "core.instance.bound"
	TypeCoreInstanceChanged       Type = "core.instance.changed"
	TypeCoreInstanceUnavailable   Type = "core.instance.unavailable"
	TypeCoreOwnershipConflict     Type = "core.ownership.conflict"
	TypeToolCallProposed          Type = "tool.call.proposed"
	TypeToolCallReady             Type = "tool.call.ready"
	TypeToolCallBlocked           Type = "tool.call.blocked"
	TypeToolCallDispatching       Type = "tool.call.dispatching"
	TypeToolCallApprovalPending   Type = "tool.call.approval_pending"
	TypeToolCallSucceeded         Type = "tool.call.succeeded"
	TypeToolCallFailed            Type = "tool.call.failed"
	TypeToolCallUnknown           Type = "tool.call.outcome_unknown"
	TypeApprovalTracked           Type = "approval.request.tracked"
	TypeApprovalSubmitting        Type = "approval.decision.submitting"
	TypeApprovalApproved          Type = "approval.approved"
	TypeApprovalRejected          Type = "approval.rejected"
	TypeApprovalExpired           Type = "approval.expired"
	TypeApprovalCancelled         Type = "approval.cancelled"
	TypeApprovalUnknown           Type = "approval.outcome_unknown"
	TypeApprovalExecutionObserved Type = "approval.execution.observed"
	TypeApprovalReconcileFailed   Type = "approval.reconciliation.failed"
	TypeApprovalObserverFailed    Type = "approval.observer.failed"
	TypeAgentSessionStarted       Type = "agent.session.started"
	TypeAgentToolStarted          Type = "agent.tool.started"
	TypeAgentToolFinished         Type = "agent.tool.finished"
	TypeAgentSessionWaiting       Type = "agent.session.waiting_approval"
	TypeAgentSessionWaitingExec   Type = "agent.session.waiting_execution"
	TypeAgentApprovalObserved     Type = "agent.session.approval_observed"
	TypeAgentToolResultObserved   Type = "agent.tool.result_observed"
	TypeAgentSessionResumed       Type = "agent.session.resumed"
	TypeAgentSessionResumeFailed  Type = "agent.session.resume_failed"
	TypeAgentSessionCompleted     Type = "agent.session.completed"
	TypeAgentSessionBlocked       Type = "agent.session.blocked"
	TypeAgentSessionFailed        Type = "agent.session.failed"
	TypeAgentSessionCancelled     Type = "agent.session.cancelled"
	TypeAgentSessionExpired       Type = "agent.session.expired"
	TypeAgentSessionUnknown       Type = "agent.session.outcome_unknown"
)

type Sequence uint64

var (
	ErrControlCapacityExceeded = errors.New("control event capacity exceeded")
	ErrInvalidEvent            = errors.New("invalid event")
)

type Entry struct {
	Type          Type
	Class         Class
	SubjectID     string
	CorrelationID string
	Data          json.RawMessage
}

type Event struct {
	EventID       string          `json:"event_id"`
	Sequence      Sequence        `json:"sequence"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Type          Type            `json:"type"`
	Class         Class           `json:"class"`
	SubjectID     string          `json:"subject_id,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

type Window struct {
	Events                   []Event  `json:"events"`
	LatestSequence           Sequence `json:"latest_sequence"`
	DroppedDiagnostics       uint64   `json:"dropped_diagnostics"`
	DiagnosticDroppedThrough Sequence `json:"diagnostic_dropped_through,omitempty"`
	Truncated                bool     `json:"truncated"`
	HasMore                  bool     `json:"has_more"`
	ControlOverflow          bool     `json:"control_overflow"`
}

type Journal struct {
	mu              sync.RWMutex
	nextSequence    Sequence
	control         eventRing
	diagnostic      eventRing
	droppedDiag     uint64
	droppedThrough  Sequence
	controlOverflow bool
	now             func() time.Time
	newID           func() (string, error)
}

type eventRing struct {
	items []Event
	head  int
	count int
}

func New() *Journal {
	return &Journal{
		nextSequence: 1,
		control:      newEventRing(DefaultControlCapacity),
		diagnostic:   newEventRing(DefaultDiagnosticCapacity),
		now:          time.Now,
		newID:        newEventID,
	}
}

func (j *Journal) Append(entry Entry) (Event, error) {
	if err := validateEntry(entry); err != nil {
		return Event{}, err
	}

	eventID, err := j.newID()
	if err != nil {
		return Event{}, fmt.Errorf("create event ID: %w", err)
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	event := Event{
		EventID:       eventID,
		Sequence:      j.nextSequence,
		OccurredAt:    j.now().UTC(),
		Type:          entry.Type,
		Class:         entry.Class,
		SubjectID:     entry.SubjectID,
		CorrelationID: entry.CorrelationID,
		Data:          append(json.RawMessage(nil), entry.Data...),
	}

	storedEvent := cloneEvent(event)
	switch entry.Class {
	case ClassControl:
		if j.control.full() {
			j.controlOverflow = true
			return Event{}, ErrControlCapacityExceeded
		}
		j.control.push(storedEvent)
	case ClassDiagnostic:
		evicted, ok := j.diagnostic.push(storedEvent)
		if ok {
			j.droppedDiag++
			j.droppedThrough = evicted.Sequence
		}
	}
	j.nextSequence++
	return cloneEvent(event), nil
}

func (j *Journal) Query(after Sequence, limit int) (Window, error) {
	if limit <= 0 {
		return Window{}, errors.New("event query limit must be positive")
	}

	j.mu.RLock()
	defer j.mu.RUnlock()

	all := make([]Event, 0, j.control.count+j.diagnostic.count)
	all = append(all, j.control.all()...)
	all = append(all, j.diagnostic.all()...)
	sort.Slice(all, func(i, k int) bool { return all[i].Sequence < all[k].Sequence })

	start := sort.Search(len(all), func(i int) bool { return all[i].Sequence > after })
	end := start + limit
	if end > len(all) {
		end = len(all)
	}
	events := make([]Event, end-start)
	for i, event := range all[start:end] {
		events[i] = cloneEvent(event)
	}
	if events == nil {
		events = []Event{}
	}

	return Window{
		Events:                   events,
		LatestSequence:           j.nextSequence - 1,
		DroppedDiagnostics:       j.droppedDiag,
		DiagnosticDroppedThrough: j.droppedThrough,
		Truncated:                after < j.droppedThrough,
		HasMore:                  end < len(all),
		ControlOverflow:          j.controlOverflow,
	}, nil
}

func validateEntry(entry Entry) error {
	if entry.Type == "" {
		return fmt.Errorf("%w: event type is required", ErrInvalidEvent)
	}
	if entry.Class != ClassControl && entry.Class != ClassDiagnostic {
		return fmt.Errorf("%w: event class %q is invalid", ErrInvalidEvent, entry.Class)
	}
	if len(entry.Data) > MaxPayloadBytes {
		return fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidEvent, MaxPayloadBytes)
	}
	if len(entry.Data) != 0 && !json.Valid(entry.Data) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrInvalidEvent)
	}
	return nil
}

func newEventRing(capacity int) eventRing {
	return eventRing{items: make([]Event, capacity)}
}

func (r *eventRing) full() bool {
	return r.count == len(r.items)
}

func (r *eventRing) push(event Event) (Event, bool) {
	if r.count < len(r.items) {
		index := (r.head + r.count) % len(r.items)
		r.items[index] = event
		r.count++
		return Event{}, false
	}

	evicted := r.items[r.head]
	r.items[r.head] = event
	r.head = (r.head + 1) % len(r.items)
	return evicted, true
}

func (r *eventRing) all() []Event {
	events := make([]Event, r.count)
	for i := range r.count {
		events[i] = r.items[(r.head+i)%len(r.items)]
	}
	return events
}

func cloneEvent(event Event) Event {
	event.Data = append(json.RawMessage(nil), event.Data...)
	return event
}

func newEventID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
