package agentsession

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
)

var (
	ErrModelRecoveryUnavailable = errors.New("model Agent Loop recovery is unavailable")
	ErrModelRecoveryBlocked     = errors.New("model Agent Loop recovery is blocked")
	ErrModelRecoveryMismatch    = errors.New("model Agent Loop recovery state does not match")
)

// ResumeModel explicitly resumes one Runtime-restarted model loop from a safe
// checkpoint. The caller supplies only the current trusted adapter, tool
// invoker, and tool catalog; none of those execution authorities are restored
// from the Session file.
func (m *Manager) ResumeModel(
	ctx context.Context,
	id string,
	adapter model.Adapter,
	invoker Invoker,
	tools []ModelTool,
) (ModelResult, error) {
	session, err := m.Get(id)
	if err != nil {
		return ModelResult{}, err
	}
	return session.ResumeModel(ctx, adapter, invoker, tools)
}

func (s *Session) ResumeModel(
	ctx context.Context,
	adapter model.Adapter,
	invoker Invoker,
	tools []ModelTool,
) (ModelResult, error) {
	if s == nil {
		return ModelResult{}, errors.New("Agent Session is required")
	}
	if adapter == nil {
		return ModelResult{}, ErrModelRequired
	}
	if invoker == nil {
		return ModelResult{}, ErrInvokerRequired
	}
	normalizedTools, byName, err := normalizeModelTools(tools)
	if err != nil {
		return ModelResult{Snapshot: s.Snapshot()}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.RLock()
	snapshot := cloneSnapshot(s.snapshot)
	checkpoint := cloneModelLoopCheckpoint(s.modelLoopCheckpoint)
	transcript := cloneModelMessages(s.transcript)
	claimed := s.modelLoop != nil || s.invoker != nil || s.resumeStarted
	s.mu.RUnlock()

	if snapshot.State != StateUnknown || !snapshot.Recovered ||
		snapshot.RecoveryReason != persistenceRecoveryReason || claimed {
		return ModelResult{Snapshot: snapshot}, fmt.Errorf(
			"%w: Session must be an unclaimed runtime_restart unknown state",
			ErrModelRecoveryUnavailable,
		)
	}
	if checkpoint == nil {
		return ModelResult{Snapshot: snapshot}, fmt.Errorf("%w: checkpoint is missing", ErrModelRecoveryUnavailable)
	}
	safe, blockedReason := modelLoopRecoverySafety(checkpoint.Stage)
	if !safe {
		if blockedReason == "" {
			blockedReason = "checkpoint_stage_unknown"
		}
		return ModelResult{Snapshot: snapshot}, fmt.Errorf(
			"%w: %s",
			ErrModelRecoveryBlocked,
			blockedReason,
		)
	}

	loop, err := rebuildRecoverableModelLoop(checkpoint, normalizedTools, byName, transcript)
	if err != nil {
		return ModelResult{Snapshot: snapshot}, err
	}

	runContext, runCancel := context.WithCancel(ctx)
	defer runCancel()
	s.mu.Lock()
	if s.snapshot.State != StateUnknown || s.snapshot.Revision != snapshot.Revision ||
		!equalModelLoopCheckpoint(s.modelLoopCheckpoint, checkpoint) ||
		s.modelLoop != nil || s.invoker != nil || s.resumeStarted {
		current := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return ModelResult{Snapshot: current}, fmt.Errorf(
			"%w: Session recovery was claimed by another action",
			ErrModelRecoveryUnavailable,
		)
	}
	loop.adapter = adapter
	s.modelLoop = loop
	s.invoker = invoker
	s.resumeStarted = true
	s.runCancel = runCancel
	s.toolDispatching = false
	s.snapshot.State = StateRunning
	s.snapshot.Revision++
	s.snapshot.CurrentCallID = ""
	s.snapshot.CurrentApprovalID = ""
	s.snapshot.CurrentApprovalStatus = ""
	s.snapshot.CurrentToolStatus = ""
	s.snapshot.CurrentExecution = nil
	s.snapshot.CurrentCompletion = ""
	s.snapshot.FinishedAt = nil
	started := cloneSnapshot(s.snapshot)
	s.mu.Unlock()
	defer s.endRunContext(runCancel)

	if err := s.record(eventlog.TypeAgentSessionResumed, started, map[string]any{
		"reason":              "manual_model_recovery",
		"checkpoint_stage":    checkpoint.Stage,
		"checkpoint_revision": checkpoint.CheckpointRevision,
	}); err != nil {
		return ModelResult{Snapshot: started}, err
	}
	return s.runModelLoop(runContext, ModelResult{Snapshot: started})
}

func rebuildRecoverableModelLoop(
	checkpoint *modelLoopCheckpoint,
	tools []ModelTool,
	byName map[string]ModelTool,
	messages []model.Message,
) (*modelLoopState, error) {
	if err := validateModelLoopCheckpoint(checkpoint); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrModelRecoveryMismatch, err)
	}
	if !modelLoopToolsMatchCheckpoint(checkpoint.Tools, tools) {
		return nil, fmt.Errorf("%w: current model tool catalog differs from the checkpoint", ErrModelRecoveryMismatch)
	}
	options, err := normalizeModelOptions(checkpoint.Options)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrModelRecoveryMismatch, err)
	}
	if checkpoint.ToolCalls > options.MaxToolCalls {
		return nil, fmt.Errorf("%w: tool-call budget no longer contains the checkpoint", ErrModelRecoveryMismatch)
	}
	assistantMessages, toolMessages, lastCalls, toolMessagesAfterLastCall, err :=
		validateRecoverableModelTranscript(checkpoint, messages)
	if err != nil {
		return nil, err
	}
	if toolMessages != checkpoint.ToolCalls {
		return nil, fmt.Errorf(
			"%w: checkpoint records %d completed tools but transcript contains %d results",
			ErrModelRecoveryMismatch,
			checkpoint.ToolCalls,
			toolMessages,
		)
	}

	loop := &modelLoopState{
		tools:     tools,
		byName:    byName,
		messages:  cloneModelMessages(messages),
		toolCalls: checkpoint.ToolCalls,
		options:   options,
	}
	switch checkpoint.Stage {
	case ModelLoopStageModelRequesting:
		if len(checkpoint.Pending) != 0 || checkpoint.NextTool != 0 {
			return nil, fmt.Errorf("%w: model-request checkpoint retains a pending tool batch", ErrModelRecoveryMismatch)
		}
		if assistantMessages != checkpoint.Iteration &&
			assistantMessages+1 != checkpoint.Iteration {
			return nil, fmt.Errorf("%w: model-request iteration count is inconsistent", ErrModelRecoveryMismatch)
		}
		// completeModelTurn advances the iteration before issuing the request.
		// Counting completed assistant responses avoids incrementing twice when
		// the previous request may have reached the provider before restart.
		loop.iteration = assistantMessages
	case ModelLoopStageToolsReady:
		if checkpoint.Iteration != assistantMessages || assistantMessages == 0 {
			return nil, fmt.Errorf("%w: tools-ready iteration count is inconsistent", ErrModelRecoveryMismatch)
		}
		if len(checkpoint.Pending) == 0 || checkpoint.NextTool >= len(checkpoint.Pending) {
			return nil, fmt.Errorf("%w: tools-ready checkpoint has no pending invocation", ErrModelRecoveryMismatch)
		}
		if len(lastCalls) != len(checkpoint.Pending) ||
			toolMessagesAfterLastCall != checkpoint.NextTool {
			return nil, fmt.Errorf("%w: pending model tool batch differs from the transcript", ErrModelRecoveryMismatch)
		}
		if checkpoint.ToolCalls+len(checkpoint.Pending) > options.MaxToolCalls {
			return nil, fmt.Errorf("%w: pending tools exceed the checkpoint budget", ErrModelRecoveryMismatch)
		}
		pending := make([]model.ToolCall, len(lastCalls))
		for index, call := range lastCalls {
			identity := checkpoint.Pending[index]
			argumentsSHA256, argumentsBytes := modelArgumentsMetadata(call.Arguments)
			if call.ID != identity.ID || call.Name != identity.Name ||
				argumentsSHA256 != identity.ArgumentsSHA256 ||
				argumentsBytes != identity.ArgumentsBytes {
				return nil, fmt.Errorf("%w: pending tool %d differs from the checkpoint", ErrModelRecoveryMismatch, index)
			}
			normalizedArguments, normalizeErr := normalizeModelArguments(call.Arguments)
			if normalizeErr != nil {
				return nil, fmt.Errorf("%w: pending tool %d arguments are invalid", ErrModelRecoveryMismatch, index)
			}
			pending[index] = model.ToolCall{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: normalizedArguments,
			}
		}
		loop.iteration = checkpoint.Iteration
		loop.pending = pending
		loop.nextTool = checkpoint.NextTool
	default:
		return nil, fmt.Errorf("%w: unsupported safe checkpoint stage %q", ErrModelRecoveryMismatch, checkpoint.Stage)
	}
	return loop, nil
}

func validateRecoverableModelTranscript(
	checkpoint *modelLoopCheckpoint,
	messages []model.Message,
) (assistantMessages int, toolMessages int, lastCalls []model.ToolCall, toolMessagesAfterLastCall int, err error) {
	if len(messages) < 2 {
		err = fmt.Errorf("%w: transcript has no system and user prefix", ErrModelRecoveryMismatch)
		return
	}
	if messages[0].Role != model.RoleSystem || messages[0].Content != modelSystemPrompt ||
		messages[1].Role != model.RoleUser || strings.TrimSpace(messages[1].Content) == "" {
		err = fmt.Errorf("%w: transcript prefix is not a recoverable Harness request", ErrModelRecoveryMismatch)
		return
	}
	if messages[0].ReasoningContent != "" || messages[1].ReasoningContent != "" {
		err = fmt.Errorf("%w: transcript prefix contains unsupported reasoning state", ErrModelRecoveryMismatch)
		return
	}

	seenCallIDs := make(map[string]bool)
	activeCalls := make(map[string]bool)
	completedActiveCalls := make(map[string]bool)
	activeOrder := make([]model.ToolCall, 0)
	lastToolCallIndex := -1
	for index := 2; index < len(messages); index++ {
		message := messages[index]
		if message.ReasoningContent != "" {
			err = fmt.Errorf("%w: persisted reasoning content is not supported", ErrModelRecoveryMismatch)
			return
		}
		switch message.Role {
		case model.RoleAssistant:
			if message.ToolCallID != "" || message.Name != "" {
				err = fmt.Errorf("%w: assistant message contains tool-result fields", ErrModelRecoveryMismatch)
				return
			}
			if len(activeCalls) != len(completedActiveCalls) {
				err = fmt.Errorf("%w: a model tool batch is incomplete before the next assistant turn", ErrModelRecoveryMismatch)
				return
			}
			assistantMessages++
			activeCalls = make(map[string]bool, len(message.ToolCalls))
			completedActiveCalls = make(map[string]bool, len(message.ToolCalls))
			activeOrder = make([]model.ToolCall, len(message.ToolCalls))
			lastCalls = nil
			if len(message.ToolCalls) == 0 {
				continue
			}
			lastToolCallIndex = index
			lastCalls = make([]model.ToolCall, len(message.ToolCalls))
			for callIndex, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || !validModelToolName(call.Name) {
					err = fmt.Errorf("%w: assistant tool call identity is invalid", ErrModelRecoveryMismatch)
					return
				}
				if _, lookupErr := normalizeModelArguments(call.Arguments); lookupErr != nil {
					err = fmt.Errorf("%w: assistant tool call arguments are invalid", ErrModelRecoveryMismatch)
					return
				}
				if seenCallIDs[call.ID] {
					err = fmt.Errorf("%w: assistant tool call ID is duplicated", ErrModelRecoveryMismatch)
					return
				}
				seenCallIDs[call.ID] = true
				activeCalls[call.ID] = true
				activeOrder[callIndex] = cloneModelToolCall(call)
				lastCalls[callIndex] = cloneModelToolCall(call)
			}
		case model.RoleTool:
			if message.ToolCallID == "" || !activeCalls[message.ToolCallID] {
				err = fmt.Errorf("%w: tool result has no current assistant tool call", ErrModelRecoveryMismatch)
				return
			}
			if completedActiveCalls[message.ToolCallID] {
				err = fmt.Errorf("%w: tool result ID is duplicated", ErrModelRecoveryMismatch)
				return
			}
			expected := activeOrder[len(completedActiveCalls)]
			if message.ToolCallID != expected.ID || message.Name != expected.Name {
				err = fmt.Errorf("%w: tool results do not follow the persisted tool-call order", ErrModelRecoveryMismatch)
				return
			}
			completedActiveCalls[message.ToolCallID] = true
			toolMessages++
		default:
			err = fmt.Errorf("%w: transcript contains an unsupported role", ErrModelRecoveryMismatch)
			return
		}
	}
	if checkpoint.Stage != ModelLoopStageToolsReady && len(activeCalls) != len(completedActiveCalls) {
		err = fmt.Errorf("%w: transcript ends with an incomplete model tool batch", ErrModelRecoveryMismatch)
		return
	}
	if checkpoint.Stage == ModelLoopStageToolsReady && lastToolCallIndex < 0 {
		err = fmt.Errorf("%w: tools-ready checkpoint has no tool-call turn", ErrModelRecoveryMismatch)
		return
	}
	toolMessagesAfterLastCall = toolMessages
	if lastToolCallIndex >= 0 {
		toolMessagesAfterLastCall = 0
		for index := lastToolCallIndex + 1; index < len(messages); index++ {
			if messages[index].Role != model.RoleTool {
				toolMessagesAfterLastCall = -1
				break
			}
			toolMessagesAfterLastCall++
		}
	}
	return
}

func modelLoopToolsMatchCheckpoint(expected []modelLoopToolIdentity, tools []ModelTool) bool {
	if len(expected) != len(tools) {
		return false
	}
	for index, tool := range tools {
		actual := modelLoopToolIdentityFor(tool)
		if expected[index] != actual {
			return false
		}
	}
	return true
}

func cloneModelToolCall(call model.ToolCall) model.ToolCall {
	cloned := call
	cloned.Arguments = append([]byte(nil), call.Arguments...)
	return cloned
}

func equalModelLoopCheckpoint(left, right *modelLoopCheckpoint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftCopy := cloneModelLoopCheckpoint(left)
	rightCopy := cloneModelLoopCheckpoint(right)
	leftCopy.UpdatedAt = leftCopy.UpdatedAt.UTC()
	rightCopy.UpdatedAt = rightCopy.UpdatedAt.UTC()
	return reflect.DeepEqual(leftCopy, rightCopy)
}
