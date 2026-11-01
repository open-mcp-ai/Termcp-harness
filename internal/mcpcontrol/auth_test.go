package mcpcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func TestHostAuthenticatesStreamableHTTPWithFileReferencedToken(t *testing.T) {
	const token = "file-referenced-mcp-token"
	mcpServer := mcpserver.NewMCPServer(
		"secure-mcp",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)
	mcpServer.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	streamable := mcpserver.NewStreamableHTTPServer(mcpServer)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, request)
	}))
	defer server.Close()

	host := NewHost()
	defer host.Close()
	if _, err := host.Register(Spec{
		ID:        "secure-mcp",
		Name:      "Secure MCP",
		Endpoint:  server.URL,
		AuthToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Connect(context.Background(), "secure-mcp"); err != nil {
		t.Fatal(err)
	}
	if err := host.LoadCapabilities(context.Background(), "secure-mcp"); err != nil {
		t.Fatal(err)
	}
	enabled, err := host.Enable("secure-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Ready || enabled.Capabilities.ToolCount != 1 {
		t.Fatalf("enabled = %+v", enabled)
	}
}
