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

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
)

func TestApprovalExecutionRefreshRequiresEnabledAuthorizer(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/approvals/approval-1/execution/refresh", strings.NewReader(`{}`)),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestApprovalExecutionRefreshRejectsUnauthorizedAndBrowserOrigins(t *testing.T) {
	refresher := &fakeApprovalExecutionRefresher{}
	handler := NewHandler(
		runningRuntime(t),
		WithApprovalExecutionRefresher(refresher),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)

	request := approvalRefreshRequest()
	request.Header.Del("Authorization")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", response.Code)
	}

	request = approvalRefreshRequest()
	request.Header.Set("Origin", "http://127.0.0.1:8787")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("origin status = %d, want 403", response.Code)
	}
	if len(refresher.Calls()) != 0 {
		t.Fatalf("refresher calls = %+v", refresher.Calls())
	}
}

func TestApprovalExecutionRefreshReturnsObservedState(t *testing.T) {
	refresher := &fakeApprovalExecutionRefresher{refresh: approval.ExecutionRefresh{
		Approval: approval.Tracked{Request: approval.Snapshot{
			ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
			Decision: approval.DecisionApprove, Revision: 3,
			Execution: &approval.Execution{State: approval.ExecutionSucceeded},
		}},
		Changed: true, Complete: true,
	}}
	handler := NewHandler(
		runningRuntime(t),
		WithApprovalExecutionRefresher(refresher),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, approvalRefreshRequest())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body approvalRefreshResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Changed || !body.Complete || body.Approval.Request.Execution == nil ||
		body.Approval.Request.Execution.State != approval.ExecutionSucceeded {
		t.Fatalf("response = %+v", body)
	}
	calls := refresher.Calls()
	if len(calls) != 1 || calls[0] != "approval-1" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestApprovalExecutionRefreshMapsErrorsAndRecordingWarning(t *testing.T) {
	tests := []struct {
		name    string
		refresh approval.ExecutionRefresh
		err     error
		status  int
		warning bool
	}{
		{"not found", approval.ExecutionRefresh{}, approval.ErrApprovalNotFound, http.StatusNotFound, false},
		{"not approved", approval.ExecutionRefresh{}, approval.ErrApprovalNotApproved, http.StatusConflict, false},
		{"unavailable", approval.ExecutionRefresh{}, approval.ErrApprovalLookupUnavailable, http.StatusServiceUnavailable, false},
		{"lookup failed", approval.ExecutionRefresh{}, errors.New("lookup failed"), http.StatusBadGateway, false},
		{
			"state changed before event failure",
			approval.ExecutionRefresh{Changed: true, Complete: true},
			errors.New("event log full"),
			http.StatusOK,
			true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(
				runningRuntime(t),
				WithApprovalExecutionRefresher(&fakeApprovalExecutionRefresher{
					refresh: test.refresh,
					err:     test.err,
				}),
				WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, approvalRefreshRequest())
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			if test.warning {
				var body approvalRefreshResponse
				if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if body.Warning == "" {
					t.Fatalf("response = %+v", body)
				}
			}
		})
	}
}

func TestApprovalReconciliationRequiresEnabledAuthorizer(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/approvals/approval-1/reconcile", strings.NewReader(`{}`)),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestApprovalReconciliationReturnsReadOnlyResult(t *testing.T) {
	reconciler := &fakeApprovalReconciler{reconciliation: approval.Reconciliation{
		Approval: approval.Tracked{Request: approval.Snapshot{
			ID: "approval-1", CallID: "call-1", Status: approval.StatusApproved,
			Decision: approval.DecisionApprove, Revision: 3,
		}},
		Changed: true, Resolved: true,
	}}
	handler := NewHandler(
		runningRuntime(t),
		WithApprovalReconciler(reconciler),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, approvalReconcileRequest())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body approvalReconcileResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Changed || !body.Resolved || body.Approval.Request.Status != approval.StatusApproved {
		t.Fatalf("response = %+v", body)
	}
	if calls := reconciler.Calls(); len(calls) != 1 || calls[0] != "approval-1" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestApprovalReconciliationMapsErrorsAndPersistenceWarning(t *testing.T) {
	tests := []struct {
		name           string
		reconciliation approval.Reconciliation
		err            error
		status         int
		warning        bool
	}{
		{"not found", approval.Reconciliation{}, approval.ErrApprovalNotFound, http.StatusNotFound, false},
		{"not unknown", approval.Reconciliation{}, approval.ErrApprovalNotUnknown, http.StatusConflict, false},
		{"unavailable", approval.Reconciliation{}, approval.ErrApprovalLookupUnavailable, http.StatusServiceUnavailable, false},
		{"lookup failed", approval.Reconciliation{}, errors.New("lookup failed"), http.StatusBadGateway, false},
		{
			"confirmed but persistence failed",
			approval.Reconciliation{
				Approval: approval.Tracked{Request: approval.Snapshot{Status: approval.StatusRejected}},
				Changed:  true,
				Resolved: true,
			},
			approval.ErrApprovalPersistence,
			http.StatusOK,
			true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(
				runningRuntime(t),
				WithApprovalReconciler(&fakeApprovalReconciler{
					reconciliation: test.reconciliation,
					err:            test.err,
				}),
				WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, approvalReconcileRequest())
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			if test.warning {
				var body approvalReconcileResponse
				if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Warning == "" {
					t.Fatalf("response = %+v", body)
				}
			}
		})
	}
}

func approvalRefreshRequest() *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/approvals/approval-1/execution/refresh",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func approvalReconcileRequest() *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/approvals/approval-1/reconcile",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

type fakeApprovalExecutionRefresher struct {
	mu      sync.Mutex
	refresh approval.ExecutionRefresh
	err     error
	calls   []string
}

func (f *fakeApprovalExecutionRefresher) RefreshExecution(
	_ context.Context,
	id string,
) (approval.ExecutionRefresh, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return f.refresh, f.err
}

func (f *fakeApprovalExecutionRefresher) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type fakeApprovalReconciler struct {
	mu             sync.Mutex
	reconciliation approval.Reconciliation
	err            error
	calls          []string
}

func (f *fakeApprovalReconciler) ReconcileUnknown(
	_ context.Context,
	id string,
) (approval.Reconciliation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return f.reconciliation, f.err
}

func (f *fakeApprovalReconciler) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
