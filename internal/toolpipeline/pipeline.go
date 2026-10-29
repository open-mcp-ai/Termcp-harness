package toolpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
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

type PolicyDecision struct {
	Allowed bool
	Effect  toolcall.Effect
	Reason  string
}

type Request struct {
	CallID    string
	ServerID  string
	ToolName  string
	Arguments json.RawMessage
}

type Outcome struct {
	Call   toolcall.Snapshot
	Result *mcp.CallToolResult
}

type Pipeline struct {
	policy            Policy
	dispatcher        Dispatcher
	journal           *eventlog.Journal
	maxArgumentsBytes int
	maxResultBytes    int
}

func New(policy Policy, dispatcher Dispatcher, journal *eventlog.Journal) (*Pipeline, error) {
	if policy == nil {
		return nil, errors.New("tool policy is required")
	}
	if dispatcher == nil {
		return nil, errors.New("tool dispatcher is required")
	}
	if journal == nil {
		return nil, errors.New("event log is required for tool execution")
	}
	return &Pipeline{
		policy:            policy,
		dispatcher:        dispatcher,
		journal:           journal,
		maxArgumentsBytes: defaultMaxArgumentsBytes,
		maxResultBytes:    defaultMaxResultBytes,
	}, nil
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

	if pending, message := reviewPending(result); pending {
		approvalPending, err := call.Apply(dispatched.Revision, toolcall.EventRequireApproval, toolcall.EvidenceRemoteApprovalRequired)
		if err != nil {
			return Outcome{Call: call.Snapshot(), Result: result}, err
		}
		if err := p.record(eventlog.TypeToolCallApprovalPending, approvalPending, map[string]any{
			"result_bytes":  len(resultBytes),
			"pending_bytes": len(message),
		}); err != nil {
			return Outcome{Call: approvalPending, Result: result}, err
		}
		return Outcome{Call: approvalPending, Result: result}, nil
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

func reviewPending(result *mcp.CallToolResult) (bool, string) {
	var payloads []any
	if result.StructuredContent != nil {
		payloads = append(payloads, result.StructuredContent)
	}
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			payloads = append(payloads, text.Text)
		}
	}
	for _, payload := range payloads {
		var body map[string]any
		switch value := payload.(type) {
		case string:
			text := strings.TrimSpace(value)
			if strings.HasPrefix(text, "TERM/1 202 Accepted") && strings.Contains(text, "Submitted for review:") {
				return true, text
			}
			if err := json.Unmarshal([]byte(text), &body); err != nil {
				continue
			}
		default:
			encoded, err := json.Marshal(value)
			if err != nil || json.Unmarshal(encoded, &body) != nil {
				continue
			}
		}
		if pending, _ := body["review_pending"].(bool); !pending {
			continue
		}
		if approved, ok := body["approved"].(bool); ok && approved {
			continue
		}
		message, _ := body["message"].(string)
		return true, message
	}
	return false, ""
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
