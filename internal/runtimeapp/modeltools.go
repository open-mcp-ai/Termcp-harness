package runtimeapp

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

type PolicyModelToolProvider struct {
	Host   modelToolSource
	Policy *toolpipeline.AllowlistPolicy
}

type modelToolSource interface {
	Tools(string) []mcpcontrol.ToolDescriptor
}

func (p *PolicyModelToolProvider) ModelTools() []agentsession.ModelTool {
	if p == nil || p.Host == nil || p.Policy == nil {
		return nil
	}
	type key struct {
		serverID string
		toolName string
	}
	exposure := make(map[key]bool)
	descriptors := make(map[key]mcpcontrol.ToolDescriptor)
	for _, rule := range p.Policy.Rules() {
		serverID := strings.TrimSpace(rule.ServerID)
		toolName := strings.TrimSpace(rule.ToolName)
		candidate := key{serverID: serverID, toolName: toolName}
		if _, exists := descriptors[candidate]; !exists {
			descriptor, ok := findMCPTool(p.Host.Tools(serverID), toolName)
			if !ok {
				continue
			}
			descriptors[candidate] = descriptor
		}
		allowed := rule.ModelResult == toolpipeline.ModelResultContent
		if previous, exists := exposure[candidate]; exists {
			exposure[candidate] = previous && allowed
		} else {
			exposure[candidate] = allowed
		}
	}
	specs := make([]agentsession.ModelToolSpec, 0, len(descriptors))
	for candidate, descriptor := range descriptors {
		schema := append(json.RawMessage(nil), descriptor.Schema...)
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		specs = append(specs, agentsession.ModelToolSpec{
			ServerID:     candidate.serverID,
			ToolName:     candidate.toolName,
			Description:  descriptor.Description,
			Schema:       schema,
			ExposeResult: exposure[candidate],
		})
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].ServerID == specs[j].ServerID {
			return specs[i].ToolName < specs[j].ToolName
		}
		return specs[i].ServerID < specs[j].ServerID
	})
	tools, err := agentsession.NewModelTools(specs)
	if err != nil {
		return nil
	}
	return tools
}

func findMCPTool(tools []mcpcontrol.ToolDescriptor, name string) (mcpcontrol.ToolDescriptor, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcpcontrol.ToolDescriptor{}, false
}
