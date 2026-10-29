package toolpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
)

func TestPipelineExecutesAllowedToolAndRecordsTrace(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText("result-secret")}
	pipeline, journal := newTestPipeline(t, dispatcher, Rule{
		ServerID:  "termcp-core",
		ToolName:  "term",
		Effect:    toolcall.EffectReadOnly,
		Arguments: json.RawMessage(`{"cmd":"argument-secret"}`),
		Reason:    "test read-only term invocation",
	})

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-1",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":"argument-secret"}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusSucceeded || outcome.Call.Attempts != 1 {
		t.Fatalf("outcome call = %+v", outcome.Call)
	}
	if outcome.Result == nil || outcome.Result.IsError {
		t.Fatalf("outcome result = %+v", outcome.Result)
	}
	calls := dispatcher.Calls()
	if len(calls) != 1 || calls[0].ServerID != "termcp-core" || calls[0].Request.Params.Name != "term" {
		t.Fatalf("dispatcher calls = %+v", calls)
	}

	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	want := []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallSucceeded,
	}
	assertEventTypes(t, window.Events, want)
	for _, event := range window.Events {
		if event.SubjectID != "call-1" || event.Class != eventlog.ClassControl {
			t.Fatalf("event identity/class = %+v", event)
		}
		payload := string(event.Data)
		if strings.Contains(payload, "argument-secret") || strings.Contains(payload, "result-secret") {
			t.Fatalf("event leaked arguments or result: %s", payload)
		}
	}
}

func TestPipelineBlocksUnmatchedArgumentsWithoutDispatch(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText("unexpected")}
	pipeline, journal := newTestPipeline(t, dispatcher, Rule{
		ServerID:  "termcp-core",
		ToolName:  "term",
		Effect:    toolcall.EffectReadOnly,
		Arguments: json.RawMessage(`{"cmd":""}`),
	})

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-blocked",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":"rm -rf /"}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusBlocked || outcome.Call.Attempts != 0 {
		t.Fatalf("blocked call = %+v", outcome.Call)
	}
	if len(dispatcher.Calls()) != 0 {
		t.Fatal("blocked call reached the dispatcher")
	}
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	assertEventTypes(t, window.Events, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallBlocked,
	})
}

func TestPipelineMapsTransportErrorToUnknownWithoutRetry(t *testing.T) {
	dispatcher := &fakeDispatcher{err: context.DeadlineExceeded}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-unknown",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Invoke() error = %v, want deadline", err)
	}
	if outcome.Call.Status != toolcall.StatusUnknown || outcome.Call.Attempts != 1 {
		t.Fatalf("unknown call = %+v", outcome.Call)
	}
	if len(dispatcher.Calls()) != 1 {
		t.Fatalf("dispatcher calls = %d, want exactly one non-retried attempt", len(dispatcher.Calls()))
	}
	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	assertEventTypes(t, window.Events, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallUnknown,
	})
}
func TestPipelineTreatsMCPToolErrorAsTrustedFailure(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultError("tool-level failure")}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-failed",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusFailed || outcome.Result == nil || !outcome.Result.IsError {
		t.Fatalf("failed outcome = %+v", outcome)
	}
	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	assertEventTypes(t, window.Events, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallFailed,
	})
}

func TestPipelineTreatsOversizedResultAsUnknown(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText(strings.Repeat("x", 256))}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())
	pipeline.maxResultBytes = 64

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-oversized",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err == nil || !strings.Contains(err.Error(), "result exceeds limit") {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusUnknown {
		t.Fatalf("oversized result status = %q, want unknown", outcome.Call.Status)
	}
	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	last := window.Events[len(window.Events)-1]
	if last.Type != eventlog.TypeToolCallUnknown {
		t.Fatalf("last event = %+v", last)
	}
}

func TestPipelineRejectsInvalidArgumentsBeforePolicyOrDispatch(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText("unexpected")}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())

	if _, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-invalid",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`[]`),
	}); err == nil {
		t.Fatal("Invoke() error = nil")
	}
	if len(dispatcher.Calls()) != 0 {
		t.Fatal("invalid arguments reached the dispatcher")
	}
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(window.Events) != 0 {
		t.Fatalf("events = %+v, want none before proposal", window.Events)
	}
}

func TestPipelinePolicyEvaluationErrorBlocksWithoutDispatch(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText("unexpected")}
	journal := eventlog.New()
	pipeline, err := New(staticPolicy{err: errors.New("policy unavailable")}, dispatcher, journal)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-policy-error",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err == nil || !strings.Contains(err.Error(), "policy unavailable") {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusBlocked {
		t.Fatalf("blocked call = %+v", outcome.Call)
	}
	if len(dispatcher.Calls()) != 0 {
		t.Fatal("policy error reached the dispatcher")
	}
	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	assertEventTypes(t, window.Events, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallBlocked,
	})
}

func TestPipelineMissingResultBecomesUnknown(t *testing.T) {
	dispatcher := &fakeDispatcher{}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-missing-result",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err == nil || !strings.Contains(err.Error(), "returned no result") {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusUnknown || len(dispatcher.Calls()) != 1 {
		t.Fatalf("outcome = %+v, calls = %d", outcome.Call, len(dispatcher.Calls()))
	}
	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	assertEventTypes(t, window.Events, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallUnknown,
	})
}

func TestPipelineReviewPendingIsNotSuccess(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText(
		`{"ok":true,"approved":false,"review_pending":true,"message":"write is awaiting review"}`,
	)}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-pending",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusApprovalPending || outcome.Call.Attempts != 1 {
		t.Fatalf("review pending call = %+v", outcome.Call)
	}
	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	assertEventTypes(t, window.Events, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallApprovalPending,
	})
}

func TestPipelineReviewPendingTERMTextIsNotSuccess(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText(
		"TERM/1 202 Accepted\nPath: write h file /tmp/x --data secret\n\n" +
			"Submitted for review: write 6 bytes to /tmp/x. A human decides it in the Web UI; nothing happens until then.",
	)}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-pending-text",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("review pending call = %+v", outcome.Call)
	}
	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	assertEventTypes(t, window.Events, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallApprovalPending,
	})
}

func TestPipelineBlocksAllowedDecisionWithoutExplicitEffect(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText("unexpected")}
	journal := eventlog.New()
	pipeline, err := New(staticPolicy{decision: PolicyDecision{Allowed: true}}, dispatcher, journal)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-no-effect",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err == nil || !strings.Contains(err.Error(), "explicit effect") {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusBlocked || outcome.Call.Effect != toolcall.EffectUnspecified {
		t.Fatalf("blocked call = %+v", outcome.Call)
	}
	if len(dispatcher.Calls()) != 0 {
		t.Fatal("unspecified-effect call reached the dispatcher")
	}
}

type staticPolicy struct {
	decision PolicyDecision
	err      error
}

func (p staticPolicy) Decide(context.Context, Request) (PolicyDecision, error) {
	return p.decision, p.err
}

type fakeDispatcher struct {
	mu     sync.Mutex
	result *mcp.CallToolResult
	err    error
	calls  []dispatchedCall
}

type dispatchedCall struct {
	ServerID string
	Request  mcp.CallToolRequest
}

func (f *fakeDispatcher) CallTool(_ context.Context, serverID string, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dispatchedCall{ServerID: serverID, Request: request})
	return f.result, f.err
}

func (f *fakeDispatcher) Calls() []dispatchedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dispatchedCall(nil), f.calls...)
}

func allowedRule() Rule {
	return Rule{
		ServerID:  "termcp-core",
		ToolName:  "term",
		Effect:    toolcall.EffectReadOnly,
		Arguments: json.RawMessage(`{"cmd":""}`),
	}
}

func newTestPipeline(t *testing.T, dispatcher Dispatcher, rules ...Rule) (*Pipeline, *eventlog.Journal) {
	t.Helper()
	policy, err := NewAllowlistPolicy(rules...)
	if err != nil {
		t.Fatalf("NewAllowlistPolicy() error = %v", err)
	}
	journal := eventlog.New()
	pipeline, err := New(policy, dispatcher, journal)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return pipeline, journal
}

func assertEventTypes(t *testing.T, events []eventlog.Event, want []eventlog.Type) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(want), events)
	}
	for i, eventType := range want {
		if events[i].Type != eventType {
			t.Fatalf("event %d type = %q, want %q", i, events[i].Type, eventType)
		}
	}
}
