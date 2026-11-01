package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestSessionModelLoopConsumesToolResultAndCompletes(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	tools := testModelTools(t)
	adapter := &scriptedModelAdapter{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Name: tools[0].Name, Arguments: json.RawMessage(`{"value":"hello"}`),
		}}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "finished"}},
	}}
	invoker := &modelToolInvoker{statuses: []toolcall.Status{toolcall.StatusSucceeded}}

	result, err := session.RunModel(
		context.Background(), adapter, invoker, tools, "read the value", ModelRunOptions{},
	)
	if err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || result.Output != "finished" ||
		result.Snapshot.ModelTurns != 2 || result.Snapshot.ModelToolCalls != 1 {
		t.Fatalf("result = %+v", result)
	}
	calls := invoker.Calls()
	if len(calls) != 1 || calls[0].ServerID != "termcp-core" || calls[0].ToolName != "term" ||
		string(calls[0].Arguments) != `{"value":"hello"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	requests := adapter.Requests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	if len(requests[1].Messages) < 4 {
		t.Fatalf("second request messages = %+v", requests[1].Messages)
	}
	toolMessage := requests[1].Messages[3]
	if toolMessage.Role != model.RoleTool || toolMessage.ToolCallID == "" ||
		toolMessage.ToolCallID != requests[1].Messages[2].ToolCalls[0].ID {
		t.Fatalf("tool result projection = %+v", requests[1].Messages)
	}
	if !strings.Contains(toolMessage.Content, `"result_withheld":true`) ||
		strings.Contains(toolMessage.Content, `"tool-ok"`) {
		t.Fatalf("default tool result projection exposed content: %s", toolMessage.Content)
	}
	assertModelLoopEvents(t, journal, true)
}

func TestSessionModelLoopThroughOpenAICompatibleAdapter(t *testing.T) {
	tools := testModelTools(t)
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
			return
		}
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
		}
		hasToolResult := false
		for _, message := range body.Messages {
			if message.Role == "tool" {
				hasToolResult = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if !hasToolResult {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"provider-1","type":"function","function":{"name":"termcp-core__term","arguments":"{\"cmd\":\"pwd\"}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"adapter loop done"}}]}`))
	}))
	defer modelServer.Close()
	adapter, err := model.NewOpenAICompatible(model.OpenAIConfig{
		Endpoint: modelServer.URL + "/v1/chat/completions",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := newTestSession(t, eventlog.New())
	invoker := &modelToolInvoker{statuses: []toolcall.Status{toolcall.StatusSucceeded}}
	result, err := session.RunModel(
		context.Background(), adapter, invoker, tools, "run pwd", ModelRunOptions{},
	)
	if err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || result.Output != "adapter loop done" ||
		len(invoker.Calls()) != 1 {
		t.Fatalf("result = %+v, calls = %+v", result, invoker.Calls())
	}
}

func TestSessionModelLoopExposesResultOnlyWhenExplicitlyEnabled(t *testing.T) {
	tools := testModelTools(t)
	tools[0].ExposeResult = true
	adapter := &scriptedModelAdapter{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Name: tools[0].Name, Arguments: json.RawMessage(`{"value":"hello"}`),
		}}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	session := newTestSession(t, eventlog.New())
	invoker := &modelToolInvoker{statuses: []toolcall.Status{toolcall.StatusSucceeded}}
	if _, err := session.RunModel(
		context.Background(), adapter, invoker, tools, "read", ModelRunOptions{},
	); err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	requests := adapter.Requests()
	if len(requests) != 2 || len(requests[1].Messages) < 4 {
		t.Fatalf("requests = %+v", requests)
	}
	content := requests[1].Messages[3].Content
	if !strings.Contains(content, `"result_exposed":true`) || !strings.Contains(content, "tool-ok") {
		t.Fatalf("explicit result projection = %s", content)
	}
}

func TestSessionModelLoopWaitsForApprovalThenResumes(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	tools := testModelTools(t)
	adapter := &scriptedModelAdapter{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Name: tools[0].Name, Arguments: json.RawMessage(`{"value":"side effect"}`),
		}}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "resumed and finished"}},
	}}
	reference, err := approval.TrustedReference("approval-1", "core-session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &modelToolInvoker{
		statuses:  []toolcall.Status{toolcall.StatusApprovalPending},
		approvals: []*approval.Reference{&reference},
	}

	result, err := session.RunModel(
		context.Background(), adapter, invoker, tools, "perform the operation", ModelRunOptions{},
	)
	if err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	if result.Snapshot.State != StateWaitingApproval || result.Snapshot.CurrentApprovalID != "approval-1" {
		t.Fatalf("waiting result = %+v", result)
	}
	callID := result.Snapshot.CurrentCallID
	if callID == "" || len(adapter.Requests()) != 1 {
		t.Fatalf("call ID = %q, model requests = %d", callID, len(adapter.Requests()))
	}
	if _, err := session.ObserveApproval(callID, approval.Snapshot{
		ID: "approval-1", CallID: callID, Status: approval.StatusApproved,
		Decision: approval.DecisionApprove, Revision: 3,
	}); err != nil {
		t.Fatalf("ObserveApproval() error = %v", err)
	}
	if session.Snapshot().State != StateWaitingExecution {
		t.Fatalf("state = %s, want waiting_execution", session.Snapshot().State)
	}

	observed, err := session.ObserveToolResult(context.Background(), invoker, toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: callID, Status: toolcall.StatusSucceeded},
	})
	if err != nil {
		t.Fatalf("ObserveToolResult() error = %v", err)
	}
	if observed.Snapshot.State != StateCompleted || session.Snapshot().ModelTurns != 2 {
		t.Fatalf("resumed snapshot = %+v", observed.Snapshot)
	}
	if len(adapter.Requests()) != 2 {
		t.Fatalf("model requests = %d, want 2", len(adapter.Requests()))
	}
	assertModelLoopEvents(t, journal, true)
}

func TestSessionModelLoopRejectsUnknownToolWithoutInvoking(t *testing.T) {
	journal := eventlog.New()
	session := newTestSession(t, journal)
	tools := testModelTools(t)
	adapter := &scriptedModelAdapter{responses: []model.Response{{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Name: "unknown__tool", Arguments: json.RawMessage(`{}`),
		}}},
	}}}
	invoker := &modelToolInvoker{}

	result, err := session.RunModel(
		context.Background(), adapter, invoker, tools, "do something", ModelRunOptions{},
	)
	if err == nil || result.Snapshot.State != StateFailed {
		t.Fatalf("RunModel() = %+v, error = %v", result, err)
	}
	if len(invoker.Calls()) != 0 {
		t.Fatalf("unknown tool was invoked: %+v", invoker.Calls())
	}
	assertModelLoopEvents(t, journal, false)
}

func TestProjectModelMessagesKeepsToolCallBlocksTogether(t *testing.T) {
	messages := []model.Message{
		{Role: model.RoleSystem, Content: "system"},
		{Role: model.RoleUser, Content: "user"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "tool", Arguments: json.RawMessage(`{}`)}}},
		{Role: model.RoleTool, ToolCallID: "call-1", Name: "tool", Content: "result-1"},
		{Role: model.RoleAssistant, Content: "answer-1"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-2", Name: "tool", Arguments: json.RawMessage(`{}`)}}},
		{Role: model.RoleTool, ToolCallID: "call-2", Name: "tool", Content: "result-2"},
	}
	projected, omitted, err := projectModelMessages(messages, 420)
	if err != nil {
		t.Fatalf("projectModelMessages() error = %v", err)
	}
	if omitted == 0 || len(projected) < 4 {
		t.Fatalf("projected = %+v, omitted = %d", projected, omitted)
	}
	lastAssistant := projected[len(projected)-2]
	lastTool := projected[len(projected)-1]
	if lastAssistant.Role != model.RoleAssistant || len(lastAssistant.ToolCalls) != 1 ||
		lastTool.Role != model.RoleTool || lastTool.ToolCallID != lastAssistant.ToolCalls[0].ID {
		t.Fatalf("projected tail = %+v", projected)
	}
}

func TestNewModelToolsCreatesStableNamespacedNames(t *testing.T) {
	tools, err := NewModelTools([]ModelToolSpec{
		{ServerID: "termcp-core", ToolName: "term", Schema: json.RawMessage(`{"type":"object"}`)},
		{ServerID: "termcp core", ToolName: "term", Schema: json.RawMessage(`{"type":"object"}`)},
	})
	if err != nil {
		t.Fatalf("NewModelTools() error = %v", err)
	}
	if tools[0].Name != "termcp-core__term" || tools[1].Name != "termcp_core__term" {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0].RequiredCompletion != toolpipeline.CompletionAccepted {
		t.Fatalf("completion = %q", tools[0].RequiredCompletion)
	}
}

func testModelTools(t *testing.T) []ModelTool {
	t.Helper()
	tools, err := NewModelTools([]ModelToolSpec{{
		ServerID: "termcp-core",
		ToolName: "term",
		Schema:   json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("NewModelTools() error = %v", err)
	}
	return tools
}

func assertModelLoopEvents(t *testing.T, journal *eventlog.Journal, completed bool) {
	t.Helper()
	window, err := journal.Query(0, 1000)
	if err != nil {
		t.Fatalf("journal Query() error = %v", err)
	}
	types := make(map[eventlog.Type]bool)
	for _, event := range window.Events {
		types[event.Type] = true
		for _, forbidden := range []string{"hello", "finished", `"value"`} {
			if strings.Contains(string(event.Data), forbidden) {
				t.Fatalf("model/tool content leaked into event %s: %s", event.Type, event.Data)
			}
		}
	}
	if !types[eventlog.TypeAgentModelRequested] {
		t.Fatalf("missing model request event: %+v", window.Events)
	}
	if completed && !types[eventlog.TypeAgentModelResponded] {
		t.Fatalf("missing model response event: %+v", window.Events)
	}
	if !completed && !types[eventlog.TypeAgentModelFailed] {
		t.Fatalf("missing model failure event: %+v", window.Events)
	}
	if completed != types[eventlog.TypeAgentSessionCompleted] {
		t.Fatalf("completed event present = %v, want %v", types[eventlog.TypeAgentSessionCompleted], completed)
	}
}

type scriptedModelAdapter struct {
	mu        sync.Mutex
	responses []model.Response
	requests  []model.Request
}

func (f *scriptedModelAdapter) Complete(_ context.Context, request model.Request) (model.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if len(f.responses) == 0 {
		return model.Response{}, errors.New("no scripted model response")
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

func (f *scriptedModelAdapter) Requests() []model.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Request(nil), f.requests...)
}

type modelToolInvoker struct {
	mu        sync.Mutex
	statuses  []toolcall.Status
	approvals []*approval.Reference
	calls     []toolpipeline.Request
}

func (f *modelToolInvoker) Invoke(_ context.Context, request toolpipeline.Request) (toolpipeline.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := len(f.calls)
	f.calls = append(f.calls, request)
	if index >= len(f.statuses) {
		return toolpipeline.Outcome{}, errors.New("unexpected tool invocation")
	}
	outcome := toolpipeline.Outcome{
		Call: toolcall.Snapshot{ID: request.CallID, Status: f.statuses[index]},
	}
	if index < len(f.approvals) {
		outcome.Approval = f.approvals[index]
	}
	if f.statuses[index] == toolcall.StatusSucceeded {
		outcome.Result = mcp.NewToolResultText("tool-ok")
	}
	return outcome, nil
}

func (f *modelToolInvoker) Calls() []toolpipeline.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolpipeline.Request(nil), f.calls...)
}
