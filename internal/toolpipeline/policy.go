package toolpipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
)

const defaultMaxArgumentsBytes = 1 << 20

type Rule struct {
	ServerID  string          `json:"server_id"`
	ToolName  string          `json:"tool_name"`
	Effect    toolcall.Effect `json:"effect"`
	Arguments json.RawMessage `json:"arguments"`
	Reason    string          `json:"reason,omitempty"`
}

type AllowlistPolicy struct {
	rules []Rule
}

func NewAllowlistPolicy(rules ...Rule) (*AllowlistPolicy, error) {
	if len(rules) == 0 {
		return nil, errors.New("tool policy requires at least one explicit allowlist rule")
	}

	compiled := make([]Rule, 0, len(rules))
	for i, rule := range rules {
		if strings.TrimSpace(rule.ServerID) == "" {
			return nil, fmt.Errorf("allowlist rule %d requires a server ID", i)
		}
		if strings.TrimSpace(rule.ToolName) == "" {
			return nil, fmt.Errorf("allowlist rule %d requires a tool name", i)
		}
		if !validEffect(rule.Effect) || rule.Effect == toolcall.EffectUnspecified {
			return nil, fmt.Errorf("allowlist rule %d requires an explicit valid effect", i)
		}
		arguments, err := normalizeArguments(rule.Arguments, defaultMaxArgumentsBytes)
		if err != nil {
			return nil, fmt.Errorf("allowlist rule %d arguments: %w", i, err)
		}
		rule.Arguments = arguments
		if strings.TrimSpace(rule.Reason) == "" {
			rule.Reason = "matched an explicit allowlist rule"
		}
		compiled = append(compiled, rule)
	}
	return &AllowlistPolicy{rules: compiled}, nil
}

func (p *AllowlistPolicy) Decide(ctx context.Context, request Request) (PolicyDecision, error) {
	if p == nil {
		return PolicyDecision{}, errors.New("tool policy is required")
	}
	if err := ctx.Err(); err != nil {
		return PolicyDecision{}, err
	}
	arguments, err := normalizeArguments(request.Arguments, defaultMaxArgumentsBytes)
	if err != nil {
		return PolicyDecision{}, err
	}

	for _, rule := range p.rules {
		if rule.ServerID != request.ServerID || rule.ToolName != request.ToolName {
			continue
		}
		if bytes.Equal(rule.Arguments, arguments) {
			return PolicyDecision{
				Allowed: true,
				Effect:  rule.Effect,
				Reason:  rule.Reason,
			}, nil
		}
	}
	return PolicyDecision{
		Effect: toolcall.EffectUnspecified,
		Reason: "no explicit allowlist rule matched the server, tool, and exact arguments",
	}, nil
}

func normalizeArguments(raw json.RawMessage, maxBytes int) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		return nil, fmt.Errorf("arguments exceed limit %d bytes", maxBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	if object == nil {
		return nil, errors.New("arguments must be a JSON object, not null")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("normalize arguments: %w", err)
	}
	if maxBytes > 0 && len(encoded) > maxBytes {
		return nil, fmt.Errorf("normalized arguments exceed limit %d bytes", maxBytes)
	}
	return encoded, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("arguments contain multiple JSON values")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func validEffect(effect toolcall.Effect) bool {
	switch effect {
	case toolcall.EffectUnspecified, toolcall.EffectReadOnly, toolcall.EffectIdempotentWrite, toolcall.EffectSideEffect:
		return true
	default:
		return false
	}
}
