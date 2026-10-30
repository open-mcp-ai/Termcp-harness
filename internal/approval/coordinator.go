package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

var (
	ErrApprovalNotFound       = errors.New("approval request not found")
	ErrApprovalNotPending     = errors.New("approval request is not pending")
	ErrApprovalUncorrelated   = errors.New("approval request has no trusted correlation")
	ErrApprovalDuplicate      = errors.New("approval request is already tracked")
	ErrApprovalClientRequired = errors.New("approval decision client is required")
)

const (
	defaultReconcileInterval = time.Second
	defaultReconcileTimeout  = 30 * time.Second
)

// DecisionClient submits a human decision to the authority that owns the
// approval request. It returns only a remotely evidenced state.
type DecisionClient interface {
	Decide(context.Context, string, Decision) (DecisionOutcome, error)
}

// DecisionLookup reads the authority's current decision and execution state
// without replaying the decision or its side effects.
type DecisionLookup interface {
	Lookup(context.Context, string) (DecisionOutcome, error)
}

type DecisionObserver interface {
	ObserveApproval(string, Snapshot) error
}

type CoordinatorOption func(*Coordinator)

func WithDecisionObserver(observer DecisionObserver) CoordinatorOption {
	return func(coordinator *Coordinator) {
		coordinator.observer = observer
	}
}

// WithReconciliation configures read-only execution reconciliation after an
// approval is confirmed. It never resubmits a decision or side effect.
func WithReconciliation(interval, timeout time.Duration) CoordinatorOption {
	return func(coordinator *Coordinator) {
		coordinator.reconcileInterval = interval
		coordinator.reconcileTimeout = timeout
	}
}

// Tracked combines the local approval state machine with the correlation
// metadata that the remote review result supplied.
type Tracked struct {
	Request   Snapshot  `json:"request"`
	Reference Reference `json:"reference"`
}

type trackedRequest struct {
	mu          sync.Mutex
	request     *Request
	reference   Reference
	reconciling bool
}

// Coordinator owns only Harness-local approval coordination. Remote truth still
// comes from DecisionClient; this type never infers a decision from time or text.
type Coordinator struct {
	mu           sync.RWMutex
	byID         map[string]*trackedRequest
	byCall       map[string]string
	journal      *eventlog.Journal
	client       DecisionClient
	lookupClient DecisionLookup
	observer     DecisionObserver

	reconcileInterval time.Duration
	reconcileTimeout  time.Duration
}

func NewCoordinator(
	journal *eventlog.Journal,
	client DecisionClient,
	options ...CoordinatorOption,
) (*Coordinator, error) {
	if journal == nil {
		return nil, errors.New("event log is required for approval coordination")
	}
	if client == nil {
		return nil, ErrApprovalClientRequired
	}
	coordinator := &Coordinator{
		byID:              make(map[string]*trackedRequest),
		byCall:            make(map[string]string),
		journal:           journal,
		client:            client,
		reconcileInterval: defaultReconcileInterval,
		reconcileTimeout:  defaultReconcileTimeout,
	}
	if lookup, ok := client.(DecisionLookup); ok {
		coordinator.lookupClient = lookup
	}
	for _, option := range options {
		if option != nil {
			option(coordinator)
		}
	}
	if coordinator.reconcileInterval <= 0 || coordinator.reconcileTimeout <= 0 {
		return nil, errors.New("approval reconciliation interval and timeout must be positive")
	}
	return coordinator, nil
}

// Track registers one trusted approval reference. Unknown or conflicting
// correlation is deliberately rejected instead of being guessed.
func (c *Coordinator) Track(callID string, reference Reference) (Tracked, error) {
	if c == nil {
		return Tracked{}, errors.New("approval coordinator is required")
	}
	if reference.Correlation != CorrelationTrusted {
		return Tracked{}, fmt.Errorf("%w: correlation is %s", ErrApprovalUncorrelated, reference.Correlation)
	}
	request, err := New(reference.ID, callID)
	if err != nil {
		return Tracked{}, err
	}

	entry := &trackedRequest{
		request:   request,
		reference: cloneReference(reference),
	}
	c.mu.Lock()
	if _, exists := c.byID[reference.ID]; exists {
		c.mu.Unlock()
		return Tracked{}, fmt.Errorf("%w: ID %q", ErrApprovalDuplicate, reference.ID)
	}
	if existingID, exists := c.byCall[callID]; exists {
		c.mu.Unlock()
		return Tracked{}, fmt.Errorf("%w: call %q is already linked to approval %q", ErrApprovalDuplicate, callID, existingID)
	}
	c.byID[reference.ID] = entry
	c.byCall[callID] = reference.ID
	c.mu.Unlock()

	tracked := Tracked{Request: request.Snapshot(), Reference: cloneReference(reference)}
	if err := c.record(eventlog.TypeApprovalTracked, tracked); err != nil {
		c.remove(reference.ID, callID)
		return Tracked{}, err
	}
	return tracked, nil
}

func (c *Coordinator) Get(id string) (Tracked, error) {
	if c == nil {
		return Tracked{}, errors.New("approval coordinator is required")
	}
	entry, err := c.lookup(id)
	if err != nil {
		return Tracked{}, err
	}
	entry.mu.Lock()
	tracked := trackedSnapshot(entry)
	entry.mu.Unlock()
	return tracked, nil
}

func (c *Coordinator) GetByCall(callID string) (Tracked, error) {
	if c == nil {
		return Tracked{}, errors.New("approval coordinator is required")
	}
	c.mu.RLock()
	id := c.byCall[callID]
	c.mu.RUnlock()
	if id == "" {
		return Tracked{}, fmt.Errorf("%w: call %q", ErrApprovalNotFound, callID)
	}
	return c.Get(id)
}

func (c *Coordinator) List() ([]Tracked, error) {
	if c == nil {
		return nil, errors.New("approval coordinator is required")
	}
	c.mu.RLock()
	ids := make([]string, 0, len(c.byID))
	for id := range c.byID {
		ids = append(ids, id)
	}
	c.mu.RUnlock()
	sort.Strings(ids)

	result := make([]Tracked, 0, len(ids))
	for _, id := range ids {
		tracked, err := c.Get(id)
		if err != nil {
			return nil, err
		}
		result = append(result, tracked)
	}
	return result, nil
}

// Decide performs a compare-and-swap check against the caller's observed
// revision, records submitting, then submits exactly one remote decision.
// Transport ambiguity leaves the approval unknown and never retries it.
func (c *Coordinator) Decide(
	ctx context.Context,
	id string,
	expected Revision,
	decision Decision,
) (Snapshot, error) {
	if c == nil {
		return Snapshot{}, errors.New("approval coordinator is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	submitEvent, err := submitEventFor(decision)
	if err != nil {
		return Snapshot{}, err
	}
	entry, err := c.lookup(id)
	if err != nil {
		return Snapshot{}, err
	}

	entry.mu.Lock()
	current := entry.request.Snapshot()
	if current.Status != StatusPending {
		entry.mu.Unlock()
		return current, fmt.Errorf("%w: %s", ErrApprovalNotPending, current.Status)
	}
	submitting, err := entry.request.Apply(expected, submitEvent)
	if err != nil {
		entry.mu.Unlock()
		return submitting, err
	}
	if err := c.record(eventlog.TypeApprovalSubmitting, trackedSnapshot(entry)); err != nil {
		entry.mu.Unlock()
		return submitting, err
	}
	entry.mu.Unlock()

	remote, remoteErr := c.client.Decide(ctx, id, decision)
	if remoteErr != nil {
		if c.lookupClient != nil && ctx.Err() == nil {
			if recovered, lookupErr := c.lookupClient.Lookup(ctx, id); lookupErr == nil {
				return c.confirmRemote(entry, submitting.Revision, decision, recovered)
			}
		}
		return c.markUnknown(entry, submitting.Revision, remoteErr)
	}
	return c.confirmRemote(entry, submitting.Revision, decision, remote)
}

func (c *Coordinator) confirmRemote(
	entry *trackedRequest,
	expected Revision,
	decision Decision,
	remote DecisionOutcome,
) (Snapshot, error) {
	entry.mu.Lock()

	event, eventType, err := confirmationFor(decision, remote.Status)
	if err != nil {
		unknown, stateErr := entry.request.Apply(expected, EventMarkUnknown)
		if stateErr != nil {
			snapshot := entry.request.Snapshot()
			entry.mu.Unlock()
			return snapshot, errors.Join(err, stateErr)
		}
		recordErr := c.recordWithExtra(
			eventlog.TypeApprovalUnknown,
			trackedSnapshot(entry),
			map[string]any{"reason": "contradictory_remote_state"},
		)
		tracked := trackedSnapshot(entry)
		entry.mu.Unlock()
		c.notify(tracked)
		return unknown, errors.Join(err, recordErr)
	}
	confirmed, err := entry.request.Apply(expected, event)
	if err != nil {
		entry.mu.Unlock()
		return confirmed, err
	}
	if remote.Execution != nil {
		if remote.Status != StatusApproved {
			entry.mu.Unlock()
			return confirmed, errors.New("approval execution result requires approved state")
		}
		confirmed, err = entry.request.SetExecution(remote.Execution)
		if err != nil {
			entry.mu.Unlock()
			return confirmed, err
		}
	}
	recordErr := c.record(eventType, trackedSnapshot(entry))
	tracked := trackedSnapshot(entry)
	entry.mu.Unlock()
	c.notify(tracked)
	if confirmed.Status == StatusApproved {
		c.startReconciliation(entry)
	}
	return confirmed, recordErr
}

func (c *Coordinator) markUnknown(entry *trackedRequest, expected Revision, cause error) (Snapshot, error) {
	entry.mu.Lock()
	unknown, err := entry.request.Apply(expected, EventMarkUnknown)
	if err != nil {
		entry.mu.Unlock()
		return unknown, errors.Join(cause, err)
	}
	recordErr := c.record(eventlog.TypeApprovalUnknown, trackedSnapshot(entry))
	tracked := trackedSnapshot(entry)
	entry.mu.Unlock()
	c.notify(tracked)
	return unknown, errors.Join(cause, recordErr)
}

func (c *Coordinator) lookup(id string) (*trackedRequest, error) {
	c.mu.RLock()
	entry := c.byID[id]
	c.mu.RUnlock()
	if entry == nil {
		return nil, fmt.Errorf("%w: %q", ErrApprovalNotFound, id)
	}
	return entry, nil
}

// TrustedCorrelationObserved reports whether at least one approval reference
// has passed the explicit trusted-correlation check. Unknown or conflicting
// references are never registered.
func (c *Coordinator) TrustedCorrelationObserved() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID) > 0
}

func (c *Coordinator) startReconciliation(entry *trackedRequest) {
	if c == nil || entry == nil || c.lookupClient == nil {
		return
	}
	entry.mu.Lock()
	if entry.reconciling || !needsExecutionReconciliation(entry.request.Snapshot()) {
		entry.mu.Unlock()
		return
	}
	entry.reconciling = true
	entry.mu.Unlock()

	go c.reconcileExecution(entry)
}

func (c *Coordinator) reconcileExecution(entry *trackedRequest) {
	defer func() {
		entry.mu.Lock()
		entry.reconciling = false
		entry.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), c.reconcileTimeout)
	defer cancel()

	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			c.recordReconciliationFailure(entry, "reconciliation_timeout", lastErr)
			return
		case <-timer.C:
		}

		done, err := c.refreshExecution(ctx, entry)
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
		}
		if done {
			if err != nil {
				c.recordReconciliationFailure(entry, "reconciliation_conflict", err)
			}
			return
		}
		timer.Reset(c.reconcileInterval)
	}
}

func (c *Coordinator) refreshExecution(ctx context.Context, entry *trackedRequest) (bool, error) {
	entry.mu.Lock()
	current := entry.request.Snapshot()
	approvalID := entry.reference.ID
	entry.mu.Unlock()
	if current.Status != StatusApproved {
		return true, nil
	}

	remote, err := c.lookupClient.Lookup(ctx, approvalID)
	if err != nil {
		return false, err
	}
	if remote.Status != StatusApproved {
		return true, fmt.Errorf("approved approval reconciled to %s", remote.Status)
	}
	if remote.Execution == nil {
		return false, nil
	}

	entry.mu.Lock()
	latest := entry.request.Snapshot()
	if latest.Status != StatusApproved {
		entry.mu.Unlock()
		return true, nil
	}
	if err := ValidateExecutionTransition(latest.Execution, remote.Execution); err != nil {
		entry.mu.Unlock()
		return true, err
	}
	if executionsEqual(latest.Execution, remote.Execution) {
		settled := executionReconciliationComplete(remote.Execution)
		entry.mu.Unlock()
		return settled, nil
	}
	updated, err := entry.request.SetExecution(remote.Execution)
	if err != nil {
		entry.mu.Unlock()
		return true, err
	}
	tracked := trackedSnapshot(entry)
	entry.mu.Unlock()

	recordErr := c.record(eventlog.TypeApprovalExecutionObserved, tracked)
	c.notify(tracked)
	return executionReconciliationComplete(updated.Execution), recordErr
}

func needsExecutionReconciliation(snapshot Snapshot) bool {
	if snapshot.Status != StatusApproved {
		return false
	}
	if snapshot.Execution == nil {
		return true
	}
	return snapshot.Execution.State == ExecutionRunning ||
		snapshot.Execution.State == ExecutionSubmitted ||
		snapshot.Execution.State == ExecutionUnknown
}

func executionReconciliationComplete(execution *Execution) bool {
	return execution != nil && (execution.State == ExecutionSucceeded || execution.State == ExecutionFailed)
}

func executionsEqual(left, right *Execution) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.State != right.State || left.Error != right.Error || !left.StartedAt.Equal(right.StartedAt) {
		return false
	}
	if left.FinishedAt == nil || right.FinishedAt == nil {
		return left.FinishedAt == nil && right.FinishedAt == nil
	}
	return left.FinishedAt.Equal(*right.FinishedAt)
}

func (c *Coordinator) recordReconciliationFailure(entry *trackedRequest, reason string, cause error) {
	if c == nil || entry == nil {
		return
	}
	tracked := trackedSnapshot(entry)
	data, err := json.Marshal(map[string]any{
		"reason":      reason,
		"error_bytes": errorBytes(cause),
	})
	if err != nil {
		return
	}
	_, _ = c.journal.Append(eventlog.Entry{
		Type:          eventlog.TypeApprovalReconcileFailed,
		Class:         eventlog.ClassDiagnostic,
		SubjectID:     tracked.Request.ID,
		CorrelationID: tracked.Request.CallID,
		Data:          data,
	})
}

func errorBytes(err error) int {
	if err == nil {
		return 0
	}
	return len(err.Error())
}

func (c *Coordinator) remove(id, callID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byID, id)
	if c.byCall[callID] == id {
		delete(c.byCall, callID)
	}
}

func (c *Coordinator) record(eventType eventlog.Type, tracked Tracked) error {
	return c.recordWithExtra(eventType, tracked, nil)
}

func (c *Coordinator) recordWithExtra(
	eventType eventlog.Type,
	tracked Tracked,
	extra map[string]any,
) error {
	payload := map[string]any{
		"status":      tracked.Request.Status,
		"decision":    tracked.Request.Decision,
		"revision":    tracked.Request.Revision,
		"call_id":     tracked.Request.CallID,
		"session_id":  tracked.Reference.SessionID,
		"expires_at":  tracked.Reference.ExpiresAt,
		"correlation": tracked.Reference.Correlation,
	}
	if execution := tracked.Request.Execution; execution != nil {
		payload["execution_state"] = execution.State
		payload["execution_started_at"] = execution.StartedAt
		payload["execution_finished_at"] = execution.FinishedAt
		payload["execution_error_bytes"] = len(execution.Error)
	}
	for key, value := range extra {
		payload[key] = value
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode approval event data: %w", err)
	}
	_, err = c.journal.Append(eventlog.Entry{
		Type:          eventType,
		Class:         eventlog.ClassControl,
		SubjectID:     tracked.Request.ID,
		CorrelationID: tracked.Request.CallID,
		Data:          data,
	})
	if err != nil {
		return fmt.Errorf("record %s event: %w", eventType, err)
	}
	return nil
}

func (c *Coordinator) notify(tracked Tracked) {
	if c.observer == nil {
		return
	}
	if err := c.observer.ObserveApproval(tracked.Request.CallID, tracked.Request); err != nil {
		data, encodeErr := json.Marshal(map[string]any{
			"reason": "observer_failed",
		})
		if encodeErr != nil {
			return
		}
		_, _ = c.journal.Append(eventlog.Entry{
			Type:          eventlog.TypeApprovalObserverFailed,
			Class:         eventlog.ClassDiagnostic,
			SubjectID:     tracked.Request.ID,
			CorrelationID: tracked.Request.CallID,
			Data:          data,
		})
	}
}

func trackedSnapshot(entry *trackedRequest) Tracked {
	return Tracked{
		Request:   entry.request.Snapshot(),
		Reference: cloneReference(entry.reference),
	}
}

func cloneReference(reference Reference) Reference {
	var expiresAt *time.Time
	if reference.ExpiresAt != nil {
		value := *reference.ExpiresAt
		expiresAt = &value
	}
	reference.ExpiresAt = expiresAt
	return reference
}

func submitEventFor(decision Decision) (Event, error) {
	switch decision {
	case DecisionApprove:
		return EventSubmitApprove, nil
	case DecisionReject:
		return EventSubmitReject, nil
	default:
		return "", fmt.Errorf("unsupported approval decision %q", decision)
	}
}

func confirmationFor(decision Decision, remote Status) (Event, eventlog.Type, error) {
	switch remote {
	case StatusApproved:
		if decision != DecisionApprove {
			return "", "", fmt.Errorf("%w: requested %s, got approved", ErrDecisionMismatch, decision)
		}
		return EventConfirmApproved, eventlog.TypeApprovalApproved, nil
	case StatusRejected:
		if decision != DecisionReject {
			return "", "", fmt.Errorf("%w: requested %s, got rejected", ErrDecisionMismatch, decision)
		}
		return EventConfirmRejected, eventlog.TypeApprovalRejected, nil
	case StatusExpired:
		return EventExpireConfirmed, eventlog.TypeApprovalExpired, nil
	case StatusCancelled:
		return EventCancelConfirmed, eventlog.TypeApprovalCancelled, nil
	default:
		return "", "", fmt.Errorf("remote approval state %q does not confirm a decision", remote)
	}
}
