// Package model defines the replaceable model boundary used by the Agent Loop.
// Model output is untrusted data; callers must validate tool calls before use.
package model

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Message struct {
	Role             Role       `json:"role"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"-"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Request struct {
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

type Response struct {
	Message Message `json:"message"`
}

// Adapter requests one model completion. Implementations must not execute tool
// calls or infer tool results; the Agent Loop owns that boundary.
type Adapter interface {
	Complete(context.Context, Request) (Response, error)
}
