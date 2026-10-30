package approval

import (
	"errors"
	"fmt"
	"time"
)

type ExecutionState string

const (
	ExecutionRunning   ExecutionState = "running"
	ExecutionSubmitted ExecutionState = "submitted"
	ExecutionSucceeded ExecutionState = "succeeded"
	ExecutionFailed    ExecutionState = "failed"
	ExecutionUnknown   ExecutionState = "unknown"
)

// Execution is the structured result of replaying an approved request.
// Approval status remains approved when replay fails.
type Execution struct {
	State      ExecutionState `json:"state"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// DecisionOutcome couples a remotely confirmed approval state with one
// execution result when the authority returns one.
type DecisionOutcome struct {
	Status    Status
	Execution *Execution
}

func validExecutionState(state ExecutionState) bool {
	switch state {
	case ExecutionRunning,
		ExecutionSubmitted,
		ExecutionSucceeded,
		ExecutionFailed,
		ExecutionUnknown:
		return true
	default:
		return false
	}
}

func normalizeExecution(execution *Execution) (*Execution, error) {
	if execution == nil {
		return nil, nil
	}
	if !validExecutionState(execution.State) {
		return nil, fmt.Errorf("invalid approval execution state %q", execution.State)
	}
	if execution.StartedAt.IsZero() {
		return nil, errors.New("approval execution start time is required")
	}
	if execution.State == ExecutionRunning && execution.FinishedAt != nil {
		return nil, errors.New("running approval execution must not have a finish time")
	}
	if execution.State != ExecutionRunning && execution.FinishedAt == nil {
		return nil, errors.New("terminal approval execution requires a finish time")
	}
	normalized := *execution
	normalized.StartedAt = normalized.StartedAt.UTC()
	if normalized.FinishedAt != nil {
		value := normalized.FinishedAt.UTC()
		normalized.FinishedAt = &value
	}
	return &normalized, nil
}

// ValidateExecutionTransition rejects execution observations that would move
// an already confirmed execution back to an earlier state. Approval state and
// execution state are independent: an approval may stay approved while its
// execution advances.
func ValidateExecutionTransition(previous, next *Execution) error {
	if next == nil || previous == nil || previous.State == next.State {
		return nil
	}
	allowed := false
	switch previous.State {
	case ExecutionRunning:
		allowed = next.State == ExecutionSubmitted ||
			next.State == ExecutionSucceeded ||
			next.State == ExecutionFailed ||
			next.State == ExecutionUnknown
	case ExecutionUnknown:
		allowed = next.State == ExecutionRunning ||
			next.State == ExecutionSubmitted ||
			next.State == ExecutionSucceeded ||
			next.State == ExecutionFailed
	case ExecutionSubmitted:
		allowed = next.State == ExecutionSucceeded ||
			next.State == ExecutionFailed ||
			next.State == ExecutionUnknown
	}
	if !allowed {
		return fmt.Errorf("invalid approval execution transition %s -> %s", previous.State, next.State)
	}
	return nil
}

func cloneExecution(execution *Execution) *Execution {
	if execution == nil {
		return nil
	}
	cloned := *execution
	if execution.FinishedAt != nil {
		value := execution.FinishedAt.UTC()
		cloned.FinishedAt = &value
	}
	return &cloned
}
