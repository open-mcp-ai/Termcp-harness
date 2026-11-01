// Package agentsession provides the deterministic first Agent Session kernel.
// It executes explicit steps through a guarded tool invoker; model integration
// and conversational context are deliberately outside this first slice.
package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

type State string

const (
	StateNew              State = "new"
	StateRunning          State = "running"
	StateCancelling       State = "cancelling"
	StateWaitingApproval  State = "waiting_approval"
	StateWaitingExecution State = "waiting_execution"
	StateCompleted        State = "completed"
	StateBlocked          State = "blocked"
	StateFailed           State = "failed"
	StateCancelled        State = "cancelled"
	StateExpired          State = "expired"
	StateUnknown          State = "unknown"
)

var (
	ErrInvalidState         = errors.New("invalid Agent Session state transition")
	ErrInvokerRequired      = errors.New("tool invoker is required")
	ErrNoSteps              = errors.New("Agent Session requires at least one step")
	ErrSessionNotNew        = errors.New("Agent Session has already run")
	ErrModelRequired        = errors.New("model adapter is required")
	ErrModelTools           = errors.New("model Agent Loop requires at least one explicit tool")
	ErrModelInput           = errors.New("model Agent Loop requires non-empty input")
	ErrCancellationConflict = errors.New("Agent Session cancellation conflict")
)

type Step = toolpipeline.Request

type Invoker interface {
	Invoke(context.Context, toolpipeline.Request) (toolpipeline.Outcome, error)
}

type CancellationPhase string

const (
	CancellationBeforeDispatch   CancellationPhase = "before_dispatch"
	CancellationModelRequest     CancellationPhase = "model_request"
	CancellationToolDispatch     CancellationPhase = "tool_dispatch"
	CancellationWaitingApproval  CancellationPhase = "waiting_approval"
	CancellationWaitingExecution CancellationPhase = "waiting_execution"
)

type Cancellation struct {
	RequestedAt          time.Time         `json:"requested_at"`
	Phase                CancellationPhase `json:"phase"`
	RemoteOutcomeUnknown bool              `json:"remote_outcome_unknown,omitempty"`
	ApprovalPending      bool              `json:"approval_pending,omitempty"`
}

type Snapshot struct {
	ID                    string                             `json:"id"`
	CoreSessionID         string                             `json:"core_session_id,omitempty"`
	State                 State                              `json:"state"`
	Revision              uint64                             `json:"revision"`
	CurrentCallID         string                             `json:"current_call_id,omitempty"`
	CurrentApprovalID     string                             `json:"current_approval_id,omitempty"`
	CurrentApprovalStatus approval.Status                    `json:"current_approval_status,omitempty"`
	CurrentToolStatus     toolcall.Status                    `json:"current_tool_status,omitempty"`
	CurrentExecution      *approval.Execution                `json:"current_execution,omitempty"`
	CurrentCompletion     toolpipeline.CompletionRequirement `json:"current_completion,omitempty"`
	ModelTurns            int                                `json:"model_turns,omitempty"`
	ModelToolCalls        int                                `json:"model_tool_calls,omitempty"`
	TranscriptMessages    int                                `json:"transcript_messages,omitempty"`
	OutputBytes           int                                `json:"output_bytes,omitempty"`
	Recovered             bool                               `json:"recovered,omitempty"`
	RecoveryReason        string                             `json:"recovery_reason,omitempty"`
	ModelLoopRecovery     *ModelLoopRecoverySummary          `json:"model_loop_recovery,omitempty"`
	Cancellation          *Cancellation                      `json:"cancellation,omitempty"`
	StartedAt             *time.Time                         `json:"started_at,omitempty"`
	FinishedAt            *time.Time                         `json:"finished_at,omitempty"`
}

type Result struct {
	Outcomes []toolpipeline.Outcome
	Snapshot Snapshot
}

type Session struct {
	mu                  sync.RWMutex
	snapshot            Snapshot
	journal             *eventlog.Journal
	now                 func() time.Time
	remainingSteps      []Step
	lastResultCall      string
	lastResultStatus    toolcall.Status
	currentCompletion   toolpipeline.CompletionRequirement
	approvalRevision    approval.Revision
	invoker             Invoker
	resumeStarted       bool
	modelLoop           *modelLoopState
	modelLoopCheckpoint *modelLoopCheckpoint
	transcript          []model.Message
	finalOutput         string
	persist             func() error
	runCancel           context.CancelFunc
	toolDispatching     bool
}

func New(id, coreSessionID string, journal *eventlog.Journal) (*Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("Agent Session ID is required")
	}
	if len(id) > 128 {
		return nil, errors.New("Agent Session ID exceeds 128 characters")
	}
	if journal == nil {
		return nil, errors.New("event log is required for Agent Session")
	}
	return &Session{
		snapshot: Snapshot{
			ID:            id,
			CoreSessionID: strings.TrimSpace(coreSessionID),
			State:         StateNew,
			Revision:      1,
		},
		journal: journal,
		now:     time.Now,
	}, nil
}

func (s *Session) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.snapshot)
}

func (s *Session) matchesToolResult(callID string, status toolcall.Status) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastResultCall == callID && s.lastResultStatus == status
}

// Run executes explicit steps in order. It never retries a step and stops at
// the first non-success terminal tool state.
func (s *Session) Run(ctx context.Context, invoker Invoker, steps []Step) (Result, error) {
	if s == nil {
		return Result{}, errors.New("Agent Session is required")
	}
	if invoker == nil {
		return Result{}, ErrInvokerRequired
	}
	if len(steps) == 0 {
		return Result{}, ErrNoSteps
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runContext, runCancel := context.WithCancel(ctx)

	s.mu.Lock()
	if s.snapshot.State != StateNew {
		current := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		runCancel()
		return Result{Snapshot: current}, fmt.Errorf("%w: state is %s", ErrSessionNotNew, current.State)
	}
	now := s.now().UTC()
	s.snapshot.State = StateRunning
	s.snapshot.Revision++
	s.snapshot.StartedAt = &now
	s.invoker = invoker
	s.resumeStarted = false
	s.runCancel = runCancel
	s.toolDispatching = false
	started := cloneSnapshot(s.snapshot)
	s.mu.Unlock()
	defer s.endRunContext(runCancel)

	if err := s.record(eventlog.TypeAgentSessionStarted, started, nil); err != nil {
		return Result{Snapshot: started}, err
	}

	result := Result{
		Outcomes: make([]toolpipeline.Outcome, 0, len(steps)),
		Snapshot: started,
	}
	return s.runSteps(runContext, invoker, steps, result)
}

func (s *Session) runSteps(
	ctx context.Context,
	invoker Invoker,
	steps []Step,
	result Result,
) (Result, error) {
	for index, step := range steps {
		if err := ctx.Err(); err != nil {
			snapshot, stateErr := s.transition(StateCancelled, "", "", eventlog.TypeAgentSessionCancelled)
			result.Snapshot = snapshot
			return result, errors.Join(err, stateErr)
		}
		step.CallID = strings.TrimSpace(step.CallID)
		if step.CallID == "" {
			snapshot, stateErr := s.transition(StateFailed, "", "", eventlog.TypeAgentSessionFailed)
			result.Snapshot = snapshot
			return result, errors.Join(errors.New("step call ID is required"), stateErr)
		}
		completion, completionErr := normalizeCompletion(step.RequiredCompletion)
		if completionErr != nil {
			snapshot, stateErr := s.transition(StateFailed, "", "", eventlog.TypeAgentSessionFailed)
			result.Snapshot = snapshot
			return result, errors.Join(completionErr, stateErr)
		}
		step.RequiredCompletion = completion

		current, err := s.setCurrentCall(step, steps[index+1:])
		if err != nil {
			result.Snapshot = current
			return result, err
		}
		if err := s.record(eventlog.TypeAgentToolStarted, current, nil); err != nil {
			return result, err
		}

		if !s.beginToolDispatch() {
			if s.cancellationRequested() {
				snapshot, stateErr := s.transition(StateCancelled, "", "", eventlog.TypeAgentSessionCancelled)
				result.Snapshot = snapshot
				return result, stateErr
			}
			result.Snapshot = s.Snapshot()
			return result, ErrInvalidState
		}
		outcome, invokeErr := invoker.Invoke(ctx, step)
		result.Outcomes = append(result.Outcomes, outcome)
		state, approvalID := stateForOutcome(outcome, invokeErr)
		snapshot, stateErr := s.finishTool(step.CallID, state, approvalID, outcome.Call.Status)
		s.setToolDispatching(false)
		result.Snapshot = snapshot
		joinErr := errors.Join(invokeErr, stateErr)
		if state != StateRunning {
			return result, joinErr
		}
		if joinErr != nil {
			return result, joinErr
		}
	}

	completed, err := s.complete()
	result.Snapshot = completed
	return result, err
}

// Cancel requests a user stop. It only reports a safe cancellation before any
// tool side effect; a tool already being dispatched converges from its actual
// result and remains unknown when that result cannot be proven.
func (s *Session) Cancel(expectedRevision uint64) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("Agent Session is required")
	}
	s.mu.Lock()
	current := cloneSnapshot(s.snapshot)
	if current.State == StateCancelled || current.State == StateCancelling {
		s.mu.Unlock()
		return current, nil
	}
	if current.State.IsTerminal() {
		s.mu.Unlock()
		return current, fmt.Errorf("%w: %s is already terminal", ErrCancellationConflict, current.State)
	}
	if expectedRevision == 0 || current.Revision != expectedRevision {
		s.mu.Unlock()
		return current, fmt.Errorf(
			"%w: expected revision %d, current revision %d",
			ErrCancellationConflict,
			expectedRevision,
			current.Revision,
		)
	}

	now := s.now().UTC()
	phase := cancellationPhaseLocked(s)
	s.snapshot.Cancellation = &Cancellation{
		RequestedAt: now,
		Phase:       phase,
	}
	if current.State == StateWaitingApproval {
		s.snapshot.Cancellation.Phase = CancellationWaitingApproval
		s.snapshot.Cancellation.ApprovalPending = true
	}
	if current.State == StateWaitingExecution {
		s.snapshot.Cancellation.Phase = CancellationWaitingExecution
		s.snapshot.Cancellation.RemoteOutcomeUnknown = true
	}
	s.snapshot.Revision++

	var cancelRun context.CancelFunc
	if current.State == StateRunning {
		s.snapshot.State = StateCancelling
		cancelRun = s.runCancel
	} else {
		s.snapshot.State = StateCancelled
		s.snapshot.FinishedAt = &now
		s.remainingSteps = nil
		s.currentCompletion = ""
		s.approvalRevision = 0
		s.invoker = nil
		s.runCancel = nil
		s.toolDispatching = false
		s.resumeStarted = false
		s.modelLoop = nil
		s.clearModelLoopCheckpointLocked()
	}
	requested := cloneSnapshot(s.snapshot)
	s.mu.Unlock()

	recordErr := s.record(eventlog.TypeAgentSessionCancelRequested, requested, map[string]any{
		"phase":                  phase,
		"approval_pending":       requested.Cancellation != nil && requested.Cancellation.ApprovalPending,
		"remote_outcome_unknown": requested.Cancellation != nil && requested.Cancellation.RemoteOutcomeUnknown,
	})
	if cancelRun != nil {
		cancelRun()
		return requested, recordErr
	}
	cancelledErr := s.record(eventlog.TypeAgentSessionCancelled, requested, nil)
	return requested, errors.Join(recordErr, cancelledErr)
}

func (s *Session) setToolDispatching(active bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.toolDispatching = active
	s.mu.Unlock()
}

func (s *Session) beginToolDispatch() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.State != StateRunning {
		return false
	}
	s.toolDispatching = true
	return true
}

func (s *Session) cancellationRequested() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.State == StateCancelling
}

func cancellationPhaseLocked(s *Session) CancellationPhase {
	if s.toolDispatching {
		return CancellationToolDispatch
	}
	if s.modelLoopCheckpoint != nil {
		switch s.modelLoopCheckpoint.Stage {
		case ModelLoopStageModelRequesting:
			return CancellationModelRequest
		case ModelLoopStageWaitingApproval:
			return CancellationWaitingApproval
		case ModelLoopStageWaitingExecution:
			return CancellationWaitingExecution
		case ModelLoopStageToolDispatching, ModelLoopStageToolObserved:
			return CancellationToolDispatch
		}
	}
	return CancellationBeforeDispatch
}

func (s *Session) endRunContext(cancel context.CancelFunc) {
	cancel()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.runCancel = nil
	s.toolDispatching = false
	s.mu.Unlock()
}

// ObserveApproval records a remotely confirmed approval transition. Approval
// does not prove the queued tool executed, so it moves to waiting_execution
// until separate trusted tool-result evidence arrives.
func (s *Session) ObserveApproval(callID string, observed approval.Snapshot) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("Agent Session is required")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" || strings.TrimSpace(observed.ID) == "" {
		return Snapshot{}, errors.New("approval and call IDs are required")
	}
	if observed.CallID != callID {
		return Snapshot{}, fmt.Errorf("%w: approval call %q does not match %q", ErrInvalidState, observed.CallID, callID)
	}

	s.mu.Lock()
	if s.snapshot.State != StateWaitingApproval && s.snapshot.State != StateWaitingExecution {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf("%w: %s cannot observe approval", ErrInvalidState, snapshot.State)
	}
	if s.snapshot.CurrentCallID != callID {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf("%w: current call %q does not match %q", ErrInvalidState, snapshot.CurrentCallID, callID)
	}
	if s.snapshot.CurrentApprovalID != "" && s.snapshot.CurrentApprovalID != observed.ID {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf("%w: approval %q does not match %q", ErrInvalidState, snapshot.CurrentApprovalID, observed.ID)
	}
	if observed.Revision != 0 && s.approvalRevision != 0 {
		if observed.Revision < s.approvalRevision {
			snapshot := cloneSnapshot(s.snapshot)
			s.mu.Unlock()
			return snapshot, fmt.Errorf(
				"%w: stale approval revision %d after %d",
				ErrInvalidState,
				observed.Revision,
				s.approvalRevision,
			)
		}
		if observed.Revision == s.approvalRevision && observed.Status != s.snapshot.CurrentApprovalStatus {
			snapshot := cloneSnapshot(s.snapshot)
			s.mu.Unlock()
			return snapshot, fmt.Errorf(
				"%w: approval revision %d cannot change status from %s to %s",
				ErrInvalidState,
				observed.Revision,
				s.snapshot.CurrentApprovalStatus,
				observed.Status,
			)
		}
	}
	if observed.Execution != nil && observed.Status != approval.StatusApproved {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf("%w: execution requires approved status", ErrInvalidState)
	}
	if !approvalObservationPermitted(s.snapshot.CurrentApprovalStatus, observed.Status) {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf(
			"%w: approval status %s cannot become %s",
			ErrInvalidState,
			s.snapshot.CurrentApprovalStatus,
			observed.Status,
		)
	}
	if s.snapshot.CurrentApprovalStatus == observed.Status &&
		(observed.Execution == nil ||
			(s.snapshot.CurrentExecution != nil &&
				equalApprovalExecution(s.snapshot.CurrentExecution, observed.Execution))) {
		if observed.Revision > s.approvalRevision {
			s.approvalRevision = observed.Revision
		}
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, nil
	}
	if s.snapshot.CurrentApprovalStatus == approval.StatusApproved &&
		observed.Status == approval.StatusApproved {
		if err := approval.ValidateExecutionTransition(s.snapshot.CurrentExecution, observed.Execution); err != nil {
			snapshot := cloneSnapshot(s.snapshot)
			s.mu.Unlock()
			return snapshot, fmt.Errorf("%w: %v", ErrInvalidState, err)
		}
	}

	nextState := s.snapshot.State
	terminal := false
	toolStatus := s.snapshot.CurrentToolStatus
	switch observed.Status {
	case approval.StatusPending, approval.StatusSubmitting, approval.StatusUnknown:
		nextState = StateWaitingApproval
	case approval.StatusApproved:
		nextState = StateWaitingExecution
		if observed.Execution != nil {
			toolStatus = toolStatusForExecution(observed.Execution.State)
			if observed.Execution.State == approval.ExecutionFailed {
				nextState = StateFailed
				terminal = true
			}
		}
	case approval.StatusRejected:
		nextState = StateBlocked
		terminal = true
	case approval.StatusExpired:
		nextState = StateExpired
		terminal = true
	case approval.StatusCancelled:
		nextState = StateCancelled
		terminal = true
	default:
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf("unsupported approval status %q", observed.Status)
	}

	s.snapshot.State = nextState
	s.snapshot.CurrentApprovalStatus = observed.Status
	s.snapshot.CurrentExecution = cloneApprovalExecution(observed.Execution)
	if observed.Revision > s.approvalRevision {
		s.approvalRevision = observed.Revision
	}
	if toolStatus != "" {
		s.snapshot.CurrentToolStatus = toolStatus
	}
	s.snapshot.Revision++
	s.updateModelLoopCheckpointForStateLocked(nextState)
	if terminal {
		now := s.now().UTC()
		s.snapshot.FinishedAt = &now
		s.remainingSteps = nil
		s.invoker = nil
		s.modelLoop = nil
	} else {
		s.snapshot.FinishedAt = nil
	}
	snapshot := cloneSnapshot(s.snapshot)
	s.mu.Unlock()

	extra := map[string]any{
		"approval_status": observed.Status,
	}
	if observed.Execution != nil {
		extra["execution_state"] = observed.Execution.State
	}
	if err := s.record(eventlog.TypeAgentApprovalObserved, snapshot, extra); err != nil {
		return snapshot, err
	}
	if nextState == StateWaitingExecution {
		return snapshot, s.record(eventlog.TypeAgentSessionWaitingExec, snapshot, nil)
	}
	if terminal {
		return snapshot, s.record(sessionEventForState(nextState), snapshot, nil)
	}
	return snapshot, nil
}

// startAutomaticResume consumes a trusted execution result and continues the
// remaining explicit steps in the background. Accepted completion lets a
// trusted submission continue the Session without relabeling it as succeeded;
// completed completion requires succeeded execution evidence.
func (s *Session) startAutomaticResume() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.snapshot.State != StateWaitingExecution || s.resumeStarted || s.invoker == nil {
		s.mu.Unlock()
		return
	}
	if s.snapshot.CurrentExecution == nil {
		s.mu.Unlock()
		return
	}
	status, ok := automaticToolStatus(
		s.snapshot.CurrentExecution.State,
		s.currentCompletion,
	)
	if !ok {
		s.mu.Unlock()
		return
	}
	callID := s.snapshot.CurrentCallID
	invoker := s.invoker
	s.resumeStarted = true
	s.mu.Unlock()

	outcome := toolpipeline.Outcome{Call: toolcall.Snapshot{ID: callID, Status: status}}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result, err := s.ObserveToolResult(ctx, invoker, outcome)
		if err != nil {
			_ = s.recordResumeFailure(err)
			s.mu.Lock()
			if s.snapshot.State == StateWaitingExecution {
				s.resumeStarted = false
			}
			s.mu.Unlock()
			return
		}
		if result.Snapshot.State == StateWaitingExecution || result.Snapshot.State == StateWaitingApproval {
			s.mu.Lock()
			s.resumeStarted = false
			s.mu.Unlock()
		}
	}()
}

func (s *Session) recordResumeFailure(cause error) error {
	s.mu.RLock()
	subjectID := s.snapshot.ID
	correlationID := s.snapshot.CurrentCallID
	state := s.snapshot.State
	s.mu.RUnlock()
	data, err := json.Marshal(map[string]any{
		"reason":        "automatic_resume_failed",
		"error_bytes":   len(cause.Error()),
		"session_state": state,
	})
	if err != nil {
		return err
	}
	_, err = s.journal.Append(eventlog.Entry{
		Type:          eventlog.TypeAgentSessionResumeFailed,
		Class:         eventlog.ClassDiagnostic,
		SubjectID:     subjectID,
		CorrelationID: correlationID,
		Data:          data,
	})
	return err
}

// ObserveToolResult applies trusted evidence for the call that was waiting on
// approval. A successful result resumes only the remaining steps; failure
// statuses stop the session, while unknown keeps it in waiting_execution so
// late trusted evidence can still converge the outcome.
func (s *Session) ObserveToolResult(
	ctx context.Context,
	invoker Invoker,
	observed toolpipeline.Outcome,
) (Result, error) {
	if s == nil {
		return Result{}, errors.New("Agent Session is required")
	}
	if invoker == nil {
		return Result{}, ErrInvokerRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callID := strings.TrimSpace(observed.Call.ID)
	if callID == "" {
		return Result{}, errors.New("tool result call ID is required")
	}
	switch observed.Call.Status {
	case toolcall.StatusSucceeded,
		toolcall.StatusAccepted,
		toolcall.StatusFailed,
		toolcall.StatusBlocked,
		toolcall.StatusCancelled,
		toolcall.StatusExpired,
		toolcall.StatusUnknown:
	default:
		return Result{}, fmt.Errorf("tool status %q is not a terminal result", observed.Call.Status)
	}
	runContext, runCancel := context.WithCancel(ctx)
	defer runCancel()

	s.mu.Lock()
	if s.lastResultCall == callID && s.lastResultStatus == observed.Call.Status {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return Result{Outcomes: []toolpipeline.Outcome{observed}, Snapshot: snapshot}, nil
	}
	if s.snapshot.State != StateWaitingExecution {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return Result{Snapshot: snapshot}, fmt.Errorf("%w: %s cannot observe a tool result", ErrInvalidState, snapshot.State)
	}
	if s.snapshot.CurrentCallID != callID {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return Result{Snapshot: snapshot}, fmt.Errorf(
			"%w: current call %q does not match tool result %q",
			ErrInvalidState,
			snapshot.CurrentCallID,
			callID,
		)
	}
	if s.snapshot.CurrentApprovalStatus != approval.StatusApproved {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return Result{Snapshot: snapshot}, fmt.Errorf(
			"%w: tool result requires approved state, got %q",
			ErrInvalidState,
			snapshot.CurrentApprovalStatus,
		)
	}
	if observed.Call.Status == toolcall.StatusAccepted && s.currentCompletion != toolpipeline.CompletionAccepted {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return Result{Snapshot: snapshot}, fmt.Errorf(
			"%w: accepted result cannot satisfy %s completion",
			ErrInvalidState,
			s.currentCompletion,
		)
	}
	s.snapshot.CurrentToolStatus = observed.Call.Status
	s.snapshot.Revision++
	s.lastResultCall = callID
	s.lastResultStatus = observed.Call.Status
	nextState := StateWaitingExecution
	terminal := false
	switch observed.Call.Status {
	case toolcall.StatusSucceeded, toolcall.StatusAccepted:
		nextState = StateRunning
	case toolcall.StatusFailed:
		nextState = StateFailed
		terminal = true
	case toolcall.StatusBlocked:
		nextState = StateBlocked
		terminal = true
	case toolcall.StatusCancelled:
		nextState = StateCancelled
		terminal = true
	case toolcall.StatusExpired:
		nextState = StateExpired
		terminal = true
	case toolcall.StatusUnknown:
		nextState = StateWaitingExecution
	}
	s.snapshot.State = nextState
	active := nextState == StateRunning
	if active {
		s.runCancel = runCancel
		s.toolDispatching = false
	}
	s.updateModelLoopCheckpointForStateLocked(nextState)
	if terminal {
		now := s.now().UTC()
		s.snapshot.FinishedAt = &now
		s.remainingSteps = nil
		s.invoker = nil
		s.modelLoop = nil
	} else {
		s.snapshot.FinishedAt = nil
	}
	remaining := cloneSteps(s.remainingSteps)
	snapshot := cloneSnapshot(s.snapshot)
	s.mu.Unlock()
	if active {
		defer s.endRunContext(runCancel)
	}

	if err := s.record(eventlog.TypeAgentToolResultObserved, snapshot, map[string]any{
		"tool_status": observed.Call.Status,
	}); err != nil {
		return Result{Outcomes: []toolpipeline.Outcome{observed}, Snapshot: snapshot}, err
	}
	if nextState == StateWaitingExecution {
		return Result{Outcomes: []toolpipeline.Outcome{observed}, Snapshot: snapshot}, nil
	}
	if terminal {
		return Result{Outcomes: []toolpipeline.Outcome{observed}, Snapshot: snapshot},
			s.record(sessionEventForState(nextState), snapshot, nil)
	}
	if err := s.record(eventlog.TypeAgentSessionResumed, snapshot, nil); err != nil {
		return Result{Outcomes: []toolpipeline.Outcome{observed}, Snapshot: snapshot}, err
	}
	if s.hasModelLoop() {
		if err := s.acceptObservedModelToolResult(observed); err != nil {
			errSnapshot, stateErr := s.failModel("tool_result_projection_failed", err)
			return Result{Outcomes: []toolpipeline.Outcome{observed}, Snapshot: errSnapshot}, errors.Join(err, stateErr)
		}
		next, loopErr := s.runModelLoop(runContext, ModelResult{
			Outcomes: []toolpipeline.Outcome{observed},
			Snapshot: s.Snapshot(),
		})
		return Result{Outcomes: next.Outcomes, Snapshot: next.Snapshot}, loopErr
	}
	return s.runSteps(
		runContext,
		invoker,
		remaining,
		Result{Outcomes: []toolpipeline.Outcome{observed}, Snapshot: snapshot},
	)
}

func stateForOutcome(outcome toolpipeline.Outcome, invokeErr error) (State, string) {
	if invokeErr != nil {
		approvalID := ""
		if outcome.Approval != nil && outcome.Approval.Correlation == approval.CorrelationTrusted {
			approvalID = outcome.Approval.ID
		}
		return StateUnknown, approvalID
	}
	switch outcome.Call.Status {
	case toolcall.StatusSucceeded:
		return StateRunning, ""
	case toolcall.StatusApprovalPending:
		if outcome.Approval != nil && outcome.Approval.Correlation == approval.CorrelationTrusted {
			return StateWaitingApproval, outcome.Approval.ID
		}
		return StateWaitingApproval, ""
	case toolcall.StatusBlocked:
		return StateBlocked, ""
	case toolcall.StatusFailed:
		return StateFailed, ""
	case toolcall.StatusCancelled:
		return StateCancelled, ""
	case toolcall.StatusExpired:
		return StateExpired, ""
	case toolcall.StatusUnknown:
		return StateUnknown, ""
	default:
		return StateFailed, ""
	}
}

func (s *Session) setCurrentCall(step Step, remaining []Step) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.State != StateRunning {
		return cloneSnapshot(s.snapshot), fmt.Errorf("%w: %s -> running", ErrInvalidState, s.snapshot.State)
	}
	s.snapshot.CurrentCallID = step.CallID
	s.snapshot.CurrentApprovalID = ""
	s.snapshot.CurrentApprovalStatus = ""
	s.snapshot.CurrentToolStatus = ""
	s.snapshot.CurrentExecution = nil
	s.snapshot.CurrentCompletion = step.RequiredCompletion
	s.currentCompletion = step.RequiredCompletion
	s.approvalRevision = 0
	s.remainingSteps = cloneSteps(remaining)
	s.lastResultCall = ""
	s.lastResultStatus = ""
	s.resumeStarted = false
	s.snapshot.Revision++
	return cloneSnapshot(s.snapshot), nil
}

func (s *Session) finishTool(
	callID string,
	state State,
	approvalID string,
	toolStatus toolcall.Status,
) (Snapshot, error) {
	s.mu.Lock()
	if (s.snapshot.State != StateRunning && s.snapshot.State != StateCancelling) ||
		s.snapshot.CurrentCallID != callID {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf("%w: current call %q", ErrInvalidState, snapshot.CurrentCallID)
	}
	cancelRequested := s.snapshot.State == StateCancelling
	if cancelRequested {
		switch state {
		case StateRunning:
			state = StateCancelled
		case StateUnknown:
			if s.snapshot.Cancellation == nil {
				s.snapshot.Cancellation = &Cancellation{
					RequestedAt: s.now().UTC(),
					Phase:       CancellationToolDispatch,
				}
			}
			s.snapshot.Cancellation.RemoteOutcomeUnknown = true
		default:
			s.snapshot.Cancellation = nil
		}
	}
	s.snapshot.State = state
	s.snapshot.CurrentApprovalID = approvalID
	s.snapshot.CurrentToolStatus = toolStatus
	s.snapshot.Revision++
	s.toolDispatching = false
	s.updateModelLoopCheckpointForStateLocked(state)
	if state.IsTerminal() {
		now := s.now().UTC()
		s.snapshot.FinishedAt = &now
		s.remainingSteps = nil
		s.invoker = nil
		s.runCancel = nil
		s.modelLoop = nil
	} else {
		s.snapshot.FinishedAt = nil
	}
	snapshot := cloneSnapshot(s.snapshot)
	eventType := sessionEventForState(state)
	s.mu.Unlock()
	recordErr := s.record(eventlog.TypeAgentToolFinished, snapshot, map[string]any{
		"session_state": state,
		"tool_status":   toolStatus,
	})
	if recordErr != nil {
		return snapshot, recordErr
	}
	if eventType != "" {
		return snapshot, s.record(eventType, snapshot, nil)
	}
	return snapshot, nil
}

func (s *Session) transition(state State, callID, approvalID string, eventType eventlog.Type) (Snapshot, error) {
	s.mu.Lock()
	now := s.now().UTC()
	cancellation := cloneCancellation(s.snapshot.Cancellation)
	if state != StateCancelled {
		cancellation = nil
	}
	s.snapshot.State = state
	s.snapshot.CurrentCallID = callID
	s.snapshot.CurrentApprovalID = approvalID
	s.snapshot.CurrentToolStatus = ""
	s.snapshot.CurrentExecution = nil
	s.snapshot.CurrentCompletion = ""
	s.snapshot.Cancellation = cancellation
	s.snapshot.Revision++
	s.snapshot.FinishedAt = &now
	s.remainingSteps = nil
	s.currentCompletion = ""
	s.approvalRevision = 0
	s.invoker = nil
	s.runCancel = nil
	s.toolDispatching = false
	s.resumeStarted = false
	s.modelLoop = nil
	s.clearModelLoopCheckpointLocked()
	snapshot := cloneSnapshot(s.snapshot)
	s.mu.Unlock()
	return snapshot, s.record(eventType, snapshot, nil)
}

func (s *Session) complete() (Snapshot, error) {
	s.mu.Lock()
	if s.snapshot.State != StateRunning {
		snapshot := cloneSnapshot(s.snapshot)
		s.mu.Unlock()
		return snapshot, fmt.Errorf("%w: %s -> completed", ErrInvalidState, s.snapshot.State)
	}
	now := s.now().UTC()
	s.snapshot.State = StateCompleted
	s.snapshot.CurrentCallID = ""
	s.snapshot.CurrentApprovalID = ""
	s.snapshot.CurrentApprovalStatus = ""
	s.snapshot.CurrentToolStatus = ""
	s.snapshot.CurrentExecution = nil
	s.snapshot.CurrentCompletion = ""
	s.snapshot.Cancellation = nil
	s.snapshot.Revision++
	s.snapshot.FinishedAt = &now
	s.remainingSteps = nil
	s.currentCompletion = ""
	s.approvalRevision = 0
	s.invoker = nil
	s.runCancel = nil
	s.toolDispatching = false
	s.resumeStarted = false
	s.modelLoop = nil
	s.clearModelLoopCheckpointLocked()
	snapshot := cloneSnapshot(s.snapshot)
	s.mu.Unlock()
	return snapshot, s.record(eventlog.TypeAgentSessionCompleted, snapshot, nil)
}

func (s *Session) record(eventType eventlog.Type, snapshot Snapshot, extra map[string]any) error {
	payload := map[string]any{
		"state":           snapshot.State,
		"revision":        snapshot.Revision,
		"core_session_id": snapshot.CoreSessionID,
	}
	for key, value := range extra {
		payload[key] = value
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Agent Session event: %w", err)
	}
	_, err = s.journal.Append(eventlog.Entry{
		Type:          eventType,
		Class:         eventlog.ClassControl,
		SubjectID:     snapshot.ID,
		CorrelationID: snapshot.CurrentCallID,
		Data:          data,
	})
	if err != nil {
		return fmt.Errorf("record %s event: %w", eventType, err)
	}
	s.mu.RLock()
	persist := s.persist
	s.mu.RUnlock()
	if persist != nil {
		if err := persist(); err != nil {
			return fmt.Errorf("persist Agent Session after %s: %w", eventType, err)
		}
	}
	return nil
}

func sessionEventForState(state State) eventlog.Type {
	switch state {
	case StateRunning:
		return ""
	case StateWaitingApproval:
		return eventlog.TypeAgentSessionWaiting
	case StateBlocked:
		return eventlog.TypeAgentSessionBlocked
	case StateFailed:
		return eventlog.TypeAgentSessionFailed
	case StateCancelled:
		return eventlog.TypeAgentSessionCancelled
	case StateExpired:
		return eventlog.TypeAgentSessionExpired
	default:
		return eventlog.TypeAgentSessionUnknown
	}
}

func cloneSteps(steps []Step) []Step {
	if steps == nil {
		return nil
	}
	cloned := make([]Step, len(steps))
	for i, step := range steps {
		cloned[i] = step
		cloned[i].Arguments = append(json.RawMessage(nil), step.Arguments...)
	}
	return cloned
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	if snapshot.StartedAt != nil {
		value := *snapshot.StartedAt
		snapshot.StartedAt = &value
	}
	if snapshot.FinishedAt != nil {
		value := *snapshot.FinishedAt
		snapshot.FinishedAt = &value
	}
	snapshot.CurrentExecution = cloneApprovalExecution(snapshot.CurrentExecution)
	snapshot.ModelLoopRecovery = cloneRecoverySummary(snapshot.ModelLoopRecovery)
	snapshot.Cancellation = cloneCancellation(snapshot.Cancellation)
	return snapshot
}

func cloneCancellation(cancellation *Cancellation) *Cancellation {
	if cancellation == nil {
		return nil
	}
	cloned := *cancellation
	return &cloned
}

func cloneApprovalExecution(execution *approval.Execution) *approval.Execution {
	if execution == nil {
		return nil
	}
	cloned := *execution
	if execution.FinishedAt != nil {
		value := *execution.FinishedAt
		cloned.FinishedAt = &value
	}
	return &cloned
}

func equalApprovalExecution(left, right *approval.Execution) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.State != right.State || left.Error != right.Error || !left.StartedAt.Equal(right.StartedAt) {
		return false
	}
	if left.FinishedAt == nil || right.FinishedAt == nil {
		return left.FinishedAt == nil && right.FinishedAt == nil
	}
	return left.FinishedAt.Equal(*right.FinishedAt)
}

func approvalObservationPermitted(current, next approval.Status) bool {
	if current == "" || current == next {
		return true
	}
	switch current {
	case approval.StatusPending:
		return next == approval.StatusSubmitting ||
			next == approval.StatusApproved ||
			next == approval.StatusRejected ||
			next == approval.StatusExpired ||
			next == approval.StatusCancelled ||
			next == approval.StatusUnknown
	case approval.StatusSubmitting:
		return next == approval.StatusApproved ||
			next == approval.StatusRejected ||
			next == approval.StatusExpired ||
			next == approval.StatusCancelled ||
			next == approval.StatusUnknown
	case approval.StatusUnknown:
		return next == approval.StatusApproved ||
			next == approval.StatusRejected ||
			next == approval.StatusExpired ||
			next == approval.StatusCancelled
	default:
		return false
	}
}

func toolStatusForExecution(state approval.ExecutionState) toolcall.Status {
	switch state {
	case approval.ExecutionRunning:
		return toolcall.StatusRunning
	case approval.ExecutionSubmitted:
		return toolcall.StatusAccepted
	case approval.ExecutionSucceeded:
		return toolcall.StatusSucceeded
	case approval.ExecutionFailed:
		return toolcall.StatusFailed
	case approval.ExecutionUnknown:
		return toolcall.StatusUnknown
	default:
		return ""
	}
}

func normalizeCompletion(value toolpipeline.CompletionRequirement) (toolpipeline.CompletionRequirement, error) {
	switch value {
	case "", toolpipeline.CompletionAccepted:
		return toolpipeline.CompletionAccepted, nil
	case toolpipeline.CompletionCompleted:
		return toolpipeline.CompletionCompleted, nil
	default:
		return "", fmt.Errorf("unsupported completion requirement %q", value)
	}
}

func automaticToolStatus(
	execution approval.ExecutionState,
	completion toolpipeline.CompletionRequirement,
) (toolcall.Status, bool) {
	switch execution {
	case approval.ExecutionSucceeded:
		return toolcall.StatusSucceeded, true
	case approval.ExecutionSubmitted:
		if completion == toolpipeline.CompletionCompleted {
			return "", false
		}
		return toolcall.StatusAccepted, true
	default:
		return "", false
	}
}

func (s State) IsTerminal() bool {
	switch s {
	case StateCompleted, StateBlocked, StateFailed, StateCancelled, StateExpired, StateUnknown:
		return true
	default:
		return false
	}
}
