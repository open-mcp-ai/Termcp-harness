package agentsession

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestModelLoopCheckpointPersistsWaitingApprovalAndRecoversBlocked(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create("checkpoint-waiting", "core-1"); err != nil {
		t.Fatal(err)
	}
	tools := testModelTools(t)
	adapter := &scriptedModelAdapter{responses: []model.Response{{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Name: tools[0].Name, Arguments: json.RawMessage(`{"value":"secret side effect"}`),
			}},
		},
	}}}
	reference, err := approval.TrustedReference("approval-1", "core-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &modelToolInvoker{
		statuses:  []toolcall.Status{toolcall.StatusApprovalPending},
		approvals: []*approval.Reference{&reference},
	}

	result, err := manager.RunModel(
		context.Background(),
		"checkpoint-waiting",
		adapter,
		invoker,
		tools,
		"perform one operation",
		ModelRunOptions{},
	)
	if err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	if result.Snapshot.State != StateWaitingApproval ||
		result.Snapshot.ModelLoopRecovery == nil ||
		result.Snapshot.ModelLoopRecovery.Stage != ModelLoopStageWaitingApproval ||
		result.Snapshot.ModelLoopRecovery.SafeBoundary ||
		result.Snapshot.ModelLoopRecovery.BlockedReason != "approval_outcome_unknown" {
		t.Fatalf("waiting snapshot = %+v", result.Snapshot)
	}
	encodedSnapshot, err := json.Marshal(result.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedSnapshot), "secret side effect") {
		t.Fatalf("recovery summary exposed tool arguments: %s", encodedSnapshot)
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].ModelLoopCheckpoint == nil ||
		persisted[0].ModelLoopCheckpoint.Stage != ModelLoopStageWaitingApproval ||
		len(persisted[0].ModelLoopCheckpoint.Pending) != 1 {
		t.Fatalf("persisted checkpoint = %+v", persisted)
	}
	checkpointJSON, err := json.Marshal(persisted[0].ModelLoopCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(checkpointJSON), "secret side effect") {
		t.Fatalf("checkpoint duplicated tool arguments: %s", checkpointJSON)
	}

	journal := eventlog.New()
	restored, err := NewManagerWithPersistence(journal, store)
	if err != nil {
		t.Fatalf("restore manager: %v", err)
	}
	session, err := restored.Get("checkpoint-waiting")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := session.Snapshot()
	if snapshot.State != StateUnknown || !snapshot.Recovered ||
		snapshot.ModelLoopRecovery == nil ||
		snapshot.ModelLoopRecovery.Stage != ModelLoopStageWaitingApproval ||
		snapshot.ModelLoopRecovery.SafeBoundary {
		t.Fatalf("restored snapshot = %+v", snapshot)
	}
	events, err := journal.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Events) != 1 || strings.Contains(string(events.Events[0].Data), "secret side effect") {
		t.Fatalf("recovery event = %+v", events.Events)
	}
}

func TestModelLoopCheckpointPersistsDispatchIntentBeforeToolInvocation(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create("checkpoint-dispatch", "core-1"); err != nil {
		t.Fatal(err)
	}
	tools := testModelTools(t)
	adapter := &scriptedModelAdapter{responses: []model.Response{
		{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Name: tools[0].Name, Arguments: json.RawMessage(`{"value":"read"}`),
			}},
		}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "finished"}},
	}}
	invoker := &checkpointInspectingInvoker{store: store}

	result, err := manager.RunModel(
		context.Background(),
		"checkpoint-dispatch",
		adapter,
		invoker,
		tools,
		"read a value",
		ModelRunOptions{},
	)
	if err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	stage, calls := invoker.Observed()
	if stage != ModelLoopStageToolDispatching || calls != 1 {
		t.Fatalf("checkpoint at invocation = %q, calls = %d", stage, calls)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].ModelLoopCheckpoint != nil {
		t.Fatalf("terminal checkpoint was not cleared: %+v", persisted)
	}
}

func TestModelLoopCheckpointValidationRejectsMalformedMetadata(t *testing.T) {
	valid := &modelLoopCheckpoint{
		Version: modelLoopCheckpointVersion,
		Stage:   ModelLoopStageModelRequesting,
		Options: ModelRunOptions{
			MaxIterations:      1,
			MaxToolCalls:       1,
			MaxContextBytes:    defaultModelContextBytes,
			MaxToolResultBytes: defaultModelToolResult,
		},
		Tools: []modelLoopToolIdentity{{
			Name:               "termcp-core__term",
			ServerID:           "termcp-core",
			ToolName:           "term",
			RequiredCompletion: string(toolpipeline.CompletionAccepted),
			SchemaSHA256:       strings.Repeat("a", 64),
			DescriptionSHA256:  strings.Repeat("b", 64),
		}},
		CheckpointRevision: 2,
		UpdatedAt:          time.Now().UTC(),
	}
	if err := validateModelLoopCheckpoint(valid); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
	valid.Options.MaxIterations = maxModelIterations + 1
	if err := validateModelLoopCheckpoint(valid); err == nil {
		t.Fatal("checkpoint with invalid iteration budget was accepted")
	}
	valid.Options.MaxIterations = 1
	valid.Version++
	if err := validateModelLoopCheckpoint(valid); err == nil {
		t.Fatal("unsupported checkpoint version was accepted")
	}
}

type checkpointInspectingInvoker struct {
	mu    sync.Mutex
	store *FileStore
	stage ModelLoopRecoveryStage
	calls int
}

func (invoker *checkpointInspectingInvoker) Invoke(
	_ context.Context,
	request toolpipeline.Request,
) (toolpipeline.Outcome, error) {
	persisted, err := invoker.store.Load()
	if err != nil {
		return toolpipeline.Outcome{}, err
	}
	stage := ModelLoopRecoveryStage("")
	if len(persisted) == 1 && persisted[0].ModelLoopCheckpoint != nil {
		stage = persisted[0].ModelLoopCheckpoint.Stage
	}
	invoker.mu.Lock()
	invoker.stage = stage
	invoker.calls++
	invoker.mu.Unlock()
	return toolpipeline.Outcome{
		Call:   toolcall.Snapshot{ID: request.CallID, Status: toolcall.StatusSucceeded},
		Result: mcp.NewToolResultText("safe result"),
	}, nil
}

func (invoker *checkpointInspectingInvoker) Observed() (ModelLoopRecoveryStage, int) {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return invoker.stage, invoker.calls
}
