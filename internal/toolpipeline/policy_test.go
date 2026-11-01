package toolpipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
)

func TestAllowlistPolicyMatchesServerToolAndExactArguments(t *testing.T) {
	policy, err := NewAllowlistPolicy(Rule{
		ServerID:  "termcp-core",
		ToolName:  "term",
		Effect:    toolcall.EffectReadOnly,
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("NewAllowlistPolicy() error = %v", err)
	}

	allowed, err := policy.Decide(context.Background(), Request{
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: json.RawMessage(`{ "cmd" : "" }`),
	})
	if err != nil || !allowed.Allowed || allowed.Effect != toolcall.EffectReadOnly {
		t.Fatalf("Decide() = %+v, error = %v", allowed, err)
	}

	for _, request := range []Request{
		{ServerID: "other", ToolName: "term", Arguments: json.RawMessage(`{"cmd":""}`)},
		{ServerID: "termcp-core", ToolName: "other", Arguments: json.RawMessage(`{"cmd":""}`)},
		{ServerID: "termcp-core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"help"}`)},
		{ServerID: "termcp-core", ToolName: "term", Arguments: json.RawMessage(`{"cmd":"","extra":true}`)},
	} {
		decision, err := policy.Decide(context.Background(), request)
		if err != nil {
			t.Fatalf("Decide(%+v) error = %v", request, err)
		}
		if decision.Allowed {
			t.Fatalf("Decide(%+v) allowed = true", request)
		}
	}
}

func TestAllowlistPolicyRequiresExplicitEffectAndObjectArguments(t *testing.T) {
	tests := []Rule{
		{ServerID: "termcp-core", ToolName: "term", Arguments: json.RawMessage(`{}`)},
		{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectUnspecified, Arguments: json.RawMessage(`{}`)},
		{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly, Arguments: json.RawMessage(`[]`)},
	}
	for _, rule := range tests {
		if _, err := NewAllowlistPolicy(rule); err == nil {
			t.Fatalf("NewAllowlistPolicy(%+v) error = nil", rule)
		}
	}
}

func TestAllowlistPolicyValidatesModelResultMode(t *testing.T) {
	if _, err := NewAllowlistPolicy(Rule{
		ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
		Arguments: json.RawMessage(`{}`), ModelResult: "unbounded",
	}); err == nil {
		t.Fatal("invalid model_result mode was accepted")
	}
	policy, err := NewAllowlistPolicy(Rule{
		ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rules := policy.Rules(); len(rules) != 1 || rules[0].ModelResult != ModelResultNone {
		t.Fatalf("rules = %+v", rules)
	}
}
