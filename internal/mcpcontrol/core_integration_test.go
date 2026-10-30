package mcpcontrol

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestTermcpCoreReadOnlyToolCall(t *testing.T) {
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	if endpoint == "" {
		t.Skip("set TERMCP_CORE_MCP_URL to run against a real Core instance")
	}
	if _, err := validateLoopbackEndpoint(endpoint); err != nil {
		t.Fatalf("invalid integration endpoint: %v", err)
	}

	mcpClient, err := client.NewStreamableHttpClient(endpoint)
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	defer mcpClient.Close()

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start MCP client: %v", err)
	}
	if _, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "termcp-harness-readonly-contract-test",
				Version: "dev",
			},
		},
	}); err != nil {
		t.Fatalf("initialize MCP server: %v", err)
	}

	result, err := mcpClient.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "term",
			Arguments: map[string]any{"cmd": ""},
		},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool() returned isError=true: %s", toolResultText(result))
	}
	if len(result.Content) == 0 {
		t.Fatal("CallTool() returned no content")
	}

	encoded, err := json.Marshal(result.Content)
	if err != nil {
		t.Fatalf("encode tool content: %v", err)
	}
	if len(encoded) == 0 || string(encoded) == "null" || string(encoded) == "[]" {
		t.Fatalf("tool content = %s, want non-empty", encoded)
	}
}

func toolResultText(result *mcp.CallToolResult) string {
	encoded, _ := json.Marshal(result.Content)
	return string(encoded)
}
