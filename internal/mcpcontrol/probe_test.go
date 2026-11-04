package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestProbeInitializesAndLoadsTools(t *testing.T) {
	fake := &fakeProbeClient{
		initializeResult: &mcp.InitializeResult{
			ProtocolVersion: "2025-06-18",
			Capabilities: mcp.ServerCapabilities{Tools: &struct {
				ListChanged bool `json:"listChanged,omitempty"`
			}{ListChanged: true}},
			ServerInfo: mcp.Implementation{Name: "termcp", Version: "v0.2.8"},
		},
		toolsResult: &mcp.ListToolsResult{Tools: []mcp.Tool{{
			Name:        "term",
			Description: "Run one terminal line",
			InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{}},
		}}},
	}
	prober := &Prober{factory: func(string, string) (clientConnection, error) { return fake, nil }}

	observation, err := prober.Probe(context.Background(), ProbeOptions{Endpoint: "http://127.0.0.1:18765/stream"})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !fake.started || !fake.initialized || !fake.closed {
		t.Fatalf("probe client lifecycle = started:%v initialized:%v closed:%v", fake.started, fake.initialized, fake.closed)
	}
	if observation.ServerName != "termcp" || observation.ServerVersion != "v0.2.8" || observation.ProtocolVersion != "2025-06-18" {
		t.Fatalf("observation identity = %+v", observation)
	}
	if !observation.ToolsSupported || len(observation.Tools) != 1 || observation.Tools[0].Name != "term" {
		t.Fatalf("observation tools = %+v", observation.Tools)
	}
	var schema map[string]any
	if err := json.Unmarshal(observation.Tools[0].Schema, &schema); err != nil {
		t.Fatalf("decode tool schema: %v", err)
	}
	if schema["type"] != "object" || schema["inputSchema"] != nil {
		t.Fatalf("tool schema = %+v", schema)
	}
	if observation.Capabilities().ToolCount != 1 {
		t.Fatalf("Capabilities() = %+v", observation.Capabilities())
	}
}

func TestProbeRejectsDuplicateAndOversizedTools(t *testing.T) {
	tests := []struct {
		name    string
		tools   []mcp.Tool
		maximum int
	}{
		{
			name:  "duplicate names",
			tools: []mcp.Tool{{Name: "term"}, {Name: "term"}},
		},
		{
			name:    "schema budget",
			tools:   []mcp.Tool{{Name: "term", Description: strings.Repeat("x", 128)}},
			maximum: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeProbeClient{
				initializeResult: &mcp.InitializeResult{
					ProtocolVersion: "2025-06-18",
					Capabilities: mcp.ServerCapabilities{Tools: &struct {
						ListChanged bool `json:"listChanged,omitempty"`
					}{}},
					ServerInfo: mcp.Implementation{Name: "test", Version: "1"},
				},
				toolsResult: &mcp.ListToolsResult{Tools: tt.tools},
			}
			prober := &Prober{factory: func(string, string) (clientConnection, error) { return fake, nil }}
			_, err := prober.Probe(context.Background(), ProbeOptions{
				Endpoint:       "http://127.0.0.1:18765/stream",
				MaxSchemaBytes: tt.maximum,
			})
			if err == nil {
				t.Fatal("Probe() error = nil, want error")
			}
		})
	}
}

func TestProbeRequiresLoopbackEndpoint(t *testing.T) {
	prober := &Prober{factory: func(string, string) (clientConnection, error) {
		return nil, errors.New("factory must not be called")
	}}
	if _, err := prober.Probe(context.Background(), ProbeOptions{Endpoint: "https://example.com/mcp"}); err == nil {
		t.Fatal("Probe() error = nil, want endpoint validation error")
	}
}

func TestProbeTermcpCoreIntegration(t *testing.T) {
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	if endpoint == "" {
		t.Skip("set TERMCP_CORE_MCP_URL to run against a real Core instance")
	}

	observation, err := NewProber().Probe(context.Background(), ProbeOptions{
		Endpoint:      endpoint,
		ClientName:    "termcp-harness-contract-test",
		ClientVersion: "dev",
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if observation.ServerName != "termcp" {
		t.Fatalf("ServerName = %q, want termcp", observation.ServerName)
	}
	if len(observation.Tools) == 0 || observation.Tools[0].Name != "term" {
		t.Fatalf("tools = %+v, want term", observation.Tools)
	}
}

type fakeProbeClient struct {
	started          bool
	initialized      bool
	closed           bool
	initializeResult *mcp.InitializeResult
	toolsResult      *mcp.ListToolsResult
}

func (f *fakeProbeClient) Start(context.Context) error {
	f.started = true
	return nil
}

func (f *fakeProbeClient) Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	f.initialized = true
	return f.initializeResult, nil
}

func (f *fakeProbeClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return f.toolsResult, nil
}

func (f *fakeProbeClient) CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("unused"), nil
}

func (f *fakeProbeClient) OnNotification(func(mcp.JSONRPCNotification)) {}

func (f *fakeProbeClient) Close() error {
	f.closed = true
	return nil
}
