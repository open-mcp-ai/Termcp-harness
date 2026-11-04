package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultOpenAITimeout         = 60 * time.Second
	defaultOpenAIMaxResponse     = 4 << 20
	defaultOpenAIMaxToolArgs     = 1 << 20
	defaultOpenAIMaxToolCalls    = 32
	defaultOpenAIMaxContentBytes = 256 << 10
)

type OpenAIConfig struct {
	Endpoint         string
	Model            string
	APIToken         string
	HTTPClient       *http.Client
	MaxOutputTokens  int
	MaxResponseBytes int
	MaxToolCalls     int
}

type OpenAICompatible struct {
	endpoint        string
	model           string
	apiToken        string
	http            *http.Client
	maxOutputTokens int
	maxResponse     int
	maxToolCalls    int
}

func NewOpenAICompatible(config OpenAIConfig) (*OpenAICompatible, error) {
	endpoint, err := validateOpenAIEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(config.Model)
	if model == "" || len(model) > 256 {
		return nil, errors.New("model ID is required and must not exceed 256 characters")
	}
	if config.MaxOutputTokens < 0 {
		return nil, errors.New("model max output tokens must not be negative")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultOpenAITimeout}
	}
	maxResponse := config.MaxResponseBytes
	if maxResponse == 0 {
		maxResponse = defaultOpenAIMaxResponse
	}
	if maxResponse < 1024 || maxResponse > 16<<20 {
		return nil, errors.New("model max response bytes must be between 1024 and 16 MiB")
	}
	maxToolCalls := config.MaxToolCalls
	if maxToolCalls == 0 {
		maxToolCalls = defaultOpenAIMaxToolCalls
	}
	if maxToolCalls < 1 || maxToolCalls > 128 {
		return nil, errors.New("model max tool calls must be between 1 and 128")
	}
	return &OpenAICompatible{
		endpoint:        endpoint,
		model:           model,
		apiToken:        strings.TrimSpace(config.APIToken),
		http:            httpClient,
		maxOutputTokens: config.MaxOutputTokens,
		maxResponse:     maxResponse,
		maxToolCalls:    maxToolCalls,
	}, nil
}

func (a *OpenAICompatible) Complete(ctx context.Context, request Request) (Response, error) {
	if a == nil {
		return Response{}, errors.New("model adapter is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	body, err := a.encodeRequest(request)
	if err != nil {
		return Response{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("build model request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	if a.apiToken != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+a.apiToken)
	}

	response, err := a.http.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("request model completion: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, int64(a.maxResponse)+1))
	if err != nil {
		return Response{}, fmt.Errorf("read model completion: %w", err)
	}
	if len(responseBody) > a.maxResponse {
		return Response{}, fmt.Errorf("model response exceeds %d bytes", a.maxResponse)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, fmt.Errorf("model completion returned HTTP %d", response.StatusCode)
	}
	return decodeOpenAIResponse(responseBody, a.maxToolCalls)
}

func (a *OpenAICompatible) encodeRequest(request Request) ([]byte, error) {
	messages := make([]openAIMessage, 0, len(request.Messages))
	for i, message := range request.Messages {
		switch message.Role {
		case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		default:
			return nil, fmt.Errorf("message %d has invalid role %q", i, message.Role)
		}
		if len(message.Content) > defaultOpenAIMaxContentBytes {
			return nil, fmt.Errorf("message %d content exceeds %d bytes", i, defaultOpenAIMaxContentBytes)
		}
		wire := openAIMessage{
			Role:       string(message.Role),
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
			Name:       message.Name,
		}
		if message.Role == RoleAssistant {
			wire.ReasoningContent = message.ReasoningContent
		}
		for _, call := range message.ToolCalls {
			if err := validateToolName(call.Name); err != nil {
				return nil, fmt.Errorf("message %d tool call name: %w", i, err)
			}
			arguments, err := normalizeArguments(call.Arguments, defaultOpenAIMaxToolArgs)
			if err != nil {
				return nil, fmt.Errorf("message %d tool call arguments: %w", i, err)
			}
			wire.ToolCalls = append(wire.ToolCalls, openAIToolCall{
				ID:   call.ID,
				Type: "function",
				Function: openAIFunctionCall{
					Name:      call.Name,
					Arguments: string(arguments),
				},
			})
		}
		messages = append(messages, wire)
	}

	tools := make([]openAITool, 0, len(request.Tools))
	for i, tool := range request.Tools {
		if err := validateToolName(tool.Name); err != nil {
			return nil, fmt.Errorf("tool %d: %w", i, err)
		}
		parameters, err := normalizeSchema(tool.Parameters)
		if err != nil {
			return nil, fmt.Errorf("tool %d parameters: %w", i, err)
		}
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunctionDefinition{
				Name:        tool.Name,
				Description: boundedDescription(tool.Description),
				Parameters:  parameters,
			},
		})
	}

	wire := openAIRequest{
		Model:           a.model,
		Messages:        messages,
		Tools:           tools,
		MaxOutputTokens: a.maxOutputTokens,
	}
	if len(tools) > 0 {
		wire.ToolChoice = "auto"
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}
	if len(encoded) > defaultOpenAIMaxResponse {
		return nil, fmt.Errorf("model request exceeds %d bytes", defaultOpenAIMaxResponse)
	}
	return encoded, nil
}

func boundedDescription(value string) string {
	const maxBytes = 8 << 10
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && value[len(value)-1]&0xC0 == 0x80 {
		value = value[:len(value)-1]
	}
	return value
}

func decodeOpenAIResponse(body []byte, maxToolCalls int) (Response, error) {
	var wire openAIResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return Response{}, fmt.Errorf("decode model completion: %w", err)
	}
	if len(wire.Choices) == 0 {
		return Response{}, errors.New("model completion has no choices")
	}
	choice := wire.Choices[0].Message
	if len(choice.ToolCalls) > maxToolCalls {
		return Response{}, fmt.Errorf("model returned %d tool calls, limit is %d", len(choice.ToolCalls), maxToolCalls)
	}
	content, err := decodeContent(choice.Content)
	if err != nil {
		return Response{}, err
	}
	message := Message{Role: RoleAssistant, Content: content}
	if len(choice.ReasoningContent) > defaultOpenAIMaxContentBytes {
		return Response{}, fmt.Errorf("model reasoning content exceeds %d bytes", defaultOpenAIMaxContentBytes)
	}
	message.ReasoningContent = choice.ReasoningContent
	for i, call := range choice.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return Response{}, fmt.Errorf("model tool call %d has unsupported type %q", i, call.Type)
		}
		if err := validateToolName(call.Function.Name); err != nil {
			return Response{}, fmt.Errorf("model tool call %d: %w", i, err)
		}
		arguments, err := normalizeArguments(json.RawMessage(call.Function.Arguments), defaultOpenAIMaxToolArgs)
		if err != nil {
			return Response{}, fmt.Errorf("model tool call %d arguments: %w", i, err)
		}
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			ID:        strings.TrimSpace(call.ID),
			Name:      call.Function.Name,
			Arguments: arguments,
		})
	}
	return Response{Message: message}, nil
}

func validateOpenAIEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse model endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("model endpoint must use http or https")
	}
	if parsed.User != nil {
		return "", errors.New("model endpoint must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("model endpoint must not contain query or fragment")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("model endpoint host is required")
	}
	if parsed.Scheme == "http" && !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("non-loopback model endpoints must use HTTPS")
		}
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return "", errors.New("model endpoint path is required")
	}
	return parsed.String(), nil
}

func decodeContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var content string
	if err := json.Unmarshal(raw, &content); err != nil {
		return "", errors.New("model message content must be a string or null")
	}
	if len(content) > defaultOpenAIMaxContentBytes {
		return "", fmt.Errorf("model message content exceeds %d bytes", defaultOpenAIMaxContentBytes)
	}
	return content, nil
}

func normalizeArguments(raw json.RawMessage, maxBytes int) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("tool arguments are required")
	}
	if len(raw) > maxBytes {
		return nil, fmt.Errorf("tool arguments exceed %d bytes", maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("tool arguments must be a JSON object: %w", err)
	}
	if object == nil {
		return nil, errors.New("tool arguments must be a JSON object, not null")
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("normalize tool arguments: %w", err)
	}
	if len(normalized) > maxBytes {
		return nil, fmt.Errorf("normalized tool arguments exceed %d bytes", maxBytes)
	}
	return normalized, nil
}

func normalizeSchema(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("tool schema must be valid JSON")
	}
	if value == nil {
		return nil, errors.New("tool schema must not be null")
	}
	return append(json.RawMessage(nil), raw...), nil
}

func validateToolName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return errors.New("tool name is required and must not exceed 128 characters")
	}
	for _, character := range name {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return fmt.Errorf("tool name %q contains unsupported characters", name)
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains multiple values")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

type openAIRequest struct {
	Model           string          `json:"model"`
	Messages        []openAIMessage `json:"messages"`
	Tools           []openAITool    `json:"tools,omitempty"`
	ToolChoice      string          `json:"tool_choice,omitempty"`
	MaxOutputTokens int             `json:"max_tokens,omitempty"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	Name             string           `json:"name,omitempty"`
}

type openAITool struct {
	Type     string                   `json:"type"`
	Function openAIFunctionDefinition `json:"function"`
}

type openAIFunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content          json.RawMessage `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}
