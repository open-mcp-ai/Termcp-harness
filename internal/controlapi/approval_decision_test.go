package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
)

func TestApprovalDecisionRequiresEnabledAuthorizer(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/approvals/approval-1/decision", strings.NewReader(`{}`)),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestApprovalDecisionRejectsUnauthorizedAndBrowserOrigins(t *testing.T) {
	authorizer := newTestApprovalAuthorizer(t)
	handler := NewHandler(
		runningRuntime(t),
		WithApprovalDecider(&fakeApprovalDecider{}),
		WithApprovalAuthorizer(authorizer),
	)
	request := decisionRequest(`{"decision":"approve","expected_revision":1}`)
	request.Header.Del("Authorization")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", response.Code)
	}

	request = decisionRequest(`{"decision":"approve","expected_revision":1}`)
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	request.Header.Set("Origin", "http://127.0.0.1:8787")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("origin status = %d, want 403", response.Code)
	}
}

func TestApprovalDecisionValidatesRequest(t *testing.T) {
	handler := testApprovalDecisionHandler(t, &fakeApprovalDecider{})
	tests := []struct {
		name        string
		body        string
		contentType string
		status      int
	}{
		{"content type", `{"decision":"approve","expected_revision":1}`, "text/plain", http.StatusUnsupportedMediaType},
		{"malformed", `{"decision":`, "application/json", http.StatusBadRequest},
		{"unknown field", `{"decision":"approve","expected_revision":1,"extra":true}`, "application/json", http.StatusBadRequest},
		{"missing revision", `{"decision":"approve"}`, "application/json", http.StatusBadRequest},
		{"invalid decision", `{"decision":"maybe","expected_revision":1}`, "application/json", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := decisionRequest(test.body)
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestApprovalDecisionReturnsConfirmedState(t *testing.T) {
	decider := &fakeApprovalDecider{snapshot: approval.Snapshot{
		ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}}
	handler := testApprovalDecisionHandler(t, decider)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, decisionRequest(`{"decision":"approve","expected_revision":1}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body approvalDecisionResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Outcome != "confirmed" || body.Approval.Status != approval.StatusApproved {
		t.Fatalf("response = %+v", body)
	}
	calls := decider.Calls()
	if len(calls) != 1 || calls[0].ID != "approval-1" ||
		calls[0].Expected != 1 || calls[0].Decision != approval.DecisionApprove {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestApprovalDecisionReturnsExecutionFailureWithoutUndoingApproval(t *testing.T) {
	started := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	decider := &fakeApprovalDecider{snapshot: approval.Snapshot{
		ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
		Execution: &approval.Execution{
			State: approval.ExecutionFailed, StartedAt: started, FinishedAt: &finished,
			Error: "listener unavailable",
		},
	}}
	handler := testApprovalDecisionHandler(t, decider)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, decisionRequest(`{"decision":"approve","expected_revision":1}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body approvalDecisionResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Outcome != "confirmed" || body.Approval.Status != approval.StatusApproved ||
		body.Approval.Execution == nil ||
		body.Approval.Execution.State != approval.ExecutionFailed {
		t.Fatalf("response = %+v", body)
	}
}

func TestApprovalDecisionReturnsAcceptedForUnknownOutcome(t *testing.T) {
	decider := &fakeApprovalDecider{
		snapshot: approval.Snapshot{Status: approval.StatusUnknown},
		err:      errors.New("transport lost"),
	}
	handler := testApprovalDecisionHandler(t, decider)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, decisionRequest(`{"decision":"reject","expected_revision":1}`))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", response.Code, response.Body.String())
	}
	var body approvalDecisionResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Outcome != "unknown" || body.Approval.Status != approval.StatusUnknown {
		t.Fatalf("response = %+v", body)
	}
}

func TestApprovalDecisionKeepsConfirmedStateWhenEventRecordingFails(t *testing.T) {
	decider := &fakeApprovalDecider{
		snapshot: approval.Snapshot{
			ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
			Decision: approval.DecisionApprove, Revision: 3,
		},
		err: errors.New("event log full"),
	}
	handler := testApprovalDecisionHandler(t, decider)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, decisionRequest(`{"decision":"approve","expected_revision":1}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body approvalDecisionResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Outcome != "confirmed" || body.Warning == "" {
		t.Fatalf("response = %+v", body)
	}
}

func TestApprovalDecisionMapsConflicts(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{approval.ErrApprovalNotFound, http.StatusNotFound},
		{approval.ErrApprovalNotPending, http.StatusConflict},
		{approval.ErrStaleRevision, http.StatusConflict},
		{approval.ErrDecisionMismatch, http.StatusConflict},
		{errors.New("unexpected"), http.StatusBadGateway},
	}
	for _, test := range tests {
		handler := testApprovalDecisionHandler(t, &fakeApprovalDecider{
			snapshot: approval.Snapshot{Status: approval.StatusPending},
			err:      test.err,
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, decisionRequest(`{"decision":"approve","expected_revision":1}`))
		if response.Code != test.status {
			t.Fatalf("error %v status = %d, want %d", test.err, response.Code, test.status)
		}
	}
}

func TestApprovalDecisionMapsContradictoryUnknownToConflict(t *testing.T) {
	handler := testApprovalDecisionHandler(t, &fakeApprovalDecider{
		snapshot: approval.Snapshot{Status: approval.StatusUnknown},
		err:      approval.ErrDecisionMismatch,
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, decisionRequest(`{"decision":"approve","expected_revision":1}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", response.Code, response.Body.String())
	}
}

const testApprovalToken = "0123456789abcdef0123456789abcdef"

func newTestApprovalAuthorizer(t *testing.T) *ApprovalAuthorizer {
	t.Helper()
	authorizer, err := NewApprovalAuthorizer(testApprovalToken)
	if err != nil {
		t.Fatalf("NewApprovalAuthorizer() error = %v", err)
	}
	return authorizer
}

func testApprovalDecisionHandler(t *testing.T, decider ApprovalDecider) http.Handler {
	t.Helper()
	return NewHandler(
		runningRuntime(t),
		WithApprovalDecider(decider),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
}

func decisionRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/approvals/approval-1/decision", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

type decisionCall struct {
	ID       string
	Expected approval.Revision
	Decision approval.Decision
}

type fakeApprovalDecider struct {
	mu       sync.Mutex
	snapshot approval.Snapshot
	err      error
	calls    []decisionCall
}

func (f *fakeApprovalDecider) Decide(
	_ context.Context,
	id string,
	expected approval.Revision,
	decision approval.Decision,
) (approval.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, decisionCall{ID: id, Expected: expected, Decision: decision})
	return f.snapshot, f.err
}

func (f *fakeApprovalDecider) Calls() []decisionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]decisionCall(nil), f.calls...)
}
