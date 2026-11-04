package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
)

func TestSessionResumeModelFromToolsReadyExecutesPendingCallAndCompletes(t *testing.T) {
	tools := testModelTools(t)
	call := model.ToolCall{
		ID:        modelCallID(1, 1),
		Name:      tools[0].Name,
		Arguments: json.RawMessage(`{"value":"read"}`),
	}
	messages := []model.Message{
		{Role: model.RoleSystem, Content: modelSystemPrompt},
		{Role: model.RoleUser, Content: "read a value"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call}},
	}
	store := persistRecoverableLoopState(t, "resume-tools-ready", ModelLoopStageToolsReady, messages, tools, []model.ToolCall{call}, 0, 1, 0)
	journal := eventlog.New()
	manager, err := NewManagerWithPersistence(journal, store)
	if err != nil {
		t.Fatal(err)
	}

	adapter := &scriptedModelAdapter{responses: []model.Response{{
		Message: model.Message{Role: model.RoleAssistant, Content: "finished after resume"},
	}}}
	invoker := &modelToolInvoker{statuses: []toolcall.Status{toolcall.StatusSucceeded}}
	result, err := manager.ResumeModel(context.Background(), "resume-tools-ready", adapter, invoker, tools)
	if err != nil {
		t.Fatalf("ResumeModel() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || result.Output != "finished after resume" ||
		result.Snapshot.ModelTurns != 2 || result.Snapshot.ModelToolCalls != 1 {
		t.Fatalf("result = %+v", result)
	}
	calls := invoker.Calls()
	if len(calls) != 1 || calls[0].CallID != call.ID ||
		string(calls[0].Arguments) != string(call.Arguments) {
		t.Fatalf("invoked calls = %+v", calls)
	}
	requests := adapter.Requests()
	if len(requests) != 1 || !requestContainsToolResult(requests[0], call.ID) {
		t.Fatalf("model requests = %+v", requests)
	}
	window, err := journal.Query(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !eventWindowContains(window, eventlog.TypeAgentSessionResumed) {
		t.Fatalf("resume event missing: %+v", window.Events)
	}
	if _, err := manager.ResumeModel(context.Background(), "resume-tools-ready", adapter, invoker, tools); !errors.Is(err, ErrModelRecoveryUnavailable) {
		t.Fatalf("second resume error = %v", err)
	}
}

func TestSessionResumeModelAtInFlightModelRequestReusesIteration(t *testing.T) {
	tools := testModelTools(t)
	messages := []model.Message{
		{Role: model.RoleSystem, Content: modelSystemPrompt},
		{Role: model.RoleUser, Content: "finish without tools"},
	}
	store := persistRecoverableLoopState(t, "resume-model-request", ModelLoopStageModelRequesting, messages, tools, nil, 0, 1, 0)
	manager, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &scriptedModelAdapter{responses: []model.Response{{
		Message: model.Message{Role: model.RoleAssistant, Content: "resumed answer"},
	}}}
	result, err := manager.ResumeModel(
		context.Background(),
		"resume-model-request",
		adapter,
		&modelToolInvoker{},
		tools,
	)
	if err != nil {
		t.Fatalf("ResumeModel() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || result.Snapshot.ModelTurns != 1 ||
		result.Output != "resumed answer" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSessionResumeModelRejectsChangedToolCatalogWithoutSideEffects(t *testing.T) {
	tools := testModelTools(t)
	call := model.ToolCall{
		ID:        modelCallID(1, 1),
		Name:      tools[0].Name,
		Arguments: json.RawMessage(`{"value":"read"}`),
	}
	messages := []model.Message{
		{Role: model.RoleSystem, Content: modelSystemPrompt},
		{Role: model.RoleUser, Content: "read a value"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call}},
	}
	store := persistRecoverableLoopState(t, "resume-mismatch", ModelLoopStageToolsReady, messages, tools, []model.ToolCall{call}, 0, 1, 0)
	manager, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := NewModelTools([]ModelToolSpec{{
		ServerID:    "termcp-core",
		ToolName:    "term",
		Description: "changed description",
		Schema:      json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &scriptedModelAdapter{responses: []model.Response{{
		Message: model.Message{Role: model.RoleAssistant, Content: "must not run"},
	}}}
	invoker := &modelToolInvoker{statuses: []toolcall.Status{toolcall.StatusSucceeded}}
	result, err := manager.ResumeModel(context.Background(), "resume-mismatch", adapter, invoker, changed)
	if !errors.Is(err, ErrModelRecoveryMismatch) {
		t.Fatalf("ResumeModel() error = %v", err)
	}
	if result.Snapshot.State != StateUnknown || len(adapter.Requests()) != 0 || len(invoker.Calls()) != 0 {
		t.Fatalf("result = %+v, model calls = %d, tool calls = %d",
			result, len(adapter.Requests()), len(invoker.Calls()))
	}
}

func TestSessionResumeModelRejectsUnsafeCheckpoint(t *testing.T) {
	tools := testModelTools(t)
	messages := []model.Message{
		{Role: model.RoleSystem, Content: modelSystemPrompt},
		{Role: model.RoleUser, Content: "wait for approval"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID: modelCallID(1, 1), Name: tools[0].Name, Arguments: json.RawMessage(`{"value":"write"}`),
		}}},
	}
	store := persistRecoverableLoopState(t, "resume-blocked", ModelLoopStageWaitingApproval, messages, tools, nil, 0, 1, 0)
	manager, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &scriptedModelAdapter{}
	invoker := &modelToolInvoker{}
	if _, err := manager.ResumeModel(context.Background(), "resume-blocked", adapter, invoker, tools); !errors.Is(err, ErrModelRecoveryBlocked) {
		t.Fatalf("ResumeModel() error = %v", err)
	}
	if len(adapter.Requests()) != 0 || len(invoker.Calls()) != 0 {
		t.Fatal("unsafe checkpoint caused a model or tool call")
	}
}

func persistRecoverableLoopState(
	t *testing.T,
	id string,
	stage ModelLoopRecoveryStage,
	messages []model.Message,
	tools []ModelTool,
	pending []model.ToolCall,
	nextTool int,
	iteration int,
	toolCalls int,
) *FileStore {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Create(id, "core-1")
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]ModelTool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	session.mu.Lock()
	session.snapshot.State = StateRunning
	session.snapshot.Revision = 4
	session.snapshot.ModelTurns = iteration
	session.snapshot.ModelToolCalls = toolCalls
	session.modelLoop = &modelLoopState{
		tools:     tools,
		byName:    byName,
		messages:  cloneModelMessages(messages),
		pending:   append([]model.ToolCall(nil), pending...),
		nextTool:  nextTool,
		iteration: iteration,
		toolCalls: toolCalls,
		options: ModelRunOptions{
			MaxIterations:      defaultModelIterations,
			MaxToolCalls:       defaultModelToolCalls,
			MaxContextBytes:    defaultModelContextBytes,
			MaxToolResultBytes: defaultModelToolResult,
		},
	}
	session.transcript = cloneModelMessages(messages)
	session.snapshot.TranscriptMessages = len(messages)
	session.setModelLoopCheckpointLocked(stage)
	session.mu.Unlock()
	if err := manager.persist(); err != nil {
		t.Fatal(err)
	}
	return store
}

func requestContainsToolResult(request model.Request, callID string) bool {
	for _, message := range request.Messages {
		if message.Role == model.RoleTool && message.ToolCallID == callID {
			return true
		}
	}
	return false
}

func eventWindowContains(window eventlog.Window, eventType eventlog.Type) bool {
	for _, event := range window.Events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
