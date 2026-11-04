package main

import (
	"encoding/json"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestPolicyModelToolProviderExposesOnlyLoadedAllowlistedTools(t *testing.T) {
	host := fakeModelToolSource{tools: map[string][]mcpcontrol.ToolDescriptor{
		"termcp-core": {{
			Name: "term", Description: "run a command", Schema: json.RawMessage(`{"type":"object"}`),
		}},
	}}
	policy, err := toolpipeline.NewAllowlistPolicy(
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
			Arguments: json.RawMessage(`{"cmd":"pwd"}`), ModelResult: toolpipeline.ModelResultContent,
		},
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
			Arguments: json.RawMessage(`{"cmd":"whoami"}`), ModelResult: toolpipeline.ModelResultContent,
		},
		toolpipeline.Rule{
			ServerID: "other", ToolName: "missing", Effect: toolcall.EffectReadOnly,
			Arguments: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := &runtimeapp.PolicyModelToolProvider{Host: host, Policy: policy}
	tools := provider.ModelTools()
	if len(tools) != 1 {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0].Name != "termcp-core__term" || tools[0].ServerID != "termcp-core" ||
		tools[0].ToolName != "term" || len(tools[0].Schema) == 0 || !tools[0].ExposeResult {
		t.Fatalf("tool = %+v", tools[0])
	}
}

func TestPolicyModelToolProviderWithholdsMixedResultPolicies(t *testing.T) {
	host := fakeModelToolSource{tools: map[string][]mcpcontrol.ToolDescriptor{
		"termcp-core": {{Name: "term", Schema: json.RawMessage(`{"type":"object"}`)}},
	}}
	policy, err := toolpipeline.NewAllowlistPolicy(
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
			Arguments: json.RawMessage(`{"cmd":"pwd"}`), ModelResult: toolpipeline.ModelResultContent,
		},
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
			Arguments: json.RawMessage(`{"cmd":"whoami"}`), ModelResult: toolpipeline.ModelResultNone,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	tools := (&runtimeapp.PolicyModelToolProvider{Host: host, Policy: policy}).ModelTools()
	if len(tools) != 1 || tools[0].ExposeResult {
		t.Fatalf("mixed policy tools = %+v", tools)
	}
}

type fakeModelToolSource struct {
	tools map[string][]mcpcontrol.ToolDescriptor
}

func (f fakeModelToolSource) Tools(serverID string) []mcpcontrol.ToolDescriptor {
	return append([]mcpcontrol.ToolDescriptor(nil), f.tools[serverID]...)
}
