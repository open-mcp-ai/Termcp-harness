package toolpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
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
	if outcome.Approval == nil || outcome.Approval.Correlation != approval.CorrelationUnknown || outcome.Approval.ID != "" {
		t.Fatalf("approval reference = %+v, want unknown without an explicit ID", outcome.Approval)
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
	if outcome.Approval == nil || outcome.Approval.Correlation != approval.CorrelationUnknown {
		t.Fatalf("TERM review reference = %+v, want unknown correlation", outcome.Approval)
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

func TestPipelineReviewPendingCapturesTrustedTERMHeaders(t *testing.T) {
	const expiresAt = int64(1793300000000)
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText(
		"TERM/1 202 Accepted\n" +
			"Path: write session-1 file /tmp/x --data hi\n" +
			"Approval-ID: approval-term\n" +
			"Session-ID: session-1\n" +
			"Expires-At: 1793300000000\n\n" +
			"Submitted for review: write 2 bytes to /tmp/x",
	)}
	tracker := &fakeApprovalTracker{}
	pipeline, _ := newTestPipelineWithOptions(
		t,
		dispatcher,
		[]Rule{allowedRule()},
		WithApprovalTracker(tracker),
	)
	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID: "call-review-terM-correlation", ServerID: "termcp-core",
		ToolName: "term", Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusApprovalPending ||
		outcome.Approval == nil ||
		outcome.Approval.Correlation != approval.CorrelationTrusted ||
		outcome.Approval.ID != "approval-term" ||
		outcome.Approval.SessionID != "session-1" ||
		outcome.Approval.ExpiresAt == nil ||
		outcome.Approval.ExpiresAt.UnixMilli() != expiresAt ||
		!strings.HasPrefix(outcome.Approval.Summary, "Submitted for review:") ||
		strings.Contains(outcome.Approval.Summary, "Approval-ID:") ||
		strings.Contains(outcome.Approval.Summary, "A human decides") ||
		strings.Contains(outcome.Approval.Summary, "Use approval_id") {
		t.Fatalf("outcome = %+v", outcome)
	}
	calls := tracker.Calls()
	if len(calls) != 1 || calls[0].Reference.ID != "approval-term" {
		t.Fatalf("tracker calls = %+v", calls)
	}
}

func TestPipelineReviewPendingCapturesTrustedApprovalReference(t *testing.T) {
	const expiresAt = int64(1793300000000)
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultStructured(map[string]any{
		"ok":             true,
		"approved":       false,
		"review_pending": true,
		"message":        "write is awaiting review",
		"approval_id":    "approval-123",
		"session_id":     "session-456",
		"expires_at":     expiresAt,
	}, "write is awaiting review")}
	pipeline, journal := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-correlated",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Approval == nil {
		t.Fatal("approval reference is nil")
	}
	if outcome.Approval.Correlation != approval.CorrelationTrusted || outcome.Approval.ID != "approval-123" || outcome.Approval.SessionID != "session-456" {
		t.Fatalf("approval reference = %+v", outcome.Approval)
	}
	if outcome.Approval.ExpiresAt == nil || outcome.Approval.ExpiresAt.UnixMilli() != expiresAt {
		t.Fatalf("approval expiry = %+v, want %d", outcome.Approval.ExpiresAt, expiresAt)
	}

	window, queryErr := journal.Query(0, 100)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	if len(window.Events) != 4 {
		t.Fatalf("event count = %d, want 4", len(window.Events))
	}
	var data map[string]any
	if err := json.Unmarshal(window.Events[3].Data, &data); err != nil {
		t.Fatalf("decode approval event: %v", err)
	}
	if data["approval_correlation"] != string(approval.CorrelationTrusted) || data["approval_id"] != "approval-123" || data["approval_session_id"] != "session-456" {
		t.Fatalf("approval event data = %+v", data)
	}
}

func TestPipelineReviewPendingCapturesApprovalReferenceFromJSONText(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultText(`{"review_pending":true,"approval_id":"approval-text","session_id":"session-text"}`)}
	pipeline, _ := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-text-correlation",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Approval == nil || outcome.Approval.Correlation != approval.CorrelationTrusted || outcome.Approval.ID != "approval-text" || outcome.Approval.SessionID != "session-text" {
		t.Fatalf("approval reference = %+v", outcome.Approval)
	}
}

func TestPipelineTracksOnlyTrustedApprovalReferences(t *testing.T) {
	tracker := &fakeApprovalTracker{}
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultStructured(map[string]any{
		"review_pending": true,
		"approval_id":    "approval-123",
		"session_id":     "session-456",
	}, "pending")}
	pipeline, _ := newTestPipelineWithOptions(t, dispatcher, []Rule{allowedRule()}, WithApprovalTracker(tracker))

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-tracked",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Approval == nil || outcome.Approval.Correlation != approval.CorrelationTrusted {
		t.Fatalf("approval = %+v", outcome.Approval)
	}
	calls := tracker.Calls()
	if len(calls) != 1 || calls[0].CallID != "call-review-tracked" || calls[0].Reference.ID != "approval-123" {
		t.Fatalf("tracked calls = %+v", calls)
	}

	untrustedDispatcher := &fakeDispatcher{result: mcp.NewToolResultText(
		`{"review_pending":true,"message":"no id"}`,
	)}
	untrustedPipeline, _ := newTestPipelineWithOptions(
		t,
		untrustedDispatcher,
		[]Rule{allowedRule()},
		WithApprovalTracker(tracker),
	)
	if _, err := untrustedPipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-untracked",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}); err != nil {
		t.Fatalf("untrusted Invoke() error = %v", err)
	}
	if len(tracker.Calls()) != 1 {
		t.Fatalf("unknown correlation was tracked: %+v", tracker.Calls())
	}
}

func TestPipelineReviewPendingRejectsConflictingApprovalIDs(t *testing.T) {
	dispatcher := &fakeDispatcher{result: &mcp.CallToolResult{Content: []mcp.Content{
		mcp.NewTextContent(`{"review_pending":true,"approval_id":"approval-one"}`),
		mcp.NewTextContent(`{"review_pending":true,"approval_id":"approval-two"}`),
	}}}
	pipeline, _ := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-conflict",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("call = %+v, want approval_pending", outcome.Call)
	}
	if outcome.Approval == nil || outcome.Approval.Correlation != approval.CorrelationConflict || outcome.Approval.ID != "" {
		t.Fatalf("approval reference = %+v, want conflict without a selected ID", outcome.Approval)
	}
}

func TestPipelineReviewPendingRejectsInvalidApprovalExpiry(t *testing.T) {
	dispatcher := &fakeDispatcher{result: mcp.NewToolResultStructured(map[string]any{
		"review_pending": true,
		"approval_id":    "approval-123",
		"expires_at":     "not-a-time",
	}, "pending")}
	pipeline, _ := newTestPipeline(t, dispatcher, allowedRule())

	outcome, err := pipeline.Invoke(context.Background(), Request{
		CallID:    "call-review-invalid-expiry",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Approval == nil || outcome.Approval.Correlation != approval.CorrelationConflict || outcome.Approval.ID != "" {
		t.Fatalf("approval reference = %+v, want conflict", outcome.Approval)
	}
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

type trackedApproval struct {
	CallID    string
	Reference approval.Reference
}

type fakeApprovalTracker struct {
	mu    sync.Mutex
	calls []trackedApproval
	err   error
}

func (f *fakeApprovalTracker) Track(callID string, reference approval.Reference) (approval.Tracked, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, trackedApproval{CallID: callID, Reference: reference})
	if f.err != nil {
		return approval.Tracked{}, f.err
	}
	return approval.Tracked{}, nil
}

func (f *fakeApprovalTracker) Calls() []trackedApproval {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trackedApproval(nil), f.calls...)
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
	return newTestPipelineWithOptions(t, dispatcher, rules, nil)
}

func newTestPipelineWithOptions(
	t *testing.T,
	dispatcher Dispatcher,
	rules []Rule,
	option Option,
) (*Pipeline, *eventlog.Journal) {
	t.Helper()
	policy, err := NewAllowlistPolicy(rules...)
	if err != nil {
		t.Fatalf("NewAllowlistPolicy() error = %v", err)
	}
	journal := eventlog.New()
	pipeline, err := New(policy, dispatcher, journal, option)
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
