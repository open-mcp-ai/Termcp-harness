package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/controlapi"
	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	runtimepkg "github.com/open-mcp-ai/termcp-harness/internal/runtime"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

// TestModelAgentLoopCoreControlAPIIntegration is opt-in because it makes real
// model requests. It only calls Core's read-only term guide (`cmd: ""`).
func TestModelAgentLoopCoreControlAPIIntegration(t *testing.T) {
	if os.Getenv("TERMCP_MODEL_CORE_E2E") != "1" {
		t.Skip("set TERMCP_MODEL_CORE_E2E=1 to run the real Control API + Core + model test")
	}
	coreMCPURL := strings.TrimSpace(os.Getenv("TERMCP_CORE_MCP_URL"))
	modelEndpoint := strings.TrimSpace(os.Getenv("TERMCP_MODEL_ENDPOINT"))
	modelID := strings.TrimSpace(os.Getenv("TERMCP_MODEL_ID"))
	tokenPath := strings.TrimSpace(os.Getenv("TERMCP_MODEL_TOKEN_FILE"))
	if coreMCPURL == "" || modelEndpoint == "" || modelID == "" || tokenPath == "" {
		t.Fatal("TERMCP_CORE_MCP_URL, TERMCP_MODEL_ENDPOINT, TERMCP_MODEL_ID, and TERMCP_MODEL_TOKEN_FILE are required")
	}
	modelToken, err := tokenfile.Read(tokenPath)
	if err != nil {
		t.Fatalf("read model token: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	journal := eventlog.New()
	host := mcpcontrol.NewHost(mcpcontrol.WithHostEventLog(journal))
	defer host.Close()
	if _, err := host.Register(mcpcontrol.Spec{
		ID:        "termcp-core",
		Name:      "Termcp Core",
		Endpoint:  coreMCPURL,
		Transport: mcpcontrol.TransportStreamableHTTP,
	}); err != nil {
		t.Fatalf("register Core MCP: %v", err)
	}
	go host.Run(ctx)
	waitForCoreMCPReady(t, ctx, host, "termcp-core")

	policy, err := toolpipeline.NewAllowlistPolicy(toolpipeline.Rule{
		ServerID:    "termcp-core",
		ToolName:    "term",
		Effect:      toolcall.EffectReadOnly,
		Arguments:   json.RawMessage(`{"cmd":""}`),
		ModelResult: toolpipeline.ModelResultContent,
		Reason:      "Read the Core term guide for the opt-in model Agent Loop test",
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}
	pipeline, err := toolpipeline.New(policy, host, journal)
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	sessions, err := agentsession.NewManager(journal)
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}
	adapter, err := model.NewOpenAICompatible(model.OpenAIConfig{
		Endpoint:        modelEndpoint,
		Model:           modelID,
		APIToken:        modelToken,
		HTTPClient:      &http.Client{Timeout: 60 * time.Second},
		MaxOutputTokens: 512,
	})
	if err != nil {
		t.Fatalf("create model adapter: %v", err)
	}
	rt, err := runtimepkg.New("model-e2e", runtimepkg.WithEventLog(journal))
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := rt.Start(); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	defer func() {
		_ = rt.MarkStopping()
		_ = rt.Stop()
	}()
	const controlToken = "0123456789abcdef0123456789abcdef"
	authorizer, err := controlapi.NewApprovalAuthorizer(controlToken)
	if err != nil {
		t.Fatalf("create authorizer: %v", err)
	}
	handler := controlapi.NewHandler(
		rt,
		controlapi.WithEventLog(journal),
		controlapi.WithMCPRegistry(host),
		controlapi.WithAgentSessions(sessions),
		controlapi.WithAgentSessionRunner(sessions, pipeline),
		controlapi.WithAgentModelLoop(sessions, adapter, &runtimeapp.PolicyModelToolProvider{Host: host, Policy: policy}),
		controlapi.WithApprovalAuthorizer(authorizer),
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatalf("create control client: %v", err)
	}
	if _, err := client.CreateAgentSession(ctx, "model-e2e-session", "", controlToken); err != nil {
		t.Fatalf("create Agent Session: %v", err)
	}
	result, err := client.RunAgentLoop(
		ctx,
		"model-e2e-session",
		`Call the termcp-core__term tool exactly once with {"cmd":""}. After receiving its result, `+
			`state that the tool call succeeded and briefly summarize what the result is.`,
		4,
		2,
		controlToken,
	)
	if err != nil {
		t.Fatalf("run real model Agent Loop: %v", err)
	}
	if result.Session.State != agentsession.StateCompleted ||
		result.Session.ModelTurns < 2 || result.Session.ModelToolCalls != 1 {
		t.Fatalf("session = %+v, output = %q", result.Session, result.Output)
	}
	if len(result.Calls) != 1 || result.Calls[0].Status != toolcall.StatusSucceeded ||
		result.Calls[0].ResultBytes == 0 {
		t.Fatalf("calls = %+v", result.Calls)
	}
	if strings.TrimSpace(result.Output) == "" {
		t.Fatal("model returned an empty final output")
	}
	window, err := journal.Query(0, 1000)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	seen := make(map[eventlog.Type]bool)
	for _, event := range window.Events {
		seen[event.Type] = true
	}
	for _, eventType := range []eventlog.Type{
		eventlog.TypeAgentModelRequested,
		eventlog.TypeAgentModelResponded,
		eventlog.TypeAgentSessionCompleted,
		eventlog.TypeToolCallSucceeded,
	} {
		if !seen[eventType] {
			t.Fatalf("missing event %s", eventType)
		}
	}
}

func waitForCoreMCPReady(t *testing.T, ctx context.Context, host *mcpcontrol.Host, id string) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, ok := host.Snapshot(id)
		if ok && snapshot.Ready && snapshot.CapabilityState == mcpcontrol.CapabilitiesLoaded {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Core MCP did not become ready: %+v", snapshot)
		case <-ticker.C:
		}
	}
}
