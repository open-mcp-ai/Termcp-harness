package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestManagerCreatesListsAndRunsSession(t *testing.T) {
	manager, err := NewManager(eventlog.New())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	session, err := manager.Create("session-b", "core-session-2")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if session.Snapshot().State != StateNew {
		t.Fatalf("session = %+v", session.Snapshot())
	}
	if _, err := manager.Create("session-a", "core-session-1"); err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	if _, err := manager.Create("session-a", ""); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate Create() error = %v", err)
	}
	if _, err := manager.Get("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
	}
	list := manager.List()
	if len(list) != 2 || list[0].ID != "session-a" || list[1].ID != "session-b" {
		t.Fatalf("List() = %+v", list)
	}

	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{{
		Call: toolcall.Snapshot{Status: toolcall.StatusSucceeded},
	}}}
	result, err := manager.Run(context.Background(), "session-a", invoker, []Step{{
		CallID: "call-1", ServerID: "core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}})
	if err != nil || result.Snapshot.State != StateCompleted {
		t.Fatalf("Run() = %+v, error = %v", result.Snapshot, err)
	}
	updated, err := manager.Get("session-a")
	if err != nil || updated.Snapshot().State != StateCompleted {
		t.Fatalf("Get(updated) = %+v, error = %v", updated.Snapshot(), err)
	}
}

func TestNewManagerRequiresEventLog(t *testing.T) {
	if _, err := NewManager(nil); err == nil {
		t.Fatal("NewManager(nil) error = nil")
	}
}

func TestManagerRoutesApprovalObservationToWaitingSession(t *testing.T) {
	manager, err := NewManager(eventlog.New())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	session, err := manager.Create("agent-session-1", "core-session-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{{
		Call:     toolcall.Snapshot{Status: toolcall.StatusApprovalPending},
		Approval: &reference,
	}}}
	if _, err := session.Run(context.Background(), invoker, []Step{{
		CallID: "call-1", ServerID: "core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := manager.ObserveApproval("call-1", approval.Snapshot{
		ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	updated, err := manager.Get("agent-session-1")
	if err != nil || updated.Snapshot().State != StateWaitingExecution {
		t.Fatalf("updated = %+v, error = %v", updated.Snapshot(), err)
	}
}

func TestManagerRoutesTrustedToolResultToWaitingExecution(t *testing.T) {
	manager, err := NewManager(eventlog.New())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	session, err := manager.Create("agent-session-1", "core-session-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{{
		Call:     toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusApprovalPending},
		Approval: &reference,
	}}}
	if _, err := session.Run(context.Background(), invoker, []Step{{
		CallID: "call-1", ServerID: "core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":"first"}`),
	}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := session.ObserveApproval("call-1", approval.Snapshot{
		ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	result, err := manager.ObserveToolResult(context.Background(), invoker, toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusSucceeded},
	})
	if err != nil {
		t.Fatalf("ObserveToolResult() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	repeated, err := manager.ObserveToolResult(context.Background(), invoker, toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusSucceeded},
	})
	if err != nil || repeated.Snapshot.Revision != result.Snapshot.Revision {
		t.Fatalf("repeated result = %+v, error = %v", repeated, err)
	}
	if _, err := manager.ObserveToolResult(context.Background(), invoker, toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: "missing", Status: toolcall.StatusSucceeded},
	}); !errors.Is(err, ErrToolResultSessionNotFound) {
		t.Fatalf("missing session error = %v", err)
	}
}

func TestManagerAutomaticallyResumesAfterSucceededExecution(t *testing.T) {
	manager, err := NewManager(eventlog.New())
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Create("agent-session-1", "core-session-1")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{
		{
			Call:     toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusApprovalPending},
			Approval: &reference,
		},
		{Call: toolcall.Snapshot{ID: "call-2", Status: toolcall.StatusSucceeded}},
	}}
	if _, err := session.Run(context.Background(), invoker, []Step{
		{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"write"}`)},
		{CallID: "call-2", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"read"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	if err := manager.ObserveApproval("call-1", approval.Snapshot{
		ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{
			State: approval.ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
		},
	}); err != nil {
		t.Fatal(err)
	}
	waitForManagerState(t, manager, "agent-session-1", StateCompleted)
	if len(invoker.Calls()) != 2 {
		t.Fatalf("invoker calls = %d, want 2", len(invoker.Calls()))
	}
}

func TestApprovalCoordinatorAutomaticallyResumesSession(t *testing.T) {
	journal := eventlog.New()
	manager, err := NewManager(journal)
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Create("agent-session-1", "core-session-1")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{
		{
			Call:     toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusApprovalPending},
			Approval: &reference,
		},
		{Call: toolcall.Snapshot{ID: "call-2", Status: toolcall.StatusSucceeded}},
	}}
	if _, err := session.Run(context.Background(), invoker, []Step{
		{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"write"}`)},
		{CallID: "call-2", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"read"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	client := &fixedDecisionClient{outcome: approval.DecisionOutcome{
		Status: approval.StatusApproved,
		Execution: &approval.Execution{
			State: approval.ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
		},
	}}
	coordinator, err := approval.NewCoordinator(
		journal,
		client,
		approval.WithDecisionObserver(manager),
	)
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := coordinator.Track("call-1", reference)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Decide(
		context.Background(),
		tracked.Request.ID,
		tracked.Request.Revision,
		approval.DecisionApprove,
	); err != nil {
		t.Fatal(err)
	}
	waitForManagerState(t, manager, "agent-session-1", StateCompleted)
	if len(invoker.Calls()) != 2 {
		t.Fatalf("invoker calls = %d, want 2", len(invoker.Calls()))
	}
}

func TestManagerSubmittedExecutionHonorsCompletionRequirement(t *testing.T) {
	for _, test := range []struct {
		name       string
		completion toolpipeline.CompletionRequirement
		completed  bool
	}{
		{"accepted", toolpipeline.CompletionAccepted, true},
		{"completed", toolpipeline.CompletionCompleted, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, err := NewManager(eventlog.New())
			if err != nil {
				t.Fatal(err)
			}
			session, err := manager.Create("agent-session-1", "core-session-1")
			if err != nil {
				t.Fatal(err)
			}
			reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
			if err != nil {
				t.Fatal(err)
			}
			invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{
				{
					Call:     toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusApprovalPending},
					Approval: &reference,
				},
				{Call: toolcall.Snapshot{ID: "call-2", Status: toolcall.StatusSucceeded}},
			}}
			if _, err := session.Run(context.Background(), invoker, []Step{
				{
					CallID: "call-1", ServerID: "core", ToolName: "term",
					Arguments: json.RawMessage(`{"cmd":"write"}`), RequiredCompletion: test.completion,
				},
				{CallID: "call-2", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"read"}`)},
			}); err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			finished := started.Add(time.Second)
			if err := manager.ObserveApproval("call-1", approval.Snapshot{
				ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
				Decision: approval.DecisionApprove, Revision: 3,
				Execution: &approval.Execution{
					State: approval.ExecutionSubmitted, StartedAt: started, FinishedAt: &finished,
				},
			}); err != nil {
				t.Fatal(err)
			}
			if test.completed {
				waitForManagerState(t, manager, "agent-session-1", StateCompleted)
			} else {
				time.Sleep(100 * time.Millisecond)
				current, err := manager.Get("agent-session-1")
				if err != nil {
					t.Fatal(err)
				}
				if current.Snapshot().State != StateWaitingExecution || len(invoker.Calls()) != 1 {
					t.Fatalf("session = %+v, calls = %d", current.Snapshot(), len(invoker.Calls()))
				}
			}
		})
	}
}

type fixedDecisionClient struct {
	outcome approval.DecisionOutcome
	err     error
}

func (f *fixedDecisionClient) Decide(
	context.Context,
	string,
	approval.Decision,
) (approval.DecisionOutcome, error) {
	return f.outcome, f.err
}

func waitForManagerState(t *testing.T, manager *Manager, id string, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, err := manager.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if session.Snapshot().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := manager.Get(id)
	t.Fatalf("session state = %s, want %s", session.Snapshot().State, want)
}
