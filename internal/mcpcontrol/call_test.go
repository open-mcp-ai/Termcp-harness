package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHostCallToolRequiresReadyServerAndKnownTool(t *testing.T) {
	client := newFakeHostClient()
	host := NewHost(withHostClientFactory(func(string, string) (clientConnection, error) { return client, nil }))
	registerTestServer(t, host)

	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}}
	if _, err := host.CallTool(context.Background(), "termcp-core", request); err == nil {
		t.Fatal("CallTool() before Connect error = nil")
	}
	if err := host.Connect(context.Background(), "termcp-core"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := host.LoadCapabilities(context.Background(), "termcp-core"); err != nil {
		t.Fatalf("LoadCapabilities() error = %v", err)
	}
	if _, err := host.CallTool(context.Background(), "termcp-core", request); err == nil {
		t.Fatal("CallTool() before Enable error = nil")
	}
	if _, err := host.Enable("termcp-core"); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}

	unknown := request
	unknown.Params.Name = "unknown"
	if _, err := host.CallTool(context.Background(), "termcp-core", unknown); err == nil {
		t.Fatal("CallTool() for unknown tool error = nil")
	}
	if len(client.toolCallRequests) != 0 {
		t.Fatalf("tool calls = %d, want 0 before a valid request", len(client.toolCallRequests))
	}

	result, err := host.CallTool(context.Background(), "termcp-core", request)
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("CallTool() result = %+v", result)
	}
	if len(client.toolCallRequests) != 1 || client.toolCallRequests[0].Params.Name != "term" {
		t.Fatalf("tool call requests = %+v", client.toolCallRequests)
	}
}

func TestHostCallToolEnforcesArgumentAndResultLimits(t *testing.T) {
	client := newFakeHostClient()
	host := newReadyCallHost(t, client)

	host.options.options.MaxToolArgumentsSize = 8
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "term",
		Arguments: json.RawMessage(`{"cmd":"too large"}`),
	}}
	if _, err := host.CallTool(context.Background(), "termcp-core", request); err == nil || !strings.Contains(err.Error(), "arguments exceed limit") {
		t.Fatalf("CallTool() argument limit error = %v", err)
	}
	if len(client.toolCallRequests) != 0 {
		t.Fatalf("tool calls = %d, want 0 after argument rejection", len(client.toolCallRequests))
	}

	host.options.options.MaxToolArgumentsSize = defaultMaxToolArgumentsSize
	host.options.options.MaxToolResultSize = 32
	client.toolCallResult = mcp.NewToolResultText(strings.Repeat("x", 128))
	if _, err := host.CallTool(context.Background(), "termcp-core", request); err == nil || !strings.Contains(err.Error(), "result exceeds limit") {
		t.Fatalf("CallTool() result limit error = %v", err)
	}
}

func TestHostCallToolTimeoutPreservesDeadlineError(t *testing.T) {
	client := newFakeHostClient()
	client.toolCallFunc = func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	host := newReadyCallHost(t, client)
	host.options.options.ToolCallTimeout = 10 * time.Millisecond

	_, err := host.CallTool(context.Background(), "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CallTool() error = %v, want context deadline", err)
	}
}

func newReadyCallHost(t *testing.T, client *fakeHostClient) *Host {
	t.Helper()
	host := NewHost(withHostClientFactory(func(string, string) (clientConnection, error) { return client, nil }))
	registerTestServer(t, host)
	if err := host.Connect(context.Background(), "termcp-core"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := host.LoadCapabilities(context.Background(), "termcp-core"); err != nil {
		t.Fatalf("LoadCapabilities() error = %v", err)
	}
	if _, err := host.Enable("termcp-core"); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	return host
}
