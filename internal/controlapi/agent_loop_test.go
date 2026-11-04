package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestAgentLoopEndpointRequiresConfiguredModel(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(
		response,
		agentLoopRequestForTest(`{"input":"hello"}`),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestAgentLoopEndpointRunsThroughModelBoundary(t *testing.T) {
	runner := &fakeAgentModelRunner{
		tools: []agentsession.ModelTool{{
			Name: "termcp-core__term", ServerID: "termcp-core", ToolName: "term",
		}},
		result: agentsession.ModelResult{
			Snapshot: agentsession.Snapshot{
				ID: "session-1", State: agentsession.StateCompleted, Revision: 6,
				ModelTurns: 2, ModelToolCalls: 1,
			},
			Output: "done",
		},
	}
	handler := NewHandler(
		runningRuntime(t),
		WithAgentSessionRunner(stubAgentSessionRunner{}, stubAgentInvoker{}),
		WithAgentModelLoop(
			runner,
			&fakeControlModelAdapter{},
			&fakeModelToolProvider{tools: runner.tools},
		),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, agentLoopRequestForTest(`{
		"input":"inspect the workspace",
		"max_iterations":3,
		"max_tool_calls":4
	}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body agentLoopResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Output != "done" || body.Session.ModelTurns != 2 || body.Session.ModelToolCalls != 1 {
		t.Fatalf("response = %+v", body)
	}
	calls := runner.Calls()
	if len(calls) != 1 || calls[0].id != "session-1" ||
		calls[0].input != "inspect the workspace" ||
		calls[0].options.MaxIterations != 3 || calls[0].options.MaxToolCalls != 4 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestAgentLoopEndpointValidatesInputAndReportsWaitingState(t *testing.T) {
	runner := &fakeAgentModelRunner{
		tools: []agentsession.ModelTool{{
			Name: "termcp-core__term", ServerID: "termcp-core", ToolName: "term",
		}},
		result: agentsession.ModelResult{Snapshot: agentsession.Snapshot{
			ID: "session-1", State: agentsession.StateWaitingApproval, Revision: 4,
			CurrentCallID: "call-1", CurrentApprovalID: "approval-1",
		}},
	}
	handler := NewHandler(
		runningRuntime(t),
		WithAgentSessionRunner(stubAgentSessionRunner{}, stubAgentInvoker{}),
		WithAgentModelLoop(
			runner,
			&fakeControlModelAdapter{},
			&fakeModelToolProvider{tools: runner.tools},
		),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, agentLoopRequestForTest(`{"input":"   "}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("blank input status = %d, want 400", response.Code)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, agentLoopRequestForTest(`{"input":"write a file"}`))
	if response.Code != http.StatusAccepted {
		t.Fatalf("waiting status = %d, want 202: %s", response.Code, response.Body.String())
	}
}

func TestAgentLoopResumeEndpointUsesFixedRuntimeConfiguration(t *testing.T) {
	runner := &fakeAgentModelRunner{
		tools: []agentsession.ModelTool{{
			Name: "termcp-core__term", ServerID: "termcp-core", ToolName: "term",
		}},
		result: agentsession.ModelResult{
			Snapshot: agentsession.Snapshot{
				ID: "session-1", State: agentsession.StateCompleted, Revision: 8,
				ModelTurns: 3, ModelToolCalls: 1,
			},
			Output: "recovered",
		},
	}
	handler := NewHandler(
		runningRuntime(t),
		WithAgentSessionRunner(stubAgentSessionRunner{}, stubAgentInvoker{}),
		WithAgentModelLoop(
			runner,
			&fakeControlModelAdapter{},
			&fakeModelToolProvider{tools: runner.tools},
		),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, agentLoopResumeRequestForTest())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body agentLoopResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Output != "recovered" || body.Session.State != agentsession.StateCompleted {
		t.Fatalf("response = %+v", body)
	}
	resumed := runner.ResumeCalls()
	if len(resumed) != 1 || resumed[0] != "session-1" {
		t.Fatalf("resume calls = %+v", resumed)
	}
}

func TestAgentLoopResumeEndpointRejectsUnsafeCheckpoint(t *testing.T) {
	runner := &fakeAgentModelRunner{
		tools: []agentsession.ModelTool{{
			Name: "termcp-core__term", ServerID: "termcp-core", ToolName: "term",
		}},
		result: agentsession.ModelResult{Snapshot: agentsession.Snapshot{
			ID: "session-1", State: agentsession.StateUnknown, Revision: 8,
			Recovered: true, RecoveryReason: "runtime_restart",
		}},
		err: agentsession.ErrModelRecoveryBlocked,
	}
	handler := NewHandler(
		runningRuntime(t),
		WithAgentSessionRunner(stubAgentSessionRunner{}, stubAgentInvoker{}),
		WithAgentModelLoop(
			runner,
			&fakeControlModelAdapter{},
			&fakeModelToolProvider{tools: runner.tools},
		),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, agentLoopResumeRequestForTest())
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", response.Code, response.Body.String())
	}
}

func agentLoopRequestForTest(body string) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/agent/sessions/session-1/loop",
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func agentLoopResumeRequestForTest() *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/agent/sessions/session-1/loop/resume",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

type modelLoopCall struct {
	id      string
	input   string
	tools   []agentsession.ModelTool
	options agentsession.ModelRunOptions
}

type fakeAgentModelRunner struct {
	mu      sync.Mutex
	tools   []agentsession.ModelTool
	result  agentsession.ModelResult
	err     error
	calls   []modelLoopCall
	resumed []string
}

func (f *fakeAgentModelRunner) RunModel(
	_ context.Context,
	id string,
	_ model.Adapter,
	_ agentsession.Invoker,
	tools []agentsession.ModelTool,
	input string,
	options agentsession.ModelRunOptions,
) (agentsession.ModelResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, modelLoopCall{
		id: id, input: input, tools: append([]agentsession.ModelTool(nil), tools...), options: options,
	})
	_ = f.tools
	return f.result, f.err
}

func (f *fakeAgentModelRunner) Calls() []modelLoopCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]modelLoopCall(nil), f.calls...)
}

func (f *fakeAgentModelRunner) ResumeModel(
	_ context.Context,
	id string,
	_ model.Adapter,
	_ agentsession.Invoker,
	_ []agentsession.ModelTool,
) (agentsession.ModelResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, id)
	return f.result, f.err
}

func (f *fakeAgentModelRunner) ResumeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resumed...)
}

type fakeControlModelAdapter struct{}

func (*fakeControlModelAdapter) Complete(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, nil
}

type fakeModelToolProvider struct {
	tools []agentsession.ModelTool
}

type stubAgentSessionRunner struct{}

func (stubAgentSessionRunner) List() []agentsession.Snapshot {
	return nil
}

func (stubAgentSessionRunner) Create(string, string) (*agentsession.Session, error) {
	return nil, agentsession.ErrSessionNotFound
}

func (stubAgentSessionRunner) Get(string) (*agentsession.Session, error) {
	return nil, agentsession.ErrSessionNotFound
}

func (stubAgentSessionRunner) Run(
	context.Context,
	string,
	agentsession.Invoker,
	[]agentsession.Step,
) (agentsession.Result, error) {
	return agentsession.Result{}, agentsession.ErrSessionNotFound
}

type stubAgentInvoker struct{}

func (stubAgentInvoker) Invoke(
	context.Context,
	toolpipeline.Request,
) (toolpipeline.Outcome, error) {
	return toolpipeline.Outcome{}, nil
}

func (f *fakeModelToolProvider) ModelTools() []agentsession.ModelTool {
	return append([]agentsession.ModelTool(nil), f.tools...)
}
