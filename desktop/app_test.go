package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
)

func TestAppBootstrapReadsRuntimeContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/healthz":
			writeJSON(t, w, map[string]any{"status": "ok", "state": "running", "version": "test"})
		case "/v1/runtime":
			writeJSON(t, w, map[string]any{"instance_id": "runtime-1", "version": "test", "state": "running"})
		case "/v1/events":
			writeJSON(t, w, map[string]any{"events": []any{}, "latest_sequence": 0})
		case "/v1/mcp/servers":
			writeJSON(t, w, map[string]any{
				"servers":                  []any{},
				"total":                    0,
				"control_plane_configured": true,
			})
		case "/v1/core":
			writeJSON(t, w, map[string]any{
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
			writeJSON(t, w, map[string]any{
				"configured": true,
				"total":      0,
				"approvals":  []any{},
			})
		case "/v1/agent/sessions":
			writeJSON(t, w, map[string]any{
				"configured": true,
				"total":      0,
				"sessions":   []any{},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatalf("controlclient.New() error = %v", err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()

	bootstrap, err := app.Bootstrap()
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if bootstrap.Runtime.InstanceID != "runtime-1" || !bootstrap.MCP.ControlPlaneOn || bootstrap.Core.Core == nil {
		t.Fatalf("Bootstrap() = %+v", bootstrap)
	}
	if got := app.RuntimeEndpoint(); got != server.URL {
		t.Fatalf("RuntimeEndpoint() = %q, want %q", got, server.URL)
	}
}

func TestAppEventsRejectsInvalidLimit(t *testing.T) {
	client, err := controlclient.New("http://127.0.0.1:8787")
	if err != nil {
		t.Fatalf("controlclient.New() error = %v", err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	if _, err := app.Events(0, 0); err == nil {
		t.Fatal("Events(limit=0) error = nil")
	}
}

func TestAppDecidesApprovalWithoutExposingToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/approvals/approval-1/decision" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		writeJSON(t, w, map[string]any{
			"approval": map[string]any{
				"id": "approval-1", "call_id": "call-1", "status": "approved",
				"decision": "approve", "revision": 3,
			},
			"outcome": "confirmed",
		})
	}))
	defer server.Close()
	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, token)
	app.ctx = context.Background()
	if !app.ApprovalControlAvailable() {
		t.Fatal("ApprovalControlAvailable() = false")
	}
	result, err := app.DecideApproval("approval-1", 1, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "confirmed" || result.Approval.Status != "approved" {
		t.Fatalf("result = %+v", result)
	}
	app.shutdown(context.Background())
	if app.ApprovalControlAvailable() {
		t.Fatal("token remained available after shutdown")
	}
}

func TestAppRefreshesApprovalExecutionWithoutExposingToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/approvals/approval-1/execution/refresh" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		writeJSON(t, w, map[string]any{
			"approval": map[string]any{
				"request": map[string]any{
					"id": "approval-1", "call_id": "call-1", "status": "approved",
					"decision": "approve", "revision": 3,
					"execution": map[string]any{"state": "succeeded"},
				},
				"reference": map[string]any{"correlation": "trusted", "approval_id": "approval-1"},
			},
			"changed": true, "complete": true,
		})
	}))
	defer server.Close()
	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, token)
	app.ctx = context.Background()
	result, err := app.RefreshApprovalExecution("approval-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Complete ||
		result.Approval.Request.Execution == nil ||
		result.Approval.Request.Execution.State != "succeeded" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAppReconcilesUnknownApprovalWithoutExposingToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/approvals/approval-1/reconcile" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		writeJSON(t, w, map[string]any{
			"approval": map[string]any{
				"request": map[string]any{
					"id": "approval-1", "call_id": "call-1", "status": "rejected",
					"decision": "reject", "revision": 3,
				},
				"reference": map[string]any{"correlation": "trusted", "approval_id": "approval-1"},
			},
			"changed": true, "resolved": true,
		})
	}))
	defer server.Close()
	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, token)
	app.ctx = context.Background()
	result, err := app.ReconcileApproval("approval-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Resolved ||
		result.Approval.Request.Status != "rejected" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAppStartsModelLoopThroughFixedBridge(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	var sessionID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/agent/sessions":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			sessionID, _ = body["id"].(string)
			if !strings.HasPrefix(sessionID, "desktop-") || body["core_session_id"] != "core-1" {
				t.Errorf("create body = %+v", body)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, map[string]any{
				"session": map[string]any{
					"id": sessionID, "core_session_id": "core-1", "state": "new", "revision": 1,
				},
			})
		case request.Method == http.MethodPost &&
			strings.HasPrefix(request.URL.Path, "/v1/agent/sessions/") &&
			strings.HasSuffix(request.URL.Path, "/loop"):
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode loop body: %v", err)
			}
			if body["input"] != "inspect" || body["max_iterations"] != float64(3) ||
				body["max_tool_calls"] != float64(4) {
				t.Errorf("loop body = %+v", body)
			}
			writeJSON(t, w, map[string]any{
				"session": map[string]any{
					"id": sessionID, "core_session_id": "core-1", "state": "completed", "revision": 5,
					"model_turns": 2, "model_tool_calls": 1,
				},
				"output": "finished",
				"calls": []any{map[string]any{
					"call_id": "model-call-1", "status": "succeeded", "result_bytes": 16,
				}},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, token)
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	result, err := app.StartModelLoop("core-1", " inspect ", 3, 4)
	if err != nil {
		t.Fatalf("StartModelLoop() error = %v", err)
	}
	if result.AgentSessionID != sessionID || result.Output != "finished" ||
		result.Session.State != "completed" || result.Session.ModelTurns != 2 ||
		len(result.Calls) != 1 || result.Calls[0].Status != "succeeded" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAppStartModelLoopValidatesInputAndControl(t *testing.T) {
	client, err := controlclient.New("http://127.0.0.1:8787")
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	if _, err := app.StartModelLoop("", "inspect", 8, 16); err == nil {
		t.Fatal("model loop ran without control token")
	}
	app.approvalToken = "0123456789abcdef0123456789abcdef"
	if _, err := app.StartModelLoop("", "   ", 8, 16); err == nil {
		t.Fatal("blank model input was accepted")
	}
	if _, err := app.StartModelLoop("", "inspect", 0, 16); err == nil {
		t.Fatal("invalid iteration budget was accepted")
	}
	if _, err := app.StartModelLoop("", "inspect", 8, 65); err == nil {
		t.Fatal("invalid tool-call budget was accepted")
	}
}

func TestAppResumesModelLoopThroughFixedBridge(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/agent/sessions/session-1/loop/resume" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		writeJSON(t, w, map[string]any{
			"session": map[string]any{
				"id": "session-1", "state": "completed", "revision": 9,
				"model_turns": 3, "model_tool_calls": 1,
			},
			"output": "resumed",
			"calls": []any{map[string]any{
				"call_id": "model-call-1", "status": "succeeded",
			}},
		})
	}))
	defer server.Close()
	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, token)
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	result, err := app.ResumeModelLoop(" session-1 ")
	if err != nil {
		t.Fatalf("ResumeModelLoop() error = %v", err)
	}
	if result.AgentSessionID != "session-1" || result.Output != "resumed" ||
		result.Session.State != "completed" || len(result.Calls) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestAppResumeModelLoopRequiresControlToken(t *testing.T) {
	client, err := controlclient.New("http://127.0.0.1:8787")
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	if _, err := app.ResumeModelLoop("session-1"); err == nil {
		t.Fatal("model loop resumed without control token")
	}
}

func TestAppReadsProtectedAgentSessionTranscript(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet ||
			request.URL.Path != "/v1/agent/sessions/session-1/transcript" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		writeJSON(t, w, map[string]any{
			"transcript": map[string]any{
				"session":  map[string]any{"id": "session-1", "state": "completed", "revision": 5},
				"messages": []any{map[string]any{"role": "assistant", "content": "answer"}},
				"output":   "answer",
			},
		})
	}))
	defer server.Close()
	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, token)
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	result, err := app.AgentSessionTranscript("session-1")
	if err != nil {
		t.Fatalf("AgentSessionTranscript() error = %v", err)
	}
	if result.Transcript.Output != "answer" || len(result.Transcript.Messages) != 1 {
		t.Fatalf("transcript = %+v", result.Transcript)
	}
}

func TestAppRejectsExitedShellForLiveTerminal(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/sessions/core-1/shells" {
			http.NotFound(w, request)
			return
		}
		writeJSON(t, w, map[string]any{
			"shells": []any{map[string]any{
				"id": "shell-1", "index": 1, "status": "exited", "mode": "pty",
			}},
		})
	}))
	defer core.Close()

	harness := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/core":
			writeJSON(t, w, map[string]any{
				"configured": true,
				"core": map[string]any{
					"endpoint": core.URL, "state": "identified", "ownership": "external",
				},
			})
		case "/v1/agent/sessions/agent-1":
			writeJSON(t, w, map[string]any{
				"session": map[string]any{
					"id": "agent-1", "core_session_id": "core-1", "state": "new", "revision": 1,
				},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer harness.Close()

	client, err := controlclient.New(harness.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	if _, err := app.TerminalOpen("agent-1", "shell-1", 24, 80); err == nil {
		t.Fatal("exited Shell was accepted for live terminal")
	}
}

func TestAppBridgesRealtimeEventStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/events/stream" || request.URL.Query().Get("after") != "0" {
			http.NotFound(w, request)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "flush unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: events\ndata: {\"events\":[{\"event_id\":\"event-1\",\"sequence\":1,\"occurred_at\":\"2026-10-31T00:00:00Z\",\"type\":\"runtime.started\",\"class\":\"control\"}],\"latest_sequence\":1}\n\n")
		flusher.Flush()
		<-request.Context().Done()
	}))
	defer server.Close()

	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	if _, err := app.OpenEventStream(0); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		polled, err := app.PollEventStream()
		if err != nil {
			t.Fatal(err)
		}
		if len(polled.Events) == 1 {
			if polled.Events[0].Sequence != 1 || polled.Cursor != 1 || polled.State != "connected" {
				t.Fatalf("polled = %+v", polled)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("event stream did not reach Desktop bridge")
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
