package agentsession

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

// TestModelAgentLoopProviderIntegration is opt-in because it makes real billed
// model requests. It uses a harmless in-memory tool implementation and never
// sends tool arguments or results to a real MCP server.
func TestModelAgentLoopProviderIntegration(t *testing.T) {
	if os.Getenv("TERMCP_MODEL_INTEGRATION") != "1" {
		t.Skip("set TERMCP_MODEL_INTEGRATION=1 to run real model integration")
	}
	endpoint := strings.TrimSpace(os.Getenv("TERMCP_MODEL_ENDPOINT"))
	modelID := strings.TrimSpace(os.Getenv("TERMCP_MODEL_ID"))
	tokenPath := strings.TrimSpace(os.Getenv("TERMCP_MODEL_TOKEN_FILE"))
	if endpoint == "" || modelID == "" || tokenPath == "" {
		t.Fatal("TERMCP_MODEL_ENDPOINT, TERMCP_MODEL_ID, and TERMCP_MODEL_TOKEN_FILE are required")
	}
	token, err := tokenfile.Read(tokenPath)
	if err != nil {
		t.Fatalf("read model token: %v", err)
	}
	adapter, err := model.NewOpenAICompatible(model.OpenAIConfig{
		Endpoint:        endpoint,
		Model:           modelID,
		APIToken:        token,
		HTTPClient:      &http.Client{Timeout: 60 * time.Second},
		MaxOutputTokens: 256,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible() error = %v", err)
	}
	tools, err := NewModelTools([]ModelToolSpec{{
		ServerID:     "termcp-core",
		ToolName:     "term",
		Description:  "Run one exact allowlisted terminal command.",
		Schema:       json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"],"additionalProperties":false}`),
		ExposeResult: true,
	}})
	if err != nil {
		t.Fatalf("NewModelTools() error = %v", err)
	}
	invoker := &providerToolInvoker{text: "/workspace/termcp-harness"}
	session := newTestSession(t, eventlog.New())
	result, err := session.RunModel(
		context.Background(),
		adapter,
		invoker,
		tools,
		`Call the termcp-core__term tool exactly once with {"cmd":"pwd"}. `+
			`After the tool result, answer in one short sentence and include the returned path exactly.`,
		ModelRunOptions{MaxIterations: 3, MaxToolCalls: 2},
	)
	if err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || result.Snapshot.ModelTurns < 2 {
		t.Fatalf("snapshot = %+v, output = %q", result.Snapshot, result.Output)
	}
	calls := invoker.Calls()
	if len(calls) != 1 || calls[0].ServerID != "termcp-core" || calls[0].ToolName != "term" ||
		string(calls[0].Arguments) != `{"cmd":"pwd"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if !strings.Contains(result.Output, "/workspace/termcp-harness") {
		t.Fatalf("model output did not use the tool result: %q", result.Output)
	}
}

type providerToolInvoker struct {
	mu   sync.Mutex
	text string
	all  []toolpipeline.Request
}

func (f *providerToolInvoker) Invoke(
	_ context.Context,
	request toolpipeline.Request,
) (toolpipeline.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.all = append(f.all, request)
	return toolpipeline.Outcome{
		Call:   toolcall.Snapshot{ID: request.CallID, Status: toolcall.StatusSucceeded},
		Result: mcp.NewToolResultText(f.text),
	}, nil
}

func (f *providerToolInvoker) Calls() []toolpipeline.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolpipeline.Request(nil), f.all...)
}
