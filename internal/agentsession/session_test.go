package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestSessionCompletesDeterministicSteps(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{
		{Call: toolcall.Snapshot{Status: toolcall.StatusSucceeded}},
		{Call: toolcall.Snapshot{Status: toolcall.StatusSucceeded}},
	}}

	result, err := session.Run(context.Background(), invoker, []Step{
		{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":""}`)},
		{CallID: "call-2", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":""}`)},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || len(result.Outcomes) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if len(invoker.Calls()) != 2 {
		t.Fatalf("invoker calls = %d, want 2", len(invoker.Calls()))
	}
	assertSessionEventTypes(t, journal, []eventlog.Type{
		eventlog.TypeAgentSessionStarted,
		eventlog.TypeAgentToolStarted,
		eventlog.TypeAgentToolFinished,
		eventlog.TypeAgentToolStarted,
		eventlog.TypeAgentToolFinished,
		eventlog.TypeAgentSessionCompleted,
	})
}

func TestSessionStopsAtApprovalWithoutRunningNextStep(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{
		{
			Call:     toolcall.Snapshot{Status: toolcall.StatusApprovalPending},
			Approval: &reference,
		},
		{Call: toolcall.Snapshot{Status: toolcall.StatusSucceeded}},
	}}

	result, err := session.Run(context.Background(), invoker, []Step{
		{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":""}`)},
		{CallID: "call-2", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":""}`)},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Snapshot.State != StateWaitingApproval ||
		result.Snapshot.CurrentApprovalID != "approval-1" ||
		result.Snapshot.CurrentCallID != "call-1" ||
		result.Snapshot.FinishedAt != nil {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	if len(invoker.Calls()) != 1 {
		t.Fatalf("invoker calls = %d, want 1", len(invoker.Calls()))
	}
	assertSessionEventTypes(t, journal, []eventlog.Type{
		eventlog.TypeAgentSessionStarted,
		eventlog.TypeAgentToolStarted,
		eventlog.TypeAgentToolFinished,
		eventlog.TypeAgentSessionWaiting,
	})
}

func TestSessionUnknownDoesNotRetry(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	invokeErr := errors.New("transport disconnected")
	invoker := &sequenceInvoker{
		outcomes: []toolpipeline.Outcome{{Call: toolcall.Snapshot{Status: toolcall.StatusUnknown}}},
		errs:     []error{invokeErr},
	}

	result, err := session.Run(context.Background(), invoker, []Step{
		{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":""}`)},
	})
	if !errors.Is(err, invokeErr) {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Snapshot.State != StateUnknown {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	if len(invoker.Calls()) != 1 {
		t.Fatalf("invoker calls = %d, want 1", len(invoker.Calls()))
	}
}

func TestSessionApprovalTrackingFailureBecomesUnknown(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	trackingErr := errors.New("approval call ID is already linked")
	invoker := &sequenceInvoker{
		outcomes: []toolpipeline.Outcome{{
			Call:     toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusApprovalPending},
			Approval: &reference,
		}},
		errs: []error{trackingErr},
	}
	result, err := session.Run(context.Background(), invoker, []Step{{
		CallID: "call-1", ServerID: "core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}})
	if !errors.Is(err, trackingErr) {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Snapshot.State != StateUnknown ||
		result.Snapshot.CurrentApprovalID != "approval-1" ||
		result.Snapshot.FinishedAt == nil {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
}

func TestSessionCancelsBeforeStartingWork(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	invoker := &sequenceInvoker{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := session.Run(ctx, invoker, []Step{{
		CallID: "call-1", ServerID: "core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Snapshot.State != StateCancelled {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	if len(invoker.Calls()) != 0 {
		t.Fatalf("invoker calls = %d, want 0", len(invoker.Calls()))
	}
	assertSessionEventTypes(t, journal, []eventlog.Type{
		eventlog.TypeAgentSessionStarted,
		eventlog.TypeAgentSessionCancelled,
	})
}

func TestSessionObservesApprovedApprovalAsWaitingExecution(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	observed := approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}
	snapshot, err := session.ObserveApproval(callID, observed)
	if err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	if snapshot.State != StateWaitingExecution ||
		snapshot.CurrentApprovalStatus != approval.StatusApproved ||
		snapshot.FinishedAt != nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	assertSessionEventTypes(t, journal, []eventlog.Type{
		eventlog.TypeAgentSessionStarted,
		eventlog.TypeAgentToolStarted,
		eventlog.TypeAgentToolFinished,
		eventlog.TypeAgentSessionWaiting,
		eventlog.TypeAgentApprovalObserved,
		eventlog.TypeAgentSessionWaitingExec,
	})
}

func TestSessionObservesRejectedApprovalAsBlocked(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	observed := approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusRejected,
		Decision: approval.DecisionReject, Revision: 3,
	}
	snapshot, err := session.ObserveApproval(callID, observed)
	if err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	if snapshot.State != StateBlocked || snapshot.CurrentApprovalStatus != approval.StatusRejected || snapshot.FinishedAt == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestSessionUnknownApprovalOutcomeKeepsWaiting(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	observed := approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusUnknown,
		Decision: approval.DecisionApprove, Revision: 3,
	}
	snapshot, err := session.ObserveApproval(callID, observed)
	if err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	if snapshot.State != StateWaitingApproval || snapshot.CurrentApprovalStatus != approval.StatusUnknown {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestSessionApprovalObservationIsIdempotentAndChecksIdentity(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	observed := approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}
	first, err := session.ObserveApproval(callID, observed)
	if err != nil {
		t.Fatalf("first ObserveApproval() error = %v", err)
	}
	second, err := session.ObserveApproval(callID, observed)
	if err != nil {
		t.Fatalf("second ObserveApproval() error = %v", err)
	}
	if second.Revision != first.Revision {
		t.Fatalf("idempotent revision = %d, want %d", second.Revision, first.Revision)
	}
	if _, err := session.ObserveApproval("other-call", observed); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong call error = %v", err)
	}
	mismatched := observed
	mismatched.CallID = "other-call"
	if _, err := session.ObserveApproval(callID, mismatched); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched approval call error = %v", err)
	}
}

func TestSessionApprovalDoesNotRegressAfterConfirmation(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	approved := approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}
	if _, err := session.ObserveApproval(callID, approved); err != nil {
		t.Fatalf("approved ObserveApproval() error = %v", err)
	}

	for _, status := range []approval.Status{
		approval.StatusPending,
		approval.StatusSubmitting,
		approval.StatusUnknown,
		approval.StatusRejected,
		approval.StatusExpired,
		approval.StatusCancelled,
	} {
		observed := approved
		observed.Status = status
		if _, err := session.ObserveApproval(callID, observed); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("ObserveApproval(%s) error = %v", status, err)
		}
		snapshot := session.Snapshot()
		if snapshot.State != StateWaitingExecution ||
			snapshot.CurrentApprovalStatus != approval.StatusApproved {
			t.Fatalf("snapshot after %s = %+v", status, snapshot)
		}
	}
}

func TestSessionRejectsStaleApprovalRevision(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	approved := approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 5,
	}
	if _, err := session.ObserveApproval(callID, approved); err != nil {
		t.Fatalf("approved ObserveApproval() error = %v", err)
	}
	stale := approved
	stale.Status = approval.StatusPending
	stale.Revision = 4
	if _, err := session.ObserveApproval(callID, stale); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("stale ObserveApproval() error = %v", err)
	}
	if snapshot := session.Snapshot(); snapshot.CurrentApprovalStatus != approval.StatusApproved || snapshot.State != StateWaitingExecution {
		t.Fatalf("snapshot after stale observation = %+v", snapshot)
	}
}

func TestSessionApprovedExecutionCanConverge(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	started := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	if _, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{State: approval.ExecutionRunning, StartedAt: started},
	}); err != nil {
		t.Fatalf("running ObserveApproval() error = %v", err)
	}
	snapshot, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{
			State: approval.ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
		},
	})
	if err != nil {
		t.Fatalf("succeeded ObserveApproval() error = %v", err)
	}
	if snapshot.CurrentExecution == nil || snapshot.CurrentExecution.State != approval.ExecutionSucceeded ||
		snapshot.CurrentToolStatus != toolcall.StatusSucceeded || snapshot.State != StateWaitingExecution {
		t.Fatalf("converged snapshot = %+v", snapshot)
	}
}

func TestSessionAcceptedResultDoesNotClaimSuccess(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	if _, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{
			State: approval.ExecutionSubmitted, StartedAt: time.Now().UTC(), FinishedAt: timePtr(time.Now().UTC()),
		},
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	result, err := session.ObserveToolResult(context.Background(), &sequenceInvoker{}, toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: callID, Status: toolcall.StatusAccepted},
	})
	if err != nil {
		t.Fatalf("ObserveToolResult() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	found := false
	for _, event := range window.Events {
		if event.Type != eventlog.TypeAgentToolResultObserved {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if data["tool_status"] != string(toolcall.StatusAccepted) {
			t.Fatalf("tool result status = %v, want accepted", data["tool_status"])
		}
		found = true
	}
	if !found {
		t.Fatal("accepted tool result event missing")
	}
}

func TestAutomaticToolStatusPreservesSubmitted(t *testing.T) {
	if status, ok := automaticToolStatus(approval.ExecutionSubmitted, toolpipeline.CompletionAccepted); !ok || status != toolcall.StatusAccepted {
		t.Fatalf("submitted accepted status = %q, ok = %v", status, ok)
	}
	if _, ok := automaticToolStatus(approval.ExecutionSubmitted, toolpipeline.CompletionCompleted); ok {
		t.Fatal("submitted satisfied completed requirement")
	}
}

func TestCompletedIsTerminal(t *testing.T) {
	if !StateCompleted.IsTerminal() {
		t.Fatal("completed state is not terminal")
	}
}

func TestSessionResumesAfterTrustedToolResult(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{
		{
			Call:     toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusApprovalPending},
			Approval: &reference,
		},
		{Call: toolcall.Snapshot{ID: "call-2", Status: toolcall.StatusSucceeded}},
	}}
	steps := []Step{
		{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"first"}`)},
		{CallID: "call-2", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"second"}`)},
	}

	if _, err := session.Run(context.Background(), invoker, steps); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	if _, err := session.ObserveApproval("call-1", approval.Snapshot{
		ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{
			State: approval.ExecutionSucceeded, StartedAt: started, FinishedAt: &finished,
		},
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}

	observed := toolpipeline.Outcome{Call: toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusSucceeded}}
	result, err := session.ObserveToolResult(context.Background(), invoker, observed)
	if err != nil {
		t.Fatalf("ObserveToolResult() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || result.Snapshot.FinishedAt == nil {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	if len(result.Outcomes) != 2 || result.Outcomes[0].Call.ID != "call-1" ||
		result.Outcomes[1].Call.ID != "call-2" {
		t.Fatalf("outcomes = %+v", result.Outcomes)
	}
	if len(invoker.Calls()) != 2 || invoker.Calls()[1].Request.CallID != "call-2" {
		t.Fatalf("invoker calls = %+v", invoker.Calls())
	}
	assertSessionEventTypes(t, journal, []eventlog.Type{
		eventlog.TypeAgentSessionStarted,
		eventlog.TypeAgentToolStarted,
		eventlog.TypeAgentToolFinished,
		eventlog.TypeAgentSessionWaiting,
		eventlog.TypeAgentApprovalObserved,
		eventlog.TypeAgentSessionWaitingExec,
		eventlog.TypeAgentToolResultObserved,
		eventlog.TypeAgentSessionResumed,
		eventlog.TypeAgentToolStarted,
		eventlog.TypeAgentToolFinished,
		eventlog.TypeAgentSessionCompleted,
	})
}

func TestSessionExecutionFailureStopsWithoutResuming(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	snapshot, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{
			State: approval.ExecutionFailed, StartedAt: started, FinishedAt: &finished,
			Error: "listener unavailable",
		},
	})
	if err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	if snapshot.State != StateFailed ||
		snapshot.CurrentToolStatus != toolcall.StatusFailed ||
		snapshot.CurrentExecution == nil ||
		snapshot.CurrentExecution.Error != "listener unavailable" ||
		snapshot.FinishedAt == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestSessionSubmittedShellInputStillWaitsForResultConsumption(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	snapshot, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{
			State: approval.ExecutionSubmitted, StartedAt: started, FinishedAt: &finished,
		},
	})
	if err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	if snapshot.State != StateWaitingExecution ||
		snapshot.CurrentToolStatus != toolcall.StatusAccepted ||
		snapshot.FinishedAt != nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestSessionApprovedExecutionUnknownCanConvergeLater(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	if _, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	invoker := &sequenceInvoker{}
	unknown := toolpipeline.Outcome{Call: toolcall.Snapshot{ID: callID, Status: toolcall.StatusUnknown}}
	result, err := session.ObserveToolResult(context.Background(), invoker, unknown)
	if err != nil {
		t.Fatalf("ObserveToolResult(unknown) error = %v", err)
	}
	if result.Snapshot.State != StateWaitingExecution ||
		result.Snapshot.CurrentToolStatus != toolcall.StatusUnknown ||
		result.Snapshot.FinishedAt != nil {
		t.Fatalf("unknown snapshot = %+v", result.Snapshot)
	}
	revision := result.Snapshot.Revision
	repeated, err := session.ObserveToolResult(context.Background(), invoker, unknown)
	if err != nil || repeated.Snapshot.Revision != revision {
		t.Fatalf("repeated unknown result = %+v, error = %v", repeated, err)
	}
	if len(invoker.Calls()) != 0 {
		t.Fatalf("invoker calls = %d, want 0", len(invoker.Calls()))
	}

	failed := toolpipeline.Outcome{Call: toolcall.Snapshot{ID: callID, Status: toolcall.StatusFailed}}
	result, err = session.ObserveToolResult(context.Background(), invoker, failed)
	if err != nil {
		t.Fatalf("ObserveToolResult(failed) error = %v", err)
	}
	if result.Snapshot.State != StateFailed || result.Snapshot.CurrentToolStatus != toolcall.StatusFailed ||
		result.Snapshot.FinishedAt == nil {
		t.Fatalf("failed snapshot = %+v", result.Snapshot)
	}
}

func TestSessionRejectsUnapprovedOrMismatchedToolResults(t *testing.T) {
	journal := eventlog.New()
	session, callID := sessionWaitingForApproval(t, journal)
	invoker := &sequenceInvoker{}
	succeeded := toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: callID, Status: toolcall.StatusSucceeded},
	}
	if _, err := session.ObserveToolResult(context.Background(), invoker, succeeded); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("pre-approval result error = %v", err)
	}

	if _, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	mismatched := succeeded
	mismatched.Call.ID = "other-call"
	if _, err := session.ObserveToolResult(context.Background(), invoker, mismatched); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched result error = %v", err)
	}
	inFlight := succeeded
	inFlight.Call.Status = toolcall.StatusRunning
	if _, err := session.ObserveToolResult(context.Background(), invoker, inFlight); err == nil {
		t.Fatal("in-flight status was accepted as a terminal result")
	}
	if len(invoker.Calls()) != 0 || session.Snapshot().State != StateWaitingExecution {
		t.Fatalf("session changed after rejected results: %+v", session.Snapshot())
	}
}

func TestSessionFailedApprovedExecutionDoesNotRunRemainingStep(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{{
		Call:     toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusApprovalPending},
		Approval: &reference,
	}}}
	if _, err := session.Run(context.Background(), invoker, []Step{
		{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"first"}`)},
		{CallID: "call-2", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"second"}`)},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := session.ObserveApproval("call-1", approval.Snapshot{
		ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	result, err := session.ObserveToolResult(context.Background(), invoker, toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: "call-1", Status: toolcall.StatusFailed},
	})
	if err != nil {
		t.Fatalf("ObserveToolResult() error = %v", err)
	}
	if result.Snapshot.State != StateFailed || len(invoker.Calls()) != 1 {
		t.Fatalf("result = %+v, invoker calls = %d", result, len(invoker.Calls()))
	}
}

func TestSessionMapsTrustedNonSuccessStates(t *testing.T) {
	tests := []struct {
		status    toolcall.Status
		wantState State
		eventType eventlog.Type
	}{
		{toolcall.StatusBlocked, StateBlocked, eventlog.TypeAgentSessionBlocked},
		{toolcall.StatusFailed, StateFailed, eventlog.TypeAgentSessionFailed},
		{toolcall.StatusCancelled, StateCancelled, eventlog.TypeAgentSessionCancelled},
		{toolcall.StatusExpired, StateExpired, eventlog.TypeAgentSessionExpired},
	}
	for _, test := range tests {
		t.Run(string(test.status), func(t *testing.T) {
			journal := eventlog.New()
			session := newTestSession(t, journal)
			invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{{
				Call: toolcall.Snapshot{Status: test.status},
			}}}
			result, err := session.Run(context.Background(), invoker, []Step{{
				CallID: "call-1", ServerID: "core", ToolName: "term",
				Arguments: json.RawMessage(`{"cmd":""}`),
			}})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Snapshot.State != test.wantState {
				t.Fatalf("state = %s, want %s", result.Snapshot.State, test.wantState)
			}
			window, err := journal.Query(0, 100)
			if err != nil {
				t.Fatalf("Query() error = %v", err)
			}
			if window.Events[len(window.Events)-1].Type != test.eventType {
				t.Fatalf("last event = %s, want %s", window.Events[len(window.Events)-1].Type, test.eventType)
			}
		})
	}
}

func TestSessionCannotRunTwice(t *testing.T) {
	session := newTestSession(t, eventlog.New())
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{{Call: toolcall.Snapshot{Status: toolcall.StatusSucceeded}}}}
	steps := []Step{{CallID: "call-1", ServerID: "core", ToolName: "term", Arguments: json.RawMessage(`{}`)}}
	if _, err := session.Run(context.Background(), invoker, steps); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if _, err := session.Run(context.Background(), invoker, steps); !errors.Is(err, ErrSessionNotNew) {
		t.Fatalf("second Run() error = %v", err)
	}
	if len(invoker.Calls()) != 1 {
		t.Fatalf("invoker calls = %d, want 1", len(invoker.Calls()))
	}
}

func TestSessionRejectsUnknownCompletionRequirementBeforeInvoke(t *testing.T) {
	session := newTestSession(t, eventlog.New())
	invoker := &sequenceInvoker{}
	result, err := session.Run(context.Background(), invoker, []Step{{
		CallID: "call-1", ServerID: "core", ToolName: "term",
		Arguments:          json.RawMessage(`{"cmd":"write"}`),
		RequiredCompletion: "eventually",
	}})
	if err == nil {
		t.Fatal("unknown completion requirement was accepted")
	}
	if result.Snapshot.State != StateFailed || len(invoker.Calls()) != 0 {
		t.Fatalf("snapshot = %+v, calls = %d", result.Snapshot, len(invoker.Calls()))
	}
}

func TestNewSessionValidatesInputs(t *testing.T) {
	if _, err := New("", "", eventlog.New()); err == nil {
		t.Fatal("empty session ID was accepted")
	}
	if _, err := New("session-1", "", nil); err == nil {
		t.Fatal("nil journal was accepted")
	}
	session := newTestSession(t, eventlog.New())
	if _, err := session.Run(context.Background(), nil, []Step{{CallID: "call-1"}}); !errors.Is(err, ErrInvokerRequired) {
		t.Fatalf("nil invoker error = %v", err)
	}
	if _, err := session.Run(context.Background(), &sequenceInvoker{}, nil); !errors.Is(err, ErrNoSteps) {
		t.Fatalf("empty steps error = %v", err)
	}
}

type invocation struct {
	Request toolpipeline.Request
}

type sequenceInvoker struct {
	mu       sync.Mutex
	outcomes []toolpipeline.Outcome
	errs     []error
	calls    []invocation
}

func (s *sequenceInvoker) Invoke(_ context.Context, request toolpipeline.Request) (toolpipeline.Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := len(s.calls)
	s.calls = append(s.calls, invocation{Request: request})
	if index >= len(s.outcomes) {
		return toolpipeline.Outcome{}, errors.New("unexpected invocation")
	}
	var err error
	if index < len(s.errs) {
		err = s.errs[index]
	}
	return s.outcomes[index], err
}

func (s *sequenceInvoker) Calls() []invocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]invocation(nil), s.calls...)
}

func newTestSession(t *testing.T, journal *eventlog.Journal) *Session {
	t.Helper()
	session, err := New("agent-session-1", "core-session-1", journal)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return session
}

func sessionWaitingForApproval(t *testing.T, journal *eventlog.Journal) (*Session, string) {
	t.Helper()
	session := newTestSession(t, journal)
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	invoker := &sequenceInvoker{outcomes: []toolpipeline.Outcome{{
		Call:     toolcall.Snapshot{Status: toolcall.StatusApprovalPending},
		Approval: &reference,
	}}}
	const callID = "call-1"
	result, err := session.Run(context.Background(), invoker, []Step{{
		CallID: callID, ServerID: "core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}})
	if err != nil || result.Snapshot.State != StateWaitingApproval {
		t.Fatalf("session setup = %+v, error = %v", result.Snapshot, err)
	}
	return session, callID
}

func assertSessionEventTypes(t *testing.T, journal *eventlog.Journal, want []eventlog.Type) {
	t.Helper()
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(window.Events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(window.Events), len(want), window.Events)
	}
	for i, eventType := range want {
		if window.Events[i].Type != eventType {
			t.Fatalf("event %d = %s, want %s", i, window.Events[i].Type, eventType)
		}
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
