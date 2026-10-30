package toolcall

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Status is the Harness-local state of one tool invocation. Remote truth still
// comes only from the MCP server or Core contract that produced the evidence.
type Status string

const (
	StatusProposed        Status = "proposed"
	StatusBlocked         Status = "blocked"
	StatusApprovalPending Status = "approval_pending"
	StatusReady           Status = "ready"
	StatusDispatched      Status = "dispatched"
	StatusAccepted        Status = "accepted"
	StatusRunning         Status = "running"
	StatusSucceeded       Status = "succeeded"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
	StatusExpired         Status = "expired"
	StatusUnknown         Status = "unknown"
)

// Effect records the declared side-effect class. An unspecified effect cannot
// be auto-retried after an ambiguous outcome.
type Effect string

const (
	EffectUnspecified     Effect = "unspecified"
	EffectReadOnly        Effect = "read_only"
	EffectIdempotentWrite Effect = "idempotent_write"
	EffectSideEffect      Effect = "side_effect"
)

type Event string

const (
	EventMarkReady        Event = "mark_ready"
	EventRequireApproval  Event = "require_approval"
	EventBlock            Event = "block"
	EventApprovalAccepted Event = "approval_accepted"
	EventApprovalRejected Event = "approval_rejected"
	EventDispatch         Event = "dispatch"
	EventAccept           Event = "accept"
	EventStart            Event = "start"
	EventSucceed          Event = "succeed"
	EventFail             Event = "fail"
	EventCancelLocal      Event = "cancel_local"
	EventCancelConfirmed  Event = "cancel_confirmed"
	EventExpireLocal      Event = "expire_local"
	EventExpireConfirmed  Event = "expire_confirmed"
	EventOutcomeUnknown   Event = "outcome_unknown"
)

// Evidence makes the reason for a transition explicit. Event and evidence
// must match so a transport timeout cannot be recorded as a trusted failure.
type Evidence string

const (
	EvidenceValidationAndPolicy    Evidence = "validation_and_policy"
	EvidencePolicyRequiresApproval Evidence = "policy_requires_approval"
	EvidencePolicyBlocked          Evidence = "policy_blocked"
	EvidenceTrustedApproval        Evidence = "trusted_approval"
	EvidenceTrustedRejection       Evidence = "trusted_rejection"
	EvidenceDispatchContext        Evidence = "validated_dispatch_context"
	EvidenceServerAccepted         Evidence = "server_accepted"
	EvidenceExecutionProgress      Evidence = "execution_progress"
	EvidenceRemoteApprovalRequired Evidence = "remote_approval_required"
	EvidenceTrustedResult          Evidence = "trusted_result"
	EvidenceTrustedFailure         Evidence = "trusted_failure"
	EvidenceLocalCancellation      Evidence = "local_cancellation"
	EvidenceTrustedCancellation    Evidence = "trusted_cancellation"
	EvidenceLocalDeadline          Evidence = "local_deadline"
	EvidenceTrustedExpiration      Evidence = "trusted_expiration"
	EvidenceTransportAmbiguity     Evidence = "transport_ambiguity"
)

type Revision uint64

var (
	ErrInvalidTransition = errors.New("invalid tool call transition")
	ErrEvidenceMismatch  = errors.New("tool call evidence mismatch")
	ErrStaleRevision     = errors.New("stale tool call revision")
)

type Spec struct {
	ID       string
	ToolName string
	SourceID string
	Effect   Effect
}

type Snapshot struct {
	ID           string   `json:"id"`
	ToolName     string   `json:"tool_name"`
	SourceID     string   `json:"source_id"`
	Effect       Effect   `json:"effect"`
	Status       Status   `json:"status"`
	Revision     Revision `json:"revision"`
	Attempts     int      `json:"attempts"`
	LastEvent    Event    `json:"last_event,omitempty"`
	LastEvidence Evidence `json:"last_evidence,omitempty"`
}

type Call struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

type transitionKey struct {
	from  Status
	event Event
}

type transition struct {
	to       Status
	evidence Evidence
}

var transitions = map[transitionKey]transition{
	{StatusProposed, EventMarkReady}:       {to: StatusReady, evidence: EvidenceValidationAndPolicy},
	{StatusProposed, EventRequireApproval}: {to: StatusApprovalPending, evidence: EvidencePolicyRequiresApproval},
	{StatusProposed, EventBlock}:           {to: StatusBlocked, evidence: EvidencePolicyBlocked},
	{StatusProposed, EventCancelLocal}:     {to: StatusCancelled, evidence: EvidenceLocalCancellation},
	{StatusProposed, EventExpireLocal}:     {to: StatusExpired, evidence: EvidenceLocalDeadline},

	{StatusApprovalPending, EventApprovalAccepted}: {to: StatusReady, evidence: EvidenceTrustedApproval},
	{StatusApprovalPending, EventApprovalRejected}: {to: StatusBlocked, evidence: EvidenceTrustedRejection},
	{StatusApprovalPending, EventCancelLocal}:      {to: StatusCancelled, evidence: EvidenceLocalCancellation},
	{StatusApprovalPending, EventExpireLocal}:      {to: StatusExpired, evidence: EvidenceLocalDeadline},

	{StatusReady, EventDispatch}:    {to: StatusDispatched, evidence: EvidenceDispatchContext},
	{StatusReady, EventCancelLocal}: {to: StatusCancelled, evidence: EvidenceLocalCancellation},
	{StatusReady, EventExpireLocal}: {to: StatusExpired, evidence: EvidenceLocalDeadline},

	{StatusDispatched, EventAccept}:          {to: StatusAccepted, evidence: EvidenceServerAccepted},
	{StatusDispatched, EventStart}:           {to: StatusRunning, evidence: EvidenceExecutionProgress},
	{StatusDispatched, EventRequireApproval}: {to: StatusApprovalPending, evidence: EvidenceRemoteApprovalRequired},
	{StatusDispatched, EventSucceed}:         {to: StatusSucceeded, evidence: EvidenceTrustedResult},
	{StatusDispatched, EventFail}:            {to: StatusFailed, evidence: EvidenceTrustedFailure},
	{StatusDispatched, EventCancelConfirmed}: {to: StatusCancelled, evidence: EvidenceTrustedCancellation},
	{StatusDispatched, EventExpireConfirmed}: {to: StatusExpired, evidence: EvidenceTrustedExpiration},
	{StatusDispatched, EventOutcomeUnknown}:  {to: StatusUnknown, evidence: EvidenceTransportAmbiguity},

	{StatusAccepted, EventStart}:           {to: StatusRunning, evidence: EvidenceExecutionProgress},
	{StatusAccepted, EventSucceed}:         {to: StatusSucceeded, evidence: EvidenceTrustedResult},
	{StatusAccepted, EventFail}:            {to: StatusFailed, evidence: EvidenceTrustedFailure},
	{StatusAccepted, EventCancelConfirmed}: {to: StatusCancelled, evidence: EvidenceTrustedCancellation},
	{StatusAccepted, EventExpireConfirmed}: {to: StatusExpired, evidence: EvidenceTrustedExpiration},
	{StatusAccepted, EventOutcomeUnknown}:  {to: StatusUnknown, evidence: EvidenceTransportAmbiguity},

	{StatusRunning, EventSucceed}:         {to: StatusSucceeded, evidence: EvidenceTrustedResult},
	{StatusRunning, EventFail}:            {to: StatusFailed, evidence: EvidenceTrustedFailure},
	{StatusRunning, EventCancelConfirmed}: {to: StatusCancelled, evidence: EvidenceTrustedCancellation},
	{StatusRunning, EventExpireConfirmed}: {to: StatusExpired, evidence: EvidenceTrustedExpiration},
	{StatusRunning, EventOutcomeUnknown}:  {to: StatusUnknown, evidence: EvidenceTransportAmbiguity},

	{StatusUnknown, EventSucceed}:         {to: StatusSucceeded, evidence: EvidenceTrustedResult},
	{StatusUnknown, EventFail}:            {to: StatusFailed, evidence: EvidenceTrustedFailure},
	{StatusUnknown, EventCancelConfirmed}: {to: StatusCancelled, evidence: EvidenceTrustedCancellation},
	{StatusUnknown, EventExpireConfirmed}: {to: StatusExpired, evidence: EvidenceTrustedExpiration},
}

func New(spec Spec) (*Call, error) {
	if strings.TrimSpace(spec.ID) == "" {
		return nil, errors.New("tool call ID is required")
	}
	if strings.TrimSpace(spec.ToolName) == "" {
		return nil, errors.New("tool name is required")
	}
	if strings.TrimSpace(spec.SourceID) == "" {
		return nil, errors.New("tool source ID is required")
	}
	if spec.Effect == "" {
		spec.Effect = EffectUnspecified
	}
	if !validEffect(spec.Effect) {
		return nil, fmt.Errorf("invalid tool effect %q", spec.Effect)
	}

	return &Call{snapshot: Snapshot{
		ID:       spec.ID,
		ToolName: spec.ToolName,
		SourceID: spec.SourceID,
		Effect:   spec.Effect,
		Status:   StatusProposed,
		Revision: 1,
	}}, nil
}

// Apply performs a compare-and-swap transition. A caller with an old revision
// cannot overwrite newer state with a late asynchronous result.
func (c *Call) Apply(expected Revision, event Event, evidence Evidence) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if expected != c.snapshot.Revision {
		return c.snapshot, fmt.Errorf("%w: expected %d, current %d", ErrStaleRevision, expected, c.snapshot.Revision)
	}

	next, ok := transitions[transitionKey{from: c.snapshot.Status, event: event}]
	if !ok {
		return c.snapshot, fmt.Errorf("%w: %s + %s", ErrInvalidTransition, c.snapshot.Status, event)
	}
	if evidence != next.evidence {
		return c.snapshot, fmt.Errorf("%w: %s requires %q, got %q", ErrEvidenceMismatch, event, next.evidence, evidence)
	}

	c.snapshot.Status = next.to
	c.snapshot.Revision++
	c.snapshot.LastEvent = event
	c.snapshot.LastEvidence = evidence
	if event == EventDispatch {
		c.snapshot.Attempts++
	}
	return c.snapshot, nil
}

func (c *Call) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (s Status) IsTerminal() bool {
	switch s {
	case StatusBlocked, StatusSucceeded, StatusFailed, StatusCancelled, StatusExpired:
		return true
	default:
		return false
	}
}

func (s Status) IsInFlight() bool {
	switch s {
	case StatusDispatched, StatusAccepted, StatusRunning:
		return true
	default:
		return false
	}
}

func validEffect(effect Effect) bool {
	switch effect {
	case EffectUnspecified, EffectReadOnly, EffectIdempotentWrite, EffectSideEffect:
		return true
	default:
		return false
	}
}
