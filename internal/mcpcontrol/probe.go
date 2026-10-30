package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultProbeTimeout   = 10 * time.Second
	defaultMaxTools       = 1024
	defaultMaxSchemaBytes = 4 << 20
)

type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
}

type Observation struct {
	Endpoint        string           `json:"endpoint"`
	ProtocolVersion string           `json:"protocol_version"`
	ServerName      string           `json:"server_name"`
	ServerVersion   string           `json:"server_version"`
	ToolsSupported  bool             `json:"tools_supported"`
	Tools           []ToolDescriptor `json:"tools"`
}

type ProbeOptions struct {
	Endpoint       string
	ClientName     string
	ClientVersion  string
	Timeout        time.Duration
	MaxTools       int
	MaxSchemaBytes int
}

type probeClient interface {
	Start(context.Context) error
	Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	Close() error
}

type probeClientFactory func(string) (probeClient, error)

type Prober struct {
	factory probeClientFactory
}

func NewProber() *Prober {
	return &Prober{factory: func(endpoint string) (probeClient, error) {
		return client.NewStreamableHttpClient(endpoint)
	}}
}

func (p *Prober) Probe(ctx context.Context, options ProbeOptions) (Observation, error) {
	endpoint, err := validateLoopbackEndpoint(options.Endpoint)
	if err != nil {
		return Observation{}, err
	}
	if p == nil || p.factory == nil {
		return Observation{}, errors.New("MCP prober has no client factory")
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultProbeTimeout
	}
	if options.MaxTools <= 0 {
		options.MaxTools = defaultMaxTools
	}
	if options.MaxSchemaBytes <= 0 {
		options.MaxSchemaBytes = defaultMaxSchemaBytes
	}
	if options.ClientName == "" {
		options.ClientName = "termcp-harness"
	}
	if options.ClientVersion == "" {
		options.ClientVersion = "dev"
	}

	probeContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	mcpClient, err := p.factory(endpoint)
	if err != nil {
		return Observation{}, fmt.Errorf("create MCP client: %w", err)
	}
	defer mcpClient.Close()

	if err := mcpClient.Start(probeContext); err != nil {
		return Observation{}, fmt.Errorf("start MCP client: %w", err)
	}

	initialized, err := mcpClient.Initialize(probeContext, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    options.ClientName,
				Version: options.ClientVersion,
			},
		},
	})
	if err != nil {
		return Observation{}, fmt.Errorf("initialize MCP server: %w", err)
	}
	if initialized.ServerInfo.Name == "" || initialized.ServerInfo.Version == "" || initialized.ProtocolVersion == "" {
		return Observation{}, errors.New("MCP initialize response is missing server identity or protocol version")
	}

	observation := Observation{
		Endpoint:        endpoint,
		ProtocolVersion: initialized.ProtocolVersion,
		ServerName:      initialized.ServerInfo.Name,
		ServerVersion:   initialized.ServerInfo.Version,
		ToolsSupported:  initialized.Capabilities.Tools != nil,
	}
	if !observation.ToolsSupported {
		return observation, nil
	}

	listed, err := mcpClient.ListTools(probeContext, mcp.ListToolsRequest{})
	if err != nil {
		return Observation{}, fmt.Errorf("list MCP tools: %w", err)
	}
	if len(listed.Tools) > options.MaxTools {
		return Observation{}, fmt.Errorf("MCP tool count %d exceeds limit %d", len(listed.Tools), options.MaxTools)
	}

	seen := make(map[string]struct{}, len(listed.Tools))
	totalSchemaBytes := 0
	observation.Tools = make([]ToolDescriptor, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		if tool.Name == "" {
			return Observation{}, errors.New("MCP server returned a tool with an empty name")
		}
		if _, exists := seen[tool.Name]; exists {
			return Observation{}, fmt.Errorf("MCP server returned duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = struct{}{}

		encoded, err := json.Marshal(tool)
		if err != nil {
			return Observation{}, fmt.Errorf("encode schema for MCP tool %q: %w", tool.Name, err)
		}
		totalSchemaBytes += len(encoded)
		if totalSchemaBytes > options.MaxSchemaBytes {
			return Observation{}, fmt.Errorf("MCP tool schemas exceed limit %d bytes", options.MaxSchemaBytes)
		}
		observation.Tools = append(observation.Tools, ToolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      encoded,
		})
	}
	return observation, nil
}

func (o Observation) Capabilities() Capabilities {
	return Capabilities{
		ProtocolVersion: o.ProtocolVersion,
		ServerName:      o.ServerName,
		ServerVersion:   o.ServerVersion,
		ToolsSupported:  o.ToolsSupported,
		ToolCount:       len(o.Tools),
	}
}
