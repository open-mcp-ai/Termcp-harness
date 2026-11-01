package eventlog

import (
	"context"
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
	TypeRuntimeStarted             Type = "runtime.started"
	TypeRuntimeStopping            Type = "runtime.stopping"
	TypeRuntimeStopped             Type = "runtime.stopped"
	TypeMCPRegistered              Type = "mcp.server.registered"
	TypeMCPConnecting              Type = "mcp.server.connecting"
	TypeMCPConnected               Type = "mcp.server.connected"
	TypeMCPCapabilitiesLoading     Type = "mcp.server.capabilities_loading"
	TypeMCPCapabilitiesLoaded      Type = "mcp.server.capabilities_loaded"
	TypeMCPCapabilitiesStale       Type = "mcp.server.capabilities_stale"
	TypeMCPEnabled                 Type = "mcp.server.enabled"
	TypeMCPDisabled                Type = "mcp.server.disabled"
	TypeMCPDisconnected            Type = "mcp.server.disconnected"
	TypeMCPUnavailable             Type = "mcp.server.unavailable"
	TypeCoreInstanceBound          Type = "core.instance.bound"
	TypeCoreInstanceChanged        Type = "core.instance.changed"
	TypeCoreInstanceUnavailable    Type = "core.instance.unavailable"
	TypeCoreOwnershipConflict      Type = "core.ownership.conflict"
	TypeToolCallProposed           Type = "tool.call.proposed"
	TypeToolCallReady              Type = "tool.call.ready"
	TypeToolCallBlocked            Type = "tool.call.blocked"
	TypeToolCallDispatching        Type = "tool.call.dispatching"
	TypeToolCallApprovalPending    Type = "tool.call.approval_pending"
	TypeToolCallSucceeded          Type = "tool.call.succeeded"
	TypeToolCallFailed             Type = "tool.call.failed"
	TypeToolCallUnknown            Type = "tool.call.outcome_unknown"
	TypeApprovalTracked            Type = "approval.request.tracked"
	TypeApprovalSubmitting         Type = "approval.decision.submitting"
	TypeApprovalApproved           Type = "approval.approved"
	TypeApprovalRejected           Type = "approval.rejected"
	TypeApprovalExpired            Type = "approval.expired"
	TypeApprovalCancelled          Type = "approval.cancelled"
	TypeApprovalUnknown            Type = "approval.outcome_unknown"
	TypeApprovalExecutionObserved  Type = "approval.execution.observed"
	TypeApprovalExecutionRefreshed Type = "approval.execution.refreshed"
	TypeApprovalReconciled         Type = "approval.reconciled"
	TypeApprovalReconcileFailed    Type = "approval.reconciliation.failed"
	TypeApprovalObserverFailed     Type = "approval.observer.failed"
	TypeAgentSessionStarted        Type = "agent.session.started"
	TypeAgentToolStarted           Type = "agent.tool.started"
	TypeAgentToolFinished          Type = "agent.tool.finished"
	TypeAgentModelRequested        Type = "agent.model.requested"
	TypeAgentModelResponded        Type = "agent.model.responded"
	TypeAgentModelFailed           Type = "agent.model.failed"
	TypeAgentSessionWaiting        Type = "agent.session.waiting_approval"
	TypeAgentSessionWaitingExec    Type = "agent.session.waiting_execution"
	TypeAgentApprovalObserved      Type = "agent.session.approval_observed"
	TypeAgentToolResultObserved    Type = "agent.tool.result_observed"
	TypeAgentSessionResumed        Type = "agent.session.resumed"
	TypeAgentSessionResumeFailed   Type = "agent.session.resume_failed"
	TypeAgentSessionCompleted      Type = "agent.session.completed"
	TypeAgentSessionBlocked        Type = "agent.session.blocked"
	TypeAgentSessionFailed         Type = "agent.session.failed"
	TypeAgentSessionCancelled      Type = "agent.session.cancelled"
	TypeAgentSessionExpired        Type = "agent.session.expired"
	TypeAgentSessionUnknown        Type = "agent.session.outcome_unknown"
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

// PersistenceState is the bounded on-disk projection of an Event Journal.
// Sequence is retained across restarts, while old events outside the in-memory
// retention window remain absent and are represented by the visible drop
// metadata.
type PersistenceState struct {
	NextSequence             Sequence `json:"next_sequence"`
	ControlCapacity          int      `json:"control_capacity"`
	DiagnosticCapacity       int      `json:"diagnostic_capacity"`
	ControlEvents            []Event  `json:"control_events"`
	DiagnosticEvents         []Event  `json:"diagnostic_events"`
	DroppedDiagnostics       uint64   `json:"dropped_diagnostics"`
	DiagnosticDroppedThrough Sequence `json:"diagnostic_dropped_through,omitempty"`
	ControlOverflow          bool     `json:"control_overflow"`
}

type Persistence interface {
	Load() (*PersistenceState, error)
	Save(PersistenceState) error
}

type Journal struct {
	mu              sync.RWMutex
	nextSequence    Sequence
	control         eventRing
	diagnostic      eventRing
	droppedDiag     uint64
	droppedThrough  Sequence
	controlOverflow bool
	changed         chan struct{}
	now             func() time.Time
	newID           func() (string, error)
	persistence     Persistence
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
		changed:      make(chan struct{}),
		now:          time.Now,
		newID:        newEventID,
	}
}

func NewWithPersistence(persistence Persistence) (*Journal, error) {
	return newWithPersistence(persistence, DefaultControlCapacity, DefaultDiagnosticCapacity)
}

func newWithPersistence(
	persistence Persistence,
	controlCapacity, diagnosticCapacity int,
) (*Journal, error) {
	if controlCapacity <= 0 || controlCapacity > DefaultControlCapacity {
		return nil, fmt.Errorf("control event capacity must be between 1 and %d", DefaultControlCapacity)
	}
	if diagnosticCapacity <= 0 || diagnosticCapacity > DefaultDiagnosticCapacity {
		return nil, fmt.Errorf("diagnostic event capacity must be between 1 and %d", DefaultDiagnosticCapacity)
	}
	journal := &Journal{
		nextSequence: 1,
		control:      newEventRing(controlCapacity),
		diagnostic:   newEventRing(diagnosticCapacity),
		changed:      make(chan struct{}),
		now:          time.Now,
		newID:        newEventID,
		persistence:  persistence,
	}
	if persistence == nil {
		return journal, nil
	}
	state, err := persistence.Load()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return journal, nil
	}
	if err := journal.restore(*state); err != nil {
		return nil, err
	}
	return journal, nil
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
	previousDroppedDiagnostics := j.droppedDiag
	previousDroppedThrough := j.droppedThrough
	var evicted Event
	var evictedExisting bool
	switch entry.Class {
	case ClassControl:
		if j.control.full() {
			previousOverflow := j.controlOverflow
			j.controlOverflow = true
			if j.persistence != nil {
				if err := j.persistLocked(); err != nil {
					j.controlOverflow = previousOverflow
					return Event{}, fmt.Errorf(
						"%w (persist control overflow marker: %v)",
						ErrControlCapacityExceeded, err,
					)
				}
			}
			return Event{}, ErrControlCapacityExceeded
		}
		j.control.push(storedEvent)
	case ClassDiagnostic:
		evicted, evictedExisting = j.diagnostic.push(storedEvent)
		if evictedExisting {
			j.droppedDiag++
			j.droppedThrough = evicted.Sequence
		}
	}
	j.nextSequence++
	if j.persistence != nil {
		if err := j.persistLocked(); err != nil {
			j.nextSequence--
			switch entry.Class {
			case ClassControl:
				j.control.undoPush(Event{}, false)
			case ClassDiagnostic:
				j.diagnostic.undoPush(evicted, evictedExisting)
				j.droppedDiag = previousDroppedDiagnostics
				j.droppedThrough = previousDroppedThrough
			}
			return Event{}, fmt.Errorf("persist event: %w", err)
		}
	}
	close(j.changed)
	j.changed = make(chan struct{})
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

// Wait blocks until an event is appended or the context is cancelled.
// Callers should query after Wait returns to retrieve every available event.
func (j *Journal) Wait(ctx context.Context) error {
	j.mu.RLock()
	changed := j.changed
	j.mu.RUnlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-changed:
		return nil
	}
}

func (j *Journal) persistLocked() error {
	return j.persistence.Save(PersistenceState{
		NextSequence:             j.nextSequence,
		ControlCapacity:          len(j.control.items),
		DiagnosticCapacity:       len(j.diagnostic.items),
		ControlEvents:            cloneEvents(j.control.all()),
		DiagnosticEvents:         cloneEvents(j.diagnostic.all()),
		DroppedDiagnostics:       j.droppedDiag,
		DiagnosticDroppedThrough: j.droppedThrough,
		ControlOverflow:          j.controlOverflow,
	})
}

func (j *Journal) restore(state PersistenceState) error {
	if err := validatePersistenceState(state); err != nil {
		return fmt.Errorf("restore Event Journal: %w", err)
	}
	j.nextSequence = state.NextSequence
	j.control = newEventRing(state.ControlCapacity)
	j.diagnostic = newEventRing(state.DiagnosticCapacity)
	for _, event := range state.ControlEvents {
		j.control.push(cloneEvent(event))
	}
	for _, event := range state.DiagnosticEvents {
		j.diagnostic.push(cloneEvent(event))
	}
	j.droppedDiag = state.DroppedDiagnostics
	j.droppedThrough = state.DiagnosticDroppedThrough
	j.controlOverflow = state.ControlOverflow
	return nil
}

func validatePersistenceState(state PersistenceState) error {
	if state.NextSequence == 0 {
		return errors.New("next sequence must be positive")
	}
	if state.ControlCapacity <= 0 || state.ControlCapacity > DefaultControlCapacity {
		return fmt.Errorf("control capacity %d is outside 1..%d",
			state.ControlCapacity, DefaultControlCapacity)
	}
	if state.DiagnosticCapacity <= 0 || state.DiagnosticCapacity > DefaultDiagnosticCapacity {
		return fmt.Errorf("diagnostic capacity %d is outside 1..%d",
			state.DiagnosticCapacity, DefaultDiagnosticCapacity)
	}
	if len(state.ControlEvents) > state.ControlCapacity {
		return fmt.Errorf("control event count %d exceeds capacity %d",
			len(state.ControlEvents), state.ControlCapacity)
	}
	if len(state.DiagnosticEvents) > state.DiagnosticCapacity {
		return fmt.Errorf("diagnostic event count %d exceeds capacity %d",
			len(state.DiagnosticEvents), state.DiagnosticCapacity)
	}
	if state.DiagnosticDroppedThrough >= state.NextSequence && state.DiagnosticDroppedThrough != 0 {
		return errors.New("diagnostic dropped-through sequence must be below next sequence")
	}
	if (state.DroppedDiagnostics == 0) != (state.DiagnosticDroppedThrough == 0) {
		return errors.New("diagnostic drop count and dropped-through sequence are inconsistent")
	}
	eventIDs := make(map[string]struct{}, len(state.ControlEvents)+len(state.DiagnosticEvents))
	sequences := make(map[Sequence]struct{}, len(state.ControlEvents)+len(state.DiagnosticEvents))
	if err := validatePersistedEvents(
		state.ControlEvents, ClassControl, state.NextSequence, eventIDs, sequences,
	); err != nil {
		return fmt.Errorf("control events: %w", err)
	}
	if err := validatePersistedEvents(
		state.DiagnosticEvents, ClassDiagnostic, state.NextSequence, eventIDs, sequences,
	); err != nil {
		return fmt.Errorf("diagnostic events: %w", err)
	}
	for _, event := range state.DiagnosticEvents {
		if event.Sequence <= state.DiagnosticDroppedThrough {
			return fmt.Errorf("diagnostic event sequence %d is below dropped-through sequence %d",
				event.Sequence, state.DiagnosticDroppedThrough)
		}
	}
	if state.ControlOverflow && len(state.ControlEvents) != state.ControlCapacity {
		return errors.New("control overflow is set while control retention is not full")
	}
	return nil
}

func validatePersistedEvents(
	events []Event,
	wantClass Class,
	nextSequence Sequence,
	eventIDs map[string]struct{},
	sequences map[Sequence]struct{},
) error {
	var previous Sequence
	for _, event := range events {
		if event.EventID == "" {
			return errors.New("event ID is required")
		}
		if _, exists := eventIDs[event.EventID]; exists {
			return fmt.Errorf("duplicate event ID %q", event.EventID)
		}
		eventIDs[event.EventID] = struct{}{}
		if event.Sequence == 0 || event.Sequence >= nextSequence {
			return fmt.Errorf("event sequence %d is outside 1..%d", event.Sequence, nextSequence-1)
		}
		if event.Sequence <= previous {
			return errors.New("event sequences are not strictly increasing")
		}
		previous = event.Sequence
		if _, exists := sequences[event.Sequence]; exists {
			return fmt.Errorf("duplicate event sequence %d", event.Sequence)
		}
		sequences[event.Sequence] = struct{}{}
		if event.OccurredAt.IsZero() {
			return fmt.Errorf("event %q has no occurrence time", event.EventID)
		}
		if event.Class != wantClass {
			return fmt.Errorf("event %q class %q does not match %q",
				event.EventID, event.Class, wantClass)
		}
		if err := validateEntry(Entry{
			Type:  event.Type,
			Class: event.Class,
			Data:  event.Data,
		}); err != nil {
			return err
		}
	}
	return nil
}

func cloneEvents(events []Event) []Event {
	if events == nil {
		return nil
	}
	cloned := make([]Event, len(events))
	for i, event := range events {
		cloned[i] = cloneEvent(event)
	}
	return cloned
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

func (r *eventRing) undoPush(evicted Event, evictedExisting bool) {
	if evictedExisting {
		previousHead := (r.head - 1 + len(r.items)) % len(r.items)
		r.head = previousHead
		r.items[previousHead] = evicted
		return
	}
	r.count--
	index := (r.head + r.count) % len(r.items)
	r.items[index] = Event{}
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
