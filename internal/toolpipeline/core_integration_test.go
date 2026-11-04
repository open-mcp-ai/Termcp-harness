package toolpipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestTermcpCoreReadOnlyPipelineIntegration(t *testing.T) {
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	if endpoint == "" {
		t.Skip("set TERMCP_CORE_MCP_URL to run against a real Core instance")
	}

	host := mcpcontrol.NewHost()
	defer host.Close()
	spec := mcpcontrol.Spec{
		ID:        "termcp-core",
		Name:      "Termcp Core",
		Endpoint:  endpoint,
		AuthToken: coreAuthToken(t),
	}
	if _, err := host.Register(spec); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := host.Connect(ctx, spec.ID); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := host.LoadCapabilities(ctx, spec.ID); err != nil {
		t.Fatalf("LoadCapabilities() error = %v", err)
	}
	if _, err := host.Enable(spec.ID); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}

	policy, err := toolpipeline.NewAllowlistPolicy(toolpipeline.Rule{
		ServerID:  spec.ID,
		ToolName:  "term",
		Effect:    toolcall.EffectReadOnly,
		Arguments: json.RawMessage(`{"cmd":""}`),
		Reason:    "Core read-only term help integration test",
	})
	if err != nil {
		t.Fatalf("NewAllowlistPolicy() error = %v", err)
	}
	journal := eventlog.New()
	pipeline, err := toolpipeline.New(policy, host, journal)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const callID = "core-term-help-integration"
	outcome, err := pipeline.Invoke(ctx, toolpipeline.Request{
		CallID:    callID,
		ServerID:  spec.ID,
		ToolName:  "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if outcome.Call.Status != toolcall.StatusSucceeded || outcome.Call.Attempts != 1 {
		t.Fatalf("outcome call = %+v", outcome.Call)
	}
	if outcome.Result == nil || outcome.Result.IsError || len(outcome.Result.Content) == 0 {
		t.Fatalf("outcome result = %+v", outcome.Result)
	}

	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	wantEvents := []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallSucceeded,
	}
	if len(window.Events) != len(wantEvents) {
		t.Fatalf("events = %+v, want %d tool call events", window.Events, len(wantEvents))
	}
	for i, event := range window.Events {
		if event.SubjectID != callID || event.Class != eventlog.ClassControl || event.Type != wantEvents[i] {
			t.Fatalf("event %d = %+v", i, event)
		}
	}
}
