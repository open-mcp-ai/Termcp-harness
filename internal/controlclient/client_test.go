package controlclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestBootstrapReadsOnlyFixedControlEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/healthz":
			writeTestJSON(w, http.StatusOK, map[string]any{"status": "ok", "state": "running", "version": "test"})
		case "/v1/runtime":
			writeTestJSON(w, http.StatusOK, map[string]any{"instance_id": "runtime-1", "version": "test", "state": "running"})
		case "/v1/events":
			if request.URL.Query().Get("after") != "0" || request.URL.Query().Get("limit") != "200" {
				t.Errorf("unexpected event query = %s", request.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, map[string]any{"events": []any{}, "latest_sequence": 0})
		case "/v1/mcp/servers":
			writeTestJSON(w, http.StatusOK, map[string]any{"servers": []any{}, "total": 0, "control_plane_configured": true})
		case "/v1/core":
			writeTestJSON(w, http.StatusOK, map[string]any{
				"configured": true,
				"core": map[string]any{
					"endpoint":     "http://127.0.0.1:18765",
					"state":        "identified",
					"ownership":    "external",
					"version":      "v0.2.8",
					"pid":          14560,
					"instance_key": "instance-key",
					"observed_at":  "2026-10-29T01:00:00Z",
					"revision":     1,
				},
			})
		case "/v1/approvals":
			writeTestJSON(w, http.StatusOK, map[string]any{
				"configured":             true,
				"correlation_capability": "trusted",
				"total":                  1,
				"approvals": []any{map[string]any{
					"request": map[string]any{
						"id": "approval-1", "call_id": "call-1", "status": "pending",
						"decision": "none", "revision": 1,
					},
					"reference": map[string]any{
						"correlation": "trusted", "approval_id": "approval-1", "session_id": "session-1",
					},
				}},
			})
		case "/v1/agent/sessions":
			writeTestJSON(w, http.StatusOK, map[string]any{
				"configured": true,
				"total":      1,
				"sessions": []any{map[string]any{
					"id": "agent-session-1", "core_session_id": "core-session-1",
					"state": "waiting_approval", "revision": 4, "current_call_id": "call-1",
				}},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.Bootstrap(context.Background())
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if result.Runtime.InstanceID != "runtime-1" || result.Health.Status != "ok" ||
		!result.MCP.ControlPlaneOn || result.Core.Core == nil || result.Core.Core.PID != 14560 ||
		!result.Approvals.Configured || result.Approvals.Total != 1 ||
		result.Approvals.CorrelationCapability != approval.CorrelationCapabilityTrusted ||
		!result.AgentSessions.Configured || result.AgentSessions.Total != 1 {
		t.Fatalf("Bootstrap() = %+v", result)
	}
}

func TestEventsValidatesLimit(t *testing.T) {
	client, err := New("http://127.0.0.1:8787")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err := client.Events(context.Background(), 0, limit); err == nil {
			t.Fatalf("Events(limit=%d) error = nil", limit)
		}
	}
}

func TestClientRejectsUnsafeEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1:8787",
		"http://example.com:8787",
		"http://token@127.0.0.1:8787",
		"http://127.0.0.1:8787/api",
		"http://127.0.0.1:8787?token=secret",
		"127.0.0.1:8787",
	} {
		if _, err := New(endpoint); err == nil {
			t.Fatalf("New(%q) error = nil", endpoint)
		}
	}
}

func TestClientLimitsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":"` + strings.Repeat("x", maxResponseBytes) + `"}`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Events(context.Background(), 0, 10); err == nil {
		t.Fatal("Events() error = nil for oversized response")
	}
}

func TestClientSubmitsApprovalDecision(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/approvals/approval-1/decision" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("Origin"); got != "" {
			t.Errorf("Origin = %q, want empty", got)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["decision"] != "approve" || body["expected_revision"] != float64(1) {
			t.Errorf("body = %+v", body)
		}
		writeTestJSON(w, http.StatusOK, map[string]any{
			"approval": map[string]any{
				"id": "approval-1", "call_id": "call-1", "status": "approved",
				"decision": "approve", "revision": 3,
			},
			"outcome": "confirmed",
		})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.DecideApproval(
		context.Background(),
		"approval-1",
		1,
		approval.DecisionApprove,
		token,
	)
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}
	if result.Outcome != "confirmed" || result.Approval.Status != approval.StatusApproved {
		t.Fatalf("result = %+v", result)
	}
}

func TestClientApprovalDecisionHandlesUnknownAndAuthenticationErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/approvals/unknown/decision":
			writeTestJSON(w, http.StatusAccepted, map[string]any{
				"approval": map[string]any{"status": "unknown"},
				"outcome":  "unknown",
			})
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.DecideApproval(
		context.Background(),
		"unknown",
		1,
		approval.DecisionReject,
		"0123456789abcdef0123456789abcdef",
	)
	if err != nil || result.Outcome != "unknown" || result.Approval.Status != approval.StatusUnknown {
		t.Fatalf("unknown result = %+v, error = %v", result, err)
	}
	if _, err := client.DecideApproval(
		context.Background(),
		"denied",
		1,
		approval.DecisionApprove,
		"0123456789abcdef0123456789abcdef",
	); err == nil {
		t.Fatal("unauthorized decision error = nil")
	}
}

func TestClientApprovalDecisionRequiresExplicitFields(t *testing.T) {
	client, err := New("http://127.0.0.1:8787")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.DecideApproval(context.Background(), "approval-1", 0, approval.DecisionApprove, "token"); err == nil {
		t.Fatal("zero revision was accepted")
	}
	if _, err := client.DecideApproval(context.Background(), "approval-1", 1, approval.DecisionNone, "token"); err == nil {
		t.Fatal("invalid decision was accepted")
	}
	if _, err := client.DecideApproval(context.Background(), "approval-1", 1, approval.DecisionApprove, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("missing token error = %v", err)
	}
}

func TestClientCreatesAndRunsAgentSessionWithoutRetrying(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	runRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/v1/agent/sessions":
			writeTestJSON(w, http.StatusCreated, map[string]any{
				"session": map[string]any{"id": "session-1", "state": "new", "revision": 1},
			})
		case "/v1/agent/sessions/session-1/run":
			runRequests++
			writeTestJSON(w, http.StatusAccepted, map[string]any{
				"session": map[string]any{
					"id": "session-1", "state": "waiting_approval", "revision": 4,
				},
				"calls": []any{map[string]any{
					"call_id": "call-1", "status": "approval_pending",
					"result_bytes": 0, "is_error": false,
				}},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.CreateAgentSession(context.Background(), "session-1", "core-1", token)
	if err != nil || created.Session.ID != "session-1" {
		t.Fatalf("CreateAgentSession() = %+v, error = %v", created, err)
	}
	result, err := client.RunAgentSession(context.Background(), "session-1", []toolpipeline.Request{{
		CallID: "call-1", ServerID: "core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":"write"}`),
	}}, token)
	if err != nil {
		t.Fatalf("RunAgentSession() error = %v", err)
	}
	if runRequests != 1 {
		t.Fatalf("run requests = %d, want exactly 1", runRequests)
	}
	if result.Session.State != "waiting_approval" ||
		len(result.Calls) != 1 ||
		result.Calls[0].Status != "approval_pending" {
		t.Fatalf("result = %+v", result)
	}
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
