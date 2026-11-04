package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// CallTool dispatches a tool only after the server is ready and the tool is
// present in the currently loaded capability snapshot. It is a transport
// primitive for the tool pipeline, not a public control-plane entry point.
func (h *Host) CallTool(ctx context.Context, id string, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h == nil {
		return nil, errors.New("MCP host is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	name := strings.TrimSpace(request.Params.Name)
	if name == "" {
		return nil, errors.New("MCP tool name is required")
	}
	if request.Params.Name != name {
		return nil, errors.New("MCP tool name must not contain surrounding whitespace")
	}

	encodedArguments, err := json.Marshal(request.Params.Arguments)
	if err != nil {
		return nil, fmt.Errorf("encode MCP tool arguments: %w", err)
	}
	if limit := h.options.options.MaxToolArgumentsSize; limit > 0 && len(encodedArguments) > limit {
		return nil, fmt.Errorf("MCP tool arguments exceed limit %d bytes", limit)
	}

	managed, err := h.managed(id)
	if err != nil {
		return nil, err
	}
	managed.mu.Lock()
	snapshot := managed.server.Snapshot()
	client := managed.client
	knownTool := containsTool(managed.tools, name)
	managed.mu.Unlock()

	if !snapshot.Ready || client == nil {
		return nil, fmt.Errorf("MCP server %q is not ready", id)
	}
	if !knownTool {
		return nil, fmt.Errorf("MCP tool %q is not present in loaded capabilities", name)
	}

	callContext := ctx
	if timeout := h.options.options.ToolCallTimeout; timeout > 0 {
		var cancel context.CancelFunc
		callContext, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	result, err := client.CallTool(callContext, request)
	if err != nil {
		return nil, fmt.Errorf("call MCP tool %q: %w", name, err)
	}
	if result == nil {
		return nil, fmt.Errorf("MCP tool %q returned no result", name)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode MCP tool %q result: %w", name, err)
	}
	if limit := h.options.options.MaxToolResultSize; limit > 0 && len(encodedResult) > limit {
		return nil, fmt.Errorf("MCP tool %q result exceeds limit %d bytes", name, limit)
	}
	return result, nil
}

func containsTool(tools []ToolDescriptor, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
