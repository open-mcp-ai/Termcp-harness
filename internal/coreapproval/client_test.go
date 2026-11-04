package coreapproval

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
)

func TestClientApprovesThroughPublicCoreContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/approvals/approval-1/approve" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer core-token" {
			t.Errorf("Authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "{}" {
			t.Errorf("body = %q, want {}", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request":{"state":"approved"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "core-token", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	outcome, err := client.Decide(context.Background(), "approval-1", approval.DecisionApprove)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if outcome.Status != approval.StatusApproved {
		t.Fatalf("status = %s, want approved", outcome.Status)
	}
}

func TestClientRejectsThroughPublicCoreContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/approvals/approval-1/reject" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"request":{"state":"rejected"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	outcome, err := client.Decide(context.Background(), "approval-1", approval.DecisionReject)
	if err != nil || outcome.Status != approval.StatusRejected {
		t.Fatalf("Decide() = %s, error = %v", outcome.Status, err)
	}
}

func TestClientReadsStructuredExecutionResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"request":{
				"state":"approved",
				"execution":{
					"state":"submitted",
					"started_at":1790129915403,
					"finished_at":1790129916410
				}
			}
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	outcome, err := client.Decide(context.Background(), "approval-1", approval.DecisionApprove)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if outcome.Status != approval.StatusApproved ||
		outcome.Execution == nil ||
		outcome.Execution.State != approval.ExecutionSubmitted ||
		outcome.Execution.StartedAt.UnixMilli() != 1790129915403 ||
		outcome.Execution.FinishedAt == nil ||
		outcome.Execution.FinishedAt.UnixMilli() != 1790129916410 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestClientReadsExecutionFailureFromConflictResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{
			"error":"approved but execution failed",
			"detail":"listener unavailable",
			"request":{
				"state":"approved",
				"execution":{
					"state":"failed",
					"started_at":1790129915403,
					"finished_at":1790129916410,
					"error":"listener unavailable"
				}
			}
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	outcome, err := client.Decide(context.Background(), "approval-1", approval.DecisionApprove)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if outcome.Status != approval.StatusApproved ||
		outcome.Execution == nil ||
		outcome.Execution.State != approval.ExecutionFailed ||
		outcome.Execution.Error != "listener unavailable" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestClientLooksUpExecutionWithoutSubmittingDecision(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/api/approvals/approval-1" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer core-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"request":{
				"state":"approved",
				"execution":{
					"state":"succeeded",
					"started_at":1790129915403,
					"finished_at":1790129916410
				}
			}
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "core-token", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	outcome, err := client.Lookup(context.Background(), "approval-1")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if requests != 1 || outcome.Status != approval.StatusApproved ||
		outcome.Execution == nil || outcome.Execution.State != approval.ExecutionSucceeded {
		t.Fatalf("requests = %d, outcome = %+v", requests, outcome)
	}
}

func TestClientClassifiesCoreDecisionConflicts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/approvals/missing/approve":
			http.Error(w, "not found", http.StatusNotFound)
		case "/api/approvals/conflict/approve":
			http.Error(w, "already decided", http.StatusConflict)
		default:
			http.Error(w, "unexpected", http.StatusTeapot)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Decide(context.Background(), "missing", approval.DecisionApprove); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error = %v, want ErrNotFound", err)
	}
	if _, err := client.Decide(context.Background(), "conflict", approval.DecisionApprove); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want ErrConflict", err)
	}
}

func TestClientRejectsInvalidResponses(t *testing.T) {
	tests := []string{
		`not-json`,
		`{"request":{}}`,
		`{"request":{"state":"submitting"}}`,
		`{"request":{"state":"approved","execution":{"state":"submitted"}}}`,
		`{"request":{"state":"approved","execution":{"state":"submitted","started_at":1}}}`,
		`{"request":{"state":"approved"}} trailing`,
	}
	for _, body := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		client, err := NewClient(server.URL, "", server.Client())
		if err != nil {
			server.Close()
			t.Fatalf("NewClient() error = %v", err)
		}
		_, err = client.Decide(context.Background(), "approval-1", approval.DecisionApprove)
		server.Close()
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("body %q error = %v, want ErrInvalidResponse", body, err)
		}
	}
}

func TestClientRejectsOversizedAndUnsafeInputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Decide(context.Background(), "approval-1", approval.DecisionApprove); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("oversized response error = %v", err)
	}
	if _, err := client.Decide(context.Background(), "../approval", approval.DecisionApprove); err == nil {
		t.Fatal("unsafe approval ID was accepted")
	}
}

func TestNewClientRequiresLoopbackOrigin(t *testing.T) {
	for _, raw := range []string{
		"",
		"ftp://127.0.0.1:18765",
		"http://192.168.1.2:18765",
		"http://user:pass@127.0.0.1:18765",
		"http://127.0.0.1:18765/api",
		"http://127.0.0.1:18765?token=secret",
	} {
		if _, err := NewClient(raw, "", nil); err == nil {
			t.Fatalf("NewClient(%q) error = nil", raw)
		}
	}
}
