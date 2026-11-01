package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

const modelLoopCheckpointVersion = 1

type ModelLoopRecoveryStage string

const (
	ModelLoopStageModelRequesting     ModelLoopRecoveryStage = "model_requesting"
	ModelLoopStageToolsReady          ModelLoopRecoveryStage = "tools_ready"
	ModelLoopStageToolDispatching     ModelLoopRecoveryStage = "tool_dispatching"
	ModelLoopStageToolObserved        ModelLoopRecoveryStage = "tool_observed"
	ModelLoopStageWaitingApproval     ModelLoopRecoveryStage = "waiting_approval"
	ModelLoopStageWaitingExecution    ModelLoopRecoveryStage = "waiting_execution"
	ModelLoopStageCheckpointCorrupted ModelLoopRecoveryStage = "checkpoint_corrupted"
)

// ModelLoopRecoverySummary is safe to expose through control API snapshots. It
// deliberately excludes model messages, tool arguments, tool results, and
// credential references.
type ModelLoopRecoverySummary struct {
	Stage              ModelLoopRecoveryStage `json:"stage"`
	SafeBoundary       bool                   `json:"safe_boundary"`
	BlockedReason      string                 `json:"blocked_reason,omitempty"`
	Iteration          int                    `json:"iteration"`
	ToolCalls          int                    `json:"tool_calls"`
	PendingToolCalls   int                    `json:"pending_tool_calls"`
	CurrentCallID      string                 `json:"current_call_id,omitempty"`
	CheckpointRevision uint64                 `json:"checkpoint_revision"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

type modelLoopCheckpoint struct {
	Version            int                     `json:"version"`
	Stage              ModelLoopRecoveryStage  `json:"stage"`
	Iteration          int                     `json:"iteration"`
	ToolCalls          int                     `json:"tool_calls"`
	Options            ModelRunOptions         `json:"options"`
	Tools              []modelLoopToolIdentity `json:"tools"`
	Pending            []modelLoopPendingCall  `json:"pending,omitempty"`
	NextTool           int                     `json:"next_tool,omitempty"`
	CurrentCallID      string                  `json:"current_call_id,omitempty"`
	CheckpointRevision uint64                  `json:"checkpoint_revision"`
	UpdatedAt          time.Time               `json:"updated_at"`
}

type modelLoopToolIdentity struct {
	Name               string `json:"name"`
	ServerID           string `json:"server_id"`
	ToolName           string `json:"tool_name"`
	RequiredCompletion string `json:"required_completion"`
	ExposeResult       bool   `json:"expose_result"`
	SchemaSHA256       string `json:"schema_sha256"`
	DescriptionSHA256  string `json:"description_sha256"`
}

type modelLoopPendingCall struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ArgumentsSHA256 string `json:"arguments_sha256"`
	ArgumentsBytes  int    `json:"arguments_bytes"`
}

func newModelLoopCheckpoint(
	loop *modelLoopState,
	stage ModelLoopRecoveryStage,
	currentCallID string,
	revision uint64,
	now time.Time,
) *modelLoopCheckpoint {
	if loop == nil {
		return nil
	}
	tools := make([]modelLoopToolIdentity, len(loop.tools))
	for index, tool := range loop.tools {
		tools[index] = modelLoopToolIdentityFor(tool)
	}
	pending := make([]modelLoopPendingCall, len(loop.pending))
	for index, call := range loop.pending {
		argumentsSHA256, argumentsBytes := modelArgumentsMetadata(call.Arguments)
		pending[index] = modelLoopPendingCall{
			ID:              call.ID,
			Name:            call.Name,
			ArgumentsSHA256: argumentsSHA256,
			ArgumentsBytes:  argumentsBytes,
		}
	}
	return &modelLoopCheckpoint{
		Version:            modelLoopCheckpointVersion,
		Stage:              stage,
		Iteration:          loop.iteration,
		ToolCalls:          loop.toolCalls,
		Options:            loop.options,
		Tools:              tools,
		Pending:            pending,
		NextTool:           loop.nextTool,
		CurrentCallID:      currentCallID,
		CheckpointRevision: revision,
		UpdatedAt:          now.UTC(),
	}
}

func modelLoopToolIdentityFor(tool ModelTool) modelLoopToolIdentity {
	schemaSum := sha256.Sum256(tool.Schema)
	descriptionSum := sha256.Sum256([]byte(tool.Description))
	return modelLoopToolIdentity{
		Name:               tool.Name,
		ServerID:           tool.ServerID,
		ToolName:           tool.ToolName,
		RequiredCompletion: string(tool.RequiredCompletion),
		ExposeResult:       tool.ExposeResult,
		SchemaSHA256:       hex.EncodeToString(schemaSum[:]),
		DescriptionSHA256:  hex.EncodeToString(descriptionSum[:]),
	}
}

func modelArgumentsMetadata(arguments []byte) (string, int) {
	normalized, err := normalizeModelArguments(arguments)
	if err != nil {
		normalized = append([]byte(nil), arguments...)
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:]), len(normalized)
}

func (checkpoint *modelLoopCheckpoint) summary() *ModelLoopRecoverySummary {
	if checkpoint == nil {
		return nil
	}
	safe, blocked := modelLoopRecoverySafety(checkpoint.Stage)
	return &ModelLoopRecoverySummary{
		Stage:              checkpoint.Stage,
		SafeBoundary:       safe,
		BlockedReason:      blocked,
		Iteration:          checkpoint.Iteration,
		ToolCalls:          checkpoint.ToolCalls,
		PendingToolCalls:   len(checkpoint.Pending),
		CurrentCallID:      checkpoint.CurrentCallID,
		CheckpointRevision: checkpoint.CheckpointRevision,
		UpdatedAt:          checkpoint.UpdatedAt,
	}
}

func modelLoopRecoverySafety(stage ModelLoopRecoveryStage) (bool, string) {
	switch stage {
	case ModelLoopStageModelRequesting:
		return true, ""
	case ModelLoopStageToolsReady:
		return true, ""
	case ModelLoopStageToolDispatching, ModelLoopStageToolObserved:
		return false, "tool_execution_may_have_occurred"
	case ModelLoopStageWaitingApproval:
		return false, "approval_outcome_unknown"
	case ModelLoopStageWaitingExecution:
		return false, "tool_execution_unconfirmed"
	case ModelLoopStageCheckpointCorrupted:
		return false, "checkpoint_invalid"
	default:
		return false, "checkpoint_stage_unknown"
	}
}

func (s *Session) setModelLoopCheckpointLocked(stage ModelLoopRecoveryStage) {
	if s == nil || s.modelLoop == nil {
		return
	}
	s.modelLoopCheckpoint = newModelLoopCheckpoint(
		s.modelLoop,
		stage,
		s.snapshot.CurrentCallID,
		s.snapshot.Revision,
		s.now(),
	)
	s.snapshot.ModelLoopRecovery = s.modelLoopCheckpoint.summary()
}

func (s *Session) setModelLoopCheckpointStage(stage ModelLoopRecoveryStage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setModelLoopCheckpointLocked(stage)
}

func (s *Session) clearModelLoopCheckpointLocked() {
	if s == nil {
		return
	}
	s.modelLoopCheckpoint = nil
	s.snapshot.ModelLoopRecovery = nil
}

func (s *Session) updateModelLoopCheckpointForStateLocked(state State) {
	if s == nil || s.modelLoop == nil {
		return
	}
	switch state {
	case StateRunning:
		s.setModelLoopCheckpointLocked(ModelLoopStageToolObserved)
	case StateWaitingApproval:
		s.setModelLoopCheckpointLocked(ModelLoopStageWaitingApproval)
	case StateWaitingExecution:
		s.setModelLoopCheckpointLocked(ModelLoopStageWaitingExecution)
	case StateUnknown:
		s.setModelLoopCheckpointLocked(ModelLoopStageToolDispatching)
	default:
		s.clearModelLoopCheckpointLocked()
	}
}

func (s *Session) persistCheckpoint() error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	persist := s.persist
	s.mu.RUnlock()
	if persist == nil {
		return nil
	}
	return persist()
}

func (s *Session) modelLoopCheckpointCopy() *modelLoopCheckpoint {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneModelLoopCheckpoint(s.modelLoopCheckpoint)
}

func validateModelLoopCheckpoint(checkpoint *modelLoopCheckpoint) error {
	if checkpoint == nil {
		return nil
	}
	if checkpoint.Version != modelLoopCheckpointVersion {
		return fmt.Errorf("unsupported model checkpoint version %d", checkpoint.Version)
	}
	if _, blocked := modelLoopRecoverySafety(checkpoint.Stage); checkpoint.Stage == ModelLoopStageCheckpointCorrupted ||
		(blocked == "checkpoint_stage_unknown") {
		return fmt.Errorf("invalid model checkpoint stage %q", checkpoint.Stage)
	}
	if checkpoint.Iteration < 0 || checkpoint.Iteration > maxModelIterations {
		return fmt.Errorf("model checkpoint iteration %d is outside limits", checkpoint.Iteration)
	}
	if checkpoint.ToolCalls < 0 || checkpoint.ToolCalls > maxModelToolCalls {
		return fmt.Errorf("model checkpoint tool calls %d is outside limits", checkpoint.ToolCalls)
	}
	if checkpoint.NextTool < 0 || checkpoint.NextTool > len(checkpoint.Pending) {
		return fmt.Errorf("model checkpoint next tool %d is outside pending calls", checkpoint.NextTool)
	}
	if len(checkpoint.Tools) == 0 || len(checkpoint.Tools) > maxModelToolDefinitions {
		return fmt.Errorf("model checkpoint tool catalog has invalid size %d", len(checkpoint.Tools))
	}
	if _, err := normalizeModelOptions(checkpoint.Options); err != nil {
		return fmt.Errorf("model checkpoint options: %w", err)
	}
	for index, tool := range checkpoint.Tools {
		if !validModelToolName(tool.Name) || strings.TrimSpace(tool.ServerID) == "" ||
			strings.TrimSpace(tool.ToolName) == "" || len(tool.SchemaSHA256) != 64 ||
			len(tool.DescriptionSHA256) != 64 {
			return fmt.Errorf("model checkpoint tool %d is invalid", index)
		}
		if tool.RequiredCompletion != string(toolpipeline.CompletionAccepted) &&
			tool.RequiredCompletion != string(toolpipeline.CompletionCompleted) {
			return fmt.Errorf("model checkpoint tool %d completion %q is invalid", index, tool.RequiredCompletion)
		}
	}
	for index, call := range checkpoint.Pending {
		if strings.TrimSpace(call.ID) == "" || !validModelToolName(call.Name) ||
			len(call.ArgumentsSHA256) != 64 || call.ArgumentsBytes <= 0 {
			return fmt.Errorf("model checkpoint pending call %d is invalid", index)
		}
	}
	if checkpoint.UpdatedAt.IsZero() {
		return errors.New("model checkpoint update time is required")
	}
	return nil
}

func cloneModelLoopCheckpoint(checkpoint *modelLoopCheckpoint) *modelLoopCheckpoint {
	if checkpoint == nil {
		return nil
	}
	cloned := *checkpoint
	cloned.Tools = append([]modelLoopToolIdentity(nil), checkpoint.Tools...)
	cloned.Pending = append([]modelLoopPendingCall(nil), checkpoint.Pending...)
	return &cloned
}

func cloneRecoverySummary(summary *ModelLoopRecoverySummary) *ModelLoopRecoverySummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	return &cloned
}
