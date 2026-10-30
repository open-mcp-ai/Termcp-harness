package approval

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

func TestCoordinatorTracksOnlyTrustedReferences(t *testing.T) {
	expires := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	reference, err := TrustedReference("approval-1", "session-1", &expires)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	client := &fakeDecisionClient{}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)

	tracked, err := coordinator.Track("call-1", reference)
	if err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if tracked.Request.Status != StatusPending || tracked.Request.CallID != "call-1" || tracked.Reference.ID != "approval-1" {
		t.Fatalf("tracked request = %+v", tracked)
	}
	byCall, err := coordinator.GetByCall("call-1")
	if err != nil || byCall.Request.ID != "approval-1" {
		t.Fatalf("GetByCall() = %+v, error = %v", byCall, err)
	}

	if _, err := coordinator.Track("call-2", UncorrelatedReference()); !errors.Is(err, ErrApprovalUncorrelated) {
		t.Fatalf("uncorrelated Track() error = %v", err)
	}
	if _, err := coordinator.Track("call-2", ConflictingReference()); !errors.Is(err, ErrApprovalUncorrelated) {
		t.Fatalf("conflicting Track() error = %v", err)
	}
	if _, err := coordinator.Track("call-2", reference); !errors.Is(err, ErrApprovalDuplicate) {
		t.Fatalf("duplicate ID Track() error = %v", err)
	}
	if _, err := coordinator.Track("call-1", reference); !errors.Is(err, ErrApprovalDuplicate) {
		t.Fatalf("duplicate call Track() error = %v", err)
	}

	assertJournalTypes(t, journal, []eventlog.Type{eventlog.TypeApprovalTracked})
}

func TestCoordinatorApprovalRequiresRemoteConfirmation(t *testing.T) {
	client := &fakeDecisionClient{status: StatusApproved}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	snapshot, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if snapshot.Status != StatusApproved || snapshot.Decision != DecisionApprove || snapshot.Revision != 3 {
		t.Fatalf("approved snapshot = %+v", snapshot)
	}
	calls := client.Calls()
	if len(calls) != 1 || calls[0].ID != "approval-1" || calls[0].Decision != DecisionApprove {
		t.Fatalf("remote calls = %+v", calls)
	}

	assertJournalTypes(t, journal, []eventlog.Type{
		eventlog.TypeApprovalTracked,
		eventlog.TypeApprovalSubmitting,
		eventlog.TypeApprovalApproved,
	})
}

func TestCoordinatorTransportFailureBecomesUnknownWithoutRetry(t *testing.T) {
	transportErr := errors.New("approval transport lost")
	client := &fakeDecisionClient{err: transportErr}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	snapshot, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove)
	if !errors.Is(err, transportErr) {
		t.Fatalf("Decide() error = %v", err)
	}
	if snapshot.Status != StatusUnknown || snapshot.Decision != DecisionApprove {
		t.Fatalf("unknown snapshot = %+v", snapshot)
	}

	if _, err := coordinator.Decide(context.Background(), tracked.Request.ID, snapshot.Revision, DecisionApprove); !errors.Is(err, ErrApprovalNotPending) {
		t.Fatalf("retry error = %v", err)
	}
	if len(client.Calls()) != 1 {
		t.Fatalf("remote call count = %d, want 1", len(client.Calls()))
	}
	assertJournalTypes(t, journal, []eventlog.Type{
		eventlog.TypeApprovalTracked,
		eventlog.TypeApprovalSubmitting,
		eventlog.TypeApprovalUnknown,
	})
}

func TestCoordinatorRecoversUnknownDecisionByReadOnlyLookup(t *testing.T) {
	transportErr := errors.New("decision response lost")
	base := &fakeDecisionClient{err: transportErr}
	started := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	client := &fakeRecoveringDecisionClient{
		fakeDecisionClient: base,
		lookupOutcome: DecisionOutcome{
			Status: StatusApproved,
			Execution: &Execution{
				State: ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
			},
		},
	}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	snapshot, err := coordinator.Decide(
		context.Background(),
		tracked.Request.ID,
		tracked.Request.Revision,
		DecisionApprove,
	)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if snapshot.Status != StatusApproved ||
		snapshot.Execution == nil ||
		snapshot.Execution.State != ExecutionSucceeded {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(base.Calls()) != 1 || client.lookups != 1 {
		t.Fatalf("decision calls = %d, lookups = %d", len(base.Calls()), client.lookups)
	}
	assertJournalTypes(t, journal, []eventlog.Type{
		eventlog.TypeApprovalTracked,
		eventlog.TypeApprovalSubmitting,
		eventlog.TypeApprovalApproved,
	})
}

func TestCoordinatorReconcilesLateExecutionEvidence(t *testing.T) {
	started := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	running := &Execution{State: ExecutionRunning, StartedAt: started}
	succeeded := &Execution{State: ExecutionSucceeded, StartedAt: started, FinishedAt: &finished}
	client := &fakeLateEvidenceClient{
		fakeDecisionClient: &fakeDecisionClient{status: StatusApproved, execution: running},
		lookups: []DecisionOutcome{
			{Status: StatusApproved, Execution: running},
			{Status: StatusApproved, Execution: succeeded},
		},
	}
	journal := eventlog.New()
	observer := &fakeDecisionObserver{}
	coordinator, err := NewCoordinator(
		journal,
		client,
		WithDecisionObserver(observer),
		WithReconciliation(time.Millisecond, time.Second),
	)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")
	if _, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, observation := range observer.Observations() {
			if observation.Snapshot.Execution != nil && observation.Snapshot.Execution.State == ExecutionSucceeded {
				if len(client.Calls()) != 1 {
					t.Fatalf("decision was resubmitted: calls = %d", len(client.Calls()))
				}
				if client.Lookups() < 2 {
					t.Fatalf("lookup count = %d, want at least 2", client.Lookups())
				}
				window, queryErr := journal.Query(0, 100)
				if queryErr != nil {
					t.Fatalf("journal Query() error = %v", queryErr)
				}
				foundExecutionObservation := false
				for _, event := range window.Events {
					if event.Type == eventlog.TypeApprovalExecutionObserved {
						foundExecutionObservation = true
						break
					}
				}
				if !foundExecutionObservation {
					t.Fatal("late execution observation was not recorded")
				}
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("late execution did not converge: %+v", observer.Observations())
}

func TestCoordinatorContradictoryRemoteStateIsUnknown(t *testing.T) {
	client := &fakeDecisionClient{status: StatusRejected}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	snapshot, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove)
	if !errors.Is(err, ErrDecisionMismatch) {
		t.Fatalf("Decide() error = %v, want ErrDecisionMismatch", err)
	}
	if snapshot.Status != StatusUnknown {
		t.Fatalf("contradictory snapshot = %+v", snapshot)
	}

	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("journal Query() error = %v", err)
	}
	last := window.Events[len(window.Events)-1]
	if last.Type != eventlog.TypeApprovalUnknown {
		t.Fatalf("last event = %+v", last)
	}
	var data map[string]any
	if err := json.Unmarshal(last.Data, &data); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if data["reason"] != "contradictory_remote_state" {
		t.Fatalf("unknown event data = %+v", data)
	}
}

func TestCoordinatorDoesNotExpireRemotePendingFromLocalClock(t *testing.T) {
	expires := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	reference, err := TrustedReference("approval-1", "session-1", &expires)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	client := &fakeDecisionClient{status: StatusApproved}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)
	tracked, err := coordinator.Track("call-1", reference)
	if err != nil {
		t.Fatalf("Track() error = %v", err)
	}

	pending, err := coordinator.Get(tracked.Request.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if pending.Request.Status != StatusPending {
		t.Fatalf("pending request = %+v", pending.Request)
	}
	confirmed, err := coordinator.Decide(context.Background(), tracked.Request.ID, pending.Request.Revision, DecisionApprove)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if confirmed.Status != StatusApproved || len(client.Calls()) != 1 {
		t.Fatalf("confirmed = %+v, remote calls = %d", confirmed, len(client.Calls()))
	}
	assertJournalTypes(t, journal, []eventlog.Type{
		eventlog.TypeApprovalTracked,
		eventlog.TypeApprovalSubmitting,
		eventlog.TypeApprovalApproved,
	})
}

func TestCoordinatorSerializesConcurrentDecisions(t *testing.T) {
	client := &fakeDecisionClient{
		status:  StatusApproved,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	coordinator := newTestCoordinator(t, eventlog.New(), client)
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	type result struct {
		snapshot Snapshot
		err      error
	}
	firstDone := make(chan result, 1)
	go func() {
		snapshot, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove)
		firstDone <- result{snapshot: snapshot, err: err}
	}()
	<-client.started

	second, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionReject)
	if !errors.Is(err, ErrApprovalNotPending) {
		t.Fatalf("concurrent Decide() error = %v", err)
	}
	if second.Status != StatusSubmitting {
		t.Fatalf("concurrent snapshot = %+v, want submitting", second)
	}
	close(client.release)
	first := <-firstDone
	if first.err != nil || first.snapshot.Status != StatusApproved {
		t.Fatalf("first decision = %+v, error = %v", first.snapshot, first.err)
	}
	if len(client.Calls()) != 1 {
		t.Fatalf("remote call count = %d, want 1", len(client.Calls()))
	}
}

func TestCoordinatorAcceptsRemoteExpirationAsTerminal(t *testing.T) {
	client := &fakeDecisionClient{status: StatusExpired}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	snapshot, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionReject)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if snapshot.Status != StatusExpired {
		t.Fatalf("expired snapshot = %+v", snapshot)
	}
	assertJournalTypes(t, journal, []eventlog.Type{
		eventlog.TypeApprovalTracked,
		eventlog.TypeApprovalSubmitting,
		eventlog.TypeApprovalExpired,
	})
}

func TestCoordinatorKeepsApprovalAndExecutionFailureSeparate(t *testing.T) {
	started := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	client := &fakeDecisionClient{
		status: StatusApproved,
		execution: &Execution{
			State:      ExecutionFailed,
			StartedAt:  started,
			FinishedAt: &finished,
			Error:      "listener unavailable",
		},
	}
	journal := eventlog.New()
	coordinator := newTestCoordinator(t, journal, client)
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	snapshot, err := coordinator.Decide(
		context.Background(),
		tracked.Request.ID,
		tracked.Request.Revision,
		DecisionApprove,
	)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if snapshot.Status != StatusApproved ||
		snapshot.Execution == nil ||
		snapshot.Execution.State != ExecutionFailed ||
		snapshot.Execution.Error != "listener unavailable" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("journal Query() error = %v", err)
	}
	last := window.Events[len(window.Events)-1]
	var data map[string]any
	if err := json.Unmarshal(last.Data, &data); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if data["execution_state"] != string(ExecutionFailed) ||
		data["execution_error_bytes"] != float64(len("listener unavailable")) {
		t.Fatalf("event execution data = %+v", data)
	}
}

func TestCoordinatorNotifiesDecisionObserver(t *testing.T) {
	observer := &fakeDecisionObserver{}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		&fakeDecisionClient{status: StatusApproved},
		WithDecisionObserver(observer),
	)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")
	if _, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	observations := observer.Observations()
	if len(observations) != 1 || observations[0].CallID != "call-1" ||
		observations[0].Snapshot.Status != StatusApproved {
		t.Fatalf("observations = %+v", observations)
	}
}

func TestCoordinatorObserverFailureIsDiagnosticOnly(t *testing.T) {
	journal := eventlog.New()
	observer := &fakeDecisionObserver{err: errors.New("observer failed")}
	coordinator, err := NewCoordinator(
		journal,
		&fakeDecisionClient{status: StatusApproved},
		WithDecisionObserver(observer),
	)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")
	snapshot, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove)
	if err != nil || snapshot.Status != StatusApproved {
		t.Fatalf("Decide() = %+v, error = %v", snapshot, err)
	}
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("journal Query() error = %v", err)
	}
	last := window.Events[len(window.Events)-1]
	if last.Type != eventlog.TypeApprovalObserverFailed || last.Class != eventlog.ClassDiagnostic {
		t.Fatalf("last event = %+v", last)
	}
}

func TestCoordinatorUsesRemoteExpirationWithoutLocalInference(t *testing.T) {
	expires := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	reference, err := TrustedReference("approval-1", "session-1", &expires)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	observer := &fakeDecisionObserver{}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		&fakeDecisionClient{status: StatusExpired},
		WithDecisionObserver(observer),
	)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if _, err := coordinator.Track("call-1", reference); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	tracked, err := coordinator.Get("approval-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if tracked.Request.Status != StatusPending {
		t.Fatalf("tracked request = %+v", tracked.Request)
	}
	if len(observer.Observations()) != 0 {
		t.Fatalf("local clock produced observations: %+v", observer.Observations())
	}
	confirmed, err := coordinator.Decide(context.Background(), tracked.Request.ID, tracked.Request.Revision, DecisionApprove)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if confirmed.Status != StatusExpired {
		t.Fatalf("confirmed request = %+v", confirmed)
	}
	observations := observer.Observations()
	if len(observations) != 1 || observations[0].Snapshot.Status != StatusExpired {
		t.Fatalf("observations = %+v", observations)
	}
}

func TestNewCoordinatorValidatesDependencies(t *testing.T) {
	if _, err := NewCoordinator(nil, &fakeDecisionClient{}); err == nil {
		t.Fatal("NewCoordinator(nil client journal) error = nil")
	}
	if _, err := NewCoordinator(eventlog.New(), nil); !errors.Is(err, ErrApprovalClientRequired) {
		t.Fatalf("NewCoordinator(nil client) error = %v", err)
	}
}

type approvalObservation struct {
	CallID   string
	Snapshot Snapshot
}

type fakeDecisionObserver struct {
	mu           sync.Mutex
	observations []approvalObservation
	err          error
}

func (f *fakeDecisionObserver) ObserveApproval(callID string, snapshot Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, approvalObservation{CallID: callID, Snapshot: snapshot})
	return f.err
}

func (f *fakeDecisionObserver) Observations() []approvalObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]approvalObservation(nil), f.observations...)
}

type decisionCall struct {
	ID       string
	Decision Decision
}

type fakeDecisionClient struct {
	mu        sync.Mutex
	status    Status
	execution *Execution
	err       error
	calls     []decisionCall
	started   chan struct{}
	release   chan struct{}
	once      sync.Once
}

type fakeRecoveringDecisionClient struct {
	*fakeDecisionClient
	lookupOutcome DecisionOutcome
	lookupErr     error
	lookups       int
}

func (f *fakeRecoveringDecisionClient) Lookup(context.Context, string) (DecisionOutcome, error) {
	f.lookups++
	return f.lookupOutcome, f.lookupErr
}

type fakeLateEvidenceClient struct {
	*fakeDecisionClient
	mu      sync.Mutex
	lookups []DecisionOutcome
	next    int
}

func (f *fakeLateEvidenceClient) Lookup(context.Context, string) (DecisionOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next >= len(f.lookups) {
		return f.lookups[len(f.lookups)-1], nil
	}
	outcome := f.lookups[f.next]
	f.next++
	return outcome, nil
}

func (f *fakeLateEvidenceClient) Lookups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.next
}

func (f *fakeDecisionClient) Decide(ctx context.Context, id string, decision Decision) (DecisionOutcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, decisionCall{ID: id, Decision: decision})
	status, execution, err := f.status, cloneExecution(f.execution), f.err
	started, release := f.started, f.release
	f.mu.Unlock()

	if started != nil {
		f.once.Do(func() { close(started) })
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return DecisionOutcome{Status: StatusUnknown}, ctx.Err()
		}
	}
	return DecisionOutcome{Status: status, Execution: execution}, err
}

func (f *fakeDecisionClient) Calls() []decisionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]decisionCall(nil), f.calls...)
}

func newTestCoordinator(t *testing.T, journal *eventlog.Journal, client DecisionClient) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(journal, client)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	return coordinator
}

func trackTestApproval(t *testing.T, coordinator *Coordinator, id, callID string) Tracked {
	t.Helper()
	reference, err := TrustedReference(id, "session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	tracked, err := coordinator.Track(callID, reference)
	if err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	return tracked
}

func assertJournalTypes(t *testing.T, journal *eventlog.Journal, want []eventlog.Type) {
	t.Helper()
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("journal Query() error = %v", err)
	}
	if len(window.Events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(window.Events), len(want), window.Events)
	}
	for i, eventType := range want {
		if window.Events[i].Type != eventType {
			t.Fatalf("event %d type = %q, want %q", i, window.Events[i].Type, eventType)
		}
		if window.Events[i].Class != eventlog.ClassControl {
			t.Fatalf("event %d class = %q, want control", i, window.Events[i].Class)
		}
	}
}
