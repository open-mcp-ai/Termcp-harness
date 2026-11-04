package mcpcontrol

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func TestHostConnectsSSETransport(t *testing.T) {
	mcpServer := mcpserver.NewMCPServer(
		"sse-mcp",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)
	mcpServer.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	server := mcpserver.NewTestServer(mcpServer)
	defer server.Close()

	host := NewHost()
	defer host.Close()
	if _, err := host.Register(Spec{
		ID:        "sse-mcp",
		Name:      "SSE MCP",
		Endpoint:  server.URL + "/sse",
		Transport: TransportSSE,
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Connect(context.Background(), "sse-mcp"); err != nil {
		t.Fatal(err)
	}
	if err := host.LoadCapabilities(context.Background(), "sse-mcp"); err != nil {
		t.Fatal(err)
	}
	enabled, err := host.Enable("sse-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Ready || enabled.Transport != TransportSSE || enabled.Capabilities.ToolCount != 1 {
		t.Fatalf("enabled = %+v", enabled)
	}
}

func TestHostConnectsStdioTransport(t *testing.T) {
	host := NewHost()
	defer host.Close()
	if _, err := host.Register(Spec{
		ID:        "stdio-mcp",
		Name:      "stdio MCP",
		Transport: TransportStdio,
		Command:   os.Args[0],
		Args:      []string{"-test.run=^TestStdioServerHelper$"},
		Env:       []string{"TERMCP_STDIO_MCP_HELPER=1"},
		WorkDir:   t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Connect(context.Background(), "stdio-mcp"); err != nil {
		t.Fatal(err)
	}
	if err := host.LoadCapabilities(context.Background(), "stdio-mcp"); err != nil {
		t.Fatal(err)
	}
	enabled, err := host.Enable("stdio-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Ready || enabled.Management != ManagementManaged ||
		enabled.Transport != TransportStdio || enabled.Capabilities.ToolCount != 1 {
		t.Fatalf("enabled = %+v", enabled)
	}
}

func TestStdioServerHelper(t *testing.T) {
	if os.Getenv("TERMCP_STDIO_MCP_HELPER") != "1" {
		return
	}
	mcpServer := mcpserver.NewMCPServer(
		"stdio-helper",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)
	mcpServer.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	if err := mcpserver.ServeStdio(mcpServer); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
