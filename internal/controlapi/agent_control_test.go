package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestAgentSessionCreateAndRunRequireAuthentication(t *testing.T) {
	manager, err := agentsession.NewManager(eventlog.New())
	if err != nil {
		t.Fatal(err)
	}
	handler := testAgentSessionHandler(t, manager, &agentTestInvoker{})

	create := httptest.NewRequest(
		http.MethodPost,
		"/v1/agent/sessions",
		strings.NewReader(`{"id":"session-1"}`),
	)
	create.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create status = %d, want 401", response.Code)
	}
}

func TestAgentSessionCreateRunAndSummarizeCalls(t *testing.T) {
	manager, err := agentsession.NewManager(eventlog.New())
	if err != nil {
		t.Fatal(err)
	}
	invoker := &agentTestInvoker{outcomes: []toolpipeline.Outcome{{
		Call: toolcall.Snapshot{
			ID: "call-1", ToolName: "term", SourceID: "core",
			Effect: toolcall.EffectReadOnly, Status: toolcall.StatusSucceeded, Revision: 4,
		},
		Result: mcp.NewToolResultText("sensitive command output"),
	}}}
	handler := testAgentSessionHandler(t, manager, invoker)

	create := authorizedJSONRequest(http.MethodPost, "/v1/agent/sessions", `{"id":"session-1","core_session_id":"core-1"}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}

	duplicate := authorizedJSONRequest(http.MethodPost, "/v1/agent/sessions", `{"id":"session-1"}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, duplicate)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", response.Code)
	}

	run := authorizedJSONRequest(http.MethodPost, "/v1/agent/sessions/session-1/run", `{
		"steps":[{
			"call_id":"call-1",
			"server_id":"core",
			"tool_name":"term",
			"arguments":{"cmd":"printf hello"}
		}]
	}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, run)
	if response.Code != http.StatusOK {
		t.Fatalf("run status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sensitive command output") {
		t.Fatal("run response leaked raw tool result content")
	}
	var body agentSessionRunResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Session.State != agentsession.StateCompleted ||
		len(body.Calls) != 1 ||
		body.Calls[0].CallID != "call-1" ||
		body.Calls[0].Status != toolcall.StatusSucceeded ||
		body.Calls[0].ResultBytes == 0 {
		t.Fatalf("run response = %+v", body)
	}
	if len(invoker.Calls()) != 1 {
		t.Fatalf("invoker calls = %d, want 1", len(invoker.Calls()))
	}

	get := httptest.NewRequest(http.MethodGet, "/v1/agent/sessions/session-1", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d", response.Code)
	}
}

func TestAgentSessionRunReturnsAcceptedForApprovalPending(t *testing.T) {
	manager, err := agentsession.NewManager(eventlog.New())
	if err != nil {
		t.Fatal(err)
	}
	reference, err := approval.TrustedReference("approval-1", "core-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &agentTestInvoker{outcomes: []toolpipeline.Outcome{{
		Call: toolcall.Snapshot{
			ID: "call-1", ToolName: "term", SourceID: "core",
			Effect: toolcall.EffectSideEffect, Status: toolcall.StatusApprovalPending, Revision: 4,
		},
		Approval: &reference,
	}}}
	handler := testAgentSessionHandler(t, manager, invoker)

	create := authorizedJSONRequest(http.MethodPost, "/v1/agent/sessions", `{"id":"session-1"}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d", response.Code)
	}
	run := authorizedJSONRequest(http.MethodPost, "/v1/agent/sessions/session-1/run", `{
		"steps":[{"call_id":"call-1","server_id":"core","tool_name":"term","arguments":{"cmd":"write"}}]
	}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, run)
	if response.Code != http.StatusAccepted {
		t.Fatalf("run status = %d, want 202: %s", response.Code, response.Body.String())
	}
	var body agentSessionRunResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Session.State != agentsession.StateWaitingApproval ||
		body.Session.CurrentApprovalID != "approval-1" {
		t.Fatalf("session = %+v", body.Session)
	}
}

const agentControlToken = "0123456789abcdef0123456789abcdef"

func testAgentSessionHandler(
	t *testing.T,
	manager *agentsession.Manager,
	invoker agentsession.Invoker,
) http.Handler {
	t.Helper()
	authorizer, err := NewApprovalAuthorizer(agentControlToken)
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(
		runningRuntime(t),
		WithAgentSessionRunner(manager, invoker),
		WithApprovalAuthorizer(authorizer),
	)
}

func authorizedJSONRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+agentControlToken)
	return request
}

type agentTestInvoker struct {
	mu       sync.Mutex
	outcomes []toolpipeline.Outcome
	calls    []toolpipeline.Request
}

func (f *agentTestInvoker) Invoke(_ context.Context, request toolpipeline.Request) (toolpipeline.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := len(f.calls)
	f.calls = append(f.calls, request)
	if index >= len(f.outcomes) {
		return toolpipeline.Outcome{}, context.Canceled
	}
	return f.outcomes[index], nil
}

func (f *agentTestInvoker) Calls() []toolpipeline.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolpipeline.Request(nil), f.calls...)
}
