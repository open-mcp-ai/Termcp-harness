package toolpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
)

const defaultMaxResultBytes = 4 << 20

type Dispatcher interface {
	CallTool(context.Context, string, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

type Policy interface {
	Decide(context.Context, Request) (PolicyDecision, error)
}

type ApprovalTracker interface {
	Track(string, approval.Reference) (approval.Tracked, error)
}

type PolicyDecision struct {
	Allowed bool
	Effect  toolcall.Effect
	Reason  string
}

type Request struct {
	CallID             string                `json:"call_id"`
	ServerID           string                `json:"server_id"`
	ToolName           string                `json:"tool_name"`
	Arguments          json.RawMessage       `json:"arguments"`
	RequiredCompletion CompletionRequirement `json:"required_completion,omitempty"`
}

// CompletionRequirement defines what the caller needs before the next Session
// step may run. Accepted is the default: the tool invocation itself completed.
// Completed requires trusted execution-completion evidence.
type CompletionRequirement string

const (
	CompletionAccepted  CompletionRequirement = "accepted"
	CompletionCompleted CompletionRequirement = "completed"
)

type Outcome struct {
	Call     toolcall.Snapshot
	Result   *mcp.CallToolResult
	Approval *approval.Reference
}

type Pipeline struct {
	policy            Policy
	dispatcher        Dispatcher
	journal           *eventlog.Journal
	maxArgumentsBytes int
	maxResultBytes    int
	approvalTracker   ApprovalTracker
}

type Option func(*Pipeline)

func WithApprovalTracker(tracker ApprovalTracker) Option {
	return func(pipeline *Pipeline) {
		pipeline.approvalTracker = tracker
	}
}

func New(
	policy Policy,
	dispatcher Dispatcher,
	journal *eventlog.Journal,
	options ...Option,
) (*Pipeline, error) {
	if policy == nil {
		return nil, errors.New("tool policy is required")
	}
	if dispatcher == nil {
		return nil, errors.New("tool dispatcher is required")
	}
	if journal == nil {
		return nil, errors.New("event log is required for tool execution")
	}
	pipeline := &Pipeline{
		policy:            policy,
		dispatcher:        dispatcher,
		journal:           journal,
		maxArgumentsBytes: defaultMaxArgumentsBytes,
		maxResultBytes:    defaultMaxResultBytes,
	}
	for _, option := range options {
		if option != nil {
			option(pipeline)
		}
	}
	return pipeline, nil
}

func (p *Pipeline) Invoke(ctx context.Context, request Request) (Outcome, error) {
	if p == nil {
		return Outcome{}, errors.New("tool pipeline is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	normalized, err := p.normalizeRequest(request)
	if err != nil {
		return Outcome{}, err
	}

	decision, policyErr := p.policy.Decide(ctx, normalized)
	if policyErr != nil {
		decision = PolicyDecision{
			Effect: toolcall.EffectUnspecified,
			Reason: "tool policy evaluation failed",
		}
	}
	if decision.Effect == "" {
		decision.Effect = toolcall.EffectUnspecified
	}
	if !validEffect(decision.Effect) || (decision.Allowed && decision.Effect == toolcall.EffectUnspecified) {
		policyErr = fmt.Errorf("allowed tool policy decision requires an explicit effect, got %q", decision.Effect)
		decision.Effect = toolcall.EffectUnspecified
		decision.Allowed = false
		decision.Reason = "tool policy did not provide an explicit effect"
	}

	call, err := toolcall.New(toolcall.Spec{
		ID:       normalized.CallID,
		ToolName: normalized.ToolName,
		SourceID: normalized.ServerID,
		Effect:   decision.Effect,
	})
	if err != nil {
		return Outcome{}, err
	}
	proposed := call.Snapshot()
	if err := p.record(eventlog.TypeToolCallProposed, proposed, map[string]any{
		"policy_reason": decision.Reason,
	}); err != nil {
		return Outcome{Call: proposed}, err
	}

	if policyErr != nil || !decision.Allowed {
		blocked, err := call.Apply(proposed.Revision, toolcall.EventBlock, toolcall.EvidencePolicyBlocked)
		if err != nil {
			return Outcome{Call: call.Snapshot()}, err
		}
		if err := p.record(eventlog.TypeToolCallBlocked, blocked, map[string]any{
			"policy_reason": decision.Reason,
		}); err != nil {
			return Outcome{Call: blocked}, err
		}
		return Outcome{Call: blocked}, policyErr
	}

	ready, err := call.Apply(proposed.Revision, toolcall.EventMarkReady, toolcall.EvidenceValidationAndPolicy)
	if err != nil {
		return Outcome{Call: call.Snapshot()}, err
	}
	if err := p.record(eventlog.TypeToolCallReady, ready, map[string]any{
		"policy_reason": decision.Reason,
	}); err != nil {
		return Outcome{Call: ready}, err
	}

	// Log dispatch intent before the MCP request. A missing event must prevent a
	// side effect rather than be discovered after the call completed.
	if err := p.record(eventlog.TypeToolCallDispatching, ready, nil); err != nil {
		return Outcome{Call: ready}, err
	}
	dispatched, err := call.Apply(ready.Revision, toolcall.EventDispatch, toolcall.EvidenceDispatchContext)
	if err != nil {
		return Outcome{Call: call.Snapshot()}, err
	}

	result, callErr := p.dispatcher.CallTool(ctx, normalized.ServerID, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      normalized.ToolName,
			Arguments: normalized.Arguments,
		},
	})
	if callErr != nil {
		unknown, stateErr := call.Apply(dispatched.Revision, toolcall.EventOutcomeUnknown, toolcall.EvidenceTransportAmbiguity)
		if stateErr != nil {
			return Outcome{Call: call.Snapshot()}, errors.Join(callErr, stateErr)
		}
		if err := p.record(eventlog.TypeToolCallUnknown, unknown, map[string]any{
			"error_kind": "transport_or_protocol_error",
		}); err != nil {
			return Outcome{Call: unknown}, errors.Join(callErr, err)
		}
		return Outcome{Call: unknown}, callErr
	}
	if result == nil {
		callErr = errors.New("tool dispatcher returned no result")
		unknown, stateErr := call.Apply(dispatched.Revision, toolcall.EventOutcomeUnknown, toolcall.EvidenceTransportAmbiguity)
		if stateErr != nil {
			return Outcome{Call: call.Snapshot()}, errors.Join(callErr, stateErr)
		}
		if err := p.record(eventlog.TypeToolCallUnknown, unknown, map[string]any{
			"error_kind": "missing_result",
		}); err != nil {
			return Outcome{Call: unknown}, errors.Join(callErr, err)
		}
		return Outcome{Call: unknown}, callErr
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		callErr = fmt.Errorf("encode tool result: %w", err)
		unknown, stateErr := call.Apply(dispatched.Revision, toolcall.EventOutcomeUnknown, toolcall.EvidenceTransportAmbiguity)
		if stateErr != nil {
			return Outcome{Call: call.Snapshot()}, errors.Join(callErr, stateErr)
		}
		if recordErr := p.record(eventlog.TypeToolCallUnknown, unknown, map[string]any{
			"error_kind": "invalid_result",
		}); recordErr != nil {
			return Outcome{Call: unknown}, errors.Join(callErr, recordErr)
		}
		return Outcome{Call: unknown}, callErr
	}
	if p.maxResultBytes > 0 && len(resultBytes) > p.maxResultBytes {
		callErr = fmt.Errorf("tool result exceeds limit %d bytes", p.maxResultBytes)
		unknown, stateErr := call.Apply(dispatched.Revision, toolcall.EventOutcomeUnknown, toolcall.EvidenceTransportAmbiguity)
		if stateErr != nil {
			return Outcome{Call: call.Snapshot()}, errors.Join(callErr, stateErr)
		}
		if recordErr := p.record(eventlog.TypeToolCallUnknown, unknown, map[string]any{
			"error_kind": "result_too_large",
		}); recordErr != nil {
			return Outcome{Call: unknown}, errors.Join(callErr, recordErr)
		}
		return Outcome{Call: unknown}, callErr
	}

	if pending, message, reference := detectReviewPending(result); pending {
		approvalPending, err := call.Apply(dispatched.Revision, toolcall.EventRequireApproval, toolcall.EvidenceRemoteApprovalRequired)
		if err != nil {
			return Outcome{Call: call.Snapshot(), Result: result}, err
		}
		extra := map[string]any{
			"result_bytes":         len(resultBytes),
			"pending_bytes":        len(message),
			"approval_correlation": string(reference.Correlation),
		}
		if reference.ID != "" {
			extra["approval_id"] = reference.ID
		}
		if reference.SessionID != "" {
			extra["approval_session_id"] = reference.SessionID
		}
		if reference.ExpiresAt != nil {
			extra["approval_expires_at"] = reference.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
		if err := p.record(eventlog.TypeToolCallApprovalPending, approvalPending, extra); err != nil {
			return Outcome{Call: approvalPending, Result: result, Approval: &reference}, err
		}
		if p.approvalTracker != nil && reference.Correlation == approval.CorrelationTrusted {
			if _, trackErr := p.approvalTracker.Track(normalized.CallID, reference); trackErr != nil {
				return Outcome{Call: approvalPending, Result: result, Approval: &reference},
					fmt.Errorf("track approval request: %w", trackErr)
			}
		}
		return Outcome{Call: approvalPending, Result: result, Approval: &reference}, nil
	}
	if result.IsError {
		failed, err := call.Apply(dispatched.Revision, toolcall.EventFail, toolcall.EvidenceTrustedFailure)
		if err != nil {
			return Outcome{Call: call.Snapshot(), Result: result}, err
		}
		if err := p.record(eventlog.TypeToolCallFailed, failed, map[string]any{
			"result_bytes": len(resultBytes),
		}); err != nil {
			return Outcome{Call: failed, Result: result}, err
		}
		return Outcome{Call: failed, Result: result}, nil
	}

	succeeded, err := call.Apply(dispatched.Revision, toolcall.EventSucceed, toolcall.EvidenceTrustedResult)
	if err != nil {
		return Outcome{Call: call.Snapshot(), Result: result}, err
	}
	if err := p.record(eventlog.TypeToolCallSucceeded, succeeded, map[string]any{
		"result_bytes": len(resultBytes),
	}); err != nil {
		return Outcome{Call: succeeded, Result: result}, err
	}
	return Outcome{Call: succeeded, Result: result}, nil
}

type reviewPendingCandidate struct {
	message   string
	reference approval.Reference
}

func detectReviewPending(result *mcp.CallToolResult) (bool, string, approval.Reference) {
	var candidates []reviewPendingCandidate
	if result.StructuredContent != nil {
		if body, ok := jsonObject(result.StructuredContent); ok {
			if candidate, pending := pendingCandidateFromBody(body); pending {
				candidates = append(candidates, candidate)
			}
		}
	}
	for _, content := range result.Content {
		text, ok := mcp.AsTextContent(content)
		if !ok {
			continue
		}
		value := strings.TrimSpace(text.Text)
		if termReviewPending(value) {
			candidates = append(candidates, reviewPendingCandidate{
				message:   termReviewMessage(value),
				reference: approvalReferenceFromTERM(value),
			})
			continue
		}
		body, ok := jsonObjectText(value)
		if !ok {
			continue
		}
		if candidate, pending := pendingCandidateFromBody(body); pending {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return false, "", approval.Reference{}
	}
	message := firstPendingMessage(candidates)
	reference := mergeApprovalReferences(candidates)
	if reference.Correlation == approval.CorrelationTrusted {
		reference.Summary = reviewSummary(message)
	}
	return true, message, reference
}

func reviewSummary(message string) string {
	message = strings.TrimSpace(message)
	for _, marker := range []string{
		" A human decides it in the Web UI",
		" Use approval_id only",
	} {
		if index := strings.Index(message, marker); index > 0 {
			message = message[:index]
		}
	}
	words := strings.Fields(message)
	if len(words) == 0 {
		return ""
	}
	const maxRunes = 512
	runes := []rune(strings.Join(words, " "))
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
		return string(runes) + "…"
	}
	return string(runes)
}

func pendingCandidateFromBody(body map[string]any) (reviewPendingCandidate, bool) {
	pending, _ := body["review_pending"].(bool)
	if !pending {
		return reviewPendingCandidate{}, false
	}
	if approved, ok := body["approved"].(bool); ok && approved {
		return reviewPendingCandidate{}, false
	}
	message, _ := body["message"].(string)
	return reviewPendingCandidate{
		message:   message,
		reference: approvalReferenceFromBody(body),
	}, true
}

func approvalReferenceFromBody(body map[string]any) approval.Reference {
	rawID, present := body["approval_id"]
	if !present {
		return approval.UncorrelatedReference()
	}
	id, ok := rawID.(string)
	if !ok || id == "" || strings.TrimSpace(id) != id {
		return approval.ConflictingReference()
	}

	sessionID := ""
	if rawSession, present := body["session_id"]; present {
		value, ok := rawSession.(string)
		if !ok || strings.TrimSpace(value) != value {
			return approval.ConflictingReference()
		}
		sessionID = value
	}

	var expiresAt *time.Time
	if rawExpiry, present := body["expires_at"]; present {
		parsed, ok := parseApprovalExpiry(rawExpiry)
		if !ok {
			return approval.ConflictingReference()
		}
		expiresAt = parsed
	}
	reference, err := approval.TrustedReference(id, sessionID, expiresAt)
	if err != nil {
		return approval.ConflictingReference()
	}
	return reference
}

func parseApprovalExpiry(value any) (*time.Time, bool) {
	switch typed := value.(type) {
	case json.Number:
		milliseconds, err := typed.Int64()
		if err != nil || milliseconds <= 0 {
			return nil, false
		}
		parsed := time.UnixMilli(milliseconds).UTC()
		return &parsed, true
	case float64:
		milliseconds := int64(typed)
		if milliseconds <= 0 || float64(milliseconds) != typed {
			return nil, false
		}
		parsed := time.UnixMilli(milliseconds).UTC()
		return &parsed, true
	case int:
		if typed <= 0 {
			return nil, false
		}
		parsed := time.UnixMilli(int64(typed)).UTC()
		return &parsed, true
	case int64:
		if typed <= 0 {
			return nil, false
		}
		parsed := time.UnixMilli(typed).UTC()
		return &parsed, true
	case string:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed))
		if err != nil {
			return nil, false
		}
		parsed = parsed.UTC()
		return &parsed, true
	default:
		return nil, false
	}
}

func mergeApprovalReferences(candidates []reviewPendingCandidate) approval.Reference {
	merged := approval.UncorrelatedReference()
	for _, candidate := range candidates {
		reference := candidate.reference
		switch reference.Correlation {
		case approval.CorrelationConflict:
			return approval.ConflictingReference()
		case approval.CorrelationTrusted:
			if merged.Correlation != approval.CorrelationTrusted {
				merged = reference
				continue
			}
			if merged.ID != reference.ID {
				return approval.ConflictingReference()
			}
			if reference.SessionID != "" {
				if merged.SessionID != "" && merged.SessionID != reference.SessionID {
					return approval.ConflictingReference()
				}
				merged.SessionID = reference.SessionID
			}
			if reference.ExpiresAt != nil {
				if merged.ExpiresAt != nil && !merged.ExpiresAt.Equal(*reference.ExpiresAt) {
					return approval.ConflictingReference()
				}
				merged.ExpiresAt = reference.ExpiresAt
			}
		}
	}
	return merged
}

func firstPendingMessage(candidates []reviewPendingCandidate) string {
	for _, candidate := range candidates {
		if candidate.message != "" {
			return candidate.message
		}
	}
	return ""
}

func termReviewPending(text string) bool {
	return strings.HasPrefix(text, "TERM/1 202 Accepted") && strings.Contains(text, "Submitted for review:")
}

func termReviewMessage(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	parts := strings.SplitN(normalized, "\n\n", 2)
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(normalized)
}

func approvalReferenceFromTERM(text string) approval.Reference {
	var (
		id        string
		sessionID string
		expiresAt *time.Time
	)
	for _, line := range strings.Split(text, "\n")[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "approval-id":
			if id != "" && id != value {
				return approval.ConflictingReference()
			}
			id = value
		case "session-id":
			if sessionID != "" && sessionID != value {
				return approval.ConflictingReference()
			}
			sessionID = value
		case "expires-at":
			milliseconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil || milliseconds <= 0 {
				return approval.ConflictingReference()
			}
			parsed := time.UnixMilli(milliseconds).UTC()
			if expiresAt != nil && !expiresAt.Equal(parsed) {
				return approval.ConflictingReference()
			}
			expiresAt = &parsed
		}
	}
	if id == "" {
		return approval.UncorrelatedReference()
	}
	reference, err := approval.TrustedReference(id, sessionID, expiresAt)
	if err != nil {
		return approval.ConflictingReference()
	}
	return reference
}

func jsonObject(value any) (map[string]any, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return jsonObjectText(string(encoded))
}

func jsonObjectText(text string) (map[string]any, bool) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil || body == nil {
		return nil, false
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, false
	}
	return body, true
}

func (p *Pipeline) normalizeRequest(request Request) (Request, error) {
	request.CallID = strings.TrimSpace(request.CallID)
	request.ServerID = strings.TrimSpace(request.ServerID)
	request.ToolName = strings.TrimSpace(request.ToolName)
	if request.CallID == "" {
		return Request{}, errors.New("tool call ID is required")
	}
	if len(request.CallID) > 128 {
		return Request{}, errors.New("tool call ID exceeds 128 characters")
	}
	if request.ServerID == "" {
		return Request{}, errors.New("tool source ID is required")
	}
	if request.ToolName == "" {
		return Request{}, errors.New("tool name is required")
	}
	arguments, err := normalizeArguments(request.Arguments, p.maxArgumentsBytes)
	if err != nil {
		return Request{}, err
	}
	request.Arguments = arguments
	return request, nil
}

func (p *Pipeline) record(eventType eventlog.Type, snapshot toolcall.Snapshot, extra map[string]any) error {
	payload := map[string]any{
		"tool_name": snapshot.ToolName,
		"source_id": snapshot.SourceID,
		"effect":    snapshot.Effect,
		"status":    snapshot.Status,
		"revision":  snapshot.Revision,
	}
	for key, value := range extra {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode tool call event data: %w", err)
	}
	_, err = p.journal.Append(eventlog.Entry{
		Type:      eventType,
		Class:     eventlog.ClassControl,
		SubjectID: snapshot.ID,
		Data:      encoded,
	})
	if err != nil {
		return fmt.Errorf("record %s event: %w", eventType, err)
	}
	return nil
}
