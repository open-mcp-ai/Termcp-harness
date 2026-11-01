package approval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

func TestFileStoreRestoresConfirmedApprovalHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	client := &fakeDecisionClient{
		status: StatusApproved,
		execution: &Execution{
			State: ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
		},
	}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		client,
		WithPersistence(store),
	)
	if err != nil {
		t.Fatal(err)
	}
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")
	confirmed, err := coordinator.Decide(
		context.Background(),
		tracked.Request.ID,
		tracked.Request.Revision,
		DecisionApprove,
	)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != StatusApproved {
		t.Fatalf("confirmed = %+v", confirmed)
	}

	restored, err := NewCoordinator(eventlog.New(), &fakeDecisionClient{}, WithPersistence(store))
	if err != nil {
		t.Fatalf("restore coordinator: %v", err)
	}
	approvals, err := restored.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 1 {
		t.Fatalf("restored approvals = %+v", approvals)
	}
	request := approvals[0].Request
	if request.Status != StatusApproved || request.Decision != DecisionApprove ||
		!request.Recovered || request.RecoveryReason != approvalRecoveryReason ||
		request.Execution == nil || request.Execution.State != ExecutionSucceeded {
		t.Fatalf("restored approval = %+v", request)
	}
}

func TestPersistentCoordinatorFailClosedForInflightApprovals(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "approvals.json"))
	if err != nil {
		t.Fatal(err)
	}
	pendingRef, err := TrustedReference("approval-pending", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	submittingRef, err := TrustedReference("approval-submitting", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save([]PersistedApproval{
		{
			Request: Snapshot{
				ID: "approval-pending", CallID: "call-pending", Status: StatusPending,
				Decision: DecisionNone, Revision: 1,
			},
			Reference: pendingRef,
		},
		{
			Request: Snapshot{
				ID: "approval-submitting", CallID: "call-submitting", Status: StatusSubmitting,
				Decision: DecisionApprove, Revision: 2,
			},
			Reference: submittingRef,
		},
	}); err != nil {
		t.Fatal(err)
	}

	client := &fakeDecisionClient{}
	journal := eventlog.New()
	coordinator, err := NewCoordinator(journal, client, WithPersistence(store))
	if err != nil {
		t.Fatalf("restore coordinator: %v", err)
	}
	approvals, err := coordinator.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 2 {
		t.Fatalf("restored approvals = %+v", approvals)
	}
	for _, approval := range approvals {
		if approval.Request.Status != StatusUnknown || !approval.Request.Recovered ||
			approval.Request.RecoveryReason != approvalRecoveryReason {
			t.Fatalf("recovered approval = %+v", approval.Request)
		}
	}
	pending := approvals[0].Request
	if pending.Decision != DecisionNone || pending.Revision != 2 {
		t.Fatalf("recovered pending = %+v", pending)
	}
	submitting := approvals[1].Request
	if submitting.Decision != DecisionApprove || submitting.Revision != 3 {
		t.Fatalf("recovered submitting = %+v", submitting)
	}
	if _, err := coordinator.Decide(
		context.Background(), pending.ID, pending.Revision, DecisionApprove,
	); !errors.Is(err, ErrApprovalNotPending) {
		t.Fatalf("decide recovered request error = %v", err)
	}
	if len(client.Calls()) != 0 {
		t.Fatalf("recovered requests submitted decisions: %+v", client.Calls())
	}
	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != 2 {
		t.Fatalf("recovery events = %+v", window.Events)
	}
	for _, event := range window.Events {
		if event.Type != eventlog.TypeApprovalUnknown {
			t.Fatalf("recovery event type = %q", event.Type)
		}
	}
}

func TestRestoreApprovedApprovalDoesNotAutoReconcileOrNotify(t *testing.T) {
	started := time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	client := &fakeRecoveringDecisionClient{
		fakeDecisionClient: &fakeDecisionClient{},
		lookupOutcome: DecisionOutcome{
			Status: StatusApproved,
			Execution: &Execution{
				State: ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
			},
		},
	}
	reference, err := TrustedReference("approval-1", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryApprovalPersistence{approvals: []PersistedApproval{{
		Request: Snapshot{
			ID: "approval-1", CallID: "call-1", Status: StatusApproved,
			Decision: DecisionApprove, Revision: 3,
			Execution: &Execution{State: ExecutionRunning, StartedAt: started},
		},
		Reference: reference,
	}}}
	observer := &fakeDecisionObserver{}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		client,
		WithPersistence(store),
		WithDecisionObserver(observer),
		WithReconciliation(time.Millisecond, time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if client.lookups != 0 || len(observer.Observations()) != 0 {
		t.Fatalf("restore performed remote/observer work: lookups=%d observations=%+v",
			client.lookups, observer.Observations())
	}

	refreshed, err := coordinator.RefreshExecution(context.Background(), "approval-1")
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.Changed || refreshed.Approval.Request.Execution == nil ||
		refreshed.Approval.Request.Execution.State != ExecutionSucceeded {
		t.Fatalf("manual refresh = %+v", refreshed)
	}
	if client.lookups != 1 || len(observer.Observations()) != 1 {
		t.Fatalf("manual refresh calls: lookups=%d observations=%+v",
			client.lookups, observer.Observations())
	}
}

func TestUnknownApprovalReconcilesThroughReadOnlyLookup(t *testing.T) {
	started := time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	reference, err := TrustedReference("approval-1", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &memoryApprovalPersistence{approvals: []PersistedApproval{{
		Request: Snapshot{
			ID: "approval-1", CallID: "call-1", Status: StatusUnknown,
			Decision: DecisionNone, Revision: 2, Recovered: true,
			RecoveryReason: approvalRecoveryReason,
		},
		Reference: reference,
	}}}
	client := &fakeRecoveringDecisionClient{
		fakeDecisionClient: &fakeDecisionClient{},
		lookupOutcome: DecisionOutcome{
			Status: StatusApproved,
			Execution: &Execution{
				State: ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
			},
		},
	}
	observer := &fakeDecisionObserver{}
	journal := eventlog.New()
	coordinator, err := NewCoordinator(
		journal,
		client,
		WithPersistence(persistence),
		WithDecisionObserver(observer),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := coordinator.ReconcileUnknown(context.Background(), "approval-1")
	if err != nil {
		t.Fatalf("ReconcileUnknown() error = %v", err)
	}
	if !result.Changed || !result.Resolved ||
		result.Approval.Request.Status != StatusApproved ||
		result.Approval.Request.Decision != DecisionApprove ||
		result.Approval.Request.Execution == nil ||
		result.Approval.Request.Execution.State != ExecutionSucceeded {
		t.Fatalf("reconciliation = %+v", result)
	}
	if len(client.Calls()) != 0 || client.lookups != 1 {
		t.Fatalf("decision calls = %d, lookups = %d", len(client.Calls()), client.lookups)
	}
	if len(observer.Observations()) != 1 {
		t.Fatalf("observations = %+v", observer.Observations())
	}
	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range window.Events {
		if event.Type == eventlog.TypeApprovalReconciled {
			found = true
			var data map[string]any
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data["remote_status"] != "approved" || data["changed"] != true ||
				data["resolved"] != true {
				t.Fatalf("reconciliation event = %+v", data)
			}
		}
	}
	if !found {
		t.Fatal("approval reconciled event was not recorded")
	}
}

func TestUnknownApprovalRemotePendingStaysUnknown(t *testing.T) {
	reference, err := TrustedReference("approval-1", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &memoryApprovalPersistence{approvals: []PersistedApproval{{
		Request: Snapshot{
			ID: "approval-1", CallID: "call-1", Status: StatusUnknown,
			Decision: DecisionNone, Revision: 2, Recovered: true,
			RecoveryReason: approvalRecoveryReason,
		},
		Reference: reference,
	}}}
	client := &fakeRecoveringDecisionClient{
		fakeDecisionClient: &fakeDecisionClient{},
		lookupOutcome:      DecisionOutcome{Status: StatusPending},
	}
	observer := &fakeDecisionObserver{}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		client,
		WithPersistence(persistence),
		WithDecisionObserver(observer),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.ReconcileUnknown(context.Background(), "approval-1")
	if err != nil {
		t.Fatalf("ReconcileUnknown() error = %v", err)
	}
	if result.Changed || result.Resolved ||
		result.Approval.Request.Status != StatusUnknown ||
		result.Approval.Request.Revision != 2 {
		t.Fatalf("reconciliation = %+v", result)
	}
	if len(client.Calls()) != 0 || client.lookups != 1 {
		t.Fatalf("decision calls = %d, lookups = %d", len(client.Calls()), client.lookups)
	}
	if len(observer.Observations()) != 0 {
		t.Fatalf("pending reconciliation notified observer: %+v", observer.Observations())
	}
}

func TestReconcileUnknownRejectsOtherStatusesAndLookupFailure(t *testing.T) {
	reference, err := TrustedReference("approval-1", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &memoryApprovalPersistence{approvals: []PersistedApproval{{
		Request: Snapshot{
			ID: "approval-1", CallID: "call-1", Status: StatusApproved,
			Decision: DecisionApprove, Revision: 3,
		},
		Reference: reference,
	}}}
	client := &fakeRecoveringDecisionClient{
		fakeDecisionClient: &fakeDecisionClient{},
		lookupErr:          errors.New("lookup failed"),
	}
	coordinator, err := NewCoordinator(eventlog.New(), client, WithPersistence(persistence))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReconcileUnknown(context.Background(), "approval-1"); !errors.Is(err, ErrApprovalNotUnknown) {
		t.Fatalf("non-unknown reconciliation error = %v", err)
	}
	if client.lookups != 0 {
		t.Fatalf("non-unknown approval performed lookup: %d", client.lookups)
	}

	persistence.approvals[0].Request = Snapshot{
		ID: "approval-1", CallID: "call-1", Status: StatusUnknown,
		Decision: DecisionNone, Revision: 2, Recovered: true,
		RecoveryReason: approvalRecoveryReason,
	}
	coordinator, err = NewCoordinator(eventlog.New(), client, WithPersistence(persistence))
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.ReconcileUnknown(context.Background(), "approval-1")
	if err == nil || result.Approval.Request.Status != StatusUnknown || result.Changed || result.Resolved {
		t.Fatalf("lookup failure result = %+v, error = %v", result, err)
	}
}

func TestUnknownApprovalReconciliationPersistenceFailureKeepsConfirmedResult(t *testing.T) {
	reference, err := TrustedReference("approval-1", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &memoryApprovalPersistence{approvals: []PersistedApproval{{
		Request: Snapshot{
			ID: "approval-1", CallID: "call-1", Status: StatusUnknown,
			Decision: DecisionNone, Revision: 2, Recovered: true,
			RecoveryReason: approvalRecoveryReason,
		},
		Reference: reference,
	}}}
	client := &fakeRecoveringDecisionClient{
		fakeDecisionClient: &fakeDecisionClient{},
		lookupOutcome:      DecisionOutcome{Status: StatusRejected},
	}
	coordinator, err := NewCoordinator(eventlog.New(), client, WithPersistence(persistence))
	if err != nil {
		t.Fatal(err)
	}
	persistence.setFailure(true)
	result, err := coordinator.ReconcileUnknown(context.Background(), "approval-1")
	if !errors.Is(err, ErrApprovalPersistence) {
		t.Fatalf("ReconcileUnknown() error = %v", err)
	}
	if !result.Changed || !result.Resolved ||
		result.Approval.Request.Status != StatusRejected ||
		result.Approval.Request.Decision != DecisionReject {
		t.Fatalf("reconciliation = %+v", result)
	}
	if len(client.Calls()) != 0 {
		t.Fatalf("reconciliation submitted a decision: %+v", client.Calls())
	}
}

func TestApprovalPersistenceFailureAvoidsRemoteDecision(t *testing.T) {
	persistence := &memoryApprovalPersistence{fail: true}
	client := &fakeDecisionClient{status: StatusApproved}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		client,
		WithPersistence(persistence),
	)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := TrustedReference("approval-1", "session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Track("call-1", reference); !errors.Is(err, ErrApprovalPersistence) {
		t.Fatalf("Track() error = %v", err)
	}
	if _, err := coordinator.Get("approval-1"); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("failed track remained in memory: %v", err)
	}

	persistence.setFailure(false)
	tracked, err := coordinator.Track("call-1", reference)
	if err != nil {
		t.Fatal(err)
	}
	persistence.setFailure(true)
	snapshot, err := coordinator.Decide(
		context.Background(),
		tracked.Request.ID,
		tracked.Request.Revision,
		DecisionApprove,
	)
	if !errors.Is(err, ErrApprovalPersistence) {
		t.Fatalf("Decide() error = %v", err)
	}
	if snapshot.Status != StatusUnknown {
		t.Fatalf("snapshot after persistence failure = %+v", snapshot)
	}
	if len(client.Calls()) != 0 {
		t.Fatalf("decision was submitted despite persistence failure: %+v", client.Calls())
	}
}

func TestApprovalPersistenceFailureAfterRemoteConfirmationReturnsError(t *testing.T) {
	persistence := &memoryApprovalPersistence{}
	client := &fakeDecisionClient{
		status:  StatusApproved,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		client,
		WithPersistence(persistence),
	)
	if err != nil {
		t.Fatal(err)
	}
	tracked := trackTestApproval(t, coordinator, "approval-1", "call-1")

	type result struct {
		snapshot Snapshot
		err      error
	}
	done := make(chan result, 1)
	go func() {
		snapshot, err := coordinator.Decide(
			context.Background(),
			tracked.Request.ID,
			tracked.Request.Revision,
			DecisionApprove,
		)
		done <- result{snapshot: snapshot, err: err}
	}()
	<-client.started
	persistence.setFailure(true)
	close(client.release)
	resultValue := <-done
	if !errors.Is(resultValue.err, ErrApprovalPersistence) {
		t.Fatalf("Decide() error = %v", resultValue.err)
	}
	if resultValue.snapshot.Status != StatusApproved {
		t.Fatalf("confirmed snapshot = %+v", resultValue.snapshot)
	}
	if len(client.Calls()) != 1 {
		t.Fatalf("remote decision calls = %d, want 1", len(client.Calls()))
	}
}

func TestFileStoreRejectsOversizedApprovalState(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "approvals.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.maxBytes = 128
	id := strings.Repeat("x", 128)
	reference, err := TrustedReference(id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save([]PersistedApproval{{
		Request: Snapshot{
			ID: id, CallID: "call-1", Status: StatusPending,
			Decision: DecisionNone, Revision: 1,
		},
		Reference: reference,
	}}); err == nil {
		t.Fatal("oversized Approval history store was accepted")
	}
}

func TestApprovalFileStoreCreatesOwnerOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX owner/group/other mode bits")
	}
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(nil); err != nil {
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

func TestApprovalFileStoreRejectsCorruptState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"approvals":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("corrupt Approval history store was accepted")
	}
}

func TestConcurrentApprovalPersistenceKeepsAllEntries(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "approvals.json"))
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinator(
		eventlog.New(),
		&fakeDecisionClient{},
		WithPersistence(store),
	)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := range workers {
		go func(index int) {
			defer wait.Done()
			id := "approval-" + string(rune('a'+index))
			callID := "call-" + string(rune('a'+index))
			reference, err := TrustedReference(id, "session-1", nil)
			if err != nil {
				t.Errorf("TrustedReference() error = %v", err)
				return
			}
			if _, err := coordinator.Track(callID, reference); err != nil {
				t.Errorf("Track() error = %v", err)
			}
		}(index)
	}
	wait.Wait()

	restored, err := NewCoordinator(eventlog.New(), &fakeDecisionClient{}, WithPersistence(store))
	if err != nil {
		t.Fatalf("restore coordinator: %v", err)
	}
	approvals, err := restored.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != workers {
		t.Fatalf("restored approval count = %d, want %d", len(approvals), workers)
	}
}

type memoryApprovalPersistence struct {
	mu        sync.Mutex
	approvals []PersistedApproval
	fail      bool
}

func (p *memoryApprovalPersistence) Load() ([]PersistedApproval, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return clonePersistedApprovals(p.approvals), nil
}

func (p *memoryApprovalPersistence) Save(approvals []PersistedApproval) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("approval persistence unavailable")
	}
	p.approvals = clonePersistedApprovals(approvals)
	return nil
}

func (p *memoryApprovalPersistence) setFailure(fail bool) {
	p.mu.Lock()
	p.fail = fail
	p.mu.Unlock()
}
